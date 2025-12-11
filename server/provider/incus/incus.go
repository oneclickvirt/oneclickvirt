package incus

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
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
	transport     *http.Transport // 保存transport以便清理
	providerID    uint            // 存储providerID用于清理
	connected     bool
	healthChecker health.HealthChecker
	version       string       // Incus 版本
	mu            sync.RWMutex // 保护并发访问
}

func NewIncusProvider() provider.Provider {
	// 创建独立的 Transport
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	provider.GetTransportCleanupManager().RegisterTransport(transport)
	return &IncusProvider{
		transport: transport,
		apiClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
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
	i.providerID = config.ID // 存储providerID

	// Transport 已在 NewIncusProvider 中创建，现在关联providerID
	if i.transport != nil && i.providerID > 0 {
		provider.GetTransportCleanupManager().RegisterTransportWithProvider(i.transport, i.providerID)
	}

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
			// 更新transport的TLS配置
			i.transport.TLSClientConfig = tlsConfig
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

	// 获取 Incus 版本
	if err := i.getIncusVersion(); err != nil {
		global.APP_LOG.Warn("Incus 版本获取失败",
			zap.Error(err))
	}

	global.APP_LOG.Info("Incus provider SSH连接成功",
		zap.String("host", utils.TruncateString(config.Host, 32)),
		zap.Int("port", config.Port),
		zap.String("version", i.version))
	return nil
}

func (i *IncusProvider) GetVersion() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.version
}

// getIncusVersion 获取 Incus 版本
func (i *IncusProvider) getIncusVersion() error {
	if i.sshClient == nil {
		return fmt.Errorf("SSH client not connected")
	}

	// 使用 incus --version 或 incus version 命令获取版本
	output, err := i.sshClient.Execute("incus --version 2>/dev/null || incus version 2>/dev/null")
	if err != nil {
		global.APP_LOG.Warn("获取 Incus 版本失败",
			zap.Error(err))
		i.version = "unknown"
		return err
	}

	// 解析版本号
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Client") || strings.HasPrefix(line, "Server") {
			continue
		}
		// 提取第一个非空行作为版本号
		i.version = line
		global.APP_LOG.Info("获取 Incus 版本成功",
			zap.String("version", i.version))
		return nil
	}

	i.version = "unknown"
	return fmt.Errorf("无法解析版本信息")
}

func (i *IncusProvider) Disconnect(ctx context.Context) error {
	if i.sshClient != nil {
		i.sshClient.Close()
		i.sshClient = nil
	}

	// 按providerID清理transport
	if i.providerID > 0 {
		provider.GetTransportCleanupManager().CleanupProvider(i.providerID)
	} else if i.transport != nil {
		// fallback: 如果providerID未设置，使用原来的方法
		i.transport.CloseIdleConnections()
		provider.GetTransportCleanupManager().UnregisterTransport(i.transport)
	}
	i.transport = nil

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

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiCreateInstanceWithProgress(ctx, config, progressCallback); err == nil {
			global.APP_LOG.Info("Incus API调用成功 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("Incus API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !i.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH方式 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
		}
	}

	// 使用SSH方式
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return i.sshCreateInstanceWithProgress(ctx, config, progressCallback)
}

func (i *IncusProvider) StartInstance(ctx context.Context, id string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiStartInstance(ctx, id); err == nil {
			global.APP_LOG.Info("Incus API调用成功 - 启动实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("Incus API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !i.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH方式 - 启动实例", zap.String("id", utils.TruncateString(id, 50)))
		}
	}

	// 使用SSH方式
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return i.sshStartInstance(id)
}

func (i *IncusProvider) StopInstance(ctx context.Context, id string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiStopInstance(ctx, id); err == nil {
			global.APP_LOG.Info("Incus API调用成功 - 停止实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("Incus API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !i.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH方式 - 停止实例", zap.String("id", utils.TruncateString(id, 50)))
		}
	}

	// 使用SSH方式
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return i.sshStopInstance(id)
}

func (i *IncusProvider) RestartInstance(ctx context.Context, id string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiRestartInstance(ctx, id); err == nil {
			global.APP_LOG.Info("Incus API调用成功 - 重启实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("Incus API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !i.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH方式 - 重启实例", zap.String("id", utils.TruncateString(id, 50)))
		}
	}

	// 使用SSH方式
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return i.sshRestartInstance(id)
}

func (i *IncusProvider) DeleteInstance(ctx context.Context, id string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiDeleteInstance(ctx, id); err == nil {
			global.APP_LOG.Info("Incus API调用成功 - 删除实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("Incus API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !i.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH方式 - 删除实例", zap.String("id", utils.TruncateString(id, 50)))
		}
	}

	// 使用SSH方式
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

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

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		images, err := i.apiListImages(ctx)
		if err == nil {
			global.APP_LOG.Debug("Incus API调用成功 - 列出镜像")
			return images, nil
		}
		global.APP_LOG.Warn("Incus API失败", zap.Error(err))

		// 检查是否可以回退到SSH
		if !i.shouldFallbackToSSH() {
			return nil, fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH方式 - 列出镜像")
	}

	// 使用SSH方式
	if !i.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH")
	}

	return i.sshListImages()
}

func (i *IncusProvider) PullImage(ctx context.Context, image string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiPullImage(ctx, image); err == nil {
			global.APP_LOG.Info("Incus API调用成功 - 拉取镜像", zap.String("image", utils.TruncateString(image, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("Incus API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !i.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH方式 - 拉取镜像", zap.String("image", utils.TruncateString(image, 50)))
		}
	}

	// 使用SSH方式
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return i.sshPullImage(image)
}

func (i *IncusProvider) DeleteImage(ctx context.Context, id string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiDeleteImage(ctx, id); err == nil {
			global.APP_LOG.Info("Incus API调用成功 - 删除镜像", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("Incus API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !i.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH方式 - 删除镜像", zap.String("id", utils.TruncateString(id, 50)))
		}
	}

	// 使用SSH方式
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

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
		return i.sshClient != nil && i.connected
	case "auto":
		fallthrough
	default:
		return i.sshClient != nil && i.connected
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

// ensureSSHBeforeFallback 在回退到SSH前检查SSH连接健康状态
func (i *IncusProvider) ensureSSHBeforeFallback(apiErr error, operation string) error {
	if !i.shouldFallbackToSSH() {
		return fmt.Errorf("API调用失败且不允许回退到SSH: %w", apiErr)
	}

	if err := i.EnsureConnection(); err != nil {
		return fmt.Errorf("API失败且SSH连接不可用: API错误=%v, SSH错误=%v", apiErr, err)
	}

	global.APP_LOG.Info(fmt.Sprintf("回退到SSH方式 - %s", operation))
	return nil
}

// SetupPortMappingWithIP 公开的方法：在远程服务器上创建端口映射（用于手动添加端口）
func (i *IncusProvider) SetupPortMappingWithIP(ctx context.Context, instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error {
	return i.setupPortMappingWithIP(instanceName, hostPort, guestPort, protocol, method, instanceIP)
}

// RemovePortMapping 公开的方法：从远程服务器上删除端口映射（用于手动删除端口）
func (i *IncusProvider) RemovePortMapping(instanceName string, hostPort int, protocol string, method string) error {
	return i.removePortMapping(instanceName, hostPort, protocol, method)
}

func init() {
	provider.RegisterProvider("incus", NewIncusProvider)
}
