package incus

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/provider/health"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

type IncusProvider struct {
	config        provider.NodeConfig
	sshClient     *utils.SSHClient
	apiClient     *http.Client
	connected     bool
	healthChecker health.HealthChecker
}

func NewIncusProvider() provider.Provider {
	return &IncusProvider{
		apiClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (i *IncusProvider) GetType() string {
	return "incus"
}

func (i *IncusProvider) GetName() string {
	return i.config.Name
}

func (i *IncusProvider) GetSupportedInstanceTypes() []string {
	return []string{"container", "vm"}
}

func (i *IncusProvider) Connect(ctx context.Context, config provider.NodeConfig) error {
	i.config = config

	// 初始化默认的API客户端（如果证书配置失败，仍然可以回退到SSH）
	i.apiClient = &http.Client{Timeout: 30 * time.Second}

	if config.CertPath != "" && config.KeyPath != "" {
		global.APP_LOG.Info("尝试配置Incus证书认证",
			zap.String("host", utils.TruncateString(config.Host, 32)),
			zap.String("certPath", utils.TruncateString(config.CertPath, 64)),
			zap.String("keyPath", utils.TruncateString(config.KeyPath, 64)))

		tlsConfig, err := i.createTLSConfig(config.CertPath, config.KeyPath)
		if err != nil {
			global.APP_LOG.Warn("创建TLS配置失败，将仅使用SSH",
				zap.Error(err),
				zap.String("certPath", config.CertPath),
				zap.String("keyPath", config.KeyPath))
		} else {
			i.apiClient = &http.Client{
				Timeout: 30 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: tlsConfig,
				},
			}
			global.APP_LOG.Info("Incus provider证书认证配置成功",
				zap.String("host", utils.TruncateString(config.Host, 32)),
				zap.String("certPath", utils.TruncateString(config.CertPath, 64)))
		}
	} else {
		global.APP_LOG.Info("未找到Incus证书配置，仅使用SSH",
			zap.String("host", utils.TruncateString(config.Host, 32)))
	}

	// 设置SSH超时配置
	sshConnectTimeout := config.SSHConnectTimeout
	sshExecuteTimeout := config.SSHExecuteTimeout
	if sshConnectTimeout <= 0 {
		sshConnectTimeout = 30 // 默认30秒
	}
	if sshExecuteTimeout <= 0 {
		sshExecuteTimeout = 300 // 默认300秒
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
	i.sshClient = client
	i.connected = true

	// 初始化健康检查器，使用Provider的SSH连接，避免创建独立连接导致节点混淆
	healthConfig := health.HealthConfig{
		Host:          config.Host,
		Port:          config.Port,
		Username:      config.Username,
		Password:      config.Password,
		PrivateKey:    config.PrivateKey,
		APIEnabled:    config.CertPath != "" && config.KeyPath != "",
		APIPort:       8443,
		APIScheme:     "https",
		SSHEnabled:    true,
		Timeout:       30 * time.Second,
		ServiceChecks: []string{"incus"},
		CertPath:      config.CertPath,
		KeyPath:       config.KeyPath,
	}

	zapLogger, _ := zap.NewProduction()
	// 使用Provider的SSH连接创建健康检查器，确保在正确的节点上执行命令
	i.healthChecker = health.NewIncusHealthCheckerWithSSH(healthConfig, zapLogger, client.GetUnderlyingClient())

	global.APP_LOG.Info("Incus provider SSH连接成功",
		zap.String("host", utils.TruncateString(config.Host, 32)),
		zap.Int("port", config.Port))
	return nil
}

func (i *IncusProvider) Disconnect(ctx context.Context) error {
	if i.sshClient != nil {
		i.sshClient.Close()
		i.sshClient = nil
	}
	i.connected = false
	return nil
}

func (i *IncusProvider) IsConnected() bool {
	return i.connected && i.sshClient != nil && i.sshClient.IsHealthy()
}

// EnsureConnection 确保SSH连接可用，如果连接不健康则尝试重连
func (i *IncusProvider) EnsureConnection() error {
	if i.sshClient == nil {
		return fmt.Errorf("SSH client not initialized")
	}

	if !i.sshClient.IsHealthy() {
		global.APP_LOG.Warn("Incus Provider SSH连接不健康，尝试重连",
			zap.String("host", utils.TruncateString(i.config.Host, 32)),
			zap.Int("port", i.config.Port))

		if err := i.sshClient.Reconnect(); err != nil {
			i.connected = false
			return fmt.Errorf("failed to reconnect SSH: %w", err)
		}

		global.APP_LOG.Info("Incus Provider SSH连接重建成功",
			zap.String("host", utils.TruncateString(i.config.Host, 32)),
			zap.Int("port", i.config.Port))
	}

	return nil
}

func (i *IncusProvider) HealthCheck(ctx context.Context) (*health.HealthResult, error) {
	if i.healthChecker == nil {
		return nil, fmt.Errorf("health checker not initialized")
	}
	return i.healthChecker.CheckHealth(ctx)
}

func (i *IncusProvider) GetHealthChecker() health.HealthChecker {
	return i.healthChecker
}

func (i *IncusProvider) ListInstances(ctx context.Context) ([]provider.Instance, error) {
	if !i.connected {
		return nil, fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		instances, err := i.apiListInstances(ctx)
		if err == nil {
			global.APP_LOG.Debug("Incus API调用成功 - 列出实例")
			return instances, nil
		}
		global.APP_LOG.Warn("Incus API失败", zap.Error(err))

		// 检查是否可以回退到SSH
		if !i.shouldFallbackToSSH() {
			return nil, fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH执行 - 列出实例")
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !i.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return i.sshListInstances()
}

func (i *IncusProvider) CreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiCreateInstance(ctx, config); err == nil {
			global.APP_LOG.Info("Incus API调用成功 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("Incus API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !i.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH执行 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return i.sshCreateInstance(ctx, config)
}

func (i *IncusProvider) CreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 尝试 API 调用
	if i.hasAPIAccess() {
		if err := i.apiCreateInstanceWithProgress(ctx, config, progressCallback); err == nil {
			global.APP_LOG.Info("Incus API 调用成功 - CreateInstance", zap.String("name", config.Name))
			return nil
		}
		global.APP_LOG.Warn("Incus API 失败，回退到 SSH - CreateInstance", zap.String("name", config.Name))
	}

	// SSH 方式
	return i.sshCreateInstanceWithProgress(ctx, config, progressCallback)
}

func (i *IncusProvider) StartInstance(ctx context.Context, id string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 尝试 API 调用
	if i.hasAPIAccess() {
		if err := i.apiStartInstance(ctx, id); err == nil {
			global.APP_LOG.Info("Incus API 调用成功 - StartInstance", zap.String("id", id))
			return nil
		}
		global.APP_LOG.Warn("Incus API 失败，回退到 SSH - StartInstance", zap.String("id", id))
	}

	// SSH 方式
	return i.sshStartInstance(id)
}

func (i *IncusProvider) StopInstance(ctx context.Context, id string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 尝试 API 调用
	if i.hasAPIAccess() {
		if err := i.apiStopInstance(ctx, id); err == nil {
			global.APP_LOG.Info("Incus API 调用成功 - StopInstance", zap.String("id", id))
			return nil
		}
		global.APP_LOG.Warn("Incus API 失败，回退到 SSH - StopInstance", zap.String("id", id))
	}

	// SSH 方式
	return i.sshStopInstance(id)
}

func (i *IncusProvider) RestartInstance(ctx context.Context, id string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 尝试 API 调用
	if i.hasAPIAccess() {
		if err := i.apiRestartInstance(ctx, id); err == nil {
			global.APP_LOG.Info("Incus API 调用成功 - RestartInstance", zap.String("id", id))
			return nil
		}
		global.APP_LOG.Warn("Incus API 失败，回退到 SSH - RestartInstance", zap.String("id", id))
	}

	// SSH 方式
	return i.sshRestartInstance(id)
}

func (i *IncusProvider) DeleteInstance(ctx context.Context, id string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 尝试 API 调用
	if i.hasAPIAccess() {
		if err := i.apiDeleteInstance(ctx, id); err == nil {
			global.APP_LOG.Info("Incus API 调用成功 - DeleteInstance", zap.String("id", id))
			return nil
		}
		global.APP_LOG.Warn("Incus API 失败，回退到 SSH - DeleteInstance", zap.String("id", id))
	}

	// SSH 方式
	return i.sshDeleteInstance(id)
}

func (i *IncusProvider) GetInstance(ctx context.Context, id string) (*provider.Instance, error) {
	instances, err := i.ListInstances(ctx)
	if err != nil {
		return nil, err
	}

	for _, instance := range instances {
		if instance.ID == id || instance.Name == id {
			return &instance, nil
		}
	}

	return nil, fmt.Errorf("instance not found: %s", id)
}

func (i *IncusProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	if !i.connected {
		return nil, fmt.Errorf("not connected")
	}

	// 尝试 API 调用
	if i.hasAPIAccess() {
		images, err := i.apiListImages(ctx)
		if err == nil {
			global.APP_LOG.Info("Incus API 调用成功 - ListImages")
			return images, nil
		}
		global.APP_LOG.Warn("Incus API 失败，回退到 SSH - ListImages", zap.Error(err))
	}

	// SSH 方式
	return i.sshListImages()
}

func (i *IncusProvider) PullImage(ctx context.Context, image string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 尝试 API 调用
	if i.hasAPIAccess() {
		if err := i.apiPullImage(ctx, image); err == nil {
			global.APP_LOG.Info("Incus API 调用成功 - PullImage", zap.String("image", image))
			return nil
		}
		global.APP_LOG.Warn("Incus API 失败，回退到 SSH - PullImage", zap.String("image", image))
	}

	// SSH 方式
	return i.sshPullImage(image)
}

func (i *IncusProvider) DeleteImage(ctx context.Context, id string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 尝试 API 调用
	if i.hasAPIAccess() {
		if err := i.apiDeleteImage(ctx, id); err == nil {
			global.APP_LOG.Info("Incus API 调用成功 - DeleteImage", zap.String("id", id))
			return nil
		}
		global.APP_LOG.Warn("Incus API 失败，回退到 SSH - DeleteImage", zap.String("id", id))
	}

	// SSH 方式
	return i.sshDeleteImage(id)
}

// createTLSConfig 创建TLS配置用于API连接
func (i *IncusProvider) createTLSConfig(certPath, keyPath string) (*tls.Config, error) {
	// 验证证书文件是否存在
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("certificate file not found: %s", certPath)
	}
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("private key file not found: %s", keyPath)
	}

	// 加载客户端证书和私钥
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate (ensure files are in PEM format): %w", err)
	}

	// 验证证书和私钥是否匹配
	global.APP_LOG.Info("Successfully loaded client certificate for Incus",
		zap.String("certPath", certPath),
		zap.String("keyPath", keyPath))

	// 创建TLS配置
	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true, // Incus通常使用自签名证书
		ClientAuth:         tls.RequireAndVerifyClientCert,
	}

	return tlsConfig, nil
}

// ExecuteSSHCommand 执行SSH命令
func (i *IncusProvider) ExecuteSSHCommand(ctx context.Context, command string) (string, error) {
	if !i.connected || i.sshClient == nil {
		return "", fmt.Errorf("Incus provider not connected")
	}

	global.APP_LOG.Debug("执行SSH命令",
		zap.String("command", utils.TruncateString(command, 200)))

	output, err := i.sshClient.Execute(command)
	if err != nil {
		global.APP_LOG.Error("SSH命令执行失败",
			zap.String("command", utils.TruncateString(command, 200)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return "", fmt.Errorf("SSH command execution failed: %w", err)
	}

	return output, nil
}

// 检查是否有 API 访问权限
func (i *IncusProvider) hasAPIAccess() bool {
	return i.config.CertPath != "" && i.config.KeyPath != ""
}

// shouldUseAPI 根据执行规则判断是否应该使用API
func (i *IncusProvider) shouldUseAPI() bool {
	switch i.config.ExecutionRule {
	case "api_only":
		return i.hasAPIAccess()
	case "ssh_only":
		return false
	case "auto":
		fallthrough
	default:
		return i.hasAPIAccess()
	}
}

// shouldUseSSH 根据执行规则判断是否应该使用SSH
func (i *IncusProvider) shouldUseSSH() bool {
	switch i.config.ExecutionRule {
	case "api_only":
		return false
	case "ssh_only":
		return true
	case "auto":
		fallthrough
	default:
		return true
	}
}

// shouldFallbackToSSH 根据执行规则判断API失败时是否可以回退到SSH
func (i *IncusProvider) shouldFallbackToSSH() bool {
	switch i.config.ExecutionRule {
	case "api_only":
		return false
	case "ssh_only":
		return false
	case "auto":
		fallthrough
	default:
		return true
	}
}

// SetupPortMappingWithIP 公开的方法：在远程服务器上创建端口映射（用于手动添加端口）
func (i *IncusProvider) SetupPortMappingWithIP(instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error {
	return i.setupPortMappingWithIP(instanceName, hostPort, guestPort, protocol, method, instanceIP)
}

// RemovePortMapping 公开的方法：从远程服务器上删除端口映射（用于手动删除端口）
func (i *IncusProvider) RemovePortMapping(instanceName string, hostPort int, protocol string, method string) error {
	return i.removePortMapping(instanceName, hostPort, protocol, method)
}

func init() {
	provider.RegisterProvider("incus", NewIncusProvider)
}
