package utils

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"oneclickvirt/global"

	"github.com/pkg/sftp"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

type SSHConfig struct {
	Host           string
	Port           int
	Username       string
	Password       string
	ConnectTimeout time.Duration
	ExecuteTimeout time.Duration
}

type SSHClient struct {
	client *ssh.Client
	config SSHConfig
}

func NewSSHClient(config SSHConfig) (*SSHClient, error) {
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = 30 * time.Second // 增加到30秒，适应Docker容器网络环境
	}
	if config.ExecuteTimeout == 0 {
		config.ExecuteTimeout = 300 * time.Second // 5分钟执行超时，足够处理复杂配置
	}

	global.APP_LOG.Debug("SSH客户端连接配置",
		zap.String("host", config.Host),
		zap.Int("port", config.Port),
		zap.Duration("connectTimeout", config.ConnectTimeout),
		zap.Duration("executeTimeout", config.ExecuteTimeout))

	sshConfig := &ssh.ClientConfig{
		User: config.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(config.Password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         config.ConnectTimeout,
	}

	// 构建连接地址，如果Host已经包含端口则直接使用，否则拼接端口
	var addr string
	if strings.Contains(config.Host, ":") {
		// Host已经包含端口（如 "192.168.1.1:22"），直接使用
		addr = config.Host
	} else {
		// Host不包含端口，拼接端口号
		addr = fmt.Sprintf("%s:%d", config.Host, config.Port)
	}

	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SSH server: %w", err)
	}

	return &SSHClient{
		client: client,
		config: config,
	}, nil
}

func (c *SSHClient) Execute(command string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	// 请求PTY以模拟交互式登录shell，确保加载完整的环境变量
	err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{
		ssh.ECHO:          0,     // 禁用回显
		ssh.TTY_OP_ISPEED: 14400, // 输入速度
		ssh.TTY_OP_OSPEED: 14400, // 输出速度
	})
	if err != nil {
		return "", fmt.Errorf("failed to request PTY: %w", err)
	}

	// 设置环境变量来确保PATH正确加载，避免使用bash -l -c的转义问题
	// 这种方式更安全，不需要处理复杂的命令转义
	envCommand := fmt.Sprintf("source /etc/profile 2>/dev/null || true; source ~/.bashrc 2>/dev/null || true; source ~/.bash_profile 2>/dev/null || true; export PATH=$PATH:/usr/local/bin:/snap/bin:/usr/sbin:/sbin; %s", command)

	// 创建一个通道来处理命令执行的超时
	done := make(chan struct{})
	var output []byte
	var execErr error

	go func() {
		output, execErr = session.CombinedOutput(envCommand)
		close(done)
	}()

	// 等待命令完成或超时
	select {
	case <-done:
		if execErr != nil {
			// 记录执行失败的详细信息，包括原始命令和转换后的命令
			if global.APP_LOG != nil {
				global.APP_LOG.Debug("SSH命令执行失败",
					zap.String("original_command", command),
					zap.String("env_wrapped_command", envCommand),
					zap.Error(execErr),
					zap.String("output", string(output)))
			}
			return string(output), fmt.Errorf("command execution failed: %w", execErr)
		}
		return string(output), nil
	case <-time.After(c.config.ExecuteTimeout):
		session.Signal(ssh.SIGKILL) // 强制终止会话
		return "", fmt.Errorf("command execution timeout after %v", c.config.ExecuteTimeout)
	}
}

func (c *SSHClient) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// TestSSHConnectionLatency 测试SSH连接延迟，执行指定次数测试并返回结果
// 复用 NewSSHClient 和 Execute 方法，确保测试环境与实际生产环境完全一致
func TestSSHConnectionLatency(config SSHConfig, testCount int) (minLatency, maxLatency, avgLatency time.Duration, err error) {
	if testCount <= 0 {
		testCount = 3 // 默认测试3次
	}

	latencies := make([]time.Duration, 0, testCount)
	var totalLatency time.Duration
	successCount := 0
	var lastError error

	global.APP_LOG.Info("开始SSH连接延迟测试",
		zap.String("host", config.Host),
		zap.Int("port", config.Port),
		zap.Int("testCount", testCount))

	for i := 0; i < testCount; i++ {
		startTime := time.Now()

		// 使用真实的 NewSSHClient 创建连接，确保测试环境与生产环境一致
		client, connErr := NewSSHClient(config)
		if connErr != nil {
			global.APP_LOG.Error("SSH连接测试失败",
				zap.Int("attempt", i+1),
				zap.Error(connErr))
			lastError = fmt.Errorf("连接失败(第%d次): %w", i+1, connErr)
			// 不立即返回，继续尝试其他次数
			time.Sleep(1 * time.Second) // 失败后等待1秒再试
			continue
		}

		// 使用真实的 Execute 方法执行命令，测试完整的执行流程（包括PTY、环境变量等）
		_, cmdErr := client.Execute("echo test")

		// 重要：立即关闭客户端，释放连接
		closeErr := client.Close()
		if closeErr != nil {
			global.APP_LOG.Warn("关闭SSH连接时出错",
				zap.Int("attempt", i+1),
				zap.Error(closeErr))
		}

		if cmdErr != nil {
			global.APP_LOG.Error("SSH命令执行失败",
				zap.Int("attempt", i+1),
				zap.Error(cmdErr))
			lastError = fmt.Errorf("命令执行失败(第%d次): %w", i+1, cmdErr)
			// 不立即返回，继续尝试其他次数
			time.Sleep(1 * time.Second) // 失败后等待1秒再试
			continue
		}

		latency := time.Since(startTime)
		latencies = append(latencies, latency)
		totalLatency += latency
		successCount++

		global.APP_LOG.Info("SSH连接测试完成",
			zap.Int("attempt", i+1),
			zap.Duration("latency", latency))

		// 两次测试之间稍作延迟，避免连接过快
		if i < testCount-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	// 检查是否至少有一次成功
	if successCount == 0 {
		if lastError != nil {
			return 0, 0, 0, fmt.Errorf("所有 %d 次连接测试均失败，最后错误: %w", testCount, lastError)
		}
		return 0, 0, 0, fmt.Errorf("所有 %d 次连接测试均失败", testCount)
	}

	// 如果部分成功，记录警告
	if successCount < testCount {
		global.APP_LOG.Warn("部分SSH连接测试失败",
			zap.Int("successCount", successCount),
			zap.Int("totalCount", testCount),
			zap.Int("failedCount", testCount-successCount))
	}

	// 计算统计数据（仅基于成功的测试）
	minLatency = latencies[0]
	maxLatency = latencies[0]
	for _, lat := range latencies {
		if lat < minLatency {
			minLatency = lat
		}
		if lat > maxLatency {
			maxLatency = lat
		}
	}
	avgLatency = totalLatency / time.Duration(successCount)

	global.APP_LOG.Info("SSH连接延迟测试完成",
		zap.Int("successCount", successCount),
		zap.Int("totalCount", testCount),
		zap.Duration("minLatency", minLatency),
		zap.Duration("maxLatency", maxLatency),
		zap.Duration("avgLatency", avgLatency),
		zap.Duration("recommendedTimeout", maxLatency*2))

	return minLatency, maxLatency, avgLatency, nil
}

// ExecuteWithLogging 执行命令并记录详细的调试信息，用于排查复杂命令的执行问题
func (c *SSHClient) ExecuteWithLogging(command string, logPrefix string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	// 请求PTY以模拟交互式登录shell，确保加载完整的环境变量
	err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{
		ssh.ECHO:          0,     // 禁用回显
		ssh.TTY_OP_ISPEED: 14400, // 输入速度
		ssh.TTY_OP_OSPEED: 14400, // 输出速度
	})
	if err != nil {
		return "", fmt.Errorf("failed to request PTY: %w", err)
	}

	// 设置环境变量来确保PATH正确加载
	envCommand := fmt.Sprintf("source /etc/profile 2>/dev/null || true; source ~/.bashrc 2>/dev/null || true; source ~/.bash_profile 2>/dev/null || true; export PATH=$PATH:/usr/local/bin:/snap/bin:/usr/sbin:/sbin; %s", command)

	// 记录执行前的信息
	if global.APP_LOG != nil {
		global.APP_LOG.Debug("SSH命令执行开始",
			zap.String("log_prefix", logPrefix),
			zap.String("original_command", command),
			zap.String("wrapped_command", envCommand))
	}

	// 创建一个通道来处理命令执行的超时
	done := make(chan struct{})
	var output []byte
	var execErr error

	go func() {
		output, execErr = session.CombinedOutput(envCommand)
		close(done)
	}()

	// 等待命令完成或超时
	select {
	case <-done:
		if execErr != nil {
			// 记录执行失败的详细信息
			if global.APP_LOG != nil {
				global.APP_LOG.Error("SSH命令执行失败",
					zap.String("log_prefix", logPrefix),
					zap.String("original_command", command),
					zap.String("wrapped_command", envCommand),
					zap.Error(execErr),
					zap.String("output", string(output)))
			}
			return string(output), fmt.Errorf("command execution failed: %w", execErr)
		}
		if global.APP_LOG != nil {
			global.APP_LOG.Debug("SSH命令执行成功",
				zap.String("log_prefix", logPrefix),
				zap.String("original_command", command),
				zap.Int("output_length", len(output)))
		}
		return string(output), nil
	case <-time.After(c.config.ExecuteTimeout):
		session.Signal(ssh.SIGKILL) // 强制终止会话
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("SSH命令执行超时",
				zap.String("log_prefix", logPrefix),
				zap.String("original_command", command),
				zap.Duration("timeout", c.config.ExecuteTimeout))
		}
		return "", fmt.Errorf("command execution timeout after %v", c.config.ExecuteTimeout)
	}
}

// UploadContent 上传内容到远程服务器指定路径
func (c *SSHClient) UploadContent(content, remotePath string, perm os.FileMode) error {
	// 创建SFTP客户端
	sftpClient, err := sftp.NewClient(c.client)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
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
