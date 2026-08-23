package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

type stagedUpdate struct {
	Directory string
	Server    string
	Web       string
	Version   string
}

var execCommand = exec.Command

func (s *Service) StartUpdate(ctx context.Context, target string) (OperationState, error) {
	cfg := loadRuntimeConfig()
	if !cfg.automaticAllowed() {
		return s.Operation(), fmt.Errorf("当前部署模式不支持面板自动升级: %s", s.Capability().Reason)
	}
	target = strings.TrimSpace(target)
	if target != "" && !releaseTagPattern.MatchString(target) {
		return s.Operation(), fmt.Errorf("版本号格式无效")
	}
	if target != "" && compareVersions(target, readInstalledVersion(cfg)) <= 0 {
		return s.Operation(), fmt.Errorf("升级目标必须高于当前版本")
	}
	operation, err := s.beginOperation("update", target)
	if err != nil {
		return operation, err
	}
	if err := launchWorker(cfg, operation); err != nil {
		s.updateOperation(operation.ID, OperationFailed, "无法启动更新工作进程", err)
		return s.Operation(), err
	}
	return operation, nil
}

func (s *Service) StartRollback(ctx context.Context, target, backupID string) (OperationState, error) {
	cfg := loadRuntimeConfig()
	if !cfg.automaticAllowed() {
		return s.Operation(), fmt.Errorf("当前部署模式不支持面板自动回退: %s", s.Capability().Reason)
	}
	target = strings.TrimSpace(target)
	backupID = strings.TrimSpace(backupID)
	if backupID != "" {
		if !safeBackupID(backupID) {
			return s.Operation(), fmt.Errorf("本地备份标识无效")
		}
		backup, _, ok := s.backupByID(cfg, backupID)
		if !ok {
			return s.Operation(), fmt.Errorf("本地备份不存在或已损坏")
		}
		if !releaseTagPattern.MatchString(backup.Version) {
			return s.Operation(), fmt.Errorf("本地备份版本号无效")
		}
		if target != "" && normalizeTag(target) != normalizeTag(backup.Version) {
			return s.Operation(), fmt.Errorf("回退版本与本地备份不一致")
		}
		target = backup.Version
		if compareVersions(target, readInstalledVersion(cfg)) >= 0 {
			return s.Operation(), fmt.Errorf("回退目标必须低于当前版本")
		}
	} else {
		if target == "" || !releaseTagPattern.MatchString(target) {
			return s.Operation(), fmt.Errorf("必须指定有效的回退版本")
		}
		if compareVersions(target, readInstalledVersion(cfg)) >= 0 {
			return s.Operation(), fmt.Errorf("回退目标必须低于当前版本")
		}
	}
	operation, err := s.beginOperation("rollback", target)
	if err != nil {
		return operation, err
	}
	operation.BackupID = backupID
	s.stateMu.Lock()
	s.state.BackupID = backupID
	s.persistStateLocked()
	s.stateMu.Unlock()
	if err := launchWorker(cfg, operation); err != nil {
		s.updateOperation(operation.ID, OperationFailed, "无法启动回退工作进程", err)
		return s.Operation(), err
	}
	return operation, nil
}

func (s *Service) StartRestart(ctx context.Context) (OperationState, error) {
	cfg := loadRuntimeConfig()
	if !cfg.automaticAllowed() {
		return s.Operation(), fmt.Errorf("当前部署模式不支持面板重启: %s", s.Capability().Reason)
	}
	operation, err := s.beginOperation("restart", "")
	if err != nil {
		return operation, err
	}
	if err := launchWorker(cfg, operation); err != nil {
		s.updateOperation(operation.ID, OperationFailed, "无法启动重启工作进程", err)
		return s.Operation(), err
	}
	return operation, nil
}

func (s *Service) runReleaseOperation(operationID, target string, rollback bool) {
	cfg := loadRuntimeConfig()
	backupID := s.Operation().BackupID
	s.updateOperation(operationID, OperationStaging, "正在获取并校验发布资产", nil)
	if rollback && backupID != "" {
		if err := s.applyBackup(cfg, operationID, backupID); err != nil {
			s.updateOperation(operationID, OperationFailed, "本地备份回退失败", err)
			return
		}
		s.updateOperation(operationID, OperationSucceeded, "已从本地备份回退并重启", nil)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	releases, err := s.fetchReleases(ctx, cfg)
	if err != nil {
		s.updateOperation(operationID, OperationFailed, "获取版本发布信息失败", err)
		return
	}
	release, ok := selectRelease(releases, target, rollback, readInstalledVersion(cfg))
	if !ok {
		s.updateOperation(operationID, OperationFailed, "目标版本不存在或不满足回退条件", fmt.Errorf("target %s", target))
		return
	}
	if target == "" {
		target = release.TagName
		s.updateOperationTarget(operationID, target)
	}

	staged, err := s.stageRelease(ctx, cfg, release)
	if err != nil {
		s.updateOperation(operationID, OperationFailed, "发布资产暂存失败", err)
		return
	}
	defer os.RemoveAll(staged.Directory)
	s.updateOperation(operationID, OperationScheduled, "发布资产已校验，准备切换并重启", nil)
	if err := s.applyStaged(cfg, staged); err != nil {
		s.updateOperation(operationID, OperationFailed, "切换或健康检查失败，已尝试恢复原版本", err)
		return
	}
	s.updateOperation(operationID, OperationSucceeded, "版本切换和服务重启已完成", nil)
}

func (s *Service) runRestartOperation(operationID string) {
	cfg := loadRuntimeConfig()
	s.updateOperation(operationID, OperationApplying, "正在重启主控服务", nil)
	if err := restartServices(cfg); err != nil {
		s.updateOperation(operationID, OperationFailed, "服务重启失败", err)
		return
	}
	if err := waitForHealth(cfg, 45*time.Second); err != nil {
		s.updateOperation(operationID, OperationFailed, "服务已重启但健康检查失败", err)
		return
	}
	s.updateOperation(operationID, OperationSucceeded, "主控服务已重启", nil)
}

func (s *Service) stageRelease(ctx context.Context, cfg runtimeConfig, release githubRelease) (stagedUpdate, error) {
	stageDir, err := createUpdateTempDir(cfg, "stage-")
	if err != nil {
		return stagedUpdate{}, err
	}
	cleanup := func() {
		_ = os.RemoveAll(stageDir)
	}
	if err := os.MkdirAll(filepath.Join(stageDir, "web"), 0750); err != nil {
		cleanup()
		return stagedUpdate{}, err
	}
	assets, err := requiredAssets(release, cfg)
	if err != nil {
		cleanup()
		return stagedUpdate{}, err
	}
	checksumDigests := map[string]string{}
	if checksumAsset, ok := assets["checksums"]; ok {
		checksumPath := filepath.Join(stageDir, checksumAssetName)
		if err := s.downloadAssetLimit(ctx, checksumAsset, cfg, checksumPath, maxChecksumManifestSize); err != nil {
			cleanup()
			return stagedUpdate{}, err
		}
		names := []string{assets["server"].Name}
		if cfg.UpdateWeb {
			names = append(names, assets["web"].Name)
		}
		data, err := os.ReadFile(checksumPath)
		if err != nil {
			cleanup()
			return stagedUpdate{}, err
		}
		checksumDigests, err = parseChecksumManifest(data, names...)
		if err != nil {
			cleanup()
			return stagedUpdate{}, err
		}
	}
	serverArchive := filepath.Join(stageDir, "server.tar.gz")
	if err := s.downloadAsset(ctx, assets["server"], cfg, serverArchive); err != nil {
		cleanup()
		return stagedUpdate{}, err
	}
	if err := verifyReleaseAssetDigest(serverArchive, assets["server"], checksumDigests, cfg.AllowUnverified); err != nil {
		cleanup()
		return stagedUpdate{}, err
	}
	if err := extractServerArchive(serverArchive, stageDir); err != nil {
		cleanup()
		return stagedUpdate{}, err
	}
	webPath := ""
	if cfg.UpdateWeb {
		webArchive := filepath.Join(stageDir, "web.zip")
		if err := s.downloadAsset(ctx, assets["web"], cfg, webArchive); err != nil {
			cleanup()
			return stagedUpdate{}, err
		}
		if err := verifyReleaseAssetDigest(webArchive, assets["web"], checksumDigests, cfg.AllowUnverified); err != nil {
			cleanup()
			return stagedUpdate{}, err
		}
		if err := extractWebArchive(webArchive, filepath.Join(stageDir, "web")); err != nil {
			cleanup()
			return stagedUpdate{}, err
		}
		webPath = filepath.Join(stageDir, "web")
	}
	return stagedUpdate{Directory: stageDir, Server: filepath.Join(stageDir, "server"), Web: webPath, Version: release.TagName}, nil
}

func (s *Service) downloadAsset(ctx context.Context, asset githubAsset, cfg runtimeConfig, destination string) error {
	return s.downloadAssetLimit(ctx, asset, cfg, destination, maxServerArchiveSize)
}

func (s *Service) downloadAssetLimit(ctx context.Context, asset githubAsset, cfg runtimeConfig, destination string, maxSize int64) error {
	if asset.Size < 0 || asset.Size > maxSize {
		return fmt.Errorf("发布资产大小异常")
	}
	var lastErr error
	for _, endpoint := range s.assetURLs(asset, cfg) {
		if !isHTTPSURL(endpoint) {
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			lastErr = err
			continue
		}
		request.Header.Set("User-Agent", "oneclickvirt-update/"+currentVersion())
		response, err := s.httpClientFor(cfg).Do(request)
		if err != nil {
			lastErr = err
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			lastErr = fmt.Errorf("下载发布资产返回 %s", response.Status)
			continue
		}
		if response.ContentLength > maxSize {
			response.Body.Close()
			lastErr = fmt.Errorf("发布资产响应过大")
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0750); err != nil {
			response.Body.Close()
			return err
		}
		temporary, err := os.CreateTemp(filepath.Dir(destination), ".download-*")
		if err != nil {
			response.Body.Close()
			return err
		}
		_, copyErr := io.CopyN(temporary, io.LimitReader(response.Body, maxSize+1), maxSize+1)
		response.Body.Close()
		closeErr := temporary.Close()
		if copyErr != nil && copyErr != io.EOF {
			lastErr = copyErr
			os.Remove(temporary.Name())
			continue
		}
		if closeErr != nil {
			lastErr = closeErr
			os.Remove(temporary.Name())
			continue
		}
		stat, statErr := os.Stat(temporary.Name())
		if statErr != nil || stat.Size() <= 0 || stat.Size() > maxSize {
			lastErr = fmt.Errorf("下载资产大小异常")
			os.Remove(temporary.Name())
			continue
		}
		if err := os.Rename(temporary.Name(), destination); err != nil {
			lastErr = err
			os.Remove(temporary.Name())
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的 HTTPS 发布下载地址")
	}
	return fmt.Errorf("下载发布资产失败: %w", lastErr)
}

func verifyReleaseAssetDigest(path string, asset githubAsset, manifest map[string]string, allowUnverified bool) error {
	digest := manifest[asset.Name]
	if digest == "" {
		if allowUnverified {
			return nil
		}
		return fmt.Errorf("发布资产缺少 SHA-256 校验清单条目: %s", asset.Name)
	}
	return verifyDigest(path, digest, false)
}

func selectRelease(releases []githubRelease, target string, rollback bool, current string) (githubRelease, bool) {
	if target == "" && !rollback {
		release, ok := latestStableRelease(releases)
		return release, ok && compareVersions(release.TagName, current) > 0
	}
	for _, release := range releases {
		if normalizeTag(release.TagName) != normalizeTag(target) {
			continue
		}
		if rollback && compareVersions(release.TagName, current) >= 0 {
			return githubRelease{}, false
		}
		return release, true
	}
	return githubRelease{}, false
}

func (s *Service) applyStaged(cfg runtimeConfig, staged stagedUpdate) error {
	if err := cfg.validateForWorker(); err != nil {
		return err
	}
	if err := validateServerBinary(staged.Server); err != nil {
		return err
	}
	if err := cfg.validateTargetPath(cfg.ServerPath); err != nil {
		return err
	}
	if cfg.UpdateWeb {
		if err := cfg.validateTargetPath(cfg.WebPath); err != nil {
			return err
		}
		if !directoryExists(staged.Web) {
			return fmt.Errorf("暂存 Web 目录不存在")
		}
	}
	backup, backupDir, err := s.createBackup(cfg, readInstalledVersion(cfg))
	if err != nil {
		return err
	}
	s.updateOperationByMessage("正在停止服务并原子切换文件")
	if err := stopServices(cfg); err != nil {
		return err
	}
	serverOld := cfg.ServerPath + ".ocv-old"
	webOld := cfg.WebPath + ".ocv-old"
	_ = os.Remove(serverOld)
	_ = os.RemoveAll(webOld)
	serverOriginalMoved := false
	serverReplaced := false
	webOriginalMoved := false
	webReplaced := false
	restore := func() error {
		var restoreErrors []error
		addRestoreError := func(operation string, restoreErr error) {
			if restoreErr != nil && !os.IsNotExist(restoreErr) {
				restoreErrors = append(restoreErrors, fmt.Errorf("%s: %w", operation, restoreErr))
			}
		}
		if serverOriginalMoved && fileExists(serverOld) {
			addRestoreError("删除失败的新主控", os.Remove(cfg.ServerPath))
			if err := os.Rename(serverOld, cfg.ServerPath); err != nil {
				if copyErr := copyFile(filepath.Join(backupDir, "server"), cfg.ServerPath, 0755); copyErr != nil {
					addRestoreError("恢复主控二进制", copyErr)
				}
			}
		} else if serverReplaced {
			addRestoreError("删除失败的新主控", os.Remove(cfg.ServerPath))
			addRestoreError("恢复主控二进制", copyFile(filepath.Join(backupDir, "server"), cfg.ServerPath, 0755))
		}
		if cfg.UpdateWeb {
			restoredWeb := false
			if webOriginalMoved && directoryExists(webOld) {
				addRestoreError("删除失败的 Web 目录", os.RemoveAll(cfg.WebPath))
				if err := os.Rename(webOld, cfg.WebPath); err == nil {
					restoredWeb = true
				} else {
					addRestoreError("恢复 Web 目录", err)
				}
			}
			if !restoredWeb && (webReplaced || webOriginalMoved) {
				addRestoreError("删除失败的 Web 目录", os.RemoveAll(cfg.WebPath))
				if directoryExists(filepath.Join(backupDir, "web")) {
					addRestoreError("恢复 Web 目录", copyDir(filepath.Join(backupDir, "web"), cfg.WebPath))
				}
			}
		}
		addRestoreError("恢复后重启主控服务", restartServices(cfg))
		return errors.Join(restoreErrors...)
	}
	failedAfterStop := func(operationErr error) error {
		if restoreErr := restore(); restoreErr != nil {
			return fmt.Errorf("%w；恢复失败: %v", operationErr, restoreErr)
		}
		return operationErr
	}
	if err := os.Rename(cfg.ServerPath, serverOld); err != nil {
		return failedAfterStop(fmt.Errorf("准备替换主控二进制失败: %w", err))
	}
	serverOriginalMoved = true
	if err := os.Rename(staged.Server, cfg.ServerPath); err != nil {
		return failedAfterStop(fmt.Errorf("替换主控二进制失败: %w", err))
	}
	serverReplaced = true
	if cfg.UpdateWeb {
		if directoryExists(cfg.WebPath) {
			if err := os.Rename(cfg.WebPath, webOld); err != nil {
				return failedAfterStop(fmt.Errorf("准备替换 Web 文件失败: %w", err))
			}
			webOriginalMoved = true
		}
		if err := os.Rename(staged.Web, cfg.WebPath); err != nil {
			return failedAfterStop(fmt.Errorf("替换 Web 文件失败: %w", err))
		}
		webReplaced = true
	}
	previousVersion := readInstalledVersion(cfg)
	if err := writeInstalledVersion(cfg, staged.Version); err != nil {
		logUpdate("warn", "写入版本标记失败", zap.Error(err))
	}
	if err := restartServices(cfg); err != nil {
		versionErr := writeInstalledVersion(cfg, previousVersion)
		restoreErr := restore()
		if versionErr != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("恢复版本标记: %w", versionErr))
		}
		if restoreErr != nil {
			return fmt.Errorf("%w；恢复失败: %v", err, restoreErr)
		}
		return err
	}
	if err := waitForHealth(cfg, 60*time.Second); err != nil {
		versionErr := writeInstalledVersion(cfg, previousVersion)
		restoreErr := restore()
		if versionErr != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("恢复版本标记: %w", versionErr))
		}
		if restoreErr != nil {
			return fmt.Errorf("%w；恢复失败: %v", err, restoreErr)
		}
		return err
	}
	_ = os.Remove(serverOld)
	_ = os.RemoveAll(webOld)
	_ = backup
	return nil
}

func (s *Service) applyBackup(cfg runtimeConfig, operationID, backupID string) error {
	backup, backupDir, ok := s.backupByID(cfg, backupID)
	if !ok {
		return fmt.Errorf("本地备份不存在或已损坏")
	}
	stageDir, err := createUpdateTempDir(cfg, "rollback-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDir)
	if err := copyFile(filepath.Join(backupDir, "server"), filepath.Join(stageDir, "server"), 0755); err != nil {
		return err
	}
	webPath := ""
	if cfg.UpdateWeb && directoryExists(filepath.Join(backupDir, "web")) {
		webPath = filepath.Join(stageDir, "web")
		if err := copyDir(filepath.Join(backupDir, "web"), webPath); err != nil {
			return err
		}
	}
	staged := stagedUpdate{Directory: stageDir, Server: filepath.Join(stageDir, "server"), Web: webPath, Version: backup.Version}
	if err := s.applyStaged(cfg, staged); err != nil {
		return fmt.Errorf("备份 %s: %w", backupID, err)
	}
	_ = operationID
	return nil
}

func createUpdateTempDir(cfg runtimeConfig, prefix string) (string, error) {
	updateDir, err := ensureUpdateDir(cfg)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(updateDir, 0750); err != nil {
		return "", fmt.Errorf("创建更新暂存目录失败: %w", err)
	}
	stageDir, err := os.MkdirTemp(updateDir, prefix)
	if err != nil {
		return "", fmt.Errorf("创建更新暂存目录失败: %w", err)
	}
	return stageDir, nil
}

func (s *Service) updateOperationTarget(id, target string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.state.ID == id {
		s.state.Target = target
		s.persistStateLocked()
	}
}

func (s *Service) updateOperationByMessage(message string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.state.Status == OperationScheduled {
		s.state.Status = OperationApplying
	}
	s.state.Message = message
	s.persistStateLocked()
}

func stopServices(cfg runtimeConfig) error {
	if _, err := cfg.commandForService("stop"); err != nil {
		return err
	}
	if err := runSystemctl("stop", cfg.ServiceName); err != nil {
		return fmt.Errorf("停止主控服务失败: %w", err)
	}
	return nil
}

func restartServices(cfg runtimeConfig) error {
	if _, err := cfg.commandForService("restart"); err != nil {
		return err
	}
	if err := runSystemctl("restart", cfg.ServiceName); err != nil {
		return fmt.Errorf("重启主控服务失败: %w", err)
	}
	for _, service := range cfg.ProxyServices {
		if err := runSystemctl("reload", service); err != nil {
			logUpdate("warn", "反向代理 reload 失败，主控已继续运行", zap.String("service", service), zap.Error(err))
		}
	}
	return nil
}

func runSystemctl(action, service string) error {
	systemctlPath, err := exec.LookPath("systemctl")
	if err != nil {
		return fmt.Errorf("systemctl 不可用")
	}
	command := execCommand(systemctlPath, action, service)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s %s: %w (%s)", action, service, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func waitForHealth(cfg runtimeConfig, timeout time.Duration) error {
	port := cfg.HealthPort
	if port <= 0 || port > 65535 {
		port = 8888
	}
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port))
		if err == nil {
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("等待主控健康检查超时")
}

func writeInstalledVersion(cfg runtimeConfig, version string) error {
	if strings.TrimSpace(version) == "" {
		return nil
	}
	return atomicWrite(currentVersionFile(cfg), []byte(version+"\n"), 0640)
}
