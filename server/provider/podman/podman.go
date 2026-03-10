package podman

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/provider/health"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

const (
	providerType      = "podman"
	cliName           = "podman"
	ipv4Network       = "podman-net"
	ipv4Subnet        = "172.20.0.0/16"
	ipv6Network       = "podman-ipv6"
	imageDir          = "/usr/local/bin/podman_ct_images"
	ipv6CheckFile     = "/usr/local/bin/podman_check_ipv6"
	storageDriverFile = "/usr/local/bin/podman_storage_driver"
	scriptRepo        = "oneclickvirt/podman"
	serviceCheckName  = "podman"
)

// PodmanProvider Podman容器运行时Provider（独立实现，不依赖docker包）
type PodmanProvider struct {
	config           provider.NodeConfig
	sshClient        *utils.SSHClient
	connected        bool
	healthChecker    health.HealthChecker
	version          string
	mu               sync.RWMutex
	imageImportGroup singleflight.Group
}

// NewPodmanProvider 创建Podman Provider实例
func NewPodmanProvider() provider.Provider {
	return &PodmanProvider{}
}

func (p *PodmanProvider) GetType() string {
	return providerType
}

func (p *PodmanProvider) GetName() string {
	return p.config.Name
}

func (p *PodmanProvider) GetSupportedInstanceTypes() []string {
	return []string{"container"}
}

func (p *PodmanProvider) Connect(ctx context.Context, config provider.NodeConfig) error {
	p.config = config
	global.APP_LOG.Info("Podman provider开始连接",
		zap.String("host", utils.TruncateString(config.Host, 32)),
		zap.Int("port", config.Port))

	sshConnectTimeout := config.SSHConnectTimeout
	sshExecuteTimeout := config.SSHExecuteTimeout
	if sshConnectTimeout <= 0 {
		sshConnectTimeout = 30
	}
	if sshExecuteTimeout <= 0 {
		sshExecuteTimeout = 300
	}

	sshConfig := utils.SSHConfig{
		Host:           config.Host,
		Port:           config.Port,
		Username:       config.Username,
		Password:       config.Password,
		PrivateKey:     config.PrivateKey,
		ConnectTimeout: time.Duration(sshConnectTimeout) * time.Second,
		ExecuteTimeout: time.Duration(sshExecuteTimeout) * time.Second,
	}
	client, err := utils.NewSSHClient(sshConfig)
	if err != nil {
		return fmt.Errorf("failed to connect via SSH: %w", err)
	}

	p.sshClient = client
	p.connected = true

	healthConfig := health.HealthConfig{
		Host:          config.Host,
		Port:          config.Port,
		Username:      config.Username,
		Password:      config.Password,
		PrivateKey:    config.PrivateKey,
		APIEnabled:    false,
		SSHEnabled:    true,
		Timeout:       30 * time.Second,
		ServiceChecks: []string{serviceCheckName},
	}

	zapLogger, _ := zap.NewProduction()
	p.healthChecker = health.NewDockerHealthCheckerWithSSH(healthConfig, zapLogger, client.GetUnderlyingClient())

	if err := p.getPodmanVersion(); err != nil {
		global.APP_LOG.Warn("Podman版本获取失败", zap.Error(err))
	}

	global.APP_LOG.Info("Podman provider连接成功",
		zap.String("host", utils.TruncateString(config.Host, 32)),
		zap.String("version", p.version))

	return nil
}

func (p *PodmanProvider) Disconnect(ctx context.Context) error {
	if p.sshClient != nil {
		p.sshClient.Close()
		p.connected = false
	}
	return nil
}

func (p *PodmanProvider) IsConnected() bool {
	return p.connected && p.sshClient != nil && p.sshClient.IsHealthy()
}

// EnsureConnection 确保SSH连接可用，如果连接不健康则尝试重连
func (p *PodmanProvider) EnsureConnection() error {
	if p.sshClient == nil {
		return fmt.Errorf("SSH client not initialized")
	}
	if !p.sshClient.IsHealthy() {
		global.APP_LOG.Warn("Podman Provider SSH连接不健康，尝试重连",
			zap.String("host", utils.TruncateString(p.config.Host, 32)))
		if err := p.sshClient.Reconnect(); err != nil {
			p.connected = false
			return fmt.Errorf("failed to reconnect SSH: %w", err)
		}
	}
	return nil
}

func (p *PodmanProvider) HealthCheck(ctx context.Context) (*health.HealthResult, error) {
	if p.healthChecker == nil {
		return nil, fmt.Errorf("health checker not initialized")
	}
	return p.healthChecker.CheckHealth(ctx)
}

func (p *PodmanProvider) GetHealthChecker() health.HealthChecker {
	return p.healthChecker
}

func (p *PodmanProvider) GetVersion() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.version
}

func (p *PodmanProvider) getPodmanVersion() error {
	if p.sshClient == nil {
		return fmt.Errorf("SSH client not connected")
	}
	versionCmd := fmt.Sprintf("%s version --format '{{.Server.Version}}' 2>/dev/null || %s --version 2>/dev/null || echo unknown", cliName, cliName)
	output, err := p.sshClient.Execute(versionCmd)
	if err != nil {
		p.version = "unknown"
		return err
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, " version ") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				p.version = strings.TrimSuffix(parts[2], ",")
				return nil
			}
		} else {
			p.version = line
			return nil
		}
	}
	p.version = "unknown"
	return fmt.Errorf("无法解析版本信息")
}

func (p *PodmanProvider) ListInstances(ctx context.Context) ([]provider.Instance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	return p.sshListInstances(ctx)
}

func (p *PodmanProvider) CreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	if p.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Podman provider不支持API调用，无法使用api_only执行规则")
	}
	return p.sshCreateInstance(ctx, config)
}

func (p *PodmanProvider) CreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	global.APP_LOG.Debug("Podman.CreateInstanceWithProgress被调用",
		zap.String("instanceName", config.Name),
		zap.Bool("connected", p.connected))
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	if p.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Podman provider不支持API调用，无法使用api_only执行规则")
	}
	return p.sshCreateInstanceWithProgress(ctx, config, progressCallback)
}

func (p *PodmanProvider) StartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	if p.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Podman provider不支持API调用，无法使用api_only执行规则")
	}
	return p.sshStartInstance(ctx, id)
}

func (p *PodmanProvider) StopInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	if p.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Podman provider不支持API调用，无法使用api_only执行规则")
	}
	return p.sshStopInstance(ctx, id)
}

func (p *PodmanProvider) RestartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	if p.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Podman provider不支持API调用，无法使用api_only执行规则")
	}
	return p.sshRestartInstance(ctx, id)
}

func (p *PodmanProvider) DeleteInstance(ctx context.Context, id string) error {
	if p.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Podman provider不支持API调用，无法使用api_only执行规则")
	}
	maxReconnectAttempts := 3
	for attempt := 1; attempt <= maxReconnectAttempts; attempt++ {
		if !p.connected {
			global.APP_LOG.Warn("Podman Provider未连接，尝试重连",
				zap.String("id", utils.TruncateString(id, 32)),
				zap.Int("attempt", attempt))
			if err := p.Connect(ctx, p.config); err != nil {
				if attempt == maxReconnectAttempts {
					return fmt.Errorf("重连失败，已达最大重试次数: %w", err)
				}
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
		}
		err := p.sshDeleteInstance(ctx, id)
		if err != nil {
			if p.isConnectionError(err) {
				p.connected = false
				if attempt < maxReconnectAttempts {
					time.Sleep(time.Duration(attempt) * time.Second)
					continue
				}
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("删除实例失败，已达最大重连尝试次数")
}

func (p *PodmanProvider) isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errorStr := strings.ToLower(err.Error())
	connectionErrors := []string{
		"connection refused", "connection lost", "connection reset",
		"network is unreachable", "no route to host", "connection timed out",
		"broken pipe", "eof", "ssh: connection lost",
		"ssh: handshake failed", "ssh: unable to authenticate",
	}
	for _, connErr := range connectionErrors {
		if strings.Contains(errorStr, connErr) {
			return true
		}
	}
	return false
}

func (p *PodmanProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	return p.sshListImages(ctx)
}

func (p *PodmanProvider) PullImage(ctx context.Context, image string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	return p.sshPullImage(ctx, image)
}

func (p *PodmanProvider) DeleteImage(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	return p.sshDeleteImage(ctx, id)
}

func (p *PodmanProvider) GetInstance(ctx context.Context, id string) (*provider.Instance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}

	output, err := p.sshClient.ExecuteWithLogging(fmt.Sprintf("%s inspect %s --format '{{.Name}}|{{.State.Status}}|{{.Config.Image}}|{{.Id}}|{{.Created}}'", cliName, id), "PODMAN_INSPECT")
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	output = strings.TrimSpace(output)
	if output == "" {
		return nil, fmt.Errorf("instance not found")
	}

	fields := strings.Split(output, "|")
	if len(fields) < 4 {
		return nil, fmt.Errorf("invalid instance data: unexpected format")
	}

	status := "unknown"
	statusField := strings.ToLower(fields[1])
	if strings.Contains(statusField, "running") {
		status = "running"
	} else if strings.Contains(statusField, "exited") {
		status = "stopped"
	} else if strings.Contains(statusField, "paused") {
		status = "paused"
	}

	instance := &provider.Instance{
		ID:     fields[3],
		Name:   strings.TrimPrefix(fields[0], "/"),
		Status: status,
		Image:  fields[2],
	}

	if status == "running" {
		p.enrichInstanceWithNetworkInfo(instance)
	}

	return instance, nil
}

func (p *PodmanProvider) enrichInstanceWithNetworkInfo(instance *provider.Instance) {
	cmd := fmt.Sprintf("%s inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{$config.IPAddress}}{{end}}'", cliName, instance.Name)
	output, err := p.sshClient.Execute(cmd)
	if err == nil {
		ipAddress := strings.TrimSpace(output)
		if ipAddress != "" && ipAddress != "<no value>" {
			instance.PrivateIP = ipAddress
			instance.IP = ipAddress
		}
	}

	vethCmd := fmt.Sprintf(`
CONTAINER_NAME='%s'
CONTAINER_PID=$(%s inspect -f '{{.State.Pid}}' "$CONTAINER_NAME" 2>/dev/null)
if [ -z "$CONTAINER_PID" ] || [ "$CONTAINER_PID" = "0" ]; then
    exit 1
fi
HOST_VETH_IFINDEX=$(nsenter -t $CONTAINER_PID -n ip link show eth0 2>/dev/null | head -n1 | sed -n 's/.*@if\([0-9]\+\).*/\1/p')
if [ -z "$HOST_VETH_IFINDEX" ]; then
    exit 1
fi
VETH_NAME=$(ip -o link show 2>/dev/null | awk -v idx="$HOST_VETH_IFINDEX" -F': ' '$1 == idx {print $2}' | cut -d'@' -f1)
if [ -n "$VETH_NAME" ]; then
    echo "$VETH_NAME"
fi
`, instance.Name, cliName)
	vethOutput, err := p.sshClient.Execute(vethCmd)
	if err == nil {
		vethInterface := utils.CleanCommandOutput(vethOutput)
		if vethInterface != "" {
			if instance.Metadata == nil {
				instance.Metadata = make(map[string]string)
			}
			instance.Metadata["network_interface"] = vethInterface
		}
	}

	if instance.PrivateIP == "" {
		fallbackCmd := fmt.Sprintf("%s inspect %s --format '{{.NetworkSettings.IPAddress}}'", cliName, instance.Name)
		fallbackOutput, fallbackErr := p.sshClient.Execute(fallbackCmd)
		if fallbackErr == nil {
			ipAddress := strings.TrimSpace(fallbackOutput)
			if ipAddress != "" && ipAddress != "<no value>" {
				instance.PrivateIP = ipAddress
				instance.IP = ipAddress
			}
		}
	}

	checkIPv6Cmd := fmt.Sprintf("%s inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{$net}}{{println}}{{end}}'", cliName, instance.Name)
	networksOutput, err := p.sshClient.Execute(checkIPv6Cmd)
	if err == nil && strings.Contains(networksOutput, ipv6Network) {
		cmd = fmt.Sprintf("%s inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{if $config.GlobalIPv6Address}}{{$config.GlobalIPv6Address}}{{end}}{{end}}'", cliName, instance.Name)
		output, err = p.sshClient.Execute(cmd)
		if err == nil {
			ipv6Address := strings.TrimSpace(output)
			if ipv6Address != "" && ipv6Address != "<no value>" {
				instance.IPv6Address = ipv6Address
			}
		}
	}
}

// checkIPv6NetworkAvailable 检查IPv6网络是否可用
func (p *PodmanProvider) checkIPv6NetworkAvailable() bool {
	if !p.connected || p.sshClient == nil {
		return false
	}
	_, err := p.sshClient.Execute(fmt.Sprintf("%s network inspect %s", cliName, ipv6Network))
	if err != nil {
		return false
	}
	ndpresponderCmd := fmt.Sprintf("%s inspect -f '{{.State.Status}}' ndpresponder 2>/dev/null", cliName)
	ndpresponderOutput, err := p.sshClient.Execute(ndpresponderCmd)
	if err != nil || strings.TrimSpace(ndpresponderOutput) != "running" {
		return false
	}
	ipv6ConfigCmd := fmt.Sprintf("[ -f %s ] && [ -s %s ] && [ \"$(sed -e '/^[[:space:]]*$/d' %s)\" != \"\" ] && echo 'valid' || echo 'invalid'", ipv6CheckFile, ipv6CheckFile, ipv6CheckFile)
	ipv6ConfigOutput, err := p.sshClient.Execute(ipv6ConfigCmd)
	if err != nil || strings.TrimSpace(ipv6ConfigOutput) != "valid" {
		return false
	}
	return true
}

// ExecuteSSHCommand 执行SSH命令
func (p *PodmanProvider) ExecuteSSHCommand(ctx context.Context, command string) (string, error) {
	if !p.connected || p.sshClient == nil {
		return "", fmt.Errorf("Podman provider not connected")
	}
	output, err := p.sshClient.Execute(command)
	if err != nil {
		return "", fmt.Errorf("SSH command execution failed: %w", err)
	}
	return output, nil
}

func (p *PodmanProvider) SetInstancePassword(ctx context.Context, instanceID, password string) error {
	if !p.connected {
		return fmt.Errorf("provider not connected")
	}
	return p.sshSetInstancePassword(ctx, instanceID, password)
}

func (p *PodmanProvider) ResetInstancePassword(ctx context.Context, instanceID string) (string, error) {
	if !p.connected {
		return "", fmt.Errorf("provider not connected")
	}
	newPassword := p.generateRandomPassword()
	err := p.sshSetInstancePassword(ctx, instanceID, newPassword)
	if err != nil {
		return "", err
	}
	return newPassword, nil
}

func (p *PodmanProvider) DiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	return p.sshDiscoverInstances(ctx)
}

func init() {
	provider.RegisterProvider("podman", NewPodmanProvider)
}
