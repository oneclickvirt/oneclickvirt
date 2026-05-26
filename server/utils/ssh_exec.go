package utils

import (
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

func (c *SSHClient) Execute(command string) (string, error) {
	// 检查连接健康状态，如果不健康则尝试重连
	if !c.IsHealthy() {
		global.APP_LOG.Warn("SSH连接不健康，尝试重连",
			zap.String("host", c.config.Host))
		if err := c.Reconnect(); err != nil {
			return "", fmt.Errorf("failed to reconnect SSH before execution: %w", err)
		}
	}

	// 尝试执行命令，如果失败则重试一次（可能是连接刚断开）
	output, err := c.executeCommand(command)
	if err != nil && strings.Contains(err.Error(), "failed to create SSH session") {
		global.APP_LOG.Warn("SSH session创建失败，尝试重连后重试",
			zap.String("host", c.config.Host),
			zap.Error(err))

		// 尝试重连
		if reconnErr := c.Reconnect(); reconnErr != nil {
			return "", fmt.Errorf("failed to reconnect SSH: %w (original error: %v)", reconnErr, err)
		}

		// 重试执行
		output, err = c.executeCommand(command)
		if err != nil {
			return output, fmt.Errorf("command failed after reconnection: %w", err)
		}
	}

	return output, err
}

// ExecuteWithTimeout 执行SSH命令，使用自定义超时时间（用于长时间运行的命令如镜像下载）
func (c *SSHClient) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	if !c.IsHealthy() {
		global.APP_LOG.Warn("SSH连接不健康，尝试重连",
			zap.String("host", c.config.Host))
		if err := c.Reconnect(); err != nil {
			return "", fmt.Errorf("failed to reconnect SSH before execution: %w", err)
		}
	}

	output, err := c.executeCommandWithCustomTimeout(command, timeout)
	if err != nil && strings.Contains(err.Error(), "failed to create SSH session") {
		global.APP_LOG.Warn("SSH session创建失败，尝试重连后重试",
			zap.String("host", c.config.Host),
			zap.Error(err))
		if reconnErr := c.Reconnect(); reconnErr != nil {
			return "", fmt.Errorf("failed to reconnect SSH: %w (original error: %v)", reconnErr, err)
		}
		output, err = c.executeCommandWithCustomTimeout(command, timeout)
		if err != nil {
			return output, fmt.Errorf("command failed after reconnection: %w", err)
		}
	}

	return output, err
}

// executeCommandWithCustomTimeout 使用自定义超时时间执行SSH命令
func (c *SSHClient) executeCommandWithCustomTimeout(command string, timeout time.Duration) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	})
	if err != nil {
		return "", fmt.Errorf("failed to request PTY: %w", err)
	}

	envCommand := fmt.Sprintf("source /etc/profile 2>/dev/null || true; source ~/.bashrc 2>/dev/null || true; source ~/.bash_profile 2>/dev/null || true; export PATH=$PATH:/usr/local/bin:/snap/bin:/usr/sbin:/sbin; %s", command)

	done := make(chan struct{})
	var output []byte
	var execErr error

	go func() {
		output, execErr = session.CombinedOutput(envCommand)
		close(done)
	}()

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	select {
	case <-done:
		if execErr != nil {
			return string(output), fmt.Errorf("command execution failed: %w", execErr)
		}
		return string(output), nil
	case <-timeoutTimer.C:
		session.Signal(ssh.SIGKILL)
		return "", fmt.Errorf("command execution timeout after %v", timeout)
	}
}

// executeCommand 执行SSH命令的内部方法
func (c *SSHClient) executeCommand(command string) (string, error) {
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
	timeoutTimer := time.NewTimer(c.config.ExecuteTimeout)
	defer timeoutTimer.Stop()

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
	case <-timeoutTimer.C:
		session.Signal(ssh.SIGKILL) // 强制终止会话
		return "", fmt.Errorf("command execution timeout after %v", c.config.ExecuteTimeout)
	}
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
			global.APP_LOG.Warn("SSH命令执行失败",
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

		global.APP_LOG.Debug("SSH连接测试完成",
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
	// 检查连接健康状态，如果不健康则尝试重连
	if !c.IsHealthy() {
		global.APP_LOG.Warn("SSH连接不健康，尝试重连",
			zap.String("host", c.config.Host),
			zap.String("log_prefix", logPrefix))
		if err := c.Reconnect(); err != nil {
			return "", fmt.Errorf("failed to reconnect SSH before execution: %w", err)
		}
	}

	// 尝试执行命令，如果失败则重试一次
	output, err := c.executeCommandWithLogging(command, logPrefix)
	if err != nil && strings.Contains(err.Error(), "failed to create SSH session") {
		global.APP_LOG.Warn("SSH session创建失败，尝试重连后重试",
			zap.String("host", c.config.Host),
			zap.String("log_prefix", logPrefix),
			zap.Error(err))

		// 尝试重连
		if reconnErr := c.Reconnect(); reconnErr != nil {
			return "", fmt.Errorf("failed to reconnect SSH: %w (original error: %v)", reconnErr, err)
		}

		// 重试执行
		output, err = c.executeCommandWithLogging(command, logPrefix)
		if err != nil {
			return output, fmt.Errorf("command failed after reconnection: %w", err)
		}
	}

	return output, err
}

// executeCommandWithLogging 执行SSH命令并记录日志的内部方法
func (c *SSHClient) executeCommandWithLogging(command string, logPrefix string) (string, error) {
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
	timeoutTimer := time.NewTimer(c.config.ExecuteTimeout)
	defer timeoutTimer.Stop()

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
	case <-timeoutTimer.C:
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

// ExecuteRaw 执行原始命令，不添加任何环境变量包装。
// 适用于执行本地脚本或不需要 profile 加载的简单命令。
func (c *SSHClient) ExecuteRaw(command string, timeout time.Duration) (string, error) {
	if !c.IsHealthy() {
		global.APP_LOG.Warn("SSH连接不健康，尝试重连",
			zap.String("host", c.config.Host))
		if err := c.Reconnect(); err != nil {
			return "", fmt.Errorf("failed to reconnect SSH before execution: %w", err)
		}
	}

	output, err := c.executeCommandRaw(command, timeout)
	if err != nil && strings.Contains(err.Error(), "failed to create SSH session") {
		global.APP_LOG.Warn("SSH session创建失败，尝试重连后重试",
			zap.String("host", c.config.Host),
			zap.Error(err))
		if reconnErr := c.Reconnect(); reconnErr != nil {
			return "", fmt.Errorf("failed to reconnect SSH: %w (original error: %v)", reconnErr, err)
		}
		output, err = c.executeCommandRaw(command, timeout)
		if err != nil {
			return output, fmt.Errorf("command failed after reconnection: %w", err)
		}
	}

	return output, err
}

// executeCommandRaw 执行原始SSH命令（无环境变量包装）
func (c *SSHClient) executeCommandRaw(command string, timeout time.Duration) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	done := make(chan struct{})
	var output []byte
	var execErr error

	go func() {
		output, execErr = session.CombinedOutput(command)
		close(done)
	}()

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	select {
	case <-done:
		if execErr != nil {
			return string(output), fmt.Errorf("command execution failed: %w", execErr)
		}
		return string(output), nil
	case <-timeoutTimer.C:
		session.Signal(ssh.SIGKILL)
		return "", fmt.Errorf("command execution timeout after %v", timeout)
	}
}

// ExecuteViaTempScript 通过临时脚本执行命令。
// 对于 SSH 模式，直接上传脚本并同步执行（SSH 本身有超时机制）。
func (c *SSHClient) ExecuteViaTempScript(scriptContent string, args []string, timeout time.Duration) (string, error) {
	// 生成唯一的临时文件路径
	tmpPath := fmt.Sprintf("/tmp/oneclickvirt_exec_%d.sh", time.Now().UnixNano())

	// 上传脚本
	if err := c.UploadContent(scriptContent, tmpPath, 0755); err != nil {
		return "", fmt.Errorf("上传临时脚本失败: %w", err)
	}

	// 构建执行命令
	argStr := ""
	for _, arg := range args {
		argStr += " " + shellEscape(arg)
	}
	execCmd := fmt.Sprintf("bash %s%s", tmpPath, argStr)

	// 执行脚本
	output, execErr := c.ExecuteRaw(execCmd, timeout)

	// 清理临时文件（非阻塞）
	c.ExecuteRaw(fmt.Sprintf("rm -f %s %s.marker %s.log 2>/dev/null", tmpPath, tmpPath, tmpPath), 10*time.Second)

	if execErr != nil {
		return output, fmt.Errorf("temp script execution failed: %w", execErr)
	}
	return output, nil
}

// shellEscape 对 shell 参数进行基本转义
func shellEscape(s string) string {
	if !strings.ContainsAny(s, " \t\n\r'\"$`\\*?[]{}|&;<>()~#!") {
		return s
	}
	// 使用单引号包裹，并转义内部的单引号
	escaped := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + escaped + "'"
}

