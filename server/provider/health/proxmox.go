package health

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"oneclickvirt/utils"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// ProxmoxHealthChecker Proxmox健康检查器
type ProxmoxHealthChecker struct {
	*BaseHealthChecker
	sshClient      *ssh.Client
	useExternalSSH bool       // 标识是否使用外部SSH连接
	shouldCloseSSH bool       // 标识是否应该关闭SSH连接（仅当自己创建时才关闭）
	mu             sync.Mutex // 保护并发访问sshClient和config字段
}

// NewProxmoxHealthChecker 创建Proxmox健康检查器
func NewProxmoxHealthChecker(config HealthConfig, logger *zap.Logger) *ProxmoxHealthChecker {
	checker := &ProxmoxHealthChecker{
		BaseHealthChecker: NewBaseHealthChecker(config, logger),
		shouldCloseSSH:    true, // 默认情况下，自己创建的连接应该关闭
	}
	if logger != nil {
		logger.Info("创建新的ProxmoxHealthChecker实例",
			zap.String("checkerType", "ProxmoxHealthChecker"),
			zap.String("instancePtr", fmt.Sprintf("%p", checker)),
			zap.String("configHost", config.Host),
			zap.Int("configPort", config.Port),
			zap.Uint("providerID", config.ProviderID),
			zap.String("providerName", config.ProviderName),
			zap.String("baseCheckerPtr", fmt.Sprintf("%p", checker.BaseHealthChecker)))
	}
	return checker
}

// NewProxmoxHealthCheckerWithSSH 创建使用外部SSH连接的Proxmox健康检查器
func NewProxmoxHealthCheckerWithSSH(config HealthConfig, logger *zap.Logger, sshClient *ssh.Client) *ProxmoxHealthChecker {
	return &ProxmoxHealthChecker{
		BaseHealthChecker: NewBaseHealthChecker(config, logger),
		sshClient:         sshClient,
		useExternalSSH:    true,
		shouldCloseSSH:    false, // 使用外部连接，不应该关闭
	}
}

// CheckHealth 执行Proxmox健康检查
func (p *ProxmoxHealthChecker) CheckHealth(ctx context.Context) (*HealthResult, error) {
	checks := []func(context.Context) CheckResult{}

	// SSH检查
	if p.config.SSHEnabled {
		checks = append(checks, p.createCheckFunc(CheckTypeSSH, p.checkSSH))
	}

	// API检查
	if p.config.APIEnabled {
		checks = append(checks, p.createCheckFunc(CheckTypeAPI, p.checkAPI))
	}

	// Proxmox服务检查
	if len(p.config.ServiceChecks) > 0 {
		checks = append(checks, p.createCheckFunc(CheckTypeService, p.checkProxmoxService))
	}

	result := p.executeChecks(ctx, checks)

	// 获取节点hostname（如果SSH连接成功）
	if result.SSHStatus == "online" && p.sshClient != nil {
		if hostname, err := p.getHostname(ctx); err == nil {
			result.HostName = hostname
			if p.logger != nil {
				p.logger.Debug("获取Proxmox节点hostname成功",
					zap.String("hostname", hostname),
					zap.String("host", p.config.Host))
			}
		} else if p.logger != nil {
			p.logger.Warn("获取Proxmox节点hostname失败",
				zap.String("host", p.config.Host),
				zap.Error(err))
		}
	}

	return result, nil
}

// checkSSH 检查SSH连接
func (p *ProxmoxHealthChecker) checkSSH(ctx context.Context) error {
	// 加锁保护并发访问
	p.mu.Lock()
	defer p.mu.Unlock()

	// 如果使用外部SSH连接，只测试连接是否可用
	// 重要：使用外部SSH连接时，绝不创建新连接，确保在正确的节点上执行
	if p.useExternalSSH {
		if p.sshClient == nil {
			return fmt.Errorf("external SSH client is nil")
		}
		// 测试现有连接
		session, err := p.sshClient.NewSession()
		if err != nil {
			return fmt.Errorf("external SSH connection test failed: %w", err)
		}
		session.Close()
		if p.logger != nil {
			p.logger.Debug("使用外部SSH连接检查成功（使用Provider的SSH连接，确保在正确节点）",
				zap.String("host", p.config.Host))
		}
		return nil
	}

	// 非外部连接模式：自己管理SSH连接
	// 重要：为了避免并发问题，总是关闭旧连接并创建新连接
	// 这确保每次health check都连接到正确的服务器
	if p.sshClient != nil {
		if p.logger != nil {
			existingRemoteAddr := ""
			if p.sshClient.Conn != nil {
				existingRemoteAddr = p.sshClient.Conn.RemoteAddr().String()
			}
			p.logger.Info("关闭现有SSH连接，准备创建新连接（防止并发连接错误）",
				zap.String("configHost", p.config.Host),
				zap.Int("configPort", p.config.Port),
				zap.String("existingRemoteAddr", existingRemoteAddr))
		}
		// 总是关闭现有连接
		p.sshClient.Close()
		p.sshClient = nil
	}

	// 构建认证方法：优先使用SSH密钥，否则使用密码
	// 构建认证方法：支持密钥和密码，SSH客户端会按顺序尝试
	var authMethods []ssh.AuthMethod

	// 如果提供了SSH私钥，添加密钥认证
	if p.config.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(p.config.PrivateKey))
		if err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
			if p.logger != nil {
				p.logger.Debug("已添加SSH密钥认证方法", zap.String("host", p.config.Host))
			}
		} else if p.logger != nil {
			p.logger.Warn("SSH私钥解析失败，将尝试使用密码认证",
				zap.String("host", p.config.Host),
				zap.Error(err))
		}
	}

	// 如果提供了密码，添加密码认证（无论是否有密钥，都添加作为备用方案）
	if p.config.Password != "" {
		authMethods = append(authMethods, ssh.Password(p.config.Password))
		if p.logger != nil {
			p.logger.Debug("已添加SSH密码认证方法", zap.String("host", p.config.Host))
		}
	}

	// 如果既没有密钥也没有密码，返回错误
	if len(authMethods) == 0 {
		return fmt.Errorf("no authentication method available: neither SSH key nor password provided")
	}

	// 建立新连接
	config := &ssh.ClientConfig{
		User:            p.config.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         p.config.Timeout,
	}

	address := fmt.Sprintf("%s:%d", p.config.Host, p.config.Port)
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return fmt.Errorf("SSH连接失败: %w", err)
	}

	// 验证SSH连接的远程地址是否匹配预期的主机（支持域名解析）
	if err := utils.VerifySSHConnection(client, p.config.Host); err != nil {
		if p.logger != nil {
			p.logger.Error("Proxmox SSH连接地址验证失败",
				zap.String("host", p.config.Host),
				zap.Int("port", p.config.Port),
				zap.Error(err))
		}
		client.Close()
		return err
	}

	p.sshClient = client
	if p.logger != nil {
		p.logger.Debug("Proxmox SSH连接验证成功", zap.String("host", p.config.Host), zap.Int("port", p.config.Port))
	}
	return nil
}

// checkAPI 检查Proxmox API
func (p *ProxmoxHealthChecker) checkAPI(ctx context.Context) error {
	// Proxmox API标准端口是8006
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes", p.config.Host)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("创建API请求失败: %w", err)
	}

	// 设置认证头
	if p.config.Token != "" && p.config.TokenID != "" {
		// 清理Token ID和Token中的不可见字符（换行符、回车符、制表符等）
		cleanTokenID := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(p.config.TokenID), "\n", ""), "\r", "")
		cleanToken := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(p.config.Token), "\n", ""), "\r", "")
		req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", cleanTokenID, cleanToken))
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Proxmox API连接失败 (检查Proxmox是否运行且API端口8006可访问，以及Token配置是否正确): %w", err)
	}
	defer resp.Body.Close()

	// 只有成功获取到节点列表才认为API健康
	// 401表示认证失败，说明Token配置有问题
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("Proxmox API认证失败 (状态码: %d) - 请检查API Token和TokenID配置", resp.StatusCode)
		}
		return fmt.Errorf("Proxmox API返回错误状态码: %d", resp.StatusCode)
	}

	if p.logger != nil {
		p.logger.Debug("Proxmox API检查成功", zap.String("url", url), zap.Int("status", resp.StatusCode))
	}
	return nil
}

// checkProxmoxService 检查Proxmox服务状态
func (p *ProxmoxHealthChecker) checkProxmoxService(ctx context.Context) error {
	// 如果使用外部SSH连接，必须确保连接已建立
	if p.useExternalSSH {
		if p.sshClient == nil {
			return fmt.Errorf("external SSH client is required for service check but is nil")
		}
		// 不建立新连接，确保使用Provider的SSH连接
	} else if p.sshClient == nil {
		// 仅在非外部连接模式下才建立新连接
		if err := p.checkSSH(ctx); err != nil {
			return fmt.Errorf("无法建立SSH连接进行服务检查: %w", err)
		}
	}

	// 检查PVE版本
	session, err := p.sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("创建SSH会话失败: %w", err)
	}
	defer session.Close()

	// 请求PTY以模拟交互式登录shell，确保加载完整的环境变量
	err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{
		ssh.ECHO:          0,     // 禁用回显
		ssh.TTY_OP_ISPEED: 14400, // 输入速度
		ssh.TTY_OP_OSPEED: 14400, // 输出速度
	})
	if err != nil {
		return fmt.Errorf("请求PTY失败: %w", err)
	}

	// 设置环境变量来确保PATH正确加载，避免bash -l -c的转义问题
	envCommand := "source /etc/profile 2>/dev/null || true; source ~/.bashrc 2>/dev/null || true; source ~/.bash_profile 2>/dev/null || true; export PATH=$PATH:/usr/local/bin:/snap/bin:/usr/sbin:/sbin; pveversion"
	output, err := session.CombinedOutput(envCommand)
	if err != nil {
		return fmt.Errorf("Proxmox服务不可用: %w", err)
	}

	if !strings.Contains(string(output), "proxmox-ve") {
		return fmt.Errorf("Proxmox VE未正确安装")
	}

	// 检查关键服务状态
	services := []string{"pvedaemon", "pveproxy", "pvestatd"}
	for _, service := range services {
		session, err := p.sshClient.NewSession()
		if err != nil {
			return fmt.Errorf("创建SSH会话失败: %w", err)
		}

		// 请求PTY以模拟交互式登录shell
		err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{
			ssh.ECHO:          0,     // 禁用回显
			ssh.TTY_OP_ISPEED: 14400, // 输入速度
			ssh.TTY_OP_OSPEED: 14400, // 输出速度
		})
		if err != nil {
			session.Close()
			return fmt.Errorf("请求PTY失败: %w", err)
		}

		// 设置环境变量来确保PATH正确加载
		envCommand := fmt.Sprintf("source /etc/profile 2>/dev/null || true; source ~/.bashrc 2>/dev/null || true; source ~/.bash_profile 2>/dev/null || true; export PATH=$PATH:/usr/local/bin:/snap/bin:/usr/sbin:/sbin; systemctl is-active %s", service)
		_, err = session.CombinedOutput(envCommand)
		session.Close()

		if err != nil {
			return fmt.Errorf("Proxmox服务 %s 未运行: %w", service, err)
		}
	}

	if p.logger != nil {
		p.logger.Debug("Proxmox服务检查成功", zap.String("host", p.config.Host), zap.Strings("services", services))
	}
	return nil
}

// getHostname 获取节点hostname
func (p *ProxmoxHealthChecker) getHostname(ctx context.Context) (string, error) {
	// 加锁保护并发访问
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.sshClient == nil {
		return "", fmt.Errorf("SSH连接未建立")
	}

	session, err := p.sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("创建SSH会话失败: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput("hostname")
	if err != nil {
		return "", fmt.Errorf("执行hostname命令失败: %w", err)
	}

	hostname := strings.TrimSpace(string(output))
	if hostname == "" {
		return "", fmt.Errorf("hostname为空")
	}

	if p.logger != nil {
		p.logger.Debug("获取到Proxmox节点hostname",
			zap.String("hostname", hostname),
			zap.String("host", p.config.Host),
			zap.Bool("useExternalSSH", p.useExternalSSH))
	}

	return hostname, nil
}

// Close 关闭连接
func (p *ProxmoxHealthChecker) Close() error {
	// 只有在应该关闭SSH连接时才关闭（即自己创建的连接）
	if p.shouldCloseSSH && p.sshClient != nil {
		err := p.sshClient.Close()
		p.sshClient = nil
		return err
	}
	// 如果使用外部连接，只清空引用，不关闭连接
	if p.useExternalSSH {
		p.sshClient = nil
	}
	return nil
}
