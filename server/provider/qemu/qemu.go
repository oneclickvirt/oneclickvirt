package qemu

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
	// ImageDir qcow2 镜像存放目录
	ImageDir = "/var/lib/libvirt/images"
	// VMLogDir VM 信息日志目录
	VMLogDir = "/root/vmlog"
	// InternalSubnet 内网网段
	InternalSubnet = "192.168.122.0/24"
	// InternalGateway 内网网关
	InternalGateway = "192.168.122.1"
	// FWBackendFile 防火墙后端标记文件
	FWBackendFile = "/usr/local/bin/qemu_fw_backend"
	// NFTTableName nftables 表名
	NFTTableName = "qemu"
)

// QEMUProvider 基于 libvirt/virsh 的 QEMU/KVM 虚拟机 Provider
type QEMUProvider struct {
	config           provider.NodeConfig
	sshClient        *utils.SafeShellExecutor // 永不为nil，所有方法安全调用
	connected        bool
	healthChecker    health.HealthChecker
	version          string
	mu               sync.RWMutex
	ipMu             sync.Mutex // IP分配互斥锁，防止并发创建时分配到相同IP
	imageImportGroup singleflight.Group
}

func NewQEMUProvider() provider.Provider {
	return &QEMUProvider{
		sshClient: utils.NewSafeShellExecutor(nil),
	}
}

func (p *QEMUProvider) GetType() string {
	return "qemu"
}

func (p *QEMUProvider) GetName() string {
	return p.config.Name
}

func (p *QEMUProvider) GetSupportedInstanceTypes() []string {
	return []string{"vm"}
}

func (p *QEMUProvider) Connect(ctx context.Context, config provider.NodeConfig) error {
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
		ServiceChecks: []string{"libvirtd"},
	}

	zapLogger, _ := zap.NewProduction()
	p.healthChecker = health.NewDockerHealthCheckerWithSSH(healthConfig, zapLogger, client.GetUnderlyingClient())

	// 获取 QEMU/libvirt 版本
	if err := p.getVersion(); err != nil {
		global.APP_LOG.Warn("QEMU版本获取失败", zap.Error(err))
	}

	global.APP_LOG.Info("QEMU provider连接成功",
		zap.String("host", utils.TruncateString(config.Host, 50)),
		zap.Int("port", config.Port),
		zap.String("version", p.version))

	return nil
}

func (p *QEMUProvider) ConnectAgent(executor utils.ShellExecutor, config provider.NodeConfig) error {
	p.config = config
	p.sshClient.SetExecutor(executor)
	p.connected = true
	p.healthChecker = nil

	// Agent 模式下版本获取改为异步，避免因 Agent 尚未建立 WebSocket 连接而阻塞 Provider 加载
	go func() {
		if err := p.getVersion(); err != nil {
			global.APP_LOG.Warn("Agent模式下QEMU版本获取失败", zap.Error(err))
		}
	}()

	global.APP_LOG.Info("QEMU provider (Agent模式) 加载完成",
		zap.String("name", config.Name),
		zap.String("type", config.Type))
	return nil
}

func (p *QEMUProvider) Disconnect(ctx context.Context) error {
	p.mu.Lock()
	p.sshClient.Close() // SafeShellExecutor.Close 内部清理executor，无需置nil
	p.mu.Unlock()
	p.connected = false
	return nil
}

func (p *QEMUProvider) IsConnected() bool {
	return p.connected && p.sshClient.HasExecutor() && p.sshClient.IsHealthy()
}

// EnsureConnection 确保SSH连接可用，如果连接不健康则尝试重连
func (p *QEMUProvider) EnsureConnection() error {
	if !p.sshClient.HasExecutor() {
		return fmt.Errorf("SSH client not initialized")
	}

	if !p.sshClient.IsHealthy() {
		global.APP_LOG.Warn("QEMU Provider SSH连接不健康，尝试重连",
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

		global.APP_LOG.Info("QEMU Provider SSH连接重建成功",
			zap.String("host", utils.TruncateString(p.config.Host, 50)),
			zap.Int("port", p.config.Port))
	}

	return nil
}

func (p *QEMUProvider) HealthCheck(ctx context.Context) (*health.HealthResult, error) {
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

func (p *QEMUProvider) GetHealthChecker() health.HealthChecker {
	return p.healthChecker
}

func (p *QEMUProvider) GetVersion() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.version
}

// getVersion 获取 QEMU/libvirt 版本
func (p *QEMUProvider) getVersion() error {
	if !p.sshClient.HasExecutor() {
		return fmt.Errorf("SSH client not connected")
	}

	output, err := p.sshClient.Execute("virsh version --short 2>/dev/null || virsh --version 2>/dev/null")
	if err != nil {
		p.version = "unknown"
		return err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			p.version = line
			return nil
		}
	}

	p.version = "unknown"
	return fmt.Errorf("unable to parse version")
}

// ExecuteSSHCommand 执行SSH命令
func (p *QEMUProvider) ExecuteSSHCommand(ctx context.Context, command string) (string, error) {
	p.mu.RLock()
	client := p.sshClient
	p.mu.RUnlock()
	if !p.connected || client == nil {
		return "", fmt.Errorf("QEMU provider not connected")
	}

	global.APP_LOG.Debug("执行SSH命令",
		zap.String("command", utils.RedactSensitiveCommand(command, 200)))

	output, err := client.Execute(command)
	if err != nil {
		global.APP_LOG.Error("SSH命令执行失败",
			zap.String("command", utils.RedactSensitiveCommand(command, 200)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return output, fmt.Errorf("SSH command execution failed: %w; output: %s", err, utils.TruncateString(output, 2000))
	}

	return output, nil
}

func init() {
	provider.RegisterProvider("qemu", NewQEMUProvider)
}
