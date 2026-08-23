package update

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	"oneclickvirt/utils"
)

var safeServiceName = regexp.MustCompile(`^[A-Za-z0-9_.@:-]+$`)
var safeRepository = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}/[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

type runtimeConfig struct {
	Mode            string
	Flavor          string
	UpdateWeb       bool
	InstallRoot     string
	ServerPath      string
	WebPath         string
	ServiceName     string
	ServiceFile     string
	ScriptPath      string
	ProxyServices   []string
	CDNEndpoints    []string
	APIEndpoints    []string
	HealthPort      int
	AllowUnverified bool
	Repo            string
}

func loadRuntimeConfig() runtimeConfig {
	installRoot := envOr("ONECLICKVIRT_INSTALL_ROOT", "/opt/oneclickvirt")
	serverPath := envOr("ONECLICKVIRT_SERVER_BIN", filepath.Join(installRoot, "server", "oneclickvirt-server"))
	webPath := envOr("ONECLICKVIRT_WEB_DIR", filepath.Join(installRoot, "web"))
	serviceName := envOr("ONECLICKVIRT_SERVICE_NAME", "oneclickvirt")
	serviceFile := envOr("ONECLICKVIRT_SERVICE_FILE", filepath.Join("/etc/systemd/system", serviceName+".service"))
	flavor := strings.ToLower(strings.TrimSpace(os.Getenv("ONECLICKVIRT_UPDATE_FLAVOR")))
	if flavor != FlavorStandalone && flavor != FlavorAllInOne {
		if detected := releaseFlavorFromFile(filepath.Join(installRoot, "SERVER_ASSET")); detected != "" {
			flavor = detected
		} else if fileExists(filepath.Join(webPath, "index.html")) {
			flavor = FlavorStandalone
		} else {
			flavor = FlavorAllInOne
		}
	}
	updateWeb := flavor == FlavorStandalone
	if configured, ok := optionalBoolEnv("ONECLICKVIRT_UPDATE_WEB"); ok {
		updateWeb = configured
	}

	mode := normalizeDeploymentMode(os.Getenv("ONECLICKVIRT_UPDATE_MODE"))
	if mode == "" {
		mode = detectDeploymentMode(installRoot, serverPath, serviceFile)
	}
	if isExplicitlyDisabled() {
		mode = ModeDisabled
	}

	proxyServices := splitList(os.Getenv("ONECLICKVIRT_PROXY_SERVICES"))
	if len(proxyServices) == 0 {
		if service := strings.TrimSpace(os.Getenv("ONECLICKVIRT_PROXY_SERVICE")); service != "" {
			proxyServices = []string{service}
		}
	}
	for _, service := range proxyServices {
		if !safeServiceName.MatchString(service) {
			proxyServices = nil
			break
		}
	}

	cdnEndpoints := make([]string, 0, 8)
	cdnEndpoints = append(cdnEndpoints, splitList(os.Getenv("ONECLICKVIRT_UPDATE_PROXY"))...)
	cdnEndpoints = append(cdnEndpoints, utils.GetCDNEndpoints()...)
	cdnEndpoints = validHTTPSURLs(uniqueStrings(cdnEndpoints))
	apiEndpoints := splitList(os.Getenv("ONECLICKVIRT_UPDATE_API_ENDPOINTS"))
	if len(apiEndpoints) == 0 {
		apiEndpoints = []string{
			"https://api.github.com",
			"https://githubapi.spiritlhl.workers.dev",
			"https://githubapi.spiritlhl.top",
		}
	}
	apiEndpoints = validHTTPSURLs(apiEndpoints)
	healthPort := global.GetAppConfig().System.Addr
	if envPort := strings.TrimSpace(os.Getenv("ONECLICKVIRT_UPDATE_HEALTH_PORT")); envPort != "" {
		if parsed, err := strconv.Atoi(envPort); err == nil {
			healthPort = parsed
		}
	}
	if healthPort <= 0 || healthPort > 65535 {
		healthPort = 8888
	}
	repo := envOr("ONECLICKVIRT_UPDATE_REPO", "oneclickvirt/oneclickvirt")
	if !validRepository(repo) {
		repo = "oneclickvirt/oneclickvirt"
	}

	return runtimeConfig{
		Mode:   mode,
		Flavor: flavor,
		// The standalone release always contains web-dist.zip. An all-in-one
		// controller may coexist with an unrelated static directory, which must
		// never be changed by the panel update worker.
		UpdateWeb:       updateWeb,
		InstallRoot:     filepath.Clean(installRoot),
		ServerPath:      filepath.Clean(serverPath),
		WebPath:         filepath.Clean(webPath),
		ServiceName:     serviceName,
		ServiceFile:     filepath.Clean(serviceFile),
		ScriptPath:      envOr("ONECLICKVIRT_UPDATE_SCRIPT", filepath.Join(installRoot, "scripts", "install.sh")),
		ProxyServices:   proxyServices,
		CDNEndpoints:    cdnEndpoints,
		APIEndpoints:    apiEndpoints,
		HealthPort:      healthPort,
		AllowUnverified: parseBoolEnv("ONECLICKVIRT_UPDATE_ALLOW_UNVERIFIED"),
		Repo:            repo,
	}
}

func (cfg runtimeConfig) automaticAllowed() bool {
	if cfg.Mode != ModeSystemd || runtime.GOOS != "linux" || os.Geteuid() != 0 || isContainerRuntime() {
		return false
	}
	if !safeServiceName.MatchString(cfg.ServiceName) || !validRepository(cfg.Repo) || !safeAbsolutePath(cfg.InstallRoot) || !safeAbsolutePath(cfg.ServerPath) || !safeAbsolutePath(cfg.ServiceFile) {
		return false
	}
	if cfg.UpdateWeb && (!safeAbsolutePath(cfg.WebPath) || !directoryExists(cfg.WebPath)) {
		return false
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return false
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	if !fileExists(cfg.ServerPath) || !fileExists(cfg.ServiceFile) || !noSymlink(cfg.ServiceFile) {
		return false
	}
	if !serviceFileMatches(cfg.ServiceFile, cfg.ServerPath) {
		return false
	}
	if !isWithin(cfg.ServerPath, cfg.InstallRoot) || (cfg.UpdateWeb && !isWithin(cfg.WebPath, cfg.InstallRoot)) {
		return false
	}
	return noSymlinkWithin(cfg.InstallRoot, cfg.ServerPath) && (!cfg.UpdateWeb || noSymlinkWithin(cfg.InstallRoot, cfg.WebPath))
}

func detectDeploymentMode(installRoot, serverPath, serviceFile string) string {
	return classifyDeploymentMode(
		runtime.GOOS,
		isContainerRuntime(),
		strings.TrimSpace(os.Getenv("COMPOSE_PROJECT_NAME")) != "",
		fileExists(serverPath),
		fileExists(serviceFile),
		serviceFileMatches(serviceFile, serverPath),
		installRoot,
	)
}

// classifyDeploymentMode is split from host probing so its conservative mode
// rules remain unit-testable. Docker Compose does not reliably inject a
// detectable marker into containers, so callers may force it with
// ONECLICKVIRT_UPDATE_MODE=compose when COMPOSE_PROJECT_NAME is unavailable.
func classifyDeploymentMode(goos string, inContainer, composeHint, serverExists, serviceExists, serviceMatches bool, installRoot string) string {
	if inContainer {
		if composeHint {
			return ModeCompose
		}
		return ModeDocker
	}
	if goos == "linux" && serverExists && serviceExists && serviceMatches {
		return ModeSystemd
	}
	if serverExists {
		return ModeSource
	}
	if strings.TrimSpace(installRoot) == "" {
		return ModeUnknown
	}
	return ModeUnknown
}

func normalizeDeploymentMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", ModeSystemd, ModeDocker, ModeCompose, ModeSource, ModeEmbedded, ModeUnknown, ModeDisabled:
		return value
	default:
		return ModeUnknown
	}
}

func serviceFileMatches(path, serverPath string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	expected := filepath.Clean(serverPath)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "ExecStart="))
		if value == expected || strings.HasPrefix(value, expected+" ") {
			return true
		}
	}
	return false
}

func isContainerRuntime() bool {
	if fileExists("/.dockerenv") || strings.EqualFold(os.Getenv("container"), "docker") || os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	text := strings.ToLower(string(data))
	return strings.Contains(text, "docker") || strings.Contains(text, "containerd") || strings.Contains(text, "kubepods")
}

func releaseFlavorFromFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if strings.Contains(strings.ToLower(string(data)), "allinone") {
		return FlavorAllInOne
	}
	if strings.Contains(strings.ToLower(string(data)), "server-linux") {
		return FlavorStandalone
	}
	return ""
}

func isExplicitlyDisabled() bool {
	value, ok := os.LookupEnv("ONECLICKVIRT_UPDATE_ENABLED")
	return ok && strings.EqualFold(strings.TrimSpace(value), "false")
}

func safeAbsolutePath(path string) bool {
	clean := filepath.Clean(path)
	return filepath.IsAbs(clean) && clean != "/" && clean != "." && !strings.ContainsAny(clean, "\x00\r\n")
}

func isWithin(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func noSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0
}

// noSymlinkWithin verifies every existing component between the controlled
// installation root and a target. Checking just the leaf would allow a
// symlinked server or web directory to escape the installation root.
func noSymlinkWithin(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if !isWithin(path, root) || !noSymlink(root) {
		return false
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." {
		return err == nil
	}
	current := root
	for _, part := range strings.Split(relative, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if !noSymlink(current) {
			return false
		}
	}
	return true
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func splitList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == '\t' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, strings.TrimRight(trimmed, "/"))
		}
	}
	return result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func parseBoolEnv(name string) bool {
	value, ok := optionalBoolEnv(name)
	return ok && value
}

func optionalBoolEnv(name string) (bool, bool) {
	value, exists := os.LookupEnv(name)
	if !exists {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true, true
	case "0", "false", "no":
		return false, true
	default:
		return false, false
	}
}

func validHTTPSURLs(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if isHTTPSURL(value) {
			result = append(result, strings.TrimRight(strings.TrimSpace(value), "/"))
		}
	}
	return uniqueStrings(result)
}

func (cfg runtimeConfig) validateTargetPath(path string) error {
	if !safeAbsolutePath(path) || !isWithin(path, cfg.InstallRoot) {
		return errors.New("更新路径必须位于受控安装目录内")
	}
	return nil
}

func (cfg runtimeConfig) validateForWorker() error {
	if !cfg.automaticAllowed() {
		return errors.New("更新计划不是受控 systemd 安装")
	}
	if !safeAbsolutePath(cfg.InstallRoot) || !safeAbsolutePath(cfg.ServerPath) || !isWithin(cfg.ServerPath, cfg.InstallRoot) {
		return errors.New("更新计划包含不安全的主控路径")
	}
	if cfg.UpdateWeb && (!safeAbsolutePath(cfg.WebPath) || !isWithin(cfg.WebPath, cfg.InstallRoot)) {
		return errors.New("更新计划包含不安全的 Web 路径")
	}
	if cfg.HealthPort <= 0 || cfg.HealthPort > 65535 {
		return errors.New("更新计划包含无效健康检查端口")
	}
	return nil
}

func validRepository(value string) bool {
	if !safeRepository.MatchString(value) {
		return false
	}
	parts := strings.Split(value, "/")
	return len(parts) == 2 && parts[0] != "." && parts[0] != ".." && parts[1] != "." && parts[1] != ".."
}

func (cfg runtimeConfig) commandForService(action string) (string, error) {
	if !safeServiceName.MatchString(cfg.ServiceName) {
		return "", fmt.Errorf("服务名不安全")
	}
	if action != "restart" && action != "start" && action != "stop" && action != "reload" {
		return "", fmt.Errorf("不支持的服务操作")
	}
	return fmt.Sprintf("systemctl %s %s", action, shellQuote(cfg.ServiceName)), nil
}

// workerEnvironment pins the transient worker to the capability that the
// authenticated server process already validated. systemd-run does not inherit
// an application's EnvironmentFile by default, so these values must travel
// with the detached process for custom roots, proxy settings, and ports to
// remain consistent across the restart.
func (cfg runtimeConfig) workerEnvironment() []string {
	return []string{
		"ONECLICKVIRT_UPDATE_ENABLED=true",
		"ONECLICKVIRT_UPDATE_MODE=" + ModeSystemd,
		"ONECLICKVIRT_UPDATE_FLAVOR=" + cfg.Flavor,
		"ONECLICKVIRT_UPDATE_WEB=" + strconv.FormatBool(cfg.UpdateWeb),
		"ONECLICKVIRT_INSTALL_ROOT=" + cfg.InstallRoot,
		"ONECLICKVIRT_SERVER_BIN=" + cfg.ServerPath,
		"ONECLICKVIRT_WEB_DIR=" + cfg.WebPath,
		"ONECLICKVIRT_SERVICE_NAME=" + cfg.ServiceName,
		"ONECLICKVIRT_SERVICE_FILE=" + cfg.ServiceFile,
		"ONECLICKVIRT_PROXY_SERVICES=" + strings.Join(cfg.ProxyServices, ","),
		"ONECLICKVIRT_UPDATE_PROXY=" + strings.Join(cfg.CDNEndpoints, ","),
		"ONECLICKVIRT_UPDATE_API_ENDPOINTS=" + strings.Join(cfg.APIEndpoints, ","),
		"ONECLICKVIRT_UPDATE_HEALTH_PORT=" + strconv.Itoa(cfg.HealthPort),
		"ONECLICKVIRT_UPDATE_ALLOW_UNVERIFIED=" + strconv.FormatBool(cfg.AllowUnverified),
		"ONECLICKVIRT_UPDATE_REPO=" + cfg.Repo,
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func currentVersion() string {
	return constant.ServerVersion
}
