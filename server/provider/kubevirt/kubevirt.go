package kubevirt

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
	// Namespace KubeVirt虚拟机所在的K8s命名空间
	Namespace = "kubevirt-vms"
	// ImageDir 镜像存放目录
	ImageDir = "/var/lib/libvirt/images"
	// VMLogDir VM日志目录
	VMLogDir = "/root/vmlog"
	// FWBackendFile 防火墙后端标记文件
	FWBackendFile = "/usr/local/bin/kubevirt_fw_backend"
	// NFTTableName nftables 表名
	NFTTableName = "kubevirt"
)

// KubeVirtProvider 基于 kubectl/virtctl 的 KubeVirt 虚拟机 Provider
type KubeVirtProvider struct {
	config           provider.NodeConfig
	sshClient        *utils.SafeShellExecutor // 永不为nil，所有方法安全调用
	connected        bool
	healthChecker    health.HealthChecker
	version          string
	mu               sync.RWMutex
	imageImportGroup singleflight.Group
}

func NewKubeVirtProvider() provider.Provider {
	return &KubeVirtProvider{
		sshClient: utils.NewSafeShellExecutor(nil),
	}
}

func (p *KubeVirtProvider) GetType() string {
	return "kubevirt"
}

func (p *KubeVirtProvider) GetName() string {
	return p.config.Name
}

func (p *KubeVirtProvider) GetSupportedInstanceTypes() []string {
	return []string{"vm"}
}

func (p *KubeVirtProvider) Connect(ctx context.Context, config provider.NodeConfig) error {
	p.config = config

	// 设置SSH超时配置
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

	p.sshClient.SetExecutor(client)
	p.connected = true

	// 初始化健康检查器，使用Provider的SSH连接
	healthConfig := health.HealthConfig{
		Host:          config.Host,
		Port:          config.Port,
		Username:      config.Username,
		Password:      config.Password,
		PrivateKey:    config.PrivateKey,
		APIEnabled:    false,
		SSHEnabled:    true,
		Timeout:       30 * time.Second,
		ServiceChecks: []string{"kubelet"},
	}

	zapLogger, _ := zap.NewProduction()
	p.healthChecker = health.NewDockerHealthCheckerWithSSH(healthConfig, zapLogger, client.GetUnderlyingClient())

	// 获取 KubeVirt 版本
	if err := p.getKubeVirtVersion(); err != nil {
		global.APP_LOG.Warn("KubeVirt版本获取失败", zap.Error(err))
	}

	global.APP_LOG.Info("KubeVirt provider连接成功",
		zap.String("host", utils.TruncateString(config.Host, 50)),
		zap.Int("port", config.Port),
		zap.String("version", p.version))

	return nil
}

func (p *KubeVirtProvider) ConnectAgent(executor utils.ShellExecutor, config provider.NodeConfig) error {
	p.config = config
	p.sshClient.SetExecutor(executor)
	p.connected = true
	p.healthChecker = nil

	// Agent 模式下版本获取改为异步，避免因 Agent 尚未建立 WebSocket 连接而阻塞 Provider 加载
	go func() {
		if err := p.getKubeVirtVersion(); err != nil {
			global.APP_LOG.Warn("Agent模式下KubeVirt版本获取失败", zap.Error(err))
		}
	}()

	global.APP_LOG.Info("KubeVirt provider (Agent模式) 加载完成",
		zap.String("name", config.Name),
		zap.String("type", config.Type))
	return nil
}

func (p *KubeVirtProvider) Disconnect(ctx context.Context) error {
	p.mu.Lock()
	p.sshClient.Close() // SafeShellExecutor.Close 内部清理executor，无需置nil
	p.mu.Unlock()
	p.connected = false
	return nil
}

func (p *KubeVirtProvider) IsConnected() bool {
	return p.connected && p.sshClient.HasExecutor() && p.sshClient.IsHealthy()
}

// EnsureConnection 确保SSH连接可用，如果连接不健康则尝试重连
func (p *KubeVirtProvider) EnsureConnection() error {
	if !p.sshClient.HasExecutor() {
		return fmt.Errorf("SSH client not initialized")
	}

	if !p.sshClient.IsHealthy() {
		global.APP_LOG.Warn("KubeVirt Provider SSH连接不健康，尝试重连",
			zap.String("host", utils.TruncateString(p.config.Host, 50)),
			zap.Int("port", p.config.Port))

		if err := p.sshClient.Reconnect(); err != nil {
			p.connected = false
			return fmt.Errorf("failed to reconnect SSH: %w", err)
		}
		if !p.sshClient.IsHealthy() {
			p.connected = false
			return fmt.Errorf("connection remains unhealthy after reconnect")
		}

		global.APP_LOG.Info("KubeVirt Provider SSH连接重建成功",
			zap.String("host", utils.TruncateString(p.config.Host, 50)),
			zap.Int("port", p.config.Port))
	}

	return nil
}

func (p *KubeVirtProvider) HealthCheck(ctx context.Context) (*health.HealthResult, error) {
	if p.healthChecker == nil {
		if !p.sshClient.HasExecutor() {
			return nil, fmt.Errorf("health checker not initialized")
		}
		status := health.HealthStatusUnhealthy
		sshStatus := "offline"
		if p.sshClient.IsHealthy() {
			status = health.HealthStatusHealthy
			sshStatus = "online"
		}
		return &health.HealthResult{
			Status:        status,
			Timestamp:     time.Now(),
			SSHStatus:     sshStatus,
			APIStatus:     "unknown",
			ServiceStatus: "unknown",
			HostName:      p.config.HostName,
		}, nil
	}
	return p.healthChecker.CheckHealth(ctx)
}

func (p *KubeVirtProvider) GetHealthChecker() health.HealthChecker {
	return p.healthChecker
}

func (p *KubeVirtProvider) GetVersion() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.version
}

// getKubeVirtVersion 获取 KubeVirt 版本
func (p *KubeVirtProvider) getKubeVirtVersion() error {
	if !p.sshClient.HasExecutor() {
		return fmt.Errorf("SSH client not connected")
	}

	output, err := p.sshClient.Execute("kubectl get kubevirt -n kubevirt -o jsonpath='{.items[0].status.observedKubeVirtVersion}' 2>/dev/null || virtctl version --client 2>/dev/null")
	if err != nil {
		p.version = "unknown"
		return err
	}

	version := strings.TrimSpace(output)
	if version != "" {
		p.version = version
		return nil
	}

	p.version = "unknown"
	return fmt.Errorf("unable to parse version")
}

// ExecuteSSHCommand 执行SSH命令
func (p *KubeVirtProvider) ExecuteSSHCommand(ctx context.Context, command string) (string, error) {
	if !p.connected || !p.sshClient.HasExecutor() {
		return "", fmt.Errorf("KubeVirt provider not connected")
	}

	global.APP_LOG.Debug("执行SSH命令",
		zap.String("command", utils.RedactSensitiveCommand(command, 200)))

	output, err := p.sshClient.Execute(command)
	if err != nil {
		global.APP_LOG.Error("SSH命令执行失败",
			zap.String("command", utils.RedactSensitiveCommand(command, 200)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return "", fmt.Errorf("SSH command execution failed: %w", err)
	}

	return output, nil
}

func init() {
	provider.RegisterProvider("kubevirt", NewKubeVirtProvider)
}
