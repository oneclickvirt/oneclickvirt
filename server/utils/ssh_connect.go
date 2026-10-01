package utils

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"oneclickvirt/global"

	"github.com/pkg/sftp"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// UploadContent 上传内容到远程服务器指定路径
func (c *SSHClient) UploadContent(content, remotePath string, perm os.FileMode) error {
	return c.UploadContentContext(context.Background(), content, remotePath, perm)
}

// UploadContentContext uploads a file through a dedicated SSH session. A
// cancelled transfer closes that session channel, leaving the shared SSH
// transport and unrelated commands available.
func (c *SSHClient) UploadContentContext(ctx context.Context, content, remotePath string, perm os.FileMode) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	endUse, useErr := c.beginUse()
	if useErr != nil {
		return useErr
	}
	defer endUse()
	// 检查连接健康状态，如果不健康则尝试重连
	if !c.IsHealthy() {
		if err := ctx.Err(); err != nil {
			return err
		}
		global.APP_LOG.Warn("SSH连接不健康，尝试重连后上传",
			zap.String("host", c.config.Host))
		if err := c.ReconnectContext(ctx); err != nil {
			return fmt.Errorf("failed to reconnect SSH before upload: %w", err)
		}
	}

	// 创建SFTP客户端
	client := c.GetUnderlyingClient()
	if client == nil {
		return fmt.Errorf("SSH client is closed")
	}
	sftpClient, session, stderrDone, err := newSFTPClientContext(ctx, client)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.IsHealthy() {
			return fmt.Errorf("failed to create SFTP session: %w", err)
		}
		// 尝试重连后重试一次
		global.APP_LOG.Warn("SFTP客户端创建失败，尝试重连后重试",
			zap.String("host", c.config.Host),
			zap.Error(err))
		if reconnErr := c.ReconnectContext(ctx); reconnErr != nil {
			return fmt.Errorf("failed to reconnect SSH: %w (original error: %v)", reconnErr, err)
		}
		client = c.GetUnderlyingClient()
		if client == nil {
			return fmt.Errorf("SSH client is closed")
		}
		sftpClient, session, stderrDone, err = newSFTPClientContext(ctx, client)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("failed to create SFTP client after reconnection: %w", err)
		}
	}
	defer func() {
		_ = session.Close()
		_ = sftpClient.Close()
		<-stderrDone
	}()

	uploadDone := make(chan error, 1)
	go func() {
		uploadDone <- uploadSFTPContent(sftpClient, content, remotePath, perm)
	}()
	select {
	case err := <-uploadDone:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		return nil
	case <-ctx.Done():
		_ = session.Close()
		_ = sftpClient.Close()
		<-uploadDone
		<-stderrDone
		return ctx.Err()
	}
}

func uploadSFTPContent(sftpClient *sftp.Client, content, remotePath string, perm os.FileMode) error {
	remoteDir := remotePath
	if lastSlash := strings.LastIndex(remotePath, "/"); lastSlash != -1 {
		remoteDir = remotePath[:lastSlash]
	}
	if remoteDir != "" && remoteDir != remotePath {
		if err := sftpClient.MkdirAll(remoteDir); err != nil {
			return fmt.Errorf("failed to create remote directory %s: %w", remoteDir, err)
		}
	}
	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file %s: %w", remotePath, err)
	}
	defer remoteFile.Close()
	if n, err := io.WriteString(remoteFile, content); err != nil {
		return fmt.Errorf("failed to write content to remote file: %w", err)
	} else if n != len(content) {
		return fmt.Errorf("failed to write content to remote file: %w", io.ErrShortWrite)
	}
	if err := sftpClient.Chmod(remotePath, perm); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}
	return nil
}

func newSFTPClientContext(ctx context.Context, client *ssh.Client) (*sftp.Client, *ssh.Session, <-chan struct{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		return nil, nil, nil, err
	}
	closeSession := func() { _ = session.Close() }
	stdin, err := session.StdinPipe()
	if err != nil {
		closeSession()
		return nil, nil, nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		closeSession()
		return nil, nil, nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		closeSession()
		return nil, nil, nil, err
	}
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
		close(stderrDone)
	}()

	requestDone := make(chan error, 1)
	go func() { requestDone <- session.RequestSubsystem("sftp") }()
	select {
	case err := <-requestDone:
		if err != nil {
			closeSession()
			<-stderrDone
			return nil, nil, nil, err
		}
	case <-ctx.Done():
		closeSession()
		<-requestDone
		<-stderrDone
		return nil, nil, nil, ctx.Err()
	}

	type sftpResult struct {
		client *sftp.Client
		err    error
	}
	clientDone := make(chan sftpResult, 1)
	go func() {
		sftpClient, err := sftp.NewClientPipe(stdout, stdin)
		clientDone <- sftpResult{client: sftpClient, err: err}
	}()
	select {
	case result := <-clientDone:
		if result.err != nil {
			closeSession()
			<-stderrDone
			return nil, nil, nil, result.err
		}
		return result.client, session, stderrDone, nil
	case <-ctx.Done():
		closeSession()
		result := <-clientDone
		if result.client != nil {
			_ = result.client.Close()
		}
		<-stderrDone
		return nil, nil, nil, ctx.Err()
	}
}

// ResolveHostToIP 解析主机名到IP地址
// 如果host已经是IP地址，直接返回；如果是域名，解析为IP地址
func ResolveHostToIP(host string) ([]string, error) {
	// 尝试解析为IP地址
	if ip := net.ParseIP(host); ip != nil {
		// 已经是IP地址，直接返回
		return []string{host}, nil
	}

	// 是域名，需要解析
	ips, err := net.LookupHost(host)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve hostname %s: %w", host, err)
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("no IP addresses found for hostname %s", host)
	}

	return ips, nil
}

// VerifySSHConnection 验证SSH连接的远程地址是否匹配预期的主机
// 支持域名解析验证：如果expectedHost是域名，会解析后与实际连接的IP比对
func VerifySSHConnection(client *ssh.Client, expectedHost string) error {
	if client == nil || client.Conn == nil {
		return fmt.Errorf("SSH client or connection is nil")
	}

	// 获取实际连接的远程地址
	remoteAddr := client.Conn.RemoteAddr().String()

	// 从 remoteAddr 提取IP（格式: "IP:Port"）
	actualIP, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return fmt.Errorf("failed to parse remote address %s: %w", remoteAddr, err)
	}

	// 解析预期的主机名到IP列表
	expectedIPs, err := ResolveHostToIP(expectedHost)
	if err != nil {
		return fmt.Errorf("failed to resolve expected host %s: %w", expectedHost, err)
	}

	// 检查实际连接的IP是否在预期的IP列表中
	for _, expectedIP := range expectedIPs {
		if actualIP == expectedIP {
			return nil // 匹配成功
		}
	}

	// 如果都不匹配，返回错误
	return fmt.Errorf("SSH connection address mismatch: expected to connect to %s (resolved to %v) but actually connected to %s",
		expectedHost, expectedIPs, actualIP)
}

// CreateSSHConnection 创建SSH连接（全局统一函数，用于WebSocket SSH等场景）
// 返回 SSH client, session 和可能的错误
func CreateSSHConnection(host string, port int, username, password string) (*ssh.Client, *ssh.Session, error) {
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	// 连接SSH服务器
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH连接失败: %w", err)
	}

	// 创建会话
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("创建SSH会话失败: %w", err)
	}

	return client, session, nil
}

// CreateSSHConnectionWithKey 创建SSH连接（使用SSH私钥认证）
func CreateSSHConnectionWithKey(host string, port int, username, privateKey string) (*ssh.Client, *ssh.Session, error) {
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return nil, nil, fmt.Errorf("解析SSH私钥失败: %w", err)
	}

	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH连接失败: %w", err)
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("创建SSH会话失败: %w", err)
	}

	return client, session, nil
}

// CreateSSHConnectionFromAddress 创建SSH连接（全局统一函数，直接使用地址字符串）
// address 格式: "host:port"
func CreateSSHConnectionFromAddress(address, username, password string) (*ssh.Client, *ssh.Session, error) {
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH连接失败: %w", err)
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("创建SSH会话失败: %w", err)
	}

	return client, session, nil
}
