package health

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"oneclickvirt/utils"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// LXDHealthChecker LXD健康检查器
type LXDHealthChecker struct {
	*BaseHealthChecker
	sshClient      *ssh.Client
	useExternalSSH bool       // 标识是否使用外部SSH连接
	shouldCloseSSH bool       // 标识是否应该关闭SSH连接（仅当自己创建时才关闭）
	mu             sync.Mutex // 保护并发访问sshClient和config字段
}

// NewLXDHealthChecker 创建LXD健康检查器
func NewLXDHealthChecker(config HealthConfig, logger *zap.Logger) *LXDHealthChecker {
	checker := &LXDHealthChecker{
		BaseHealthChecker: NewBaseHealthChecker(config, logger),
		shouldCloseSSH:    true, // 默认情况下，自己创建的连接应该关闭
	}
	if logger != nil {
		logger.Info("创建新的LXDHealthChecker实例",
			zap.String("checkerType", "LXDHealthChecker"),
			zap.String("instancePtr", fmt.Sprintf("%p", checker)),
			zap.String("configHost", config.Host),
			zap.Int("configPort", config.Port),
			zap.Uint("providerID", config.ProviderID),
			zap.String("providerName", config.ProviderName),
			zap.String("baseCheckerPtr", fmt.Sprintf("%p", checker.BaseHealthChecker)))
	}
	return checker
}

// NewLXDHealthCheckerWithSSH 创建使用外部SSH连接的LXD健康检查器
func NewLXDHealthCheckerWithSSH(config HealthConfig, logger *zap.Logger, sshClient *ssh.Client) *LXDHealthChecker {
	return &LXDHealthChecker{
		BaseHealthChecker: NewBaseHealthChecker(config, logger),
		sshClient:         sshClient,
		useExternalSSH:    true,
		shouldCloseSSH:    false, // 使用外部连接，不应该关闭
	}
}

// CheckHealth 执行LXD健康检查
func (l *LXDHealthChecker) CheckHealth(ctx context.Context) (*HealthResult, error) {
	checks := []func(context.Context) CheckResult{}

	// SSH检查
	if l.config.SSHEnabled {
		checks = append(checks, l.createCheckFunc(CheckTypeSSH, l.checkSSH))
	}

	// API检查
	if l.config.APIEnabled {
		checks = append(checks, l.createCheckFunc(CheckTypeAPI, l.checkAPI))
	}

	// LXD服务检查
	if len(l.config.ServiceChecks) > 0 {
		checks = append(checks, l.createCheckFunc(CheckTypeService, l.checkLXDService))
	}

	result := l.executeChecks(ctx, checks)

	// 获取节点hostname（如果SSH连接成功）
	if result.SSHStatus == "online" && l.sshClient != nil {
		if hostname, err := l.getHostname(ctx); err == nil {
			result.HostName = hostname
			if l.logger != nil {
				l.logger.Debug("获取LXD节点hostname成功",
					zap.String("hostname", hostname),
					zap.String("host", l.config.Host))
			}
		} else if l.logger != nil {
			l.logger.Warn("获取LXD节点hostname失败",
				zap.String("host", l.config.Host),
				zap.Error(err))
		}
	}

	return result, nil
}

// checkSSH 检查SSH连接
func (l *LXDHealthChecker) checkSSH(ctx context.Context) error {
	// 加锁保护并发访问
	l.mu.Lock()
	defer l.mu.Unlock()

	// 如果使用外部SSH连接，只测试连接是否可用
	// 重要：使用外部SSH连接时，绝不创建新连接，确保在正确的节点上执行
	if l.useExternalSSH {
		if l.sshClient == nil {
			return fmt.Errorf("external SSH client is nil")
		}
		// 测试现有连接
		session, err := l.sshClient.NewSession()
		if err != nil {
			return fmt.Errorf("external SSH connection test failed: %w", err)
		}
		session.Close()
		if l.logger != nil {
			l.logger.Debug("使用外部SSH连接检查成功（使用Provider的SSH连接，确保在正确节点）",
				zap.String("host", l.config.Host))
		}
		return nil
	}

	// 非外部连接模式：自己管理SSH连接
	// 重要：为了避免并发问题，总是关闭旧连接并创建新连接
	// 这确保每次health check都连接到正确的服务器
	if l.sshClient != nil {
		if l.logger != nil {
			existingRemoteAddr := ""
			if l.sshClient.Conn != nil {
				existingRemoteAddr = l.sshClient.Conn.RemoteAddr().String()
			}
			l.logger.Info("关闭现有SSH连接，准备创建新连接（防止并发连接错误）",
				zap.String("configHost", l.config.Host),
				zap.Int("configPort", l.config.Port),
				zap.String("existingRemoteAddr", existingRemoteAddr))
		}
		// 总是关闭现有连接
		l.sshClient.Close()
		l.sshClient = nil
	}

	// 构建认证方法：支持密钥和密码，SSH客户端会按顺序尝试
	var authMethods []ssh.AuthMethod

	// 如果提供了SSH私钥，添加密钥认证
	if l.config.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(l.config.PrivateKey))
		if err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
			if l.logger != nil {
				l.logger.Debug("已添加SSH密钥认证方法", zap.String("host", l.config.Host))
			}
		} else if l.logger != nil {
			l.logger.Warn("SSH私钥解析失败，将尝试使用密码认证",
				zap.String("host", l.config.Host),
				zap.Error(err))
		}
	}

	// 如果提供了密码，添加密码认证（无论是否有密钥，都添加作为备用方案）
	if l.config.Password != "" {
		authMethods = append(authMethods, ssh.Password(l.config.Password))
		if l.logger != nil {
			l.logger.Debug("已添加SSH密码认证方法", zap.String("host", l.config.Host))
		}
	}

	// 如果既没有密钥也没有密码，返回错误
	if len(authMethods) == 0 {
		return fmt.Errorf("no authentication method available: neither SSH key nor password provided")
	}

	// 建立新连接
	config := &ssh.ClientConfig{
		User:            l.config.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         l.config.Timeout,
	}

	address := fmt.Sprintf("%s:%d", l.config.Host, l.config.Port)
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return fmt.Errorf("SSH连接失败: %w", err)
	}

	// 验证SSH连接的远程地址是否匹配预期的主机（支持域名解析）
	if err := utils.VerifySSHConnection(client, l.config.Host); err != nil {
		if l.logger != nil {
			l.logger.Error("LXD SSH连接地址验证失败",
				zap.String("host", l.config.Host),
				zap.Int("port", l.config.Port),
				zap.Error(err))
		}
		client.Close()
		return err
	}

	l.sshClient = client
	if l.logger != nil {
		l.logger.Debug("LXD SSH连接验证成功", zap.String("host", l.config.Host), zap.Int("port", l.config.Port))
	}
	return nil
}

// checkAPI 检查LXD API
func (l *LXDHealthChecker) checkAPI(ctx context.Context) error {
	// LXD API标准端口是8443
	url := fmt.Sprintf("https://%s:8443/1.0/instances", l.config.Host)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("创建API请求失败: %w", err)
	}

	// 配置客户端证书认证
	if (l.config.CertPath != "" && l.config.KeyPath != "") || (l.config.CertContent != "" && l.config.KeyContent != "") {
		var cert tls.Certificate

		// 优先使用证书内容，如果没有再使用文件路径
		if l.config.CertContent != "" && l.config.KeyContent != "" {
			cert, err = tls.X509KeyPair([]byte(l.config.CertContent), []byte(l.config.KeyContent))
			if err != nil {
				return fmt.Errorf("LXD客户端证书内容加载失败: %w", err)
			}
		} else {
			// 创建带证书认证的HTTP客户端
			cert, err = tls.LoadX509KeyPair(l.config.CertPath, l.config.KeyPath)
			if err != nil {
				return fmt.Errorf("LXD客户端证书加载失败 (路径: %s, %s): %w", l.config.CertPath, l.config.KeyPath, err)
			}
		}

		tlsConfig := &tls.Config{
			Certificates:       []tls.Certificate{cert},
			InsecureSkipVerify: true, // LXD通常使用自签名证书
		}

		l.httpClient = &http.Client{
			Timeout: l.config.Timeout,
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		}
	}

	// 认证头（如果有token）
	if l.config.Token != "" {
		// LXD使用客户端证书认证，这里只是一个占位符
		// 实际应用中需要配置客户端证书
		req.Header.Set("Authorization", "Bearer "+l.config.Token)
	}

	resp, err := l.httpClient.Do(req)
	if err != nil {
		// 如果是连接错误，提供更详细的信息
		return fmt.Errorf("LXD API连接失败 (检查LXD是否运行且API端口8443可访问，以及客户端证书是否正确配置): %w", err)
	}
	defer resp.Body.Close()

	// 只有成功获取到实例列表才认为API健康
	// 403/401表示认证失败，说明证书配置有问题
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("LXD API认证失败 (状态码: %d) - 请检查客户端证书配置", resp.StatusCode)
		}
		return fmt.Errorf("LXD API返回错误状态码: %d", resp.StatusCode)
	}

	if l.logger != nil {
		l.logger.Debug("LXD API检查成功", zap.String("url", url), zap.Int("status", resp.StatusCode))
	}
	return nil
}

// checkLXDService 检查LXD服务状态
func (l *LXDHealthChecker) checkLXDService(ctx context.Context) error {
	// 如果使用外部SSH连接，必须确保连接已建立
	if l.useExternalSSH {
		if l.sshClient == nil {
			return fmt.Errorf("external SSH client is required for service check but is nil")
		}
		// 不建立新连接，确保使用Provider的SSH连接
	} else if l.sshClient == nil {
		// 仅在非外部连接模式下才建立新连接
		if err := l.checkSSH(ctx); err != nil {
			return fmt.Errorf("无法建立SSH连接进行服务检查: %w", err)
		}
	}

	// 执行LXD版本检查
	session, err := l.sshClient.NewSession()
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
	envCommand := "source /etc/profile 2>/dev/null || true; source ~/.bashrc 2>/dev/null || true; source ~/.bash_profile 2>/dev/null || true; export PATH=$PATH:/usr/local/bin:/snap/bin:/usr/sbin:/sbin; lxd --version"
	output, err := session.CombinedOutput(envCommand)
	if err != nil {
		return fmt.Errorf("LXD服务不可用: %w", err)
	}

	if strings.TrimSpace(string(output)) == "" {
		return fmt.Errorf("LXD未正确安装")
	}

	// 检查LXD守护进程状态
	session2, err := l.sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("创建SSH会话失败: %w", err)
	}
	defer session2.Close()

	// 请求PTY以模拟交互式登录shell
	err = session2.RequestPty("xterm", 80, 40, ssh.TerminalModes{
		ssh.ECHO:          0,     // 禁用回显
		ssh.TTY_OP_ISPEED: 14400, // 输入速度
		ssh.TTY_OP_OSPEED: 14400, // 输出速度
	})
	if err != nil {
		return fmt.Errorf("请求PTY失败: %w", err)
	}

	// 设置环境变量来确保PATH正确加载
	envCommand2 := "source /etc/profile 2>/dev/null || true; source ~/.bashrc 2>/dev/null || true; source ~/.bash_profile 2>/dev/null || true; export PATH=$PATH:/usr/local/bin:/snap/bin:/usr/sbin:/sbin; lxc list"
	_, err = session2.CombinedOutput(envCommand2)
	if err != nil {
		return fmt.Errorf("LXD守护进程未运行或无法连接: %w", err)
	}

	if l.logger != nil {
		l.logger.Debug("LXD服务检查成功", zap.String("host", l.config.Host))
	}
	return nil
}

// getHostname 获取节点hostname
func (l *LXDHealthChecker) getHostname(ctx context.Context) (string, error) {
	// 加锁保护并发访问
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.sshClient == nil {
		return "", fmt.Errorf("SSH连接未建立")
	}

	session, err := l.sshClient.NewSession()
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

	if l.logger != nil {
		l.logger.Debug("获取到LXD节点hostname",
			zap.String("hostname", hostname),
			zap.String("host", l.config.Host),
			zap.Bool("useExternalSSH", l.useExternalSSH))
	}

	return hostname, nil
}

// Close 关闭连接
func (l *LXDHealthChecker) Close() error {
	// 只有在应该关闭SSH连接时才关闭（即自己创建的连接）
	if l.shouldCloseSSH && l.sshClient != nil {
		err := l.sshClient.Close()
		l.sshClient = nil
		return err
	}
	// 如果使用外部连接，只清空引用，不关闭连接
	if l.useExternalSSH {
		l.sshClient = nil
	}
	return nil
}
