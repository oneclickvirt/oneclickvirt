package utils

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

type SSHConfig struct {
	Host           string
	Port           int
	Username       string
	Password       string
	PrivateKey     string // SSH私钥内容，优先于密码使用
	ConnectTimeout time.Duration
	ExecuteTimeout time.Duration
}

type SSHClient struct {
	client          *ssh.Client
	config          SSHConfig
	lastHealthTime  time.Time          // 上次健康检查时间
	keepaliveCancel context.CancelFunc // keepalive goroutine控制
	keepaliveWg     *sync.WaitGroup    // keepalive goroutine同步（指针避免拷贝）
	mu              sync.RWMutex       // 保护并发访问
	closed          bool               // 标记是否已关闭
}

func NewSSHClient(config SSHConfig) (*SSHClient, error) {
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = 30 * time.Second
	}
	if config.ExecuteTimeout == 0 {
		config.ExecuteTimeout = 300 * time.Second // 执行超时，避免长时间阻塞
	}

	global.APP_LOG.Debug("SSH客户端连接配置",
		zap.String("host", config.Host),
		zap.Int("port", config.Port),
		zap.Duration("connectTimeout", config.ConnectTimeout),
		zap.Duration("executeTimeout", config.ExecuteTimeout))

	client, keepaliveCancel, keepaliveWg, err := dialSSH(config)
	if err != nil {
		return nil, err
	}

	return &SSHClient{
		client:          client,
		config:          config,
		lastHealthTime:  time.Now(),
		keepaliveCancel: keepaliveCancel,
		keepaliveWg:     keepaliveWg,
		closed:          false,
	}, nil
}

// dialSSH 建立SSH连接的内部方法
func dialSSH(config SSHConfig) (*ssh.Client, context.CancelFunc, *sync.WaitGroup, error) {
	// 构建认证方法：支持密钥和密码，SSH客户端会按顺序尝试
	var authMethods []ssh.AuthMethod

	// 如果提供了SSH私钥，添加密钥认证
	if config.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(config.PrivateKey))
		if err != nil {
			global.APP_LOG.Warn("SSH私钥解析失败，将尝试使用密码认证",
				zap.String("host", config.Host),
				zap.Error(err))
		} else {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
			global.APP_LOG.Debug("已添加SSH密钥认证方法",
				zap.String("host", config.Host))
		}
	}

	// 如果提供了密码，添加密码认证（无论是否有密钥，都添加作为备用方案）
	if config.Password != "" {
		authMethods = append(authMethods, ssh.Password(config.Password))
		global.APP_LOG.Debug("已添加SSH密码认证方法",
			zap.String("host", config.Host))
	}

	// 如果既没有密钥也没有密码，返回错误
	if len(authMethods) == 0 {
		return nil, nil, nil, fmt.Errorf("no authentication method available: neither SSH key nor password provided")
	}

	sshConfig := &ssh.ClientConfig{
		User:            config.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         config.ConnectTimeout,
	}

	addr := buildSSHAddress(config.Host, config.Port)

	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to connect to SSH server: %w", err)
	}

	// 启用 KeepAlive，保持连接活跃，使用context控制生命周期
	ctx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("SSH keepalive goroutine panic",
					zap.String("host", config.Host),
					zap.Any("panic", r),
					zap.Stack("stack"))
			}
		}()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		failedCount := 0
		maxFailures := 3 // 连续失败3次后退出

		for {
			select {
			case <-ctx.Done():
				// Context被取消，立即退出
				global.APP_LOG.Debug("SSH keepalive goroutine正常退出",
					zap.String("host", config.Host))
				return
			case <-ticker.C:
				// 双重检查client有效性
				if client == nil {
					global.APP_LOG.Debug("SSH client已关闭，keepalive退出",
						zap.String("host", config.Host))
					return
				}

				// 检查连接状态
				if _, _, err := client.Conn.SendRequest("keepalive@openssh.com", true, nil); err != nil {
					failedCount++
					global.APP_LOG.Debug("SSH keepalive失败",
						zap.String("host", config.Host),
						zap.Int("failedCount", failedCount),
						zap.Error(err))

					if failedCount >= maxFailures {
						global.APP_LOG.Warn("SSH keepalive连续失败，停止发送",
							zap.String("host", config.Host),
							zap.Int("failedCount", failedCount))
						return
					}
					continue
				}

				// 成功，重置失败计数
				failedCount = 0
			}
		}
	}()

	return client, cancel, wg, nil
}

// buildSSHAddress preserves explicitly configured host:port targets while
// correctly bracketing IPv6 literals. The previous colon check treated a bare
// IPv6 address as if it already contained a port.
func buildSSHAddress(host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if configuredHost, configuredPort, err := net.SplitHostPort(host); err == nil && configuredHost != "" && configuredPort != "" {
		return net.JoinHostPort(strings.Trim(configuredHost, "[]"), configuredPort)
	}
	if port <= 0 {
		port = 22
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
}

// IsHealthy 检查SSH连接是否健康
func (c *SSHClient) IsHealthy() bool {
	if c.client == nil {
		return false
	}

	// 如果最近5秒内检查过，认为是健康的（避免频繁检查）
	if time.Since(c.lastHealthTime) < 5*time.Second {
		return true
	}

	// 尝试创建一个session来测试连接
	session, err := c.client.NewSession()
	if err != nil {
		global.APP_LOG.Warn("SSH连接健康检查失败",
			zap.String("host", c.config.Host),
			zap.Error(err))
		return false
	}
	session.Close()

	c.lastHealthTime = time.Now()
	return true
}

// GetUnderlyingClient 获取底层的ssh.Client，供其他组件使用（如health checker）
// 调用者不应该关闭返回的client，它由SSHClient管理
func (c *SSHClient) GetUnderlyingClient() *ssh.Client {
	return c.client
}

// Dial opens a TCP connection to an address reachable from the SSH host.
// It is used for console protocols whose QEMU endpoint intentionally listens
// only on the node loopback interface (for example Incus/LXD SPICE sockets
// exposed by a short-lived websockify process).
func (c *SSHClient) Dial(network, address string) (net.Conn, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("SSH client is not connected")
	}
	if network == "" {
		network = "tcp"
	}
	if !c.IsHealthy() {
		if err := c.Reconnect(); err != nil {
			return nil, fmt.Errorf("failed to reconnect SSH before dial: %w", err)
		}
	}
	conn, err := c.client.Dial(network, address)
	if err != nil {
		return nil, fmt.Errorf("failed to dial remote %s: %w", address, err)
	}
	return conn, nil
}

// Close 关闭SSH连接并等待所有goroutine退出
func (c *SSHClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	// 取消keepalive goroutine
	if c.keepaliveCancel != nil {
		c.keepaliveCancel()
	}

	// 等待keepalive goroutine退出
	done := make(chan struct{})
	go func() {
		defer close(done)
		if c.keepaliveWg != nil {
			c.keepaliveWg.Wait()
		}
	}()

	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()

	select {
	case <-done:
		// goroutine已退出
		global.APP_LOG.Debug("SSH keepalive goroutine已正常退出",
			zap.String("host", c.config.Host))
	case <-timer.C:
		global.APP_LOG.Warn("SSH keepalive goroutine退出超时，强制继续",
			zap.String("host", c.config.Host))
		// 超时也要继续关闭连接，不能阻塞
	}

	// 关闭SSH客户端
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// Reconnect 重新建立SSH连接
func (c *SSHClient) Reconnect() error {
	global.APP_LOG.Debug("尝试重新建立SSH连接",
		zap.String("host", c.config.Host),
		zap.Int("port", c.config.Port))

	// 关闭旧连接和keepalive goroutine
	if c.keepaliveCancel != nil {
		c.keepaliveCancel()
		// 等待旧的keepalive goroutine退出
		done := make(chan struct{})
		go func() {
			if c.keepaliveWg != nil {
				c.keepaliveWg.Wait()
			}
			close(done)
		}()

		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()

		select {
		case <-done:
		case <-timer.C:
			global.APP_LOG.Warn("等待旧keepalive goroutine退出超时", zap.String("host", c.config.Host))
		}
	}
	if c.client != nil {
		c.client.Close()
	}

	// 建立新连接
	client, keepaliveCancel, keepaliveWg, err := dialSSH(c.config)
	if err != nil {
		return fmt.Errorf("failed to reconnect SSH: %w", err)
	}

	c.client = client
	c.keepaliveCancel = keepaliveCancel
	c.keepaliveWg = keepaliveWg
	c.lastHealthTime = time.Now()
	c.closed = false

	global.APP_LOG.Info("SSH连接重建成功",
		zap.String("host", c.config.Host),
		zap.Int("port", c.config.Port))

	return nil
}
