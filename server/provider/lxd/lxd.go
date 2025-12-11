package lxd

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

type LXDProvider struct {
	config        provider.NodeConfig
	sshClient     *utils.SSHClient
	apiClient     *http.Client
	transport     *http.Transport
	providerID    uint // 存储providerID用于清理
	connected     bool
	healthChecker health.HealthChecker
	version       string       // LXD 版本
	mu            sync.RWMutex // 保护并发访问
}

func NewLXDProvider() provider.Provider {
	// 创建独立的 Transport
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	provider.GetTransportCleanupManager().RegisterTransport(transport)
	return &LXDProvider{
		transport: transport,
		apiClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
	}
}

func (l *LXDProvider) GetType() string {
	return "lxd"
}

func (l *LXDProvider) GetName() string {
	return l.config.Name
}

func (l *LXDProvider) GetSupportedInstanceTypes() []string {
	return []string{"container", "vm"}
}

func (l *LXDProvider) Connect(ctx context.Context, config provider.NodeConfig) error {
	l.config = config
	l.providerID = config.ID // 存储providerID

	// Transport 已在 NewLXDProvider 中创建，现在关联providerID
	if l.transport != nil && l.providerID > 0 {
		provider.GetTransportCleanupManager().RegisterTransportWithProvider(l.transport, l.providerID)
	}

	// 如果有证书配置，设置TLS配置
	if config.CertPath != "" && config.KeyPath != "" {
		global.APP_LOG.Info("尝试配置LXD证书认证",
			zap.String("host", utils.TruncateString(config.Host, 50)),
			zap.String("certPath", utils.TruncateString(config.CertPath, 100)),
			zap.String("keyPath", utils.TruncateString(config.KeyPath, 100)))

		tlsConfig, err := l.createTLSConfig(config.CertPath, config.KeyPath)
		if err != nil {
			global.APP_LOG.Warn("创建TLS配置失败，将仅使用SSH",
				zap.Error(err),
				zap.String("certPath", utils.TruncateString(config.CertPath, 100)),
				zap.String("keyPath", utils.TruncateString(config.KeyPath, 100)))
		} else {
			l.transport.TLSClientConfig = tlsConfig
			global.APP_LOG.Info("LXD provider证书认证配置成功",
				zap.String("host", utils.TruncateString(config.Host, 50)),
				zap.String("certPath", utils.TruncateString(config.CertPath, 100)))
		}
	} else {
		global.APP_LOG.Info("未找到LXD证书配置，仅使用SSH",
			zap.String("host", utils.TruncateString(config.Host, 50)))
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

	// 尝试 SSH 连接
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

	l.sshClient = client
	l.connected = true

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
		ServiceChecks: []string{"lxd"},
		CertPath:      config.CertPath,
		KeyPath:       config.KeyPath,
	}

	zapLogger, _ := zap.NewProduction()
	// 使用Provider的SSH连接创建健康检查器，确保在正确的节点上执行命令
	l.healthChecker = health.NewLXDHealthCheckerWithSSH(healthConfig, zapLogger, client.GetUnderlyingClient())

	// 获取 LXD 版本
	if err := l.getLXDVersion(); err != nil {
		global.APP_LOG.Warn("LXD 版本获取失败",
			zap.Error(err))
	}

	global.APP_LOG.Info("LXD provider SSH连接成功",
		zap.String("host", utils.TruncateString(config.Host, 50)),
		zap.Int("port", config.Port),
		zap.String("version", l.version))

	return nil
}

func (l *LXDProvider) GetVersion() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.version
}

// getLXDVersion 获取 LXD 版本
func (l *LXDProvider) getLXDVersion() error {
	if l.sshClient == nil {
		return fmt.Errorf("SSH client not connected")
	}

	// 使用 lxd --version 或 lxc version 命令获取版本
	output, err := l.sshClient.Execute("lxd --version 2>/dev/null || lxc version 2>/dev/null")
	if err != nil {
		global.APP_LOG.Warn("获取 LXD 版本失败",
			zap.Error(err))
		l.version = "unknown"
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
		l.version = line
		global.APP_LOG.Info("获取 LXD 版本成功",
			zap.String("version", l.version))
		return nil
	}

	l.version = "unknown"
	return fmt.Errorf("无法解析版本信息")
}

func (l *LXDProvider) Disconnect(ctx context.Context) error {
	if l.sshClient != nil {
		l.sshClient.Close()
		l.sshClient = nil
	}

	// 按providerID清理transport
	if l.providerID > 0 {
		provider.GetTransportCleanupManager().CleanupProvider(l.providerID)
	} else if l.transport != nil {
		// fallback: 如果providerID未设置，使用原来的方法
		l.transport.CloseIdleConnections()
		provider.GetTransportCleanupManager().UnregisterTransport(l.transport)
	}
	l.transport = nil

	l.connected = false
	return nil
}

func (l *LXDProvider) IsConnected() bool {
	return l.connected && l.sshClient != nil && l.sshClient.IsHealthy()
}

// EnsureConnection 确保SSH连接可用，如果连接不健康则尝试重连
func (l *LXDProvider) EnsureConnection() error {
	if l.sshClient == nil {
		return fmt.Errorf("SSH client not initialized")
	}

	if !l.sshClient.IsHealthy() {
		global.APP_LOG.Warn("LXD Provider SSH连接不健康，尝试重连",
			zap.String("host", utils.TruncateString(l.config.Host, 50)),
			zap.Int("port", l.config.Port))

		if err := l.sshClient.Reconnect(); err != nil {
			l.connected = false
			return fmt.Errorf("failed to reconnect SSH: %w", err)
		}

		global.APP_LOG.Info("LXD Provider SSH连接重建成功",
			zap.String("host", utils.TruncateString(l.config.Host, 50)),
			zap.Int("port", l.config.Port))
	}

	return nil
}

func (l *LXDProvider) HealthCheck(ctx context.Context) (*health.HealthResult, error) {
	if l.healthChecker == nil {
		return nil, fmt.Errorf("health checker not initialized")
	}
	return l.healthChecker.CheckHealth(ctx)
}

func (l *LXDProvider) GetHealthChecker() health.HealthChecker {
	return l.healthChecker
}

func (l *LXDProvider) ListInstances(ctx context.Context) ([]provider.Instance, error) {
	if !l.connected {
		return nil, fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		instances, err := l.apiListInstances(ctx)
		if err == nil {
			global.APP_LOG.Debug("LXD API调用成功 - 列出实例")
			return instances, nil
		}
		global.APP_LOG.Warn("LXD API失败", zap.Error(err))

		// 检查是否可以回退到SSH
		if !l.shouldFallbackToSSH() {
			return nil, fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH执行 - 列出实例")
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshListInstances(ctx)
}

func (l *LXDProvider) CreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	if !l.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiCreateInstance(ctx, config); err == nil {
			global.APP_LOG.Info("LXD API调用成功 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("LXD API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !l.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH执行 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshCreateInstance(ctx, config)
}

func (l *LXDProvider) CreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	if !l.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiCreateInstanceWithProgress(ctx, config, progressCallback); err == nil {
			global.APP_LOG.Info("LXD API调用成功 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("LXD API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !l.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH执行 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshCreateInstanceWithProgress(ctx, config, progressCallback)
}

func (l *LXDProvider) StartInstance(ctx context.Context, id string) error {
	if !l.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiStartInstance(ctx, id); err == nil {
			global.APP_LOG.Info("LXD API调用成功 - 启动实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("LXD API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !l.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH执行 - 启动实例", zap.String("id", utils.TruncateString(id, 50)))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshStartInstance(ctx, id)
}

func (l *LXDProvider) StopInstance(ctx context.Context, id string) error {
	if !l.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiStopInstance(ctx, id); err == nil {
			global.APP_LOG.Info("LXD API调用成功 - 停止实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("LXD API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !l.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH执行 - 停止实例", zap.String("id", utils.TruncateString(id, 50)))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshStopInstance(ctx, id)
}

func (l *LXDProvider) RestartInstance(ctx context.Context, id string) error {
	if !l.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiRestartInstance(ctx, id); err == nil {
			global.APP_LOG.Info("LXD API调用成功 - 重启实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("LXD API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !l.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH执行 - 重启实例", zap.String("id", utils.TruncateString(id, 50)))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshRestartInstance(ctx, id)
}

func (l *LXDProvider) DeleteInstance(ctx context.Context, id string) error {
	if !l.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiDeleteInstance(ctx, id); err == nil {
			global.APP_LOG.Info("LXD API调用成功 - 删除实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("LXD API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !l.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH执行 - 删除实例", zap.String("id", utils.TruncateString(id, 50)))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshDeleteInstance(ctx, id)
}

func (l *LXDProvider) GetInstance(ctx context.Context, id string) (*provider.Instance, error) {
	instances, err := l.ListInstances(ctx)
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

func (l *LXDProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	if !l.connected {
		return nil, fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		images, err := l.apiListImages(ctx)
		if err == nil {
			global.APP_LOG.Info("LXD API调用成功 - 获取镜像列表")
			return images, nil
		}
		global.APP_LOG.Warn("LXD API失败", zap.Error(err))

		// 检查是否可以回退到SSH
		if !l.shouldFallbackToSSH() {
			return nil, fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH执行 - 获取镜像列表")
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshListImages(ctx)
}

func (l *LXDProvider) PullImage(ctx context.Context, image string) error {
	if !l.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiPullImage(ctx, image); err == nil {
			global.APP_LOG.Info("LXD API调用成功 - 拉取镜像", zap.String("image", utils.TruncateString(image, 100)))
			return nil
		} else {
			global.APP_LOG.Warn("LXD API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !l.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH执行 - 拉取镜像", zap.String("image", utils.TruncateString(image, 100)))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshPullImage(ctx, image)
}

func (l *LXDProvider) DeleteImage(ctx context.Context, id string) error {
	if !l.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiDeleteImage(ctx, id); err == nil {
			global.APP_LOG.Info("LXD API调用成功 - 删除镜像", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("LXD API失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !l.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH执行 - 删除镜像", zap.String("id", utils.TruncateString(id, 50)))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshDeleteImage(ctx, id)
}

// createTLSConfig 创建TLS配置用于API连接
func (l *LXDProvider) createTLSConfig(certPath, keyPath string) (*tls.Config, error) {
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
	global.APP_LOG.Info("LXD客户端证书加载成功",
		zap.String("certPath", certPath),
		zap.String("keyPath", keyPath))

	// 创建TLS配置
	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true, // LXD通常使用自签名证书
		ClientAuth:         tls.RequireAndVerifyClientCert,
	}

	return tlsConfig, nil
}

// ExecuteSSHCommand 执行SSH命令
func (l *LXDProvider) ExecuteSSHCommand(ctx context.Context, command string) (string, error) {
	if !l.connected || l.sshClient == nil {
		return "", fmt.Errorf("LXD provider not connected")
	}

	global.APP_LOG.Debug("执行SSH命令",
		zap.String("command", utils.TruncateString(command, 200)))

	output, err := l.sshClient.Execute(command)
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
func (l *LXDProvider) hasAPIAccess() bool {
	return l.config.CertPath != "" && l.config.KeyPath != ""
}

// shouldUseAPI 根据执行规则判断是否应该使用API
func (l *LXDProvider) shouldUseAPI() bool {
	switch l.config.ExecutionRule {
	case "api_only":
		return l.hasAPIAccess()
	case "ssh_only":
		return false
	case "auto":
		fallthrough
	default:
		return l.hasAPIAccess()
	}
}

// shouldUseSSH 根据执行规则判断是否应该使用SSH
func (l *LXDProvider) shouldUseSSH() bool {
	switch l.config.ExecutionRule {
	case "api_only":
		return false
	case "ssh_only":
		return l.sshClient != nil && l.connected
	case "auto":
		fallthrough
	default:
		return l.sshClient != nil && l.connected
	}
}

// shouldFallbackToSSH 根据执行规则判断API失败时是否可以回退到SSH
func (l *LXDProvider) shouldFallbackToSSH() bool {
	switch l.config.ExecutionRule {
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
func (l *LXDProvider) ensureSSHBeforeFallback(apiErr error, operation string) error {
	if !l.shouldFallbackToSSH() {
		return fmt.Errorf("API调用失败且不允许回退到SSH: %w", apiErr)
	}

	if err := l.EnsureConnection(); err != nil {
		return fmt.Errorf("API失败且SSH连接不可用: API错误=%v, SSH错误=%v", apiErr, err)
	}

	global.APP_LOG.Info(fmt.Sprintf("回退到SSH方式 - %s", operation))
	return nil
}

// SetupPortMappingWithIP 公开的方法：在远程服务器上创建端口映射（用于手动添加端口）
func (l *LXDProvider) SetupPortMappingWithIP(ctx context.Context, instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error {
	return l.setupPortMappingWithIP(instanceName, hostPort, guestPort, protocol, method, instanceIP)
}

// RemovePortMapping 公开的方法：从远程服务器上删除端口映射（用于手动删除端口）
func (l *LXDProvider) RemovePortMapping(instanceName string, hostPort int, protocol string, method string) error {
	return l.removePortMapping(instanceName, hostPort, protocol, method)
}

func init() {
	provider.RegisterProvider("lxd", NewLXDProvider)
}
