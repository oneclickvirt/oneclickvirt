package utils

import (
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
	// 检查连接健康状态，如果不健康则尝试重连
	if !c.IsHealthy() {
		global.APP_LOG.Warn("SSH连接不健康，尝试重连后上传",
			zap.String("host", c.config.Host))
		if err := c.Reconnect(); err != nil {
			return fmt.Errorf("failed to reconnect SSH before upload: %w", err)
		}
	}

	// 创建SFTP客户端
	sftpClient, err := sftp.NewClient(c.client)
	if err != nil {
		// 尝试重连后重试一次
		global.APP_LOG.Warn("SFTP客户端创建失败，尝试重连后重试",
			zap.String("host", c.config.Host),
			zap.Error(err))
		if reconnErr := c.Reconnect(); reconnErr != nil {
			return fmt.Errorf("failed to reconnect SSH: %w (original error: %v)", reconnErr, err)
		}
		sftpClient, err = sftp.NewClient(c.client)
		if err != nil {
			return fmt.Errorf("failed to create SFTP client after reconnection: %w", err)
		}
	}
	defer sftpClient.Close()

	// 创建远程文件的目录（如果不存在）
	remoteDir := remotePath
	if lastSlash := strings.LastIndex(remotePath, "/"); lastSlash != -1 {
		remoteDir = remotePath[:lastSlash]
	}

	if remoteDir != "" && remoteDir != remotePath {
		err = sftpClient.MkdirAll(remoteDir)
		if err != nil {
			return fmt.Errorf("failed to create remote directory %s: %w", remoteDir, err)
		}
	}

	// 创建远程文件
	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file %s: %w", remotePath, err)
	}
	defer remoteFile.Close()

	// 写入内容
	_, err = io.WriteString(remoteFile, content)
	if err != nil {
		return fmt.Errorf("failed to write content to remote file: %w", err)
	}

	// 设置文件权限
	err = sftpClient.Chmod(remotePath, perm)
	if err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	return nil
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
