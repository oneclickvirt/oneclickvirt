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

// IncusHealthChecker Incus健康检查器
type IncusHealthChecker struct {
	*BaseHealthChecker
	sshClient      *ssh.Client
	useExternalSSH bool       // 标识是否使用外部SSH连接
	shouldCloseSSH bool       // 标识是否应该关闭SSH连接（仅当自己创建时才关闭）
	mu             sync.Mutex // 保护并发访问sshClient和config字段
}

// NewIncusHealthChecker 创建Incus健康检查器
func NewIncusHealthChecker(config HealthConfig, logger *zap.Logger) *IncusHealthChecker {
	checker := &IncusHealthChecker{
		BaseHealthChecker: NewBaseHealthChecker(config, logger),
		shouldCloseSSH:    true, // 默认情况下，自己创建的连接应该关闭
	}
	if logger != nil {
		logger.Info("创建新的IncusHealthChecker实例",
			zap.String("checkerType", "IncusHealthChecker"),
			zap.String("instancePtr", fmt.Sprintf("%p", checker)),
			zap.String("configHost", config.Host),
			zap.Int("configPort", config.Port),
			zap.Uint("providerID", config.ProviderID),
			zap.String("providerName", config.ProviderName),
			zap.String("baseCheckerPtr", fmt.Sprintf("%p", checker.BaseHealthChecker)))
	}
	return checker
}

// NewIncusHealthCheckerWithSSH 创建使用外部SSH连接的Incus健康检查器
func NewIncusHealthCheckerWithSSH(config HealthConfig, logger *zap.Logger, sshClient *ssh.Client) *IncusHealthChecker {
	return &IncusHealthChecker{
		BaseHealthChecker: NewBaseHealthChecker(config, logger),
		sshClient:         sshClient,
		useExternalSSH:    true,
		shouldCloseSSH:    false, // 使用外部连接，不应该关闭
	}
}

// CheckHealth 执行Incus健康检查
func (i *IncusHealthChecker) CheckHealth(ctx context.Context) (*HealthResult, error) {
	checks := []func(context.Context) CheckResult{}

	// SSH检查
	if i.config.SSHEnabled {
		checks = append(checks, i.createCheckFunc(CheckTypeSSH, i.checkSSH))
	}

	// API检查
	if i.config.APIEnabled {
		checks = append(checks, i.createCheckFunc(CheckTypeAPI, i.checkAPI))
	}

	// Incus服务检查
	if len(i.config.ServiceChecks) > 0 {
		checks = append(checks, i.createCheckFunc(CheckTypeService, i.checkIncusService))
	}

	result := i.executeChecks(ctx, checks)

	// 获取节点hostname（如果SSH连接成功）
	if result.SSHStatus == "online" && i.sshClient != nil {
		if hostname, err := i.getHostname(ctx); err == nil {
			result.HostName = hostname
			if i.logger != nil {
				i.logger.Debug("获取Incus节点hostname成功",
					zap.String("hostname", hostname),
					zap.String("host", i.config.Host))
			}
		} else if i.logger != nil {
			i.logger.Warn("获取Incus节点hostname失败",
				zap.String("host", i.config.Host),
				zap.Error(err))
		}
	}

	return result, nil
}

// checkSSH 检查SSH连接
func (i *IncusHealthChecker) checkSSH(ctx context.Context) error {
	// 加锁保护并发访问
	i.mu.Lock()
	defer i.mu.Unlock()

	// 如果使用外部SSH连接，只测试连接是否可用
	// 重要：使用外部SSH连接时，绝不创建新连接，确保在正确的节点上执行
	if i.useExternalSSH {
		if i.sshClient == nil {
			return fmt.Errorf("external SSH client is nil")
		}
		// 测试现有连接
		session, err := i.sshClient.NewSession()
		if err != nil {
			return fmt.Errorf("external SSH connection test failed: %w", err)
		}
		session.Close()
		if i.logger != nil {
			i.logger.Debug("使用外部SSH连接检查成功（使用Provider的SSH连接，确保在正确节点）",
				zap.String("host", i.config.Host))
		}
		return nil
	}

	// 非外部连接模式：自己管理SSH连接
	// 重要：为了避免并发问题，总是关闭旧连接并创建新连接
	// 这确保每次health check都连接到正确的服务器
	if i.sshClient != nil {
		if i.logger != nil {
			existingRemoteAddr := ""
			if i.sshClient.Conn != nil {
				existingRemoteAddr = i.sshClient.Conn.RemoteAddr().String()
			}
			i.logger.Info("关闭现有SSH连接，准备创建新连接（防止并发连接错误）",
				zap.String("configHost", i.config.Host),
				zap.Int("configPort", i.config.Port),
				zap.String("existingRemoteAddr", existingRemoteAddr))
		}
		// 总是关闭现有连接
		i.sshClient.Close()
		i.sshClient = nil
	}

	// 构建认证方法：支持密钥和密码，SSH客户端会按顺序尝试
	var authMethods []ssh.AuthMethod

	// 如果提供了SSH私钥，添加密钥认证
	if i.config.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(i.config.PrivateKey))
		if err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
			if i.logger != nil {
				i.logger.Debug("已添加SSH密钥认证方法", zap.String("host", i.config.Host))
			}
		} else if i.logger != nil {
			i.logger.Warn("SSH私钥解析失败，将尝试使用密码认证",
				zap.String("host", i.config.Host),
				zap.Error(err))
		}
	}

	// 如果提供了密码，添加密码认证（无论是否有密钥，都添加作为备用方案）
	if i.config.Password != "" {
		authMethods = append(authMethods, ssh.Password(i.config.Password))
		if i.logger != nil {
			i.logger.Debug("已添加SSH密码认证方法", zap.String("host", i.config.Host))
		}
	}

	// 如果既没有密钥也没有密码，返回错误
	if len(authMethods) == 0 {
		return fmt.Errorf("no authentication method available: neither SSH key nor password provided")
	}

	// 建立新连接
	config := &ssh.ClientConfig{
		User:            i.config.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         i.config.Timeout,
	}

	address := fmt.Sprintf("%s:%d", i.config.Host, i.config.Port)
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return fmt.Errorf("SSH连接失败: %w", err)
	}

	// 验证SSH连接的远程地址是否匹配预期的主机（支持域名解析）
	if err := utils.VerifySSHConnection(client, i.config.Host); err != nil {
		if i.logger != nil {
			i.logger.Error("Incus SSH连接地址验证失败",
				zap.String("host", i.config.Host),
				zap.Int("port", i.config.Port),
				zap.Error(err))
		}
		client.Close()
		return err
	}

	i.sshClient = client
	if i.logger != nil {
		i.logger.Debug("Incus SSH连接验证成功", zap.String("host", i.config.Host), zap.Int("port", i.config.Port))
	}
	return nil
}

// checkAPI 检查Incus API
func (i *IncusHealthChecker) checkAPI(ctx context.Context) error {
	// Incus API标准端口是8443
	url := fmt.Sprintf("https://%s:8443/1.0/instances", i.config.Host)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("创建API请求失败: %w", err)
	}

	// 配置客户端证书认证
	if i.config.CertPath != "" && i.config.KeyPath != "" {
		// 创建带证书认证的HTTP客户端
		cert, err := tls.LoadX509KeyPair(i.config.CertPath, i.config.KeyPath)
		if err != nil {
			return fmt.Errorf("Incus客户端证书加载失败 (路径: %s, %s): %w", i.config.CertPath, i.config.KeyPath, err)
		}

		tlsConfig := &tls.Config{
			Certificates:       []tls.Certificate{cert},
			InsecureSkipVerify: true, // Incus通常使用自签名证书
		}

		// 清理旧的HTTP Client（如果存在）
		if i.httpClient != nil && i.httpClient.Transport != nil {
			if transport, ok := i.httpClient.Transport.(*http.Transport); ok {
				transport.CloseIdleConnections()
			}
		}

		// 创建新的Transport并注册到清理管理器
		transport := &http.Transport{
			TLSClientConfig: tlsConfig,
		}

		// 注册到清理管理器（防止内存泄漏）
		if GetTransportCleanupManager != nil {
			mgr := GetTransportCleanupManager()
			if i.config.ProviderID > 0 {
				mgr.RegisterTransportWithProvider(transport, i.config.ProviderID)
			} else {
				mgr.RegisterTransport(transport)
			}
		}

		i.httpClient = &http.Client{
			Timeout:   i.config.Timeout,
			Transport: transport,
		}
	}

	// 认证头（如果有token）
	if i.config.Token != "" {
		// Incus使用客户端证书认证，这里只是一个占位符
		req.Header.Set("Authorization", "Bearer "+i.config.Token)
	}

	resp, err := i.httpClient.Do(req)
	if err != nil {
		// 如果是连接错误，提供更详细的信息
		return fmt.Errorf("Incus API连接失败 (检查Incus是否运行且API端口8443可访问，以及客户端证书是否正确配置): %w", err)
	}
	defer resp.Body.Close()

	// 只有成功获取到实例列表才认为API健康
	// 403/401表示认证失败，说明证书配置有问题
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("Incus API认证失败 (状态码: %d) - 请检查客户端证书配置", resp.StatusCode)
		}
		return fmt.Errorf("Incus API返回错误状态码: %d", resp.StatusCode)
	}

	if i.logger != nil {
		i.logger.Debug("Incus API检查成功", zap.String("url", url), zap.Int("status", resp.StatusCode))
	}
	return nil
}

// checkIncusService 检查Incus服务状态
func (i *IncusHealthChecker) checkIncusService(ctx context.Context) error {
	// 如果使用外部SSH连接，必须确保连接已建立
	if i.useExternalSSH {
		if i.sshClient == nil {
			return fmt.Errorf("external SSH client is required for service check but is nil")
		}
		// 不建立新连接，确保使用Provider的SSH连接
	} else if i.sshClient == nil {
		// 仅在非外部连接模式下才建立新连接
		if err := i.checkSSH(ctx); err != nil {
			return fmt.Errorf("无法建立SSH连接进行服务检查: %w", err)
		}
	}

	// 执行Incus版本检查
	session, err := i.sshClient.NewSession()
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
	envCommand := "source /etc/profile 2>/dev/null || true; source ~/.bashrc 2>/dev/null || true; source ~/.bash_profile 2>/dev/null || true; export PATH=$PATH:/usr/local/bin:/snap/bin:/usr/sbin:/sbin; incus --version"
	output, err := session.CombinedOutput(envCommand)
	if err != nil {
		return fmt.Errorf("Incus服务不可用: %w", err)
	}

	if strings.TrimSpace(string(output)) == "" {
		return fmt.Errorf("Incus未正确安装")
	}

	// 检查Incus守护进程状态
	session2, err := i.sshClient.NewSession()
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
	envCommand2 := "source /etc/profile 2>/dev/null || true; source ~/.bashrc 2>/dev/null || true; source ~/.bash_profile 2>/dev/null || true; export PATH=$PATH:/usr/local/bin:/snap/bin:/usr/sbin:/sbin; incus list"
	_, err = session2.CombinedOutput(envCommand2)
	if err != nil {
		return fmt.Errorf("Incus守护进程未运行或无法连接: %w", err)
	}

	if i.logger != nil {
		i.logger.Debug("Incus服务检查成功", zap.String("host", i.config.Host))
	}
	return nil
}

// getHostname 获取节点hostname
func (i *IncusHealthChecker) getHostname(ctx context.Context) (string, error) {
	// 加锁保护并发访问
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.sshClient == nil {
		return "", fmt.Errorf("SSH连接未建立")
	}

	session, err := i.sshClient.NewSession()
	if err != nil {
		return "", fmt.Errorf("创建SSH会话失败: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput("hostname")
	if err != nil {
		return "", fmt.Errorf("执行hostname命令失败: %w", err)
	}

	hostname := utils.CleanCommandOutput(string(output))
	if hostname == "" {
		return "", fmt.Errorf("hostname为空")
	}

	if i.logger != nil {
		i.logger.Debug("获取到Incus节点hostname",
			zap.String("hostname", hostname),
			zap.String("host", i.config.Host),
			zap.Bool("useExternalSSH", i.useExternalSSH))
	}

	return hostname, nil
}

// Close 关闭连接
func (i *IncusHealthChecker) Close() error {
	// 只有在应该关闭SSH连接时才关闭（即自己创建的连接）
	if i.shouldCloseSSH && i.sshClient != nil {
		err := i.sshClient.Close()
		i.sshClient = nil
		return err
	}
	// 如果使用外部连接，只清空引用，不关闭连接
	if i.useExternalSSH {
		i.sshClient = nil
	}
	return nil
}
