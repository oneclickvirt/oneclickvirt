package vmware

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/provider/health"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

const (
	defaultLibraryPath = "/var/lib/oneclickvirt/vmware"
)

type VMwareProvider struct {
	config        provider.NodeConfig
	executor      utils.ShellExecutor
	connected     bool
	healthChecker health.HealthChecker
	version       string
	mu            sync.RWMutex
}

func NewVMwareProvider() provider.Provider {
	return &VMwareProvider{}
}

func (p *VMwareProvider) GetType() string {
	return "vmware"
}

func (p *VMwareProvider) GetName() string {
	return p.config.Name
}

func (p *VMwareProvider) GetSupportedInstanceTypes() []string {
	return []string{"vm"}
}

func (p *VMwareProvider) Connect(ctx context.Context, config provider.NodeConfig) error {
	p.config = config

	sshConnectTimeout := config.SSHConnectTimeout
	sshExecuteTimeout := config.SSHExecuteTimeout
	if sshConnectTimeout <= 0 {
		sshConnectTimeout = 30
	}
	if sshExecuteTimeout <= 0 {
		sshExecuteTimeout = 300
	}

	client, err := utils.NewSSHClient(utils.SSHConfig{
		Host:           config.Host,
		Port:           config.Port,
		Username:       config.Username,
		Password:       config.Password,
		PrivateKey:     config.PrivateKey,
		ConnectTimeout: time.Duration(sshConnectTimeout) * time.Second,
		ExecuteTimeout: time.Duration(sshExecuteTimeout) * time.Second,
	})
	if err != nil {
		return fmt.Errorf("failed to connect via SSH: %w", err)
	}

	p.executor = client
	p.connected = true
	p.initHealthChecker(config, client.GetUnderlyingClient())
	if err := p.refreshVersion(); err != nil {
		global.APP_LOG.Warn("VMware version detection failed", zap.Error(err))
	}
	global.APP_LOG.Info("VMware provider connected",
		zap.String("host", utils.TruncateString(config.Host, 50)),
		zap.String("version", p.version))
	return nil
}

func (p *VMwareProvider) ConnectAgent(executor utils.ShellExecutor, config provider.NodeConfig) error {
	p.config = config
	p.executor = executor
	p.connected = true
	p.healthChecker = nil
	go func() {
		if err := p.refreshVersion(); err != nil {
			global.APP_LOG.Warn("VMware version detection failed in agent mode", zap.Error(err))
		}
	}()
	global.APP_LOG.Info("VMware provider loaded in agent mode",
		zap.String("name", config.Name))
	return nil
}

func (p *VMwareProvider) initHealthChecker(config provider.NodeConfig, sshClient interface{}) {
	healthConfig := health.HealthConfig{
		Host:          config.Host,
		Port:          config.Port,
		Username:      config.Username,
		Password:      config.Password,
		PrivateKey:    config.PrivateKey,
		APIEnabled:    false,
		SSHEnabled:    true,
		Timeout:       30 * time.Second,
		ServiceChecks: []string{"vmrun"},
	}
	zapLogger, _ := zap.NewProduction()
	_ = sshClient
	p.healthChecker = health.NewDockerHealthChecker(healthConfig, zapLogger)
}

func (p *VMwareProvider) Disconnect(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.executor != nil {
		_ = p.executor.Close()
	}
	p.executor = nil
	p.connected = false
	return nil
}

func (p *VMwareProvider) IsConnected() bool {
	p.mu.RLock()
	exec := p.executor
	connected := p.connected
	p.mu.RUnlock()
	return connected && exec != nil && exec.IsHealthy()
}

func (p *VMwareProvider) HealthCheck(ctx context.Context) (*health.HealthResult, error) {
	p.mu.RLock()
	exec := p.executor
	p.mu.RUnlock()
	if exec == nil || !exec.IsHealthy() {
		return &health.HealthResult{Status: health.HealthStatusUnhealthy, Timestamp: time.Now(), SSHStatus: "offline", APIStatus: "unknown"}, nil
	}
	if _, err := exec.ExecuteWithTimeout("command -v vmrun >/dev/null 2>&1", 10*time.Second); err != nil {
		return &health.HealthResult{Status: health.HealthStatusUnhealthy, Timestamp: time.Now(), SSHStatus: "online", APIStatus: "unknown", ServiceStatus: "vmrun missing"}, nil
	}
	return &health.HealthResult{Status: health.HealthStatusHealthy, Timestamp: time.Now(), SSHStatus: "online", APIStatus: "unknown", ServiceStatus: "vmrun"}, nil
}

func (p *VMwareProvider) GetHealthChecker() health.HealthChecker {
	return p.healthChecker
}

func (p *VMwareProvider) GetVersion() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.version
}

func (p *VMwareProvider) refreshVersion() error {
	exec := p.getExecutor()
	if exec == nil {
		return fmt.Errorf("executor not initialized")
	}
	out, err := exec.ExecuteWithTimeout("(vmrun -v 2>/dev/null || vmrun 2>&1 | head -1 || true)", 15*time.Second)
	if err != nil {
		p.setVersion("unknown")
		return err
	}
	line := firstNonEmptyLine(out)
	if line == "" {
		line = "unknown"
	}
	p.setVersion(line)
	return nil
}

func (p *VMwareProvider) setVersion(version string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.version = strings.TrimSpace(version)
}

func (p *VMwareProvider) ListInstances(ctx context.Context) ([]provider.Instance, error) {
	exec := p.getExecutor()
	if exec == nil {
		return nil, fmt.Errorf("VMware provider not connected")
	}

	runningOut, _ := exec.ExecuteWithTimeout("vmrun list 2>/dev/null || true", 30*time.Second)
	running := parseVMRunList(runningOut)
	runningSet := make(map[string]struct{}, len(running))
	for _, vmx := range running {
		runningSet[vmx] = struct{}{}
	}

	library := p.libraryPath()
	findCmd := fmt.Sprintf("test -d %s && find %s -type f -name '*.vmx' 2>/dev/null | sort || true", shellQuote(library), shellQuote(library))
	allOut, err := exec.ExecuteWithTimeout(findCmd, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to list VMware VMX files: %w", err)
	}

	seen := make(map[string]struct{})
	instances := make([]provider.Instance, 0)
	for _, vmx := range splitLines(allOut) {
		if _, ok := seen[vmx]; ok {
			continue
		}
		seen[vmx] = struct{}{}
		instances = append(instances, p.instanceFromVMX(vmx, runningSet))
	}
	for _, vmx := range running {
		if _, ok := seen[vmx]; ok {
			continue
		}
		instances = append(instances, p.instanceFromVMX(vmx, runningSet))
	}
	return instances, nil
}

func (p *VMwareProvider) CreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return p.CreateInstanceWithProgress(ctx, config, nil)
}

func (p *VMwareProvider) CreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progress provider.ProgressCallback) error {
	exec := p.getExecutor()
	if exec == nil {
		return fmt.Errorf("VMware provider not connected")
	}
	if strings.TrimSpace(config.Name) == "" {
		return fmt.Errorf("instance name is required")
	}

	updateProgress(progress, 10, "preparing VMware template")
	src := p.resolveTemplatePath(config)
	if src == "" {
		return fmt.Errorf("VMware template is required: set image_path to a .vmx file or use image name under storage templates")
	}

	dstDir := path.Join(p.libraryPath(), "instances", config.Name)
	dst := path.Join(dstDir, config.Name+".vmx")
	cloneCmd := fmt.Sprintf("mkdir -p %s && vmrun clone %s %s full -cloneName=%s",
		shellQuote(dstDir), shellQuote(src), shellQuote(dst), shellQuote(config.Name))
	updateProgress(progress, 35, "cloning VMware template")
	if out, err := exec.ExecuteWithTimeout(cloneCmd, 20*time.Minute); err != nil {
		global.APP_LOG.Warn("vmrun clone failed, falling back to directory copy",
			zap.String("output", utils.TruncateString(out, 500)),
			zap.Error(err))
		copyCmd := fmt.Sprintf("rm -rf %s && mkdir -p %s && cp -a %s/. %s/ && old=$(find %s -maxdepth 1 -type f -name '*.vmx' | head -1) && test -n \"$old\" && mv \"$old\" %s",
			shellQuote(dstDir), shellQuote(dstDir), shellQuote(path.Dir(src)), shellQuote(dstDir), shellQuote(dstDir), shellQuote(dst))
		if copyOut, copyErr := exec.ExecuteWithTimeout(copyCmd, 20*time.Minute); copyErr != nil {
			return fmt.Errorf("failed to clone VMware template: %w; fallback failed: %v; output: %s", err, copyErr, utils.TruncateString(copyOut, 500))
		}
	}

	updateProgress(progress, 65, "applying VMware resource settings")
	if err := p.applyResourceSettings(exec, dst, config); err != nil {
		global.APP_LOG.Warn("VMware resource settings partially failed", zap.Error(err))
	}

	updateProgress(progress, 85, "starting VMware instance")
	if err := p.StartInstance(ctx, dst); err != nil {
		return err
	}
	updateProgress(progress, 100, "VMware instance created")
	return nil
}

func (p *VMwareProvider) StartInstance(ctx context.Context, id string) error {
	return p.vmrunAction(id, "start", "nogui")
}

func (p *VMwareProvider) StopInstance(ctx context.Context, id string) error {
	if err := p.vmrunAction(id, "stop", "soft"); err != nil {
		return p.vmrunAction(id, "stop", "hard")
	}
	return nil
}

func (p *VMwareProvider) RestartInstance(ctx context.Context, id string) error {
	if err := p.StopInstance(ctx, id); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	return p.StartInstance(ctx, id)
}

func (p *VMwareProvider) DeleteInstance(ctx context.Context, id string) error {
	exec := p.getExecutor()
	if exec == nil {
		return fmt.Errorf("VMware provider not connected")
	}
	vmx, err := p.resolveVMX(id)
	if err != nil {
		return err
	}
	_ = p.StopInstance(ctx, vmx)
	cmd := fmt.Sprintf("vmrun deleteVM %s 2>/dev/null || rm -rf %s", shellQuote(vmx), shellQuote(path.Dir(vmx)))
	_, err = exec.ExecuteWithTimeout(cmd, 5*time.Minute)
	return err
}

func (p *VMwareProvider) GetInstance(ctx context.Context, id string) (*provider.Instance, error) {
	vmx, err := p.resolveVMX(id)
	if err != nil {
		return nil, err
	}
	runningOut, _ := p.getExecutor().ExecuteWithTimeout("vmrun list 2>/dev/null || true", 30*time.Second)
	runningSet := make(map[string]struct{})
	for _, item := range parseVMRunList(runningOut) {
		runningSet[item] = struct{}{}
	}
	inst := p.instanceFromVMX(vmx, runningSet)
	return &inst, nil
}

func (p *VMwareProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	exec := p.getExecutor()
	if exec == nil {
		return nil, fmt.Errorf("VMware provider not connected")
	}
	templatesDir := path.Join(p.libraryPath(), "templates")
	cmd := fmt.Sprintf("test -d %s && find %s -type f -name '*.vmx' 2>/dev/null | sort || true", shellQuote(templatesDir), shellQuote(templatesDir))
	out, err := exec.ExecuteWithTimeout(cmd, 30*time.Second)
	if err != nil {
		return nil, err
	}
	images := make([]provider.Image, 0)
	for _, vmx := range splitLines(out) {
		name := path.Base(path.Dir(vmx))
		if name == "." || name == "/" || name == "" {
			name = strings.TrimSuffix(path.Base(vmx), ".vmx")
		}
		images = append(images, provider.Image{
			ID:          vmx,
			Name:        name,
			Tag:         "template",
			Description: "VMware VMX template",
			Metadata:    map[string]string{"vmx": vmx},
		})
	}
	return images, nil
}

func (p *VMwareProvider) PullImage(ctx context.Context, image string) error {
	return fmt.Errorf("VMware provider uses local .vmx templates; place templates under %s/templates", p.libraryPath())
}

func (p *VMwareProvider) DeleteImage(ctx context.Context, id string) error {
	exec := p.getExecutor()
	if exec == nil {
		return fmt.Errorf("VMware provider not connected")
	}
	if !strings.HasSuffix(id, ".vmx") {
		id = p.resolveTemplatePath(provider.InstanceConfig{Image: id})
	}
	if id == "" {
		return fmt.Errorf("VMware template not found")
	}
	_, err := exec.ExecuteWithTimeout(fmt.Sprintf("rm -rf %s", shellQuote(path.Dir(id))), 5*time.Minute)
	return err
}

func (p *VMwareProvider) SetInstancePassword(ctx context.Context, instanceID, password string) error {
	return fmt.Errorf("VMware guest password management requires guest tools and is not supported by vmrun provider")
}

func (p *VMwareProvider) ResetInstancePassword(ctx context.Context, instanceID string) (string, error) {
	return "", fmt.Errorf("VMware guest password reset requires guest tools and is not supported by vmrun provider")
}

func (p *VMwareProvider) ExecuteSSHCommand(ctx context.Context, command string) (string, error) {
	exec := p.getExecutor()
	if exec == nil {
		return "", fmt.Errorf("VMware provider not connected")
	}
	return exec.Execute(command)
}

func (p *VMwareProvider) DiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	instances, err := p.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]provider.DiscoveredInstance, 0, len(instances))
	for _, inst := range instances {
		result = append(result, provider.DiscoveredInstance{
			UUID:         inst.ID,
			Name:         inst.Name,
			Status:       inst.Status,
			InstanceType: "vm",
			Image:        inst.Image,
			RawData:      inst.Metadata,
		})
	}
	return result, nil
}

func (p *VMwareProvider) getExecutor() utils.ShellExecutor {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.executor
}

func (p *VMwareProvider) libraryPath() string {
	if strings.TrimSpace(p.config.StoragePoolPath) != "" {
		return strings.TrimRight(p.config.StoragePoolPath, "/")
	}
	pool := strings.TrimSpace(p.config.StoragePool)
	if pool == "" || pool == "local" {
		return defaultLibraryPath
	}
	if strings.HasPrefix(pool, "/") || strings.HasPrefix(pool, "~") {
		return strings.TrimRight(pool, "/")
	}
	return path.Join(defaultLibraryPath, pool)
}

func (p *VMwareProvider) resolveTemplatePath(config provider.InstanceConfig) string {
	if strings.HasSuffix(config.ImagePath, ".vmx") {
		return config.ImagePath
	}
	if strings.HasSuffix(config.Image, ".vmx") {
		return config.Image
	}
	name := strings.TrimSpace(config.Image)
	if name == "" {
		return ""
	}
	base := path.Join(p.libraryPath(), "templates")
	return path.Join(base, name, name+".vmx")
}

func (p *VMwareProvider) resolveVMX(id string) (string, error) {
	exec := p.getExecutor()
	if exec == nil {
		return "", fmt.Errorf("VMware provider not connected")
	}
	id = strings.TrimSpace(id)
	if strings.HasSuffix(id, ".vmx") {
		return id, nil
	}
	library := p.libraryPath()
	cmd := fmt.Sprintf("find %s -type f -name '*.vmx' 2>/dev/null | awk -v n=%s 'BEGIN{found=\"\"} {base=$0; sub(/^.*\\//,\"\",base); sub(/\\.vmx$/,\"\",base); dir=$0; sub(/\\/[^\\/]*$/,\"\",dir); dbase=dir; sub(/^.*\\//,\"\",dbase); if (base==n || dbase==n) {found=$0; print; exit}}'",
		shellQuote(library), shellQuote(id))
	out, err := exec.ExecuteWithTimeout(cmd, 30*time.Second)
	if err != nil {
		return "", err
	}
	vmx := firstNonEmptyLine(out)
	if vmx == "" {
		return "", fmt.Errorf("VMware instance %s not found", id)
	}
	return vmx, nil
}

func (p *VMwareProvider) vmrunAction(id, action, mode string) error {
	exec := p.getExecutor()
	if exec == nil {
		return fmt.Errorf("VMware provider not connected")
	}
	vmx, err := p.resolveVMX(id)
	if err != nil {
		return err
	}
	cmd := fmt.Sprintf("vmrun %s %s %s", action, shellQuote(vmx), shellQuote(mode))
	_, err = exec.ExecuteWithTimeout(cmd, 5*time.Minute)
	return err
}

func (p *VMwareProvider) instanceFromVMX(vmx string, runningSet map[string]struct{}) provider.Instance {
	name := path.Base(path.Dir(vmx))
	if name == "." || name == "/" || name == "" {
		name = strings.TrimSuffix(path.Base(vmx), ".vmx")
	}
	status := "stopped"
	if _, ok := runningSet[vmx]; ok {
		status = "running"
	}
	return provider.Instance{
		ID:       vmx,
		Name:     name,
		Status:   status,
		Type:     "vm",
		Image:    "vmx",
		Created:  time.Now(),
		Metadata: map[string]string{"vmx": vmx},
	}
}

func (p *VMwareProvider) applyResourceSettings(exec utils.ShellExecutor, vmx string, config provider.InstanceConfig) error {
	cpu := parseFirstInt(config.CPU)
	memoryMB := parseFirstInt(config.Memory)
	updates := make([]string, 0)
	if cpu > 0 {
		updates = append(updates, fmt.Sprintf("numvcpus = \"%d\"", cpu))
	}
	if memoryMB > 0 {
		updates = append(updates, fmt.Sprintf("memsize = \"%d\"", memoryMB))
	}
	if len(updates) > 0 {
		script := fmt.Sprintf(`
set -e
vmx=%s
tmp="${vmx}.tmp"
cp "$vmx" "$tmp"
`, shellQuote(vmx))
		for _, line := range updates {
			key := strings.Split(line, " = ")[0]
			script += fmt.Sprintf("grep -q '^%s' \"$tmp\" && sed -i 's/^%s.*/%s/' \"$tmp\" || printf '%%s\\n' %s >> \"$tmp\"\n",
				key, key, shellEscapeForSed(line), shellQuote(line))
		}
		script += `mv "$tmp" "$vmx"` + "\n"
		if _, err := exec.ExecuteWithTimeout(script, 30*time.Second); err != nil {
			return err
		}
	}
	diskMB := parseFirstInt(config.Disk)
	if diskMB > 0 {
		diskGB := diskMB / 1024
		if diskGB < 1 {
			diskGB = 1
		}
		cmd := fmt.Sprintf("if command -v vmware-vdiskmanager >/dev/null 2>&1; then disk=$(find %s -maxdepth 1 -type f -name '*.vmdk' | head -1); test -n \"$disk\" && vmware-vdiskmanager -x %dGB \"$disk\" || true; fi",
			shellQuote(path.Dir(vmx)), diskGB)
		_, _ = exec.ExecuteWithTimeout(cmd, 5*time.Minute)
	}
	return nil
}

func parseVMRunList(output string) []string {
	lines := splitLines(output)
	result := make([]string, 0)
	for _, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), "total running vms:") {
			continue
		}
		if strings.HasSuffix(line, ".vmx") {
			result = append(result, line)
		}
	}
	return result
}

func splitLines(output string) []string {
	raw := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func firstNonEmptyLine(output string) string {
	for _, line := range splitLines(output) {
		return line
	}
	return ""
}

func parseFirstInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	re := regexp.MustCompile(`\d+`)
	raw := re.FindString(value)
	if raw == "" {
		return 0
	}
	n, _ := strconv.Atoi(raw)
	if strings.Contains(strings.ToLower(value), "gb") {
		return n * 1024
	}
	return n
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func shellEscapeForSed(s string) string {
	return strings.ReplaceAll(s, "/", "\\/")
}

func updateProgress(cb provider.ProgressCallback, pct int, msg string) {
	if cb != nil {
		cb(pct, msg)
	}
}

func init() {
	provider.RegisterProvider("vmware", NewVMwareProvider)
}
