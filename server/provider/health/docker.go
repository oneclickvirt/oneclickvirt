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

// DockerHealthChecker Docker健康检查器
type DockerHealthChecker struct {
	*BaseHealthChecker
	sshClient      *ssh.Client
	useExternalSSH bool       // 标识是否使用外部SSH连接
	shouldCloseSSH bool       // 标识是否应该关闭SSH连接（仅当自己创建时才关闭）
	mu             sync.Mutex // 保护并发访问sshClient和config字段
}

// NewDockerHealthChecker 创建Docker健康检查器
func NewDockerHealthChecker(config HealthConfig, logger *zap.Logger) *DockerHealthChecker {
	checker := &DockerHealthChecker{
		BaseHealthChecker: NewBaseHealthChecker(config, logger),
		shouldCloseSSH:    true, // 默认情况下，自己创建的连接应该关闭
	}
	if logger != nil {
		logger.Info("创建SSH通用健康检查器",
			zap.String("checkerType", "SSH通用检查"),
			zap.String("instancePtr", fmt.Sprintf("%p", checker)),
			zap.String("configHost", config.Host),
			zap.Int("configPort", config.Port),
			zap.Uint("providerID", config.ProviderID),
			zap.String("providerName", config.ProviderName),
			zap.String("baseCheckerPtr", fmt.Sprintf("%p", checker.BaseHealthChecker)))
	}
	return checker
}

// NewDockerHealthCheckerWithSSH 创建使用外部SSH连接的Docker健康检查器
func NewDockerHealthCheckerWithSSH(config HealthConfig, logger *zap.Logger, sshClient *ssh.Client) *DockerHealthChecker {
	return &DockerHealthChecker{
		BaseHealthChecker: NewBaseHealthChecker(config, logger),
		sshClient:         sshClient,
		useExternalSSH:    true,
		shouldCloseSSH:    false, // 使用外部连接，不应该关闭
	}
}

// CheckHealth 执行SSH通用健康检查
func (d *DockerHealthChecker) CheckHealth(ctx context.Context) (*HealthResult, error) {
	if d.logger != nil {
		d.logger.Debug("SSH通用健康检查开始",
			zap.Uint("providerID", d.config.ProviderID),
			zap.String("providerName", d.config.ProviderName),
			zap.String("configHost", d.config.Host),
			zap.Int("configPort", d.config.Port),
			zap.String("instancePtr", fmt.Sprintf("%p", d)))
	}

	checks := []func(context.Context) CheckResult{}

	// SSH检查
	if d.config.SSHEnabled {
		checks = append(checks, d.createCheckFunc(CheckTypeSSH, d.checkSSH))
	}

	// API检查
	if d.config.APIEnabled {
		checks = append(checks, d.createCheckFunc(CheckTypeAPI, d.checkAPI))
	}

	// Docker服务检查
	if len(d.config.ServiceChecks) > 0 {
		checks = append(checks, d.createCheckFunc(CheckTypeService, d.checkDockerService))
	}

	result := d.executeChecks(ctx, checks)

	// 获取节点hostname（如果SSH连接成功）
	if result.SSHStatus == "online" && d.sshClient != nil {
		if hostname, err := d.getHostname(ctx); err == nil {
			result.HostName = hostname
			if d.logger != nil {
				d.logger.Debug("获取节点hostname成功",
					zap.String("hostname", hostname),
					zap.String("host", d.config.Host))
			}
		} else if d.logger != nil {
			d.logger.Warn("获取节点hostname失败",
				zap.String("host", d.config.Host),
				zap.Error(err))
		}
	}

	return result, nil
}

// checkSSH 检查SSH连接
func (d *DockerHealthChecker) checkSSH(ctx context.Context) error {
	// 加锁保护并发访问
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.logger != nil {
		d.logger.Debug("checkSSH开始执行",
			zap.Uint("providerID", d.config.ProviderID),
			zap.String("providerName", d.config.ProviderName),
			zap.String("instancePtr", fmt.Sprintf("%p", d)),
			zap.String("configHost", d.config.Host),
			zap.Int("configPort", d.config.Port))
	}

	// 如果使用外部SSH连接，只测试连接是否可用
	// 重要：使用外部SSH连接时，绝不创建新连接，确保在正确的节点上执行
	if d.useExternalSSH {
		if d.sshClient == nil {
			return fmt.Errorf("external SSH client is nil")
		}
		// 测试现有连接
		session, err := d.sshClient.NewSession()
		if err != nil {
			return fmt.Errorf("external SSH connection test failed: %w", err)
		}
		session.Close()
		if d.logger != nil {
			d.logger.Debug("使用外部SSH连接检查成功（使用Provider的SSH连接，确保在正确节点）",
				zap.String("host", d.config.Host))
		}
		return nil
	}

	// 非外部连接模式：自己管理SSH连接
	// 重要：为了避免并发问题，总是关闭旧连接并创建新连接
	// 这确保每次health check都连接到正确的服务器
	if d.sshClient != nil {
		// 获取现有连接的远程地址用于日志
		existingRemoteAddr := ""
		if d.sshClient.Conn != nil {
			existingRemoteAddr = d.sshClient.Conn.RemoteAddr().String()
		}

		if d.logger != nil {
			d.logger.Info("关闭现有SSH连接，准备创建新连接（防止并发连接错误）",
				zap.String("configHost", d.config.Host),
				zap.Int("configPort", d.config.Port),
				zap.String("existingRemoteAddr", existingRemoteAddr))
		}

		// 总是关闭现有连接
		d.sshClient.Close()
		d.sshClient = nil
	}

	// 构建认证方法：优先使用SSH密钥，否则使用密码
	// 构建认证方法：支持密钥和密码，SSH客户端会按顺序尝试
	var authMethods []ssh.AuthMethod

	// 如果提供了SSH私钥，添加密钥认证
	if d.config.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(d.config.PrivateKey))
		if err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
			if d.logger != nil {
				d.logger.Debug("已添加SSH密钥认证方法", zap.String("host", d.config.Host))
			}
		} else if d.logger != nil {
			d.logger.Warn("SSH私钥解析失败，将尝试使用密码认证",
				zap.String("host", d.config.Host),
				zap.Error(err))
		}
	}

	// 如果提供了密码，添加密码认证（无论是否有密钥，都添加作为备用方案）
	if d.config.Password != "" {
		authMethods = append(authMethods, ssh.Password(d.config.Password))
		if d.logger != nil {
			d.logger.Debug("已添加SSH密码认证方法", zap.String("host", d.config.Host))
		}
	}

	// 如果既没有密钥也没有密码，返回错误
	if len(authMethods) == 0 {
		return fmt.Errorf("no authentication method available: neither SSH key nor password provided")
	}

	// 建立新连接
	config := &ssh.ClientConfig{
		User:            d.config.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         d.config.Timeout,
	}

	address := fmt.Sprintf("%s:%d", d.config.Host, d.config.Port)
	// 保存预期的host用于后续验证（避免并发修改）
	expectedHost := d.config.Host
	expectedPort := d.config.Port
	providerID := d.config.ProviderID
	providerName := d.config.ProviderName

	if d.logger != nil {
		d.logger.Debug("准备建立SSH连接",
			zap.Uint("providerID", providerID),
			zap.String("providerName", providerName),
			zap.String("expectedHost", expectedHost),
			zap.Int("expectedPort", expectedPort),
			zap.String("address", address),
			zap.String("username", d.config.Username))
	}

	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		if d.logger != nil {
			d.logger.Error("SSH Dial失败",
				zap.Uint("providerID", providerID),
				zap.String("providerName", providerName),
				zap.String("address", address),
				zap.Error(err))
		}
		return fmt.Errorf("SSH连接失败: %w", err)
	}

	// 验证SSH连接的远程地址是否匹配预期的主机（支持域名解析）
	if err := utils.VerifySSHConnection(client, expectedHost); err != nil {
		if d.logger != nil {
			d.logger.Error("Docker SSH连接地址验证失败",
				zap.Uint("providerID", providerID),
				zap.String("providerName", providerName),
				zap.String("expectedHost", expectedHost),
				zap.Int("expectedPort", expectedPort),
				zap.Error(err))
		}
		client.Close()
		return err
	}

	d.sshClient = client
	if d.logger != nil {
		d.logger.Debug("SSH连接验证成功",
			zap.Uint("providerID", providerID),
			zap.String("providerName", providerName),
			zap.String("expectedHost", expectedHost))
	}
	return nil
}

// checkAPI 检查Docker API
func (d *DockerHealthChecker) checkAPI(ctx context.Context) error {
	url := fmt.Sprintf("%s://%s:%d/version", d.config.APIScheme, d.config.Host, d.config.APIPort)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("创建API请求失败: %w", err)
	}

	// 如果有证书配置，设置TLS
	if d.config.CertPath != "" && d.config.KeyPath != "" {
		// 不做处理
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API返回错误状态码: %d", resp.StatusCode)
	}

	if d.logger != nil {
		d.logger.Debug("Docker API检查成功", zap.String("url", url), zap.Int("status", resp.StatusCode))
	}
	return nil
}

// checkDockerService 检查Docker服务状态
func (d *DockerHealthChecker) checkDockerService(ctx context.Context) error {
	// 如果使用外部SSH连接，必须确保连接已建立
	if d.useExternalSSH {
		if d.sshClient == nil {
			return fmt.Errorf("external SSH client is required for service check but is nil")
		}
		// 不建立新连接，确保使用Provider的SSH连接
	} else if d.sshClient == nil {
		// 仅在非外部连接模式下才建立新连接
		if err := d.checkSSH(ctx); err != nil {
			return fmt.Errorf("无法建立SSH连接进行服务检查: %w", err)
		}
	}

	// 执行Docker版本检查
	session, err := d.sshClient.NewSession()
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
	envCommand := "source /etc/profile 2>/dev/null || true; source ~/.bashrc 2>/dev/null || true; source ~/.bash_profile 2>/dev/null || true; export PATH=$PATH:/usr/local/bin:/snap/bin:/usr/sbin:/sbin; docker version"
	output, err := session.CombinedOutput(envCommand)
	if err != nil {
		return fmt.Errorf("Docker服务不可用: %w", err)
	}

	if !strings.Contains(string(output), "Server:") {
		return fmt.Errorf("Docker守护进程未运行")
	}

	if d.logger != nil {
		d.logger.Debug("Docker服务检查成功", zap.String("host", d.config.Host))
	}
	return nil
}

// getHostname 获取节点hostname
func (d *DockerHealthChecker) getHostname(ctx context.Context) (string, error) {
	// 加锁保护并发访问
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.sshClient == nil {
		return "", fmt.Errorf("SSH连接未建立")
	}

	session, err := d.sshClient.NewSession()
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

	// 获取实际连接的远程地址
	remoteAddr := ""
	if d.sshClient != nil && d.sshClient.Conn != nil {
		remoteAddr = d.sshClient.Conn.RemoteAddr().String()
	}

	if d.logger != nil {
		d.logger.Debug("获取到节点hostname",
			zap.String("hostname", hostname),
			zap.String("configHost", d.config.Host),
			zap.String("actualRemoteAddr", remoteAddr),
			zap.Bool("useExternalSSH", d.useExternalSSH),
			zap.Uint("providerID", d.config.ProviderID),
			zap.String("providerName", d.config.ProviderName),
			zap.String("instancePtr", fmt.Sprintf("%p", d)))
	}

	return hostname, nil
}

// Close 关闭连接
func (d *DockerHealthChecker) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.logger != nil {
		d.logger.Debug("关闭SSH通用健康检查器",
			zap.String("checkerType", "SSH通用检查"),
			zap.String("instancePtr", fmt.Sprintf("%p", d)),
			zap.Bool("shouldCloseSSH", d.shouldCloseSSH),
			zap.Bool("useExternalSSH", d.useExternalSSH),
			zap.Bool("hasSSHClient", d.sshClient != nil),
			zap.Uint("providerID", d.config.ProviderID),
			zap.String("providerName", d.config.ProviderName))
	}

	// 只有在应该关闭SSH连接时才关闭（即自己创建的连接）
	if d.shouldCloseSSH && d.sshClient != nil {
		err := d.sshClient.Close()
		d.sshClient = nil
		if d.logger != nil {
			if err != nil {
				d.logger.Warn("关闭SSH连接失败", zap.Error(err))
			} else {
				d.logger.Debug("成功关闭SSH连接")
			}
		}
		return err
	}
	// 如果使用外部连接，只清空引用，不关闭连接
	if d.useExternalSSH {
		d.sshClient = nil
		if d.logger != nil {
			d.logger.Debug("清空外部SSH连接引用（不关闭）")
		}
	}
	return nil
}
