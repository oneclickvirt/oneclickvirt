package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Service coordinates panel-triggered updates. It is deliberately process
// local: a second request is rejected while an update/restart is in progress,
// and the on-disk backup manifest remains the durable rollback source.
type Service struct {
	operationMu sync.Mutex
	stateMu     sync.RWMutex
	state       OperationState
	clientMu    sync.Mutex
	client      *http.Client
	cacheMu     sync.Mutex
	cache       releaseCache
	now         func() time.Time
}

type releaseCache struct {
	repo      string
	releases  []githubRelease
	checkedAt time.Time
}

const maxOperationAge = 25 * time.Minute

var defaultService = NewService()

func NewService() *Service {
	service := &Service{
		state: OperationState{Status: OperationIdle},
		now:   time.Now,
	}
	service.restoreState()
	return service
}

func GetService() *Service {
	return defaultService
}

func (s *Service) Capability() Capability {
	cfg := loadRuntimeConfig()
	capability := Capability{
		Mode:          cfg.Mode,
		Flavor:        cfg.Flavor,
		Automatic:     cfg.automaticAllowed(),
		InstallRoot:   cfg.InstallRoot,
		ServerPath:    cfg.ServerPath,
		WebPath:       cfg.WebPath,
		ServiceName:   cfg.ServiceName,
		ProxyServices: append([]string(nil), cfg.ProxyServices...),
		CDNEndpoints:  safeCDNEndpoints(cfg.CDNEndpoints),
		Commands:      manualCommands(cfg),
	}

	if capability.Automatic {
		capability.CanUpdate = true
		capability.CanRollback = true
		capability.CanRestart = true
		return capability
	}

	switch cfg.Mode {
	case ModeDocker, ModeCompose:
		capability.Reason = "容器部署的文件由镜像/编排管理，面板不会直接改写容器；请使用下方命令更新并重建服务"
	case ModeSource:
		capability.Reason = "源码部署需要在源码目录完成构建和迁移，面板仅展示可复制命令"
	case ModeEmbedded:
		capability.Reason = "当前为一体化或嵌入式运行模式，未检测到受控 systemd 安装；请使用脚本或手动命令"
	case ModeDisabled:
		capability.Reason = "管理员已通过 ONECLICKVIRT_UPDATE_ENABLED=false 禁用面板更新"
	default:
		capability.Reason = "未识别为受控 systemd 安装，面板不会执行主机写入"
	}
	return capability
}

func (s *Service) CheckUpdates(ctx context.Context) (UpdateInfo, error) {
	cfg := loadRuntimeConfig()
	current := readInstalledVersion(cfg)
	info := UpdateInfo{
		CurrentVersion: current,
		Capability:     s.Capability(),
		Rollback:       s.listBackups(cfg),
		Operation:      s.Operation(),
		CheckedAt:      s.now(),
		Releases:       []Release{},
	}
	releases, err := s.fetchReleases(ctx, cfg)
	if err != nil {
		return info, err
	}
	for _, release := range releases {
		public := publicRelease(release, cfg)
		public.CanUpdate = public.CanApply && compareVersions(release.TagName, current) > 0
		public.CanRollback = public.CanApply && compareVersions(release.TagName, current) < 0
		info.Releases = append(info.Releases, public)
	}
	if latest, ok := latestStableRelease(releases); ok {
		info.LatestVersion = latest.TagName
		info.ReleaseURL = latest.HTMLURL
		info.UpdateAvailable = compareVersions(info.LatestVersion, current) > 0
	}
	return info, nil
}

func (s *Service) RollbackVersions(ctx context.Context) (UpdateInfo, error) {
	cfg := loadRuntimeConfig()
	info, err := s.CheckUpdates(ctx)
	if err != nil {
		// Local backups are still useful when GitHub/CDN metadata is unavailable.
		info.Rollback = s.listBackups(cfg)
		if len(info.Rollback) == 0 {
			return info, err
		}
	}
	current := normalizeTag(readInstalledVersion(cfg))
	filtered := make([]Release, 0, len(info.Releases))
	for _, release := range info.Releases {
		if release.CanRollback && compareVersions(release.Tag, current) < 0 {
			filtered = append(filtered, release)
		}
	}
	info.Releases = filtered
	return info, nil
}

func (s *Service) Operation() OperationState {
	s.refreshStateFromDisk()
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return cloneOperation(s.state)
}

func (s *Service) beginOperation(action, target string) (OperationState, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	current := s.Operation()
	if current.Status == OperationStaging || current.Status == OperationScheduled || current.Status == OperationApplying {
		return current, fmt.Errorf("更新或重启操作正在进行中")
	}
	state := OperationState{
		ID:        fmt.Sprintf("ocv-%d", s.now().UnixNano()),
		Action:    action,
		Target:    target,
		Status:    OperationScheduled,
		StartedAt: s.now(),
	}
	s.stateMu.Lock()
	s.state = state
	s.persistStateLocked()
	s.stateMu.Unlock()
	return cloneOperation(state), nil
}

func (s *Service) updateOperation(id, status, message string, operationErr error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.state.ID != id {
		return
	}
	s.state.Status = status
	s.state.Message = message
	s.state.Error = ""
	if operationErr != nil {
		s.state.Error = operationErr.Error()
	}
	if status == OperationSucceeded || status == OperationFailed {
		finished := s.now()
		s.state.FinishedAt = &finished
	}
	s.persistStateLocked()
}

func cloneOperation(value OperationState) OperationState {
	if value.FinishedAt != nil {
		finished := *value.FinishedAt
		value.FinishedAt = &finished
	}
	return value
}

func manualCommands(cfg runtimeConfig) []Command {
	target := "<版本号>"
	scriptPath := cfg.ScriptPath
	var scriptCommand string
	if strings.TrimSpace(scriptPath) != "" && fileExists(scriptPath) {
		scriptCommand = fmt.Sprintf("sudo INSTALL_VERSION=%s bash %s upgrade", shellQuote(target), shellQuote(scriptPath))
	} else {
		scriptCommand = fmt.Sprintf("curl -fsSL https://raw.githubusercontent.com/%s/main/scripts/install.sh -o /tmp/oneclickvirt-install.sh && sudo INSTALL_VERSION=%s bash /tmp/oneclickvirt-install.sh upgrade", cfg.Repo, shellQuote(target))
	}
	rollbackCommand := scriptCommand
	commands := []Command{
		{Key: "script-upgrade", Label: "使用原有安装脚本升级", Description: "指定目标版本；脚本仍是独立的人工升级路径。", Command: scriptCommand, Available: true, Destructive: true},
		{Key: "script-rollback", Label: "使用原有安装脚本回退到指定版本", Description: "回退仅替换主控与 Web 资产，数据库迁移不会自动逆向。", Command: rollbackCommand, Available: true, Destructive: true},
	}

	switch cfg.Mode {
	case ModeDocker:
		commands = append(commands, Command{
			Key: "docker-inspect", Label: "导出当前 Docker 容器配置", Description: "先保留现有端口、环境变量和挂载参数，供自定义容器重建时核对。", Command: "docker inspect oneclickvirt > oneclickvirt.before-update.json", Available: true, Destructive: false,
		})
		commands = append(commands, Command{
			Key: "docker-default-allinone", Label: "按 README 默认参数重建 all-in-one Docker 容器", Description: "仅适用于名称为 oneclickvirt、使用 README 默认端口和命名卷的内置数据库容器；自定义域名、端口或 no-db 部署请保留原始 docker run 参数重建。", Command: "docker pull oneclickvirt/oneclickvirt:latest && docker rm -f oneclickvirt && docker run -d --name oneclickvirt -p 80:80 -v oneclickvirt-data:/var/lib/mysql -v oneclickvirt-storage:/app/storage --restart unless-stopped oneclickvirt/oneclickvirt:latest", Available: true, Destructive: true,
		})
	case ModeCompose:
		commands = append(commands, Command{
			Key: "compose", Label: "Compose 构建并重建 API 与 Web", Description: "在包含 docker-compose.yaml 的仓库目录执行；只重建 api 和 web，保留 mysql_data 命名卷。", Command: fmt.Sprintf("git fetch --tags && git checkout %s && docker compose up -d --build --force-recreate api web", shellQuote(target)), Available: true, Destructive: true,
		})
	case ModeSource:
		commands = append(commands, Command{
			Key: "source", Label: "源码构建前后端", Description: "在源码根目录执行，并按当前服务管理方式替换构建产物和重启。", Command: fmt.Sprintf("git fetch --tags && git checkout %s && (cd web && npm ci && npm run build) && (cd server && go build -o oneclickvirt-server .)", shellQuote(target)), Available: true, Destructive: true,
		})
	}
	if serviceCommand, err := cfg.commandForService("restart"); err == nil {
		parts := []string{"sudo " + serviceCommand}
		for _, proxyService := range cfg.ProxyServices {
			proxyCommand, err := (runtimeConfig{ServiceName: proxyService}).commandForService("reload")
			if err == nil {
				parts = append(parts, "sudo "+proxyCommand)
			}
		}
		commands = append(commands, Command{Key: "restart", Label: "重启主控并重载已配置反向代理", Description: "仅重载通过 ONECLICKVIRT_PROXY_SERVICES 明确指定的 systemd 反向代理服务。", Command: strings.Join(parts, " && "), Available: cfg.Mode == ModeSystemd, Destructive: true})
	}
	return commands
}

func safeCDNEndpoints(values []string) []string {
	return validHTTPSURLs(values)
}

func (s *Service) cachedReleases(repo string) ([]githubRelease, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cache.repo != repo || s.cache.checkedAt.IsZero() || s.now().Sub(s.cache.checkedAt) > 5*time.Minute {
		return nil, false
	}
	return append([]githubRelease(nil), s.cache.releases...), true
}

func (s *Service) storeReleases(repo string, releases []githubRelease) {
	s.cacheMu.Lock()
	s.cache = releaseCache{repo: repo, releases: append([]githubRelease(nil), releases...), checkedAt: s.now()}
	s.cacheMu.Unlock()
}

func (s *Service) clearCache() {
	s.cacheMu.Lock()
	s.cache = releaseCache{}
	s.cacheMu.Unlock()
}

func ensureUpdateDir(cfg runtimeConfig) (string, error) {
	if !safeAbsolutePath(cfg.InstallRoot) || cfg.InstallRoot == "/opt" || cfg.InstallRoot == "/usr" {
		return "", fmt.Errorf("拒绝使用不安全的安装目录")
	}
	if err := cfg.validateTargetPath(cfg.ServerPath); err != nil {
		return "", err
	}
	if cfg.UpdateWeb {
		if err := cfg.validateTargetPath(cfg.WebPath); err != nil {
			return "", err
		}
	}
	path := filepath.Join(cfg.InstallRoot, ".oneclickvirt-update")
	if !isWithin(path, cfg.InstallRoot) {
		return "", fmt.Errorf("更新目录越界")
	}
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
		return "", fmt.Errorf("更新目录不是受控目录")
	}
	return path, nil
}

func (s *Service) restoreState() {
	s.refreshStateFromDisk()
}

func (s *Service) refreshStateFromDisk() {
	cfg := loadRuntimeConfig()
	updateDir, err := ensureUpdateDir(cfg)
	if err != nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(updateDir, "state.json"))
	if err != nil {
		return
	}
	var diskState OperationState
	if json.Unmarshal(data, &diskState) != nil || diskState.ID == "" {
		return
	}
	if isActiveOperation(diskState) && (diskState.StartedAt.IsZero() || s.now().Sub(diskState.StartedAt) > maxOperationAge) {
		diskState.Status = OperationFailed
		diskState.Error = "更新工作进程超时或已中断，请检查服务日志和本地备份"
		finished := s.now()
		diskState.FinishedAt = &finished
		_ = writeOperationState(cfg, diskState)
	}

	s.stateMu.Lock()
	if shouldAdoptPersistedOperation(s.state, diskState) {
		s.state = diskState
	}
	s.stateMu.Unlock()
}

func (s *Service) persistState() {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	s.persistStateLocked()
}

func (s *Service) persistStateLocked() {
	cfg := loadRuntimeConfig()
	_ = writeOperationState(cfg, s.state)
}

func writeOperationState(cfg runtimeConfig, state OperationState) error {
	updateDir, err := ensureUpdateDir(cfg)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(updateDir, "state.json"), data, 0640)
}

func isActiveOperation(state OperationState) bool {
	return state.Status == OperationStaging || state.Status == OperationScheduled || state.Status == OperationApplying
}

func shouldAdoptPersistedOperation(current, persisted OperationState) bool {
	if persisted.ID == "" {
		return false
	}
	if current.ID == "" || current.ID == persisted.ID || current.StartedAt.IsZero() {
		return true
	}
	return persisted.StartedAt.After(current.StartedAt)
}

// RunWorker is called by the detached systemd worker process. Keeping this
// entrypoint in the controller binary preserves the same release/parser code
// while allowing the HTTP process to be restarted safely during replacement.
func (s *Service) RunWorker(operationID, action, target, backupID string) error {
	cfg := loadRuntimeConfig()
	if err := cfg.validateForWorker(); err != nil {
		return err
	}
	if action != "update" && action != "rollback" && action != "restart" {
		return fmt.Errorf("更新工作进程操作无效")
	}
	if action == "restart" && (target != "" || backupID != "") {
		return fmt.Errorf("重启工作进程包含意外参数")
	}
	operation := s.Operation()
	if operation.ID != operationID || operation.Action != action || operation.Target != target || operation.BackupID != backupID {
		return fmt.Errorf("更新工作进程参数与已提交操作不匹配")
	}
	if !isActiveOperation(operation) {
		return fmt.Errorf("更新工作进程操作已结束")
	}
	s.stateMu.Lock()
	s.state.Status = OperationApplying
	s.state.Message = "更新工作进程已启动"
	s.persistStateLocked()
	s.stateMu.Unlock()
	if action == "restart" {
		s.runRestartOperation(operationID)
		return operationError(s.Operation())
	}
	s.runReleaseOperation(operationID, target, action == "rollback")
	return operationError(s.Operation())
}

func operationError(state OperationState) error {
	if state.Status == OperationFailed {
		if state.Error != "" {
			return fmt.Errorf("%s", state.Error)
		}
		return fmt.Errorf("更新工作进程失败")
	}
	return nil
}

func launchWorker(cfg runtimeConfig, operation OperationState) error {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return fmt.Errorf("systemd-run 不可用")
	}
	if !safeServiceName.MatchString(cfg.ServiceName) || !safeAbsolutePath(cfg.ServerPath) {
		return fmt.Errorf("更新工作进程路径不安全")
	}
	unit := "oneclickvirt-update-" + safeVersionForPath(operation.ID)
	args := []string{
		"--unit=" + unit,
		"--collect",
		"--no-block",
		"--quiet",
		"--service-type=oneshot",
		"--property=TimeoutStartSec=20min",
	}
	for _, environment := range cfg.workerEnvironment() {
		args = append(args, "--setenv="+environment)
	}
	args = append(args,
		cfg.ServerPath,
		"update-worker",
		"--operation-id", operation.ID,
		"--action", operation.Action,
	)
	if operation.Target != "" {
		args = append(args, "--target", operation.Target)
	}
	if operation.BackupID != "" {
		args = append(args, "--backup-id", operation.BackupID)
	}
	command := exec.Command("systemd-run", args...)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("启动更新工作进程失败: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func currentVersionFile(cfg runtimeConfig) string {
	return filepath.Join(cfg.InstallRoot, "VERSION")
}

func readInstalledVersion(cfg runtimeConfig) string {
	data, err := os.ReadFile(currentVersionFile(cfg))
	if err == nil {
		version := strings.TrimSpace(string(data))
		if version != "" && normalizeTag(version) != "unknown" && releaseTagPattern.MatchString(version) {
			return version
		}
	}
	return currentVersion()
}
