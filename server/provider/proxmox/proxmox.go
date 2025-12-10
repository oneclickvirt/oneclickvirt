package proxmox

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/provider/health"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// Proxmox VMID分配常量
// 统一管理VMID范围，不区分VM和Container类型
const (
	// MinVMID 最小VMID，保留1-9给系统使用
	MinVMID = 10
	// MaxVMID 最大VMID，对应内网IP 172.16.1.255
	MaxVMID = 255
	// MaxInstances 最大实例数量（10-255共246个）
	MaxInstances = 246
	// InternalIPPrefix 内网IP前缀
	InternalIPPrefix = "172.16.1"
	// InternalGateway 内网网关
	InternalGateway = "172.16.1.1"
)

type ProxmoxProvider struct {
	config        provider.NodeConfig
	sshClient     *utils.SSHClient
	apiClient     *http.Client
	transport     *http.Transport
	providerID    uint // 存储providerID用于清理
	connected     bool
	node          string // Proxmox 节点名
	providerUUID  string // Provider UUID，用于查询数据库中的配置
	healthChecker health.HealthChecker
	mu            sync.RWMutex // 保护并发访问
}

func NewProxmoxProvider() provider.Provider {
	// 创建独立的 Transport，不使用 sync.Pool
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	// 注册到清理管理器（自动去重）
	provider.GetTransportCleanupManager().RegisterTransport(transport)
	return &ProxmoxProvider{
		transport: transport,
		apiClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
	}
}

func (p *ProxmoxProvider) GetType() string {
	return "proxmox"
}

func (p *ProxmoxProvider) GetName() string {
	return p.config.Name
}

func (p *ProxmoxProvider) GetSupportedInstanceTypes() []string {
	return []string{"container", "vm"}
}

func (p *ProxmoxProvider) Connect(ctx context.Context, config provider.NodeConfig) error {
	p.config = config
	p.providerUUID = config.UUID // 存储Provider UUID
	p.providerID = config.ID     // 存储providerID

	// 注册transport并关联providerID
	if p.transport != nil && p.providerID > 0 {
		provider.GetTransportCleanupManager().RegisterTransportWithProvider(p.transport, p.providerID)
	}

	// 如果有本地存储的 Token 文件，尝试从文件加载 Token 信息
	if err := p.loadTokenFromFiles(); err != nil {
		global.APP_LOG.Warn("从本地文件加载token失败，使用配置值", zap.Error(err))
	}

	// 如果本地文件没有 Token，尝试从 NodeConfig 的扩展配置中解析
	if !p.hasAPIAccess() {
		if err := p.loadTokenFromConfig(); err != nil {
			global.APP_LOG.Warn("从配置加载token失败，将仅使用SSH", zap.Error(err))
		}
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

	p.sshClient = client
	p.connected = true

	// 获取节点名：优先使用配置中的HostName（数据库存储的），否则动态获取
	if config.HostName != "" {
		p.node = config.HostName
		global.APP_LOG.Info("使用数据库配置的Proxmox主机名",
			zap.String("hostName", p.node),
			zap.String("provider", config.Name),
			zap.String("host", utils.TruncateString(config.Host, 32)))
	} else {
		// 动态获取节点名
		if err := p.getNodeName(ctx); err != nil {
			global.APP_LOG.Warn("获取主机名失败，使用默认值",
				zap.Error(err),
				zap.String("host", utils.TruncateString(config.Host, 32)))
			p.node = "pve" // 默认节点名
		} else {
			global.APP_LOG.Info("动态获取Proxmox主机名成功",
				zap.String("hostName", p.node),
				zap.String("provider", config.Name),
				zap.String("host", utils.TruncateString(config.Host, 32)))
		}
	}

	// 初始化健康检查器，使用Provider的SSH连接，避免创建独立连接导致节点混淆
	healthConfig := health.HealthConfig{
		Host:          config.Host,
		Port:          config.Port,
		Username:      config.Username,
		Password:      config.Password,
		PrivateKey:    config.PrivateKey,
		APIEnabled:    p.hasAPIAccess(),
		APIPort:       8006,
		APIScheme:     "https",
		SSHEnabled:    true,
		SkipTLSVerify: true, // Proxmox通常使用自签名证书，需要跳过TLS验证
		Timeout:       30 * time.Second,
		ServiceChecks: []string{"pvestatd", "pvedaemon", "pveproxy"},
		Token:         config.Token,
		TokenID:       config.TokenID,
	}

	zapLogger, _ := zap.NewProduction()
	// 使用Provider的SSH连接创建健康检查器，确保在正确的节点上执行命令
	p.healthChecker = health.NewProxmoxHealthCheckerWithSSH(healthConfig, zapLogger, client.GetUnderlyingClient())

	global.APP_LOG.Info("Proxmox provider SSH连接成功",
		zap.String("host", utils.TruncateString(config.Host, 32)),
		zap.Int("port", config.Port),
		zap.String("node", utils.TruncateString(p.node, 32)),
		zap.Bool("hasToken", p.hasAPIAccess()))

	return nil
}

func (p *ProxmoxProvider) Disconnect(ctx context.Context) error {
	if p.sshClient != nil {
		p.sshClient.Close()
		p.sshClient = nil
	}

	// 按providerID清理transport
	if p.providerID > 0 {
		provider.GetTransportCleanupManager().CleanupProvider(p.providerID)
	} else if p.transport != nil {
		// fallback: 如果providerID未设置，使用原来的方法
		p.transport.CloseIdleConnections()
		provider.GetTransportCleanupManager().UnregisterTransport(p.transport)
	}
	p.transport = nil

	p.connected = false
	return nil
}

func (p *ProxmoxProvider) IsConnected() bool {
	return p.connected && p.sshClient != nil && p.sshClient.IsHealthy()
}

// EnsureConnection 确保SSH连接可用，如果连接不健康则尝试重连
func (p *ProxmoxProvider) EnsureConnection() error {
	if p.sshClient == nil {
		return fmt.Errorf("SSH client not initialized")
	}

	if !p.sshClient.IsHealthy() {
		global.APP_LOG.Warn("Proxmox Provider SSH连接不健康，尝试重连",
			zap.String("host", utils.TruncateString(p.config.Host, 32)),
			zap.Int("port", p.config.Port))

		if err := p.sshClient.Reconnect(); err != nil {
			p.connected = false
			return fmt.Errorf("failed to reconnect SSH: %w", err)
		}

		global.APP_LOG.Info("Proxmox Provider SSH连接重建成功",
			zap.String("host", utils.TruncateString(p.config.Host, 32)),
			zap.Int("port", p.config.Port))
	}

	return nil
}

func (p *ProxmoxProvider) HealthCheck(ctx context.Context) (*health.HealthResult, error) {
	if p.healthChecker == nil {
		return nil, fmt.Errorf("health checker not initialized")
	}
	return p.healthChecker.CheckHealth(ctx)
}

func (p *ProxmoxProvider) GetHealthChecker() health.HealthChecker {
	return p.healthChecker
}

// 获取节点名
func (p *ProxmoxProvider) getNodeName(ctx context.Context) error {
	output, err := p.sshClient.Execute("hostname")
	if err != nil {
		return err
	}
	p.node = strings.TrimSpace(output)
	return nil
}

// ExecuteSSHCommand 执行SSH命令
func (p *ProxmoxProvider) ExecuteSSHCommand(ctx context.Context, command string) (string, error) {
	if !p.connected || p.sshClient == nil {
		return "", fmt.Errorf("Proxmox provider not connected")
	}

	global.APP_LOG.Debug("执行SSH命令",
		zap.String("command", utils.TruncateString(command, 200)))

	output, err := p.sshClient.Execute(command)
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
func (p *ProxmoxProvider) hasAPIAccess() bool {
	// 检查是否配置了 API Token ID 和 Token Secret
	return p.config.TokenID != "" && p.config.Token != ""
}

// shouldUseAPI 根据执行规则判断是否应该使用API
func (p *ProxmoxProvider) shouldUseAPI() bool {
	switch p.config.ExecutionRule {
	case "api_only":
		return p.hasAPIAccess()
	case "ssh_only":
		return false
	case "auto":
		fallthrough
	default:
		return p.hasAPIAccess()
	}
}

// shouldUseSSH 根据执行规则判断是否应该使用SSH
func (p *ProxmoxProvider) shouldUseSSH() bool {
	switch p.config.ExecutionRule {
	case "api_only":
		return false
	case "ssh_only":
		return p.sshClient != nil && p.connected
	case "auto":
		fallthrough
	default:
		return p.sshClient != nil && p.connected
	}
}

// GetIPv6NetworkInterface 获取实例对应的宿主机IPv6网络接口名称
// 对于Proxmox，根据实例类型和ID识别：
// - LXC容器：veth<ctid>i0 或 veth<ctid>i1（如果有多个网络接口）
// - KVM虚拟机：tap<vmid>i0 或 tap<vmid>i1（如果有多个网络接口）
func (p *ProxmoxProvider) GetIPv6NetworkInterface(ctx context.Context, instanceName string) (string, error) {
	// 从数据库查询实例信息，检查是否有公网IPv6地址
	var instance struct {
		PublicIPv6 string
	}
	query := `SELECT public_ipv6 FROM instances WHERE name = ? AND provider_id = ?`
	err := global.APP_DB.Raw(query, instanceName, p.providerID).Scan(&instance).Error
	if err != nil || instance.PublicIPv6 == "" {
		global.APP_LOG.Debug("实例没有公网IPv6地址，跳过IPv6网络接口检测",
			zap.String("instanceName", instanceName),
			zap.String("publicIPv6", instance.PublicIPv6),
			zap.Error(err))
		return "", fmt.Errorf("no public IPv6 address for instance %s", instanceName)
	}

	// 从实例名称中提取VMID/CTID和实例类型
	vmid, instanceType, err := p.parseInstanceInfo(ctx, instanceName)
	if err != nil {
		return "", fmt.Errorf("failed to parse instance info: %w", err)
	}

	// 根据实例类型构建可能的接口名称
	var interfacePrefix string
	if instanceType == "container" {
		interfacePrefix = "veth"
	} else {
		interfacePrefix = "tap"
	}

	// 检测实例的网络配置，可能有多个网络接口
	// 优先查找 i1（IPv6接口），如果没有则使用 i0
	for _, ifIndex := range []string{"i1", "i0"} {
		interfaceName := fmt.Sprintf("%s%s%s", interfacePrefix, vmid, ifIndex)
		checkCmd := fmt.Sprintf("ip link show %s 2>/dev/null", interfaceName)
		output, err := p.sshClient.Execute(checkCmd)
		if err == nil && strings.TrimSpace(output) != "" {
			// 验证该接口是否有IPv6地址
			checkIPv6Cmd := fmt.Sprintf("ip -6 addr show dev %s | grep -q 'inet6.*global'", interfaceName)
			_, err := p.sshClient.Execute(checkIPv6Cmd)
			if err == nil {
				global.APP_LOG.Info("检测到Proxmox实例的IPv6网络接口",
					zap.String("instanceName", instanceName),
					zap.String("vmid", vmid),
					zap.String("type", instanceType),
					zap.String("interface", interfaceName))
				return interfaceName, nil
			}
		}
	}

	return "", fmt.Errorf("no IPv6 network interface found for instance %s", instanceName)
}

// parseInstanceInfo 从实例名称解析VMID和实例类型
func (p *ProxmoxProvider) parseInstanceInfo(ctx context.Context, instanceName string) (string, string, error) {
	// 首先尝试从数据库中查找实例
	var instance struct {
		VMID         string
		InstanceType string
	}

	query := `SELECT vm_id as vmid, instance_type FROM instances WHERE name = ? AND provider_id = ?`
	err := global.APP_DB.Raw(query, instanceName, p.providerID).Scan(&instance).Error
	if err == nil && instance.VMID != "" {
		return instance.VMID, instance.InstanceType, nil
	}

	// 如果数据库查询失败，尝试通过SSH命令查询
	// 先检查是否是容器
	checkContainerCmd := fmt.Sprintf("pct list | grep -w '%s' | awk '{print $1}'", instanceName)
	output, err := p.sshClient.Execute(checkContainerCmd)
	if err == nil && strings.TrimSpace(output) != "" {
		return strings.TrimSpace(output), "container", nil
	}

	// 再检查是否是虚拟机
	checkVMCmd := fmt.Sprintf("qm list | grep -w '%s' | awk '{print $1}'", instanceName)
	output, err = p.sshClient.Execute(checkVMCmd)
	if err == nil && strings.TrimSpace(output) != "" {
		return strings.TrimSpace(output), "vm", nil
	}

	return "", "", fmt.Errorf("instance %s not found", instanceName)
}

// shouldFallbackToSSH 根据执行规则判断 API失败时是否可以回退到SSH
func (p *ProxmoxProvider) shouldFallbackToSSH() bool {
	switch p.config.ExecutionRule {
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
func (p *ProxmoxProvider) ensureSSHBeforeFallback(apiErr error, operation string) error {
	if !p.shouldFallbackToSSH() {
		return fmt.Errorf("API调用失败且不允许回退到SSH: %w", apiErr)
	}

	if err := p.EnsureConnection(); err != nil {
		return fmt.Errorf("API失败且SSH连接不可用: API错误=%v, SSH错误=%v", apiErr, err)
	}

	global.APP_LOG.Info(fmt.Sprintf("回退到SSH方式 - %s", operation))
	return nil
}

// setAPIAuth 为 HTTP 请求设置 API 认证头
func (p *ProxmoxProvider) setAPIAuth(req *http.Request) {
	if p.config.TokenID != "" && p.config.Token != "" {
		// 清理Token ID和Token中的不可见字符（换行符、回车符、制表符等）
		cleanTokenID := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(p.config.TokenID), "\n", ""), "\r", "")
		cleanToken := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(p.config.Token), "\n", ""), "\r", "")

		// 使用 API Token 认证，格式: PVEAPIToken=USER@REALM!TOKENID=SECRET
		authHeader := fmt.Sprintf("PVEAPIToken=%s=%s", cleanTokenID, cleanToken)
		req.Header.Set("Authorization", authHeader)
	}
}

func init() {
	provider.RegisterProvider("proxmox", NewProxmoxProvider)
}
