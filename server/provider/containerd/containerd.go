package containerd

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
	providerType      = "containerd"
	cliName           = "nerdctl"
	ipv4Network       = "containerd-net"
	ipv4Subnet        = "172.20.0.0/16"
	ipv6Network       = "containerd-ipv6"
	imageDir          = "/usr/local/bin/containerd_ct_images"
	ipv6CheckFile     = "/usr/local/bin/containerd_check_ipv6"
	storageDriverFile = "/usr/local/bin/containerd_storage_driver"
	scriptRepo        = "oneclickvirt/containerd"
	serviceCheckName  = "nerdctl"
)

// ContainerdProvider Containerd/nerdctl容器运行时Provider（独立实现，不依赖docker包）
type ContainerdProvider struct {
	config           provider.NodeConfig
	sshClient        *utils.SSHClient
	connected        bool
	healthChecker    health.HealthChecker
	version          string
	mu               sync.RWMutex
	imageImportGroup singleflight.Group
}

// NewContainerdProvider 创建Containerd Provider实例
func NewContainerdProvider() provider.Provider {
	return &ContainerdProvider{}
}

func (c *ContainerdProvider) GetType() string {
	return providerType
}

func (c *ContainerdProvider) GetName() string {
	return c.config.Name
}

func (c *ContainerdProvider) GetSupportedInstanceTypes() []string {
	return []string{"container"}
}

func (c *ContainerdProvider) Connect(ctx context.Context, config provider.NodeConfig) error {
	c.config = config
	global.APP_LOG.Info("Containerd provider开始连接",
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

	c.sshClient = client
	c.connected = true

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
	c.healthChecker = health.NewDockerHealthCheckerWithSSH(healthConfig, zapLogger, client.GetUnderlyingClient())

	if err := c.getContainerdVersion(); err != nil {
		global.APP_LOG.Warn("Containerd版本获取失败", zap.Error(err))
	}

	global.APP_LOG.Info("Containerd provider连接成功",
		zap.String("host", utils.TruncateString(config.Host, 32)),
		zap.String("version", c.version))

	return nil
}

func (c *ContainerdProvider) Disconnect(ctx context.Context) error {
	if c.sshClient != nil {
		c.sshClient.Close()
		c.connected = false
	}
	return nil
}

func (c *ContainerdProvider) IsConnected() bool {
	return c.connected && c.sshClient != nil && c.sshClient.IsHealthy()
}

// EnsureConnection 确保SSH连接可用
func (c *ContainerdProvider) EnsureConnection() error {
	if c.sshClient == nil {
		return fmt.Errorf("SSH client not initialized")
	}
	if !c.sshClient.IsHealthy() {
		global.APP_LOG.Warn("Containerd Provider SSH连接不健康，尝试重连",
			zap.String("host", utils.TruncateString(c.config.Host, 32)))
		if err := c.sshClient.Reconnect(); err != nil {
			c.connected = false
			return fmt.Errorf("failed to reconnect SSH: %w", err)
		}
	}
	return nil
}

func (c *ContainerdProvider) HealthCheck(ctx context.Context) (*health.HealthResult, error) {
	if c.healthChecker == nil {
		return nil, fmt.Errorf("health checker not initialized")
	}
	return c.healthChecker.CheckHealth(ctx)
}

func (c *ContainerdProvider) GetHealthChecker() health.HealthChecker {
	return c.healthChecker
}

func (c *ContainerdProvider) GetVersion() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

func (c *ContainerdProvider) getContainerdVersion() error {
	if c.sshClient == nil {
		return fmt.Errorf("SSH client not connected")
	}
	versionCmd := fmt.Sprintf("%s version --format '{{.Server.Version}}' 2>/dev/null || %s --version 2>/dev/null || echo unknown", cliName, cliName)
	output, err := c.sshClient.Execute(versionCmd)
	if err != nil {
		c.version = "unknown"
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
				c.version = strings.TrimSuffix(parts[2], ",")
				return nil
			}
		} else {
			c.version = line
			return nil
		}
	}
	c.version = "unknown"
	return fmt.Errorf("无法解析版本信息")
}

func (c *ContainerdProvider) ListInstances(ctx context.Context) ([]provider.Instance, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected")
	}
	return c.sshListInstances(ctx)
}

func (c *ContainerdProvider) CreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	if !c.connected {
		return fmt.Errorf("not connected")
	}
	if c.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Containerd provider不支持API调用，无法使用api_only执行规则")
	}
	return c.sshCreateInstance(ctx, config)
}

func (c *ContainerdProvider) CreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	global.APP_LOG.Debug("Containerd.CreateInstanceWithProgress被调用",
		zap.String("instanceName", config.Name),
		zap.Bool("connected", c.connected))
	if !c.connected {
		return fmt.Errorf("not connected")
	}
	if c.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Containerd provider不支持API调用，无法使用api_only执行规则")
	}
	return c.sshCreateInstanceWithProgress(ctx, config, progressCallback)
}

func (c *ContainerdProvider) StartInstance(ctx context.Context, id string) error {
	if !c.connected {
		return fmt.Errorf("not connected")
	}
	if c.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Containerd provider不支持API调用，无法使用api_only执行规则")
	}
	return c.sshStartInstance(ctx, id)
}

func (c *ContainerdProvider) StopInstance(ctx context.Context, id string) error {
	if !c.connected {
		return fmt.Errorf("not connected")
	}
	if c.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Containerd provider不支持API调用，无法使用api_only执行规则")
	}
	return c.sshStopInstance(ctx, id)
}

func (c *ContainerdProvider) RestartInstance(ctx context.Context, id string) error {
	if !c.connected {
		return fmt.Errorf("not connected")
	}
	if c.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Containerd provider不支持API调用，无法使用api_only执行规则")
	}
	return c.sshRestartInstance(ctx, id)
}

func (c *ContainerdProvider) DeleteInstance(ctx context.Context, id string) error {
	if c.config.ExecutionRule == "api_only" {
		return fmt.Errorf("Containerd provider不支持API调用，无法使用api_only执行规则")
	}
	maxReconnectAttempts := 3
	for attempt := 1; attempt <= maxReconnectAttempts; attempt++ {
		if !c.connected {
			global.APP_LOG.Warn("Containerd Provider未连接，尝试重连",
				zap.String("id", utils.TruncateString(id, 32)),
				zap.Int("attempt", attempt))
			if err := c.Connect(ctx, c.config); err != nil {
				if attempt == maxReconnectAttempts {
					return fmt.Errorf("重连失败，已达最大重试次数: %w", err)
				}
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
		}
		err := c.sshDeleteInstance(ctx, id)
		if err != nil {
			if c.isConnectionError(err) {
				c.connected = false
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

func (c *ContainerdProvider) isConnectionError(err error) bool {
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

func (c *ContainerdProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected")
	}
	return c.sshListImages(ctx)
}

func (c *ContainerdProvider) PullImage(ctx context.Context, image string) error {
	if !c.connected {
		return fmt.Errorf("not connected")
	}
	return c.sshPullImage(ctx, image)
}

func (c *ContainerdProvider) DeleteImage(ctx context.Context, id string) error {
	if !c.connected {
		return fmt.Errorf("not connected")
	}
	return c.sshDeleteImage(ctx, id)
}

func (c *ContainerdProvider) GetInstance(ctx context.Context, id string) (*provider.Instance, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected")
	}

	output, err := c.sshClient.ExecuteWithLogging(fmt.Sprintf("%s inspect %s --format '{{.Name}}|{{.State.Status}}|{{.Config.Image}}|{{.Id}}|{{.Created}}'", cliName, id), "CONTAINERD_INSPECT")
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
		c.enrichInstanceWithNetworkInfo(instance)
	}

	return instance, nil
}

func (c *ContainerdProvider) enrichInstanceWithNetworkInfo(instance *provider.Instance) {
	cmd := fmt.Sprintf("%s inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{$config.IPAddress}}{{end}}'", cliName, instance.Name)
	output, err := c.sshClient.Execute(cmd)
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
	vethOutput, err := c.sshClient.Execute(vethCmd)
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
		fallbackOutput, fallbackErr := c.sshClient.Execute(fallbackCmd)
		if fallbackErr == nil {
			ipAddress := strings.TrimSpace(fallbackOutput)
			if ipAddress != "" && ipAddress != "<no value>" {
				instance.PrivateIP = ipAddress
				instance.IP = ipAddress
			}
		}
	}

	checkIPv6Cmd := fmt.Sprintf("%s inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{$net}}{{println}}{{end}}'", cliName, instance.Name)
	networksOutput, err := c.sshClient.Execute(checkIPv6Cmd)
	if err == nil && strings.Contains(networksOutput, ipv6Network) {
		cmd = fmt.Sprintf("%s inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{if $config.GlobalIPv6Address}}{{$config.GlobalIPv6Address}}{{end}}{{end}}'", cliName, instance.Name)
		output, err = c.sshClient.Execute(cmd)
		if err == nil {
			ipv6Address := strings.TrimSpace(output)
			if ipv6Address != "" && ipv6Address != "<no value>" {
				instance.IPv6Address = ipv6Address
			}
		}
	}
}

// checkIPv6NetworkAvailable 检查IPv6网络是否可用
func (c *ContainerdProvider) checkIPv6NetworkAvailable() bool {
	if !c.connected || c.sshClient == nil {
		return false
	}
	_, err := c.sshClient.Execute(fmt.Sprintf("%s network inspect %s", cliName, ipv6Network))
	if err != nil {
		return false
	}
	ndpresponderCmd := fmt.Sprintf("%s inspect -f '{{.State.Status}}' ndpresponder 2>/dev/null", cliName)
	ndpresponderOutput, err := c.sshClient.Execute(ndpresponderCmd)
	if err != nil || strings.TrimSpace(ndpresponderOutput) != "running" {
		return false
	}
	ipv6ConfigCmd := fmt.Sprintf("[ -f %s ] && [ -s %s ] && [ \"$(sed -e '/^[[:space:]]*$/d' %s)\" != \"\" ] && echo 'valid' || echo 'invalid'", ipv6CheckFile, ipv6CheckFile, ipv6CheckFile)
	ipv6ConfigOutput, err := c.sshClient.Execute(ipv6ConfigCmd)
	if err != nil || strings.TrimSpace(ipv6ConfigOutput) != "valid" {
		return false
	}
	return true
}

// ExecuteSSHCommand 执行SSH命令
func (c *ContainerdProvider) ExecuteSSHCommand(ctx context.Context, command string) (string, error) {
	if !c.connected || c.sshClient == nil {
		return "", fmt.Errorf("Containerd provider not connected")
	}
	output, err := c.sshClient.Execute(command)
	if err != nil {
		return "", fmt.Errorf("SSH command execution failed: %w", err)
	}
	return output, nil
}

func (c *ContainerdProvider) SetInstancePassword(ctx context.Context, instanceID, password string) error {
	if !c.connected {
		return fmt.Errorf("provider not connected")
	}
	return c.sshSetInstancePassword(ctx, instanceID, password)
}

func (c *ContainerdProvider) ResetInstancePassword(ctx context.Context, instanceID string) (string, error) {
	if !c.connected {
		return "", fmt.Errorf("provider not connected")
	}
	newPassword := c.generateRandomPassword()
	err := c.sshSetInstancePassword(ctx, instanceID, newPassword)
	if err != nil {
		return "", err
	}
	return newPassword, nil
}

func (c *ContainerdProvider) DiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !c.connected {
		return nil, fmt.Errorf("not connected")
	}
	return c.sshDiscoverInstances(ctx)
}

func init() {
	provider.RegisterProvider("containerd", NewContainerdProvider)
}
