package vmcli

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	rootProvider "oneclickvirt/provider"
	"oneclickvirt/provider/health"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

const defaultLibraryRoot = "/var/lib/oneclickvirt"

type Spec struct {
	Type           string
	DisplayName    string
	CLI            string
	VersionCommand string
	HealthCommand  string
}

type Provider struct {
	spec          Spec
	config        rootProvider.NodeConfig
	executor      utils.ShellExecutor
	connected     bool
	healthChecker health.HealthChecker
	version       string
	mu            sync.RWMutex
}

func New(spec Spec) rootProvider.Provider {
	return &Provider{spec: spec}
}

func VirtualBoxSpec() Spec {
	return Spec{
		Type:           "virtualbox",
		DisplayName:    "VirtualBox",
		CLI:            "VBoxManage",
		VersionCommand: "VBoxManage --version 2>/dev/null || true",
		HealthCommand:  "command -v VBoxManage >/dev/null 2>&1",
	}
}

func MultipassSpec() Spec {
	return Spec{
		Type:           "multipass",
		DisplayName:    "Multipass",
		CLI:            "multipass",
		VersionCommand: "multipass version 2>/dev/null | head -1 || true",
		HealthCommand:  "command -v multipass >/dev/null 2>&1",
	}
}

func VagrantSpec() Spec {
	return Spec{
		Type:           "vagrant",
		DisplayName:    "Vagrant",
		CLI:            "vagrant",
		VersionCommand: "vagrant --version 2>/dev/null || true",
		HealthCommand:  "command -v vagrant >/dev/null 2>&1",
	}
}

func (p *Provider) GetType() string {
	return p.spec.Type
}

func (p *Provider) GetName() string {
	return p.config.Name
}

func (p *Provider) GetSupportedInstanceTypes() []string {
	return []string{"vm"}
}

func (p *Provider) Connect(ctx context.Context, config rootProvider.NodeConfig) error {
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
		global.APP_LOG.Warn("VM CLI provider version detection failed",
			zap.String("type", p.spec.Type),
			zap.Error(err))
	}
	return nil
}

func (p *Provider) ConnectAgent(executor utils.ShellExecutor, config rootProvider.NodeConfig) error {
	p.config = config
	p.executor = executor
	p.connected = true
	p.healthChecker = nil
	go func() {
		if err := p.refreshVersion(); err != nil {
			global.APP_LOG.Warn("Agent VM CLI provider version detection failed",
				zap.String("type", p.spec.Type),
				zap.Error(err))
		}
	}()
	return nil
}

func (p *Provider) initHealthChecker(config rootProvider.NodeConfig, sshClient *ssh.Client) {
	healthConfig := health.HealthConfig{
		Host:          config.Host,
		Port:          config.Port,
		Username:      config.Username,
		Password:      config.Password,
		PrivateKey:    config.PrivateKey,
		APIEnabled:    false,
		SSHEnabled:    true,
		Timeout:       30 * time.Second,
		ServiceChecks: []string{p.spec.CLI},
	}
	zapLogger, _ := zap.NewProduction()
	p.healthChecker = health.NewDockerHealthCheckerWithSSH(healthConfig, zapLogger, sshClient)
}

func (p *Provider) Disconnect(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.executor != nil {
		_ = p.executor.Close()
	}
	p.executor = nil
	p.connected = false
	return nil
}

func (p *Provider) IsConnected() bool {
	p.mu.RLock()
	exec := p.executor
	connected := p.connected
	p.mu.RUnlock()
	return connected && exec != nil && exec.IsHealthy()
}

func (p *Provider) HealthCheck(ctx context.Context) (*health.HealthResult, error) {
	exec := p.getExecutor()
	if exec == nil || !exec.IsHealthy() {
		return &health.HealthResult{Status: health.HealthStatusUnhealthy, Timestamp: time.Now(), SSHStatus: "offline", APIStatus: "unknown"}, nil
	}
	if _, err := exec.ExecuteWithTimeout(p.spec.HealthCommand, 10*time.Second); err != nil {
		return &health.HealthResult{Status: health.HealthStatusUnhealthy, Timestamp: time.Now(), SSHStatus: "online", APIStatus: "unknown", ServiceStatus: p.spec.CLI + " missing"}, nil
	}
	return &health.HealthResult{Status: health.HealthStatusHealthy, Timestamp: time.Now(), SSHStatus: "online", APIStatus: "unknown", ServiceStatus: p.spec.CLI}, nil
}

func (p *Provider) GetHealthChecker() health.HealthChecker {
	return p.healthChecker
}

func (p *Provider) GetVersion() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.version
}

func (p *Provider) ListInstances(ctx context.Context) ([]rootProvider.Instance, error) {
	exec := p.getExecutor()
	if exec == nil {
		return nil, fmt.Errorf("%s provider not connected", p.spec.DisplayName)
	}
	out, err := exec.ExecuteWithTimeout(p.listScript(), 60*time.Second)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	instances := make([]rootProvider.Instance, 0)
	for _, line := range splitLines(out) {
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		if id == "" || name == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		image := ""
		if len(parts) > 3 {
			image = strings.TrimSpace(parts[3])
		}
		instances = append(instances, rootProvider.Instance{
			ID:       id,
			Name:     name,
			Status:   normalizeStatus(parts[2]),
			Type:     "vm",
			Image:    image,
			Created:  time.Now(),
			Metadata: map[string]string{"provider": p.spec.Type},
		})
	}
	return instances, nil
}

func (p *Provider) CreateInstance(ctx context.Context, config rootProvider.InstanceConfig) error {
	return p.CreateInstanceWithProgress(ctx, config, nil)
}

func (p *Provider) CreateInstanceWithProgress(ctx context.Context, config rootProvider.InstanceConfig, progress rootProvider.ProgressCallback) error {
	exec := p.getExecutor()
	if exec == nil {
		return fmt.Errorf("%s provider not connected", p.spec.DisplayName)
	}
	name := strings.TrimSpace(config.Name)
	if name == "" {
		return fmt.Errorf("instance name is required")
	}
	updateProgress(progress, 10, "preparing "+p.spec.DisplayName+" instance")
	script, timeout := p.createScript(config)
	updateProgress(progress, 35, "creating "+p.spec.DisplayName+" instance")
	if out, err := exec.ExecuteWithTimeout(script, timeout); err != nil {
		return fmt.Errorf("%s create failed: %w; output: %s", p.spec.DisplayName, err, utils.TruncateString(out, 600))
	}
	updateProgress(progress, 100, p.spec.DisplayName+" instance created")
	return nil
}

func (p *Provider) StartInstance(ctx context.Context, id string) error {
	return p.lifecycle(id, "start")
}

func (p *Provider) StopInstance(ctx context.Context, id string) error {
	return p.lifecycle(id, "stop")
}

func (p *Provider) RestartInstance(ctx context.Context, id string) error {
	if err := p.StopInstance(ctx, id); err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	return p.StartInstance(ctx, id)
}

func (p *Provider) DeleteInstance(ctx context.Context, id string) error {
	return p.lifecycle(id, "delete")
}

func (p *Provider) GetInstance(ctx context.Context, id string) (*rootProvider.Instance, error) {
	instances, err := p.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	for _, inst := range instances {
		if inst.ID == id || inst.Name == id {
			copy := inst
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("%s instance %s not found", p.spec.DisplayName, id)
}

func (p *Provider) ListImages(ctx context.Context) ([]rootProvider.Image, error) {
	exec := p.getExecutor()
	if exec == nil {
		return nil, fmt.Errorf("%s provider not connected", p.spec.DisplayName)
	}
	out, err := exec.ExecuteWithTimeout(p.imageListScript(), 60*time.Second)
	if err != nil {
		return nil, err
	}
	images := make([]rootProvider.Image, 0)
	for _, line := range splitLines(out) {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		if id == "" || name == "" {
			continue
		}
		images = append(images, rootProvider.Image{
			ID:          id,
			Name:        name,
			Tag:         p.spec.Type,
			Description: p.spec.DisplayName + " template or box",
			Metadata:    map[string]string{"provider": p.spec.Type},
		})
	}
	return images, nil
}

func (p *Provider) PullImage(ctx context.Context, image string) error {
	return fmt.Errorf("%s image download is managed by the provider CLI; add templates or boxes on the node first", p.spec.DisplayName)
}

func (p *Provider) DeleteImage(ctx context.Context, id string) error {
	return fmt.Errorf("%s image deletion is not supported by this provider", p.spec.DisplayName)
}

func (p *Provider) SetInstancePassword(ctx context.Context, instanceID, password string) error {
	return fmt.Errorf("%s guest password management is not supported by this provider", p.spec.DisplayName)
}

func (p *Provider) ResetInstancePassword(ctx context.Context, instanceID string) (string, error) {
	return "", fmt.Errorf("%s guest password reset is not supported by this provider", p.spec.DisplayName)
}

func (p *Provider) ExecuteSSHCommand(ctx context.Context, command string) (string, error) {
	exec := p.getExecutor()
	if exec == nil {
		return "", fmt.Errorf("%s provider not connected", p.spec.DisplayName)
	}
	return exec.Execute(command)
}

func (p *Provider) DiscoverInstances(ctx context.Context) ([]rootProvider.DiscoveredInstance, error) {
	instances, err := p.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]rootProvider.DiscoveredInstance, 0, len(instances))
	for _, inst := range instances {
		result = append(result, rootProvider.DiscoveredInstance{
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

func (p *Provider) getExecutor() utils.ShellExecutor {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.executor
}

func (p *Provider) refreshVersion() error {
	exec := p.getExecutor()
	if exec == nil {
		return fmt.Errorf("executor not initialized")
	}
	out, err := exec.ExecuteWithTimeout(p.spec.VersionCommand, 15*time.Second)
	if err != nil {
		p.setVersion("unknown")
		return err
	}
	version := firstNonEmptyLine(out)
	if version == "" {
		version = "unknown"
	}
	p.setVersion(version)
	return nil
}

func (p *Provider) setVersion(version string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.version = strings.TrimSpace(version)
}

func (p *Provider) basePath() string {
	if strings.TrimSpace(p.config.StoragePoolPath) != "" {
		return strings.TrimRight(p.config.StoragePoolPath, "/")
	}
	pool := strings.TrimSpace(p.config.StoragePool)
	if pool == "" || pool == "local" {
		return path.Join(defaultLibraryRoot, p.spec.Type)
	}
	if strings.HasPrefix(pool, "/") || strings.HasPrefix(pool, "~") {
		return strings.TrimRight(pool, "/")
	}
	return path.Join(defaultLibraryRoot, p.spec.Type, pool)
}

func (p *Provider) listScript() string {
	base := shellQuote(p.basePath())
	switch p.spec.Type {
	case "virtualbox":
		return `
set +e
running="$(VBoxManage list runningvms 2>/dev/null | sed -E 's/^"(.*)" \{(.*)\}$/\2/')"
VBoxManage list vms 2>/dev/null | sed -E 's/^"(.*)" \{(.*)\}$/\2	\1/' | while IFS='	' read -r uuid name; do
  [ -z "$uuid" ] && continue
  status=stopped
  printf '%s\n' "$running" | grep -qx "$uuid" && status=running
  printf '%s\t%s\t%s\tvirtualbox\n' "$uuid" "$name" "$status"
done
`
	case "multipass":
		return `
set +e
multipass list --format csv 2>/dev/null | awk -F, 'NR>1 {gsub(/^"|"$/, "", $1); gsub(/^"|"$/, "", $2); gsub(/^"|"$/, "", $5); printf "%s\t%s\t%s\t%s\n", $1, $1, tolower($2), $5}'
`
	case "vagrant":
		return fmt.Sprintf(`
set +e
base=%s
test -d "$base" || exit 0
find "$base" -maxdepth 2 -name Vagrantfile 2>/dev/null | while read -r vf; do
  dir="$(dirname "$vf")"
  name="$(basename "$dir")"
  status="$(cd "$dir" && vagrant status --machine-readable 2>/dev/null | awk -F, '$3=="state-human-short"{print tolower($4); exit}')"
  [ -z "$status" ] && status=unknown
  box="$(awk -F'"' '/config.vm.box/{print $2; exit}' "$vf")"
  printf '%%s\t%%s\t%%s\t%%s\n' "$name" "$name" "$status" "$box"
done
`, base)
	default:
		return "true"
	}
}

func (p *Provider) imageListScript() string {
	base := shellQuote(p.basePath())
	switch p.spec.Type {
	case "virtualbox":
		return `
set +e
VBoxManage list vms 2>/dev/null | sed -E 's/^"(.*)" \{(.*)\}$/\2	\1/'
`
	case "multipass":
		return `
set +e
multipass find --format csv 2>/dev/null | awk -F, 'NR>1 {gsub(/^"|"$/, "", $1); printf "%s\t%s\n", $1, $1}'
`
	case "vagrant":
		return fmt.Sprintf(`
set +e
vagrant box list 2>/dev/null | awk '{print $1 "\t" $1}'
base=%s
test -d "$base" || exit 0
find "$base" -maxdepth 2 -name Vagrantfile 2>/dev/null | while read -r vf; do
  box="$(awk -F'"' '/config.vm.box/{print $2; exit}' "$vf")"
  [ -n "$box" ] && printf '%%s\t%%s\n' "$box" "$box"
done
`, base)
	default:
		return "true"
	}
}

func (p *Provider) createScript(config rootProvider.InstanceConfig) (string, time.Duration) {
	name := strings.TrimSpace(config.Name)
	image := strings.TrimSpace(config.Image)
	if image == "" {
		image = strings.TrimSpace(config.ImagePath)
	}
	cpu := parsePositiveInt(config.CPU, 1)
	memoryMB := parseMemoryMB(config.Memory, 512)
	diskGB := parseDiskGB(config.Disk, 10)
	base := p.basePath()

	switch p.spec.Type {
	case "virtualbox":
		return fmt.Sprintf(`
set -e
name=%s
image=%s
base=%s
mkdir -p "$base/$name"
if [ -n "$image" ] && VBoxManage showvminfo "$image" >/dev/null 2>&1; then
  VBoxManage clonevm "$image" --name "$name" --register
else
  VBoxManage createvm --name "$name" --ostype Linux_64 --basefolder "$base" --register
  VBoxManage modifyvm "$name" --cpus %d --memory %d --nic1 nat
  VBoxManage createhd --filename "$base/$name/$name.vdi" --size %d
  VBoxManage storagectl "$name" --name SATA --add sata --controller IntelAhci
  VBoxManage storageattach "$name" --storagectl SATA --port 0 --device 0 --type hdd --medium "$base/$name/$name.vdi"
fi
VBoxManage startvm "$name" --type headless >/dev/null 2>&1 || true
`, shellQuote(name), shellQuote(image), shellQuote(base), cpu, memoryMB, diskGB*1024), 20 * time.Minute
	case "multipass":
		if image == "" {
			image = "lts"
		}
		return fmt.Sprintf(`
set -e
if multipass info %s >/dev/null 2>&1; then
  echo "multipass instance already exists"
  exit 1
fi
multipass launch %s --name %s --cpus %d --memory %dM --disk %dG
`, shellQuote(name), shellQuote(image), shellQuote(name), cpu, memoryMB, diskGB), 30 * time.Minute
	case "vagrant":
		if image == "" {
			image = "generic/ubuntu2204"
		}
		return fmt.Sprintf(`
set -e
name=%s
box=%s
base=%s
dir="$base/$name"
mkdir -p "$dir"
cat > "$dir/Vagrantfile" <<'VAGRANTFILE'
Vagrant.configure("2") do |config|
  config.vm.box = "%s"
  config.vm.hostname = "%s"
  config.vm.provider "virtualbox" do |vb|
    vb.cpus = %d
    vb.memory = %d
  end
  config.vm.provider "libvirt" do |lv|
    lv.cpus = %d
    lv.memory = %d
  end
end
VAGRANTFILE
cd "$dir"
vagrant up --provider=libvirt || vagrant up --provider=virtualbox || vagrant up
`, shellQuote(name), shellQuote(image), shellQuote(base), escapeVagrantString(image), escapeVagrantString(name), cpu, memoryMB, cpu, memoryMB), 40 * time.Minute
	default:
		return "true", time.Minute
	}
}

func (p *Provider) lifecycle(id, action string) error {
	exec := p.getExecutor()
	if exec == nil {
		return fmt.Errorf("%s provider not connected", p.spec.DisplayName)
	}
	script := p.lifecycleScript(id, action)
	if out, err := exec.ExecuteWithTimeout(script, 15*time.Minute); err != nil {
		return fmt.Errorf("%s %s failed: %w; output: %s", p.spec.DisplayName, action, err, utils.TruncateString(out, 500))
	}
	return nil
}

func (p *Provider) lifecycleScript(id, action string) string {
	qID := shellQuote(strings.TrimSpace(id))
	switch p.spec.Type {
	case "virtualbox":
		switch action {
		case "start":
			return fmt.Sprintf("VBoxManage startvm %s --type headless", qID)
		case "stop":
			return fmt.Sprintf("VBoxManage controlvm %s acpipowerbutton >/dev/null 2>&1 || VBoxManage controlvm %s poweroff", qID, qID)
		case "delete":
			return fmt.Sprintf("VBoxManage controlvm %s poweroff >/dev/null 2>&1 || true\nVBoxManage unregistervm %s --delete", qID, qID)
		}
	case "multipass":
		switch action {
		case "start":
			return fmt.Sprintf("multipass start %s", qID)
		case "stop":
			return fmt.Sprintf("multipass stop %s", qID)
		case "delete":
			return fmt.Sprintf("multipass delete %s --purge || (multipass delete %s && multipass purge)", qID, qID)
		}
	case "vagrant":
		dir := shellQuote(path.Join(p.basePath(), strings.TrimSpace(id)))
		switch action {
		case "start":
			return fmt.Sprintf("cd %s && vagrant up", dir)
		case "stop":
			return fmt.Sprintf("cd %s && vagrant halt", dir)
		case "delete":
			return fmt.Sprintf("dir=%s\ncd \"$dir\" && vagrant destroy -f\nrm -rf \"$dir\"", dir)
		}
	}
	return "true"
}

func splitLines(s string) []string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func normalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "started":
		return "running"
	case "stopped", "poweroff", "powered off", "shutoff", "not created":
		return "stopped"
	case "paused", "saved":
		return "stopped"
	default:
		if strings.TrimSpace(status) == "" {
			return "unknown"
		}
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func parsePositiveInt(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err == nil && n > 0 {
		return n
	}
	var digits strings.Builder
	for _, ch := range raw {
		if ch >= '0' && ch <= '9' {
			digits.WriteRune(ch)
		} else if digits.Len() > 0 {
			break
		}
	}
	if digits.Len() > 0 {
		if n, err := strconv.Atoi(digits.String()); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func parseMemoryMB(raw string, fallback int) int {
	value := parsePositiveInt(raw, fallback)
	lower := strings.ToLower(strings.TrimSpace(raw))
	if strings.Contains(lower, "g") {
		return value * 1024
	}
	return value
}

func parseDiskGB(raw string, fallback int) int {
	value := parsePositiveInt(raw, fallback)
	lower := strings.ToLower(strings.TrimSpace(raw))
	if strings.Contains(lower, "m") {
		gb := value / 1024
		if gb <= 0 {
			gb = 1
		}
		return gb
	}
	return value
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func escapeVagrantString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

func updateProgress(callback rootProvider.ProgressCallback, pct int, msg string) {
	if callback != nil {
		callback(pct, msg)
	}
}
