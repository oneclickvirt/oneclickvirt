package agent

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// AgentShellExecutor implements utils.ShellExecutor by routing commands through
// the AgentHub WebSocket connection for agent-mode providers.
// It holds a reference to the hub (not the conn) so it always uses the current
// live connection even after the agent reconnects.
type AgentShellExecutor struct {
	providerID uint
	hub        *AgentHub

	// 并发控制：限制同一 Provider 同时执行的 Agent 命令数量，防止 WebSocket 饱和
	execSem chan struct{} // 信号量，限制并发执行数
}

// 每个 Provider 最大并发 Agent 命令数（WebSocket 通道复用）
const maxConcurrentAgentCommands = 2

// NewAgentShellExecutor creates an AgentShellExecutor for the given provider.
func NewAgentShellExecutor(providerID uint, hub *AgentHub) *AgentShellExecutor {
	return &AgentShellExecutor{
		providerID: providerID,
		hub:        hub,
		execSem:    make(chan struct{}, maxConcurrentAgentCommands),
	}
}

// acquireExecSlot 获取执行槽位，带有超时。防止命令堆积导致 goroutine 泄漏。
func (a *AgentShellExecutor) acquireExecSlot(timeout time.Duration) error {
	select {
	case a.execSem <- struct{}{}:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("等待 Agent 命令槽位超时（%s），Provider %d 当前命令过多", timeout, a.providerID)
	}
}

// releaseExecSlot 释放执行槽位
func (a *AgentShellExecutor) releaseExecSlot() {
	<-a.execSem
}

func (a *AgentShellExecutor) getConn() (*AgentConn, error) {
	// 使用更长的等待时间（60秒），适应 Agent 重连、网络波动等场景。
	// Agent 自身的重连间隔通常为 10-30 秒，60 秒窗口足以覆盖绝大多数重连场景。
	deadline := time.Now().Add(60 * time.Second)
	delay := 500 * time.Millisecond
	maxDelay := 5 * time.Second
	firstWarning := true
	for {
		conn, ok := a.hub.GetConn(a.providerID)
		if ok && conn != nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("agent not connected for provider %d", a.providerID)
		}
		if firstWarning && time.Now().After(deadline.Add(-50*time.Second)) {
			// 等待超过 10 秒后记录警告，便于排查
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("等待 Agent 连接中",
					zap.Uint("providerID", a.providerID),
					zap.Duration("elapsed", 60*time.Second-time.Until(deadline)))
			}
			firstWarning = false
		}
		time.Sleep(delay)
		// 指数退避，最大 5 秒
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

func wrapShellEnv(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return trimmed
	}
	return fmt.Sprintf(". /etc/profile >/dev/null 2>&1 || true; [ -f ~/.bashrc ] && . ~/.bashrc >/dev/null 2>&1 || true; [ -f ~/.bash_profile ] && . ~/.bash_profile >/dev/null 2>&1 || true; export PATH=$PATH:/usr/local/bin:/snap/bin:/usr/sbin:/sbin; %s", command)
}

// Execute runs a command on the remote agent with a default 300s timeout.
func (a *AgentShellExecutor) Execute(command string) (string, error) {
	// 获取并发槽位，最多等待 10 秒
	if err := a.acquireExecSlot(10 * time.Second); err != nil {
		return "", err
	}
	defer a.releaseExecSlot()

	conn, err := a.getConn()
	if err != nil {
		return "", err
	}
	return conn.ExecuteWithTimeout(wrapShellEnv(command), 300*time.Second)
}

// ExecuteWithTimeout runs a command on the remote agent with a custom timeout.
func (a *AgentShellExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	// 获取并发槽位，最多等待 10 秒
	if err := a.acquireExecSlot(10 * time.Second); err != nil {
		return "", err
	}
	defer a.releaseExecSlot()

	conn, err := a.getConn()
	if err != nil {
		return "", err
	}
	return conn.ExecuteWithTimeout(wrapShellEnv(command), timeout)
}

// ExecuteWithLogging runs a command and logs debug information around it.
func (a *AgentShellExecutor) ExecuteWithLogging(command string, logPrefix string) (string, error) {
	if global.APP_LOG != nil {
		global.APP_LOG.Debug("Agent命令执行开始",
			zap.String("log_prefix", logPrefix),
			zap.String("command", command),
			zap.Uint("providerID", a.providerID))
	}
	output, err := a.Execute(command)
	if global.APP_LOG != nil {
		if err != nil {
			global.APP_LOG.Debug("Agent命令执行失败",
				zap.String("log_prefix", logPrefix),
				zap.Error(err))
		} else {
			global.APP_LOG.Debug("Agent命令执行成功",
				zap.String("log_prefix", logPrefix),
				zap.Int("output_len", len(output)))
		}
	}
	return output, err
}

// ExecuteRaw runs a command on the remote agent WITHOUT shell environment wrapping.
// This is preferred for running local scripts or lightweight polling commands.
func (a *AgentShellExecutor) ExecuteRaw(command string, timeout time.Duration) (string, error) {
	// 获取并发槽位，最多等待 10 秒
	if err := a.acquireExecSlot(10 * time.Second); err != nil {
		return "", err
	}
	defer a.releaseExecSlot()

	conn, err := a.getConn()
	if err != nil {
		return "", err
	}
	return conn.ExecuteWithTimeout(command, timeout)
}

// ExecuteViaTempScript uploads a shell script to the agent node and executes it
// with the given arguments. For agent-mode nodes, execution is via nohup to avoid
// WebSocket timeouts, with polling for completion. This is the RECOMMENDED method
// for any command that enters a container or VM (e.g., lxc exec, incus exec,
// docker exec, pct exec, qm guest exec).
func (a *AgentShellExecutor) ExecuteViaTempScript(scriptContent string, args []string, timeout time.Duration) (string, error) {
	ts := time.Now().UnixNano()
	tmpPath := fmt.Sprintf("/tmp/oneclickvirt_exec_%d.sh", ts)
	markerPath := tmpPath + ".marker"
	logPath := tmpPath + ".log"

	// Inject marker/log paths into the script so it knows where to write results.
	// We append the marker setup at the beginning of the script.
	fullScript := fmt.Sprintf("MARKER_FILE=%q\nLOG_FILE=%q\n%s", markerPath, logPath, scriptContent)

	// Upload the script to the agent node
	if err := a.UploadContent(fullScript, tmpPath, 0755); err != nil {
		return "", fmt.Errorf("上传临时脚本失败: %w", err)
	}

	// Build argument string
	argStr := ""
	for _, arg := range args {
		argStr += " " + shellEscapeArg(arg)
	}

	// Execute via nohup (detached from WebSocket) so long-running container/VM entry
	// commands don't block or timeout the WebSocket connection.
	startCmd := fmt.Sprintf("nohup bash %s%s > %s 2>&1 & echo $!", tmpPath, argStr, logPath)
	pidOutput, err := a.ExecuteRaw(startCmd, 15*time.Second)
	if err != nil {
		// Cleanup even on start failure
		a.ExecuteRaw(fmt.Sprintf("rm -f %s %s %s 2>/dev/null", tmpPath, markerPath, logPath), 10*time.Second)
		return "", fmt.Errorf("启动 temp 脚本失败: %w", err)
	}
	pid := strings.TrimSpace(pidOutput)
	if global.APP_LOG != nil {
		global.APP_LOG.Debug("Temp 脚本已启动",
			zap.String("pid", pid),
			zap.String("tmpPath", tmpPath),
			zap.Uint("providerID", a.providerID))
	}

	// Poll for completion marker
	deadline := time.Now().Add(timeout)
	pollInterval := 2 * time.Second
	lastLogSize := 0
	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)

		// Check if the script process is still alive
		aliveOutput, _ := a.ExecuteRaw(fmt.Sprintf("kill -0 %s 2>/dev/null && echo alive || echo dead", pid), 10*time.Second)
		alive := strings.TrimSpace(aliveOutput) == "alive"

		// Read marker file
		markerOutput, markerErr := a.ExecuteRaw(fmt.Sprintf("cat %s 2>/dev/null", markerPath), 10*time.Second)
		if markerErr == nil {
			marker := strings.TrimSpace(markerOutput)
			if marker == "PASSWORD_OK" || marker == "TEMP_SCRIPT_OK" {
				// Success! Read the full log
				logOutput, _ := a.ExecuteRaw(fmt.Sprintf("cat %s 2>/dev/null", logPath), 15*time.Second)
				a.ExecuteRaw(fmt.Sprintf("rm -f %s %s %s 2>/dev/null", tmpPath, markerPath, logPath), 10*time.Second)
				return logOutput, nil
			}
			if marker == "TEMP_SCRIPT_FAILED" || marker == "PASSWORD_FAIL" {
				logOutput, _ := a.ExecuteRaw(fmt.Sprintf("cat %s 2>/dev/null", logPath), 15*time.Second)
				a.ExecuteRaw(fmt.Sprintf("rm -f %s %s %s 2>/dev/null", tmpPath, markerPath, logPath), 10*time.Second)
				return logOutput, fmt.Errorf("temp script reported failure")
			}
		}

		// If process died without writing marker, it crashed
		if !alive {
			logOutput, _ := a.ExecuteRaw(fmt.Sprintf("cat %s 2>/dev/null", logPath), 15*time.Second)
			a.ExecuteRaw(fmt.Sprintf("rm -f %s %s %s 2>/dev/null", tmpPath, markerPath, logPath), 10*time.Second)
			if logOutput != "" {
				return logOutput, fmt.Errorf("temp script exited unexpectedly (PID %s)", pid)
			}
			return "", fmt.Errorf("temp script exited unexpectedly (PID %s) with no output", pid)
		}

		// Log progress for long-running scripts
		if global.APP_LOG != nil && pollInterval >= 10*time.Second {
			logOutput, _ := a.ExecuteRaw(fmt.Sprintf("wc -c < %s 2>/dev/null || echo 0", logPath), 10*time.Second)
			logSize := 0
			fmt.Sscanf(strings.TrimSpace(logOutput), "%d", &logSize)
			if logSize > lastLogSize {
				lastLogSize = logSize
				global.APP_LOG.Debug("Temp 脚本执行中",
					zap.String("pid", pid),
					zap.Int("logSize", logSize),
					zap.Uint("providerID", a.providerID))
			}
		}

		// Adaptive polling: slow down after 30 seconds
		if time.Now().After(deadline.Add(-timeout/2)) && pollInterval < 10*time.Second {
			pollInterval = 10 * time.Second
		}
	}

	// Timeout - kill the script and read partial output
	a.ExecuteRaw(fmt.Sprintf("kill -9 %s 2>/dev/null || true", pid), 10*time.Second)
	logOutput, _ := a.ExecuteRaw(fmt.Sprintf("cat %s 2>/dev/null", logPath), 15*time.Second)
	a.ExecuteRaw(fmt.Sprintf("rm -f %s %s %s 2>/dev/null", tmpPath, markerPath, logPath), 10*time.Second)
	return logOutput, fmt.Errorf("temp script execution timeout after %v (PID %s)", timeout, pid)
}

// shellEscapeArg escapes a shell argument using single quotes.
func shellEscapeArg(s string) string {
	if !strings.ContainsAny(s, " \t\n\r'\"$`\\*?[]{}|&;<>()~#!") {
		return s
	}
	escaped := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + escaped + "'"
}

// UploadContent writes file content to the remote agent host using a base64 round-trip.
func (a *AgentShellExecutor) UploadContent(content, remotePath string, perm os.FileMode) error {
	directory := filepath.Dir(remotePath)
	encodedContent := base64.StdEncoding.EncodeToString([]byte(content))
	command := fmt.Sprintf(
		"mkdir -p %q && base64 -d > %q <<'EOF'\n%s\nEOF\nchmod %o %q",
		directory, remotePath, encodedContent, perm, remotePath,
	)
	_, err := a.ExecuteWithTimeout(command, 300*time.Second)
	return err
}

// IsHealthy returns true when the agent WebSocket connection is currently active.
func (a *AgentShellExecutor) IsHealthy() bool {
	_, ok := a.hub.GetConn(a.providerID)
	return ok
}

// Reconnect is a no-op: agents manage their own reconnect loop automatically.
func (a *AgentShellExecutor) Reconnect() error {
	return nil
}

// Close is a no-op: the AgentHub owns the connection lifecycle.
func (a *AgentShellExecutor) Close() error {
	return nil
}
