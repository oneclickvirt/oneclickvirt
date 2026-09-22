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
	transportFailed <-chan struct{}    // keepalive reports failure without closing the shared transport
	mu              sync.RWMutex       // 保护并发访问
	reconnectMu     sync.Mutex
	observed        *ssh.Client
	transportDone   <-chan struct{}
	closed          bool // 标记是否已关闭
	activeUses      int
	retiring        bool
	retired         []retiredSSHTransport
}

// retiredSSHTransport is kept alive until operations using the previous
// transport release their leases. Reconnect must not close a shared SSH
// connection underneath an unrelated command or WebSSH session.
type retiredSSHTransport struct {
	client *ssh.Client
	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

func (c *SSHClient) beginUse() (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.retiring {
		return nil, fmt.Errorf("SSH client is closed or retiring")
	}
	c.activeUses++
	return c.endUse, nil
}

func (c *SSHClient) endUse() {
	c.mu.Lock()
	if c.activeUses > 0 {
		c.activeUses--
	}
	if c.activeUses != 0 || len(c.retired) == 0 {
		c.mu.Unlock()
		return
	}
	retired := c.retired
	c.retired = nil
	c.mu.Unlock()
	for _, transport := range retired {
		_ = closeSSHTransport(transport)
	}
}

func closeSSHTransport(transport retiredSSHTransport) error {
	if transport.cancel != nil {
		transport.cancel()
	}
	var err error
	if transport.client != nil {
		err = transport.client.Close()
	}
	if transport.wg != nil {
		done := make(chan struct{})
		go func() { transport.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	return err
}

func (c *SSHClient) inUse() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activeUses > 0
}

// Atomically refuse new users only if no operation currently owns this client.
func (c *SSHClient) retireIfIdle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activeUses > 0 {
		return false
	}
	c.retiring = true
	return true
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

	client, keepaliveCancel, keepaliveWg, transportFailed, err := dialSSH(config)
	if err != nil {
		return nil, err
	}

	return &SSHClient{
		client:          client,
		config:          config,
		lastHealthTime:  time.Now(),
		keepaliveCancel: keepaliveCancel,
		keepaliveWg:     keepaliveWg,
		transportFailed: transportFailed,
		closed:          false,
	}, nil
}

// dialSSH 建立SSH连接的内部方法
func dialSSH(config SSHConfig) (*ssh.Client, context.CancelFunc, *sync.WaitGroup, <-chan struct{}, error) {
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
		return nil, nil, nil, nil, fmt.Errorf("no authentication method available: neither SSH key nor password provided")
	}

	sshConfig := &ssh.ClientConfig{
		User:            config.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         config.ConnectTimeout,
	}

	addr := buildSSHAddress(config.Host, config.Port)

	// ssh.Dial's Timeout only bounds TCP dialing, not banner/authentication.
	timeout := config.ConnectTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tcp, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to connect to SSH server: %w", err)
	}
	_ = tcp.SetDeadline(time.Now().Add(timeout))
	conn, channels, requests, err := ssh.NewClientConn(tcp, addr, sshConfig)
	if err != nil {
		tcp.Close()
		return nil, nil, nil, nil, fmt.Errorf("SSH handshake failed: %w", err)
	}
	_ = tcp.SetDeadline(time.Time{})
	client := ssh.NewClient(conn, channels, requests)

	// 启用 KeepAlive，保持连接活跃，使用context控制生命周期
	ctx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	transportFailed := make(chan struct{})
	var transportFailedOnce sync.Once
	markTransportFailed := func() {
		transportFailedOnce.Do(func() { close(transportFailed) })
	}
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
				result := make(chan error, 1)
				go func() { _, _, err := client.Conn.SendRequest("keepalive@openssh.com", true, nil); result <- err }()
				var probeErr error
				timer := time.NewTimer(10 * time.Second)
				select {
				case probeErr = <-result:
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
					// A keepalive request is not an exclusive operation. Do not close
					// the provider-wide transport while commands or WebSSH sessions
					// may still be using it; report the failure to the owner instead.
					failedCount++
					if failedCount >= maxFailures {
						markTransportFailed()
						return
					}
					continue
				}
				timer.Stop()
				if err := probeErr; err != nil {
					failedCount++
					global.APP_LOG.Debug("SSH keepalive失败",
						zap.String("host", config.Host),
						zap.Int("failedCount", failedCount),
						zap.Error(err))

					if failedCount >= maxFailures {
						global.APP_LOG.Warn("SSH keepalive连续失败，停止发送",
							zap.String("host", config.Host),
							zap.Int("failedCount", failedCount))
						markTransportFailed()
						return
					}
					continue
				}

				// 成功，重置失败计数
				failedCount = 0
			}
		}
	}()

	return client, cancel, wg, transportFailed, nil
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

// IsHealthy observes the transport lifecycle, never opens a session. MaxSessions
// rejection or disabled SFTP is a request-level error, not a dead connection.
func (c *SSHClient) IsHealthy() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.retiring || c.client == nil {
		return false
	}
	if c.transportFailed != nil {
		select {
		case <-c.transportFailed:
			return false
		default:
		}
	}
	if c.observed != c.client {
		client := c.client
		done := make(chan struct{})
		c.observed, c.transportDone = client, done
		go func() { _ = client.Wait(); close(done) }()
	}
	select {
	case <-c.transportDone:
		return false
	default:
		return true
	}
}

// GetUnderlyingClient returns a snapshot owned by SSHClient; callers must not close it.
func (c *SSHClient) GetUnderlyingClient() *ssh.Client {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil
	}
	return c.client
}

func (c *SSHClient) newSession() (*ssh.Session, error) {
	client := c.GetUnderlyingClient()
	if client == nil {
		return nil, fmt.Errorf("SSH client is closed")
	}
	return client.NewSession()
}

func (c *SSHClient) Dial(network, address string) (net.Conn, error) {
	endUse, err := c.beginUse()
	if err != nil {
		return nil, err
	}
	if !c.IsHealthy() {
		if err := c.Reconnect(); err != nil {
			endUse()
			return nil, err
		}
	}
	client := c.GetUnderlyingClient()
	if client == nil {
		endUse()
		return nil, fmt.Errorf("SSH client is closed")
	}
	if network == "" {
		network = "tcp"
	}
	conn, err := client.Dial(network, address)
	if err != nil {
		endUse()
		return nil, err
	}
	return &leasedSSHConn{Conn: conn, release: endUse}, nil
}

type leasedSSHConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *leasedSSHConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// Close is terminal: an in-flight reconnect may not resurrect this wrapper.
func (c *SSHClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	client, cancel, wg := c.client, c.keepaliveCancel, c.keepaliveWg
	retired := c.retired
	c.client, c.keepaliveCancel, c.keepaliveWg, c.transportFailed = nil, nil, nil, nil
	c.retired = nil
	c.mu.Unlock()
	err := closeSSHTransport(retiredSSHTransport{client: client, cancel: cancel, wg: wg})
	for _, transport := range retired {
		_ = closeSSHTransport(transport)
	}
	return err
}

// Reconnect coalesces concurrent recovery and never retires a healthy shared
// transport because one command could not open a channel.
func (c *SSHClient) Reconnect() error {
	if c == nil {
		return fmt.Errorf("SSH client is nil")
	}
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()
	if c.IsHealthy() {
		return nil
	}
	c.mu.RLock()
	closed, old := c.closed, c.client
	config := c.config
	c.mu.RUnlock()
	if closed {
		return fmt.Errorf("SSH client is closed")
	}
	var client *ssh.Client
	var cancel context.CancelFunc
	var wg *sync.WaitGroup
	var transportFailed <-chan struct{}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		client, cancel, wg, transportFailed, err = dialSSH(config)
		if err == nil {
			break
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
		}
	}
	if err != nil {
		return fmt.Errorf("failed to reconnect SSH: %w", err)
	}
	c.mu.Lock()
	if c.closed || c.client != old {
		c.mu.Unlock()
		cancel()
		client.Close()
		return fmt.Errorf("SSH client was closed or replaced during reconnect")
	}
	oldCancel := c.keepaliveCancel
	oldWg := c.keepaliveWg
	c.client, c.keepaliveCancel, c.keepaliveWg, c.transportFailed = client, cancel, wg, transportFailed
	c.observed, c.transportDone = nil, nil
	c.lastHealthTime = time.Now()
	oldTransport := retiredSSHTransport{client: old, cancel: oldCancel, wg: oldWg}
	if oldTransport.client != nil {
		if c.activeUses == 0 {
			c.mu.Unlock()
			_ = closeSSHTransport(oldTransport)
			return nil
		}
		c.retired = append(c.retired, oldTransport)
	}
	c.mu.Unlock()
	return nil
}
