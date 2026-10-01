package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"github.com/google/uuid"
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
// 10 个并发槽位足以覆盖 GPU 检测、流量同步、资源采集、健康检查等场景，
// 同时防止 WebSocket 帧队列过度堆积。
const maxConcurrentAgentCommands = 10
const (
	minConnWaitTimeout = 3 * time.Second
	maxConnWaitTimeout = 60 * time.Second
)

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

func normalizeConnWaitTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 30 * time.Second
	}
	if timeout < minConnWaitTimeout {
		return minConnWaitTimeout
	}
	if timeout > maxConnWaitTimeout {
		return maxConnWaitTimeout
	}
	return timeout
}

func normalizeSemaphoreWaitTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 10 * time.Second
	}
	wait := timeout / 2
	if wait < time.Second {
		wait = time.Second
	}
	if wait > 10*time.Second {
		wait = 10 * time.Second
	}
	return wait
}

func (a *AgentShellExecutor) getConn(waitTimeout time.Duration) (*AgentConn, error) {
	waitTimeout = normalizeConnWaitTimeout(waitTimeout)
	// 使用更长的等待时间（60秒），适应 Agent 重连、网络波动等场景。
	// Agent 自身的重连间隔通常为 10-30 秒，60 秒窗口足以覆盖绝大多数重连场景。
	deadline := time.Now().Add(waitTimeout)
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
		if firstWarning && time.Now().After(deadline.Add(-waitTimeout+10*time.Second)) {
			// 等待超过 10 秒后记录警告，便于排查
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("等待 Agent 连接中",
					zap.Uint("providerID", a.providerID),
					zap.Duration("elapsed", waitTimeout-time.Until(deadline)))
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

func (a *AgentShellExecutor) getConnContext(ctx context.Context, waitTimeout time.Duration) (*AgentConn, error) {
	waitTimeout = normalizeConnWaitTimeout(waitTimeout)
	deadline := time.NewTimer(waitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, ok := a.hub.GetConn(a.providerID)
		if ok && conn != nil {
			return conn, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("agent not connected for provider %d", a.providerID)
		case <-ticker.C:
		}
	}
}

func (a *AgentShellExecutor) ExecuteContext(ctx context.Context, command string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case a.execSem <- struct{}{}:
		defer a.releaseExecSlot()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	conn, err := a.getConnContext(ctx, 300*time.Second)
	if err != nil {
		return "", err
	}
	return conn.ExecuteContext(ctx, wrapShellEnv(command))
}

// executeRawContext runs one raw Agent request with the caller's cancellation
// boundary. It is used by temporary-script polling so cancellation can stop a
// detached script without waiting for the normal request timeout.
func (a *AgentShellExecutor) executeRawContext(ctx context.Context, command string, timeout time.Duration) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	select {
	case a.execSem <- struct{}{}:
		defer a.releaseExecSlot()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := a.getConnContext(requestCtx, timeout)
	if err != nil {
		return "", err
	}
	return conn.ExecuteContext(requestCtx, command)
}

func wrapShellEnv(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return trimmed
	}
	// 使用统一的命令环境包装（不加载用户级配置，避免 Agent 场景下的交互式阻塞）。
	return utils.BuildEnvCommandNoUser(command)
}

// Execute runs a command on the remote agent with a default 300s timeout.
func (a *AgentShellExecutor) Execute(command string) (string, error) {
	// 获取并发槽位，最多等待 10 秒
	if err := a.acquireExecSlot(normalizeSemaphoreWaitTimeout(300 * time.Second)); err != nil {
		return "", err
	}
	defer a.releaseExecSlot()

	conn, err := a.getConn(300 * time.Second)
	if err != nil {
		return "", err
	}
	output, execErr := conn.ExecuteWithTimeout(wrapShellEnv(command), 300*time.Second)
	// ExecuteWithTimeout only times out this request. AgentConn removes the
	// request from its pending map when it returns, so a late response is
	// ignored without disturbing other requests that share this connection.
	// A shared WebSocket is closed only by the hub after an actual transport
	// failure (read/write/ping), never because one command exceeded its budget.
	return output, execErr
}

// ExecuteWithTimeout runs a command on the remote agent with a custom timeout.
func (a *AgentShellExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	// 获取并发槽位，最多等待 10 秒
	if err := a.acquireExecSlot(normalizeSemaphoreWaitTimeout(timeout)); err != nil {
		return "", err
	}
	defer a.releaseExecSlot()

	conn, err := a.getConn(timeout)
	if err != nil {
		return "", err
	}
	output, execErr := conn.ExecuteWithTimeout(wrapShellEnv(command), timeout)
	// A request timeout is scoped to this request. Do not close the provider's
	// shared WebSocket: WebSSH sessions, tunnels, and other Agent commands may
	// be using it concurrently.
	return output, execErr
}

// ExecuteWithLogging runs a command and logs debug information around it.
func (a *AgentShellExecutor) ExecuteWithLogging(command string, logPrefix string) (string, error) {
	if global.APP_LOG != nil {
		global.APP_LOG.Debug("Agent命令执行开始",
			zap.String("log_prefix", logPrefix),
			zap.String("command", utils.RedactSensitiveCommand(command, 200)),
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
	if err := a.acquireExecSlot(normalizeSemaphoreWaitTimeout(timeout)); err != nil {
		return "", err
	}
	defer a.releaseExecSlot()

	conn, err := a.getConn(timeout)
	if err != nil {
		return "", err
	}
	output, execErr := conn.ExecuteWithTimeout(command, timeout)
	// ExecuteRaw follows the same per-request timeout semantics as the wrapped
	// executor. Transport failures are handled by AgentHub's read loop; a
	// command timeout must not tear down the shared connection.
	return output, execErr
}

// ExecuteViaTempScript uploads a shell script to the agent node and executes it
// with the given arguments. For agent-mode nodes, execution is via nohup to avoid
// WebSocket timeouts, with polling for completion. This is the RECOMMENDED method
// for any command that enters a container or VM (e.g., lxc exec, incus exec,
// docker exec, pct exec, qm guest exec).
func (a *AgentShellExecutor) ExecuteViaTempScript(scriptContent string, args []string, timeout time.Duration) (string, error) {
	return a.ExecuteViaTempScriptContext(context.Background(), scriptContent, args, timeout)
}

// ExecuteViaTempScriptContext is the cancellable Agent-mode implementation.
// The script runs in its own process group; cancellation terminates that group
// before the provider lock can be released.
func (a *AgentShellExecutor) ExecuteViaTempScriptContext(ctx context.Context, scriptContent string, args []string, timeout time.Duration) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// UUID paths avoid collisions when concurrent callers happen to observe the
	// same clock tick. A collision would mix script, marker, and log files and
	// could make one caller report another caller's result.
	tmpPath := fmt.Sprintf("/tmp/oneclickvirt_exec_%s.sh", uuid.NewString())
	markerPath := tmpPath + ".marker"
	logPath := tmpPath + ".log"

	// Inject marker/log paths into the script so it knows where to write results.
	// We append the marker setup at the beginning of the script.
	fullScript := fmt.Sprintf("MARKER_FILE=%q\nLOG_FILE=%q\n%s", markerPath, logPath, scriptContent)

	// Upload the script to the agent node
	if err := a.UploadContentContext(runCtx, fullScript, tmpPath, 0755); err != nil {
		return "", fmt.Errorf("上传临时脚本失败: %w", err)
	}

	// Build argument string
	argStr := ""
	for _, arg := range args {
		argStr += " " + shellEscapeArg(arg)
	}

	interpreter := utils.TempScriptInterpreter(scriptContent)
	// Execute via nohup (detached from WebSocket) so long-running container/VM entry
	// commands don't block or timeout the WebSocket connection.
	// Start the script in its own process group when setsid is available. A
	// timeout must terminate the complete operation (including lxc/incus/docker
	// children), not only the wrapper shell. The fallback remains compatible
	// with minimal systems that do not ship setsid; the negative-PID kill below
	// simply becomes a no-op when no dedicated group exists.
	startCmd := fmt.Sprintf("if ! interpreter_path=$(command -v %s 2>/dev/null) || [ ! -x \"$interpreter_path\" ]; then printf 'TEMP_SCRIPT_FAILED\\n' > %s; printf 'required interpreter %s is unavailable\\n' > %s; echo MISSING_INTERPRETER; elif command -v setsid >/dev/null 2>&1; then nohup setsid \"$interpreter_path\" %s%s > %s 2>&1 & echo $!; else nohup \"$interpreter_path\" %s%s > %s 2>&1 & echo $!; fi",
		utils.ShellSingleQuote(interpreter), utils.ShellSingleQuote(markerPath), utils.ShellSingleQuote(interpreter), utils.ShellSingleQuote(logPath),
		utils.ShellSingleQuote(tmpPath), argStr, utils.ShellSingleQuote(logPath),
		utils.ShellSingleQuote(tmpPath), argStr, utils.ShellSingleQuote(logPath))
	pidOutput, err := a.executeRawContext(runCtx, startCmd, 15*time.Second)
	if err != nil {
		// Cleanup even on start failure
		a.ExecuteRaw(fmt.Sprintf("rm -f %s %s %s 2>/dev/null", utils.ShellSingleQuote(tmpPath), utils.ShellSingleQuote(markerPath), utils.ShellSingleQuote(logPath)), 10*time.Second)
		return "", fmt.Errorf("启动 temp 脚本失败: %w", err)
	}
	if strings.TrimSpace(pidOutput) == "MISSING_INTERPRETER" {
		_, _ = a.ExecuteRaw(fmt.Sprintf("rm -f %s %s %s 2>/dev/null", utils.ShellSingleQuote(tmpPath), utils.ShellSingleQuote(markerPath), utils.ShellSingleQuote(logPath)), 10*time.Second)
		return "", fmt.Errorf("启动 temp 脚本失败：远程节点缺少解释器 %s", interpreter)
	}
	pid, err := parseAgentPID(pidOutput)
	if err != nil {
		// Never interpolate untrusted Agent output into a follow-up shell
		// command when the start response is malformed.
		_, _ = a.ExecuteRaw(fmt.Sprintf("rm -f %s %s %s 2>/dev/null", utils.ShellSingleQuote(tmpPath), utils.ShellSingleQuote(markerPath), utils.ShellSingleQuote(logPath)), 10*time.Second)
		return "", fmt.Errorf("启动 temp 脚本失败: %w", err)
	}
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
		// Do not overshoot very short caller deadlines by an unconditional
		// two-second sleep. The remote probe calls below have their own bounded
		// timeouts; this sleep only schedules the next poll.
		sleepFor := pollInterval
		if remaining := time.Until(deadline); remaining < sleepFor {
			sleepFor = remaining
		}
		if sleepFor > 0 {
			timer := time.NewTimer(sleepFor)
			select {
			case <-runCtx.Done():
				timer.Stop()
				a.terminateTempScript(pid)
				a.cleanupTempScript(tmpPath, markerPath, logPath)
				return "", runCtx.Err()
			case <-timer.C:
			}
		}
		if !time.Now().Before(deadline) {
			break
		}

		// Check if the script process is still alive
		aliveOutput, aliveErr := a.executeRawContext(runCtx, tempScriptProcessStateCommand(pid), 10*time.Second)
		processDead := tempScriptProcessIsDead(aliveOutput, aliveErr)

		// Read marker file
		markerOutput, markerErr := a.executeRawContext(runCtx, fmt.Sprintf("cat %s 2>/dev/null", utils.ShellSingleQuote(markerPath)), 10*time.Second)
		if markerErr == nil {
			marker := strings.TrimSpace(markerOutput)
			if marker == "PASSWORD_OK" || marker == "TEMP_SCRIPT_OK" {
				// Success! Read the full log
				logOutput, _ := a.executeRawContext(runCtx, fmt.Sprintf("cat %s 2>/dev/null", utils.ShellSingleQuote(logPath)), 15*time.Second)
				a.cleanupTempScript(tmpPath, markerPath, logPath)
				return logOutput, nil
			}
			if marker == "TEMP_SCRIPT_FAILED" || marker == "PASSWORD_FAIL" {
				logOutput, _ := a.executeRawContext(runCtx, fmt.Sprintf("cat %s 2>/dev/null", utils.ShellSingleQuote(logPath)), 15*time.Second)
				a.cleanupTempScript(tmpPath, markerPath, logPath)
				return logOutput, fmt.Errorf("temp script reported failure")
			}
		}
		if err := runCtx.Err(); err != nil {
			a.terminateTempScript(pid)
			a.cleanupTempScript(tmpPath, markerPath, logPath)
			return "", err
		}

		// If process died without writing marker, it crashed
		if processDead {
			// The wrapper may have exited while a descendant still owns the
			// operation. Best-effort group cleanup keeps a failed script from
			// leaking a container/VM command into a later request.
			a.terminateTempScript(pid)
			logOutput, _ := a.executeRawContext(runCtx, fmt.Sprintf("cat %s 2>/dev/null", utils.ShellSingleQuote(logPath)), 15*time.Second)
			a.cleanupTempScript(tmpPath, markerPath, logPath)
			if logOutput != "" {
				return logOutput, fmt.Errorf("temp script exited unexpectedly (PID %s)", pid)
			}
			return "", fmt.Errorf("temp script exited unexpectedly (PID %s) with no output", pid)
		}

		// Log progress for long-running scripts
		if global.APP_LOG != nil && pollInterval >= 10*time.Second {
			logOutput, _ := a.executeRawContext(runCtx, fmt.Sprintf("wc -c < %s 2>/dev/null || echo 0", utils.ShellSingleQuote(logPath)), 10*time.Second)
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
	a.terminateTempScript(pid)
	logOutput, _ := a.ExecuteRaw(fmt.Sprintf("cat %s 2>/dev/null", utils.ShellSingleQuote(logPath)), 15*time.Second)
	a.cleanupTempScript(tmpPath, markerPath, logPath)
	if err := runCtx.Err(); err != nil {
		return logOutput, err
	}
	return logOutput, fmt.Errorf("temp script execution timeout after %v (PID %s)", timeout, pid)
}

func (a *AgentShellExecutor) cleanupTempScript(tmpPath, markerPath, logPath string) {
	_, _ = a.ExecuteRaw(fmt.Sprintf("rm -f %s %s %s 2>/dev/null", utils.ShellSingleQuote(tmpPath), utils.ShellSingleQuote(markerPath), utils.ShellSingleQuote(logPath)), 10*time.Second)
}

func (a *AgentShellExecutor) terminateTempScript(pid string) {
	// pid is normalized by parseAgentPID before reaching this method. Kill the
	// process group first, then the leader as a fallback for systems without
	// setsid. Never interpolate raw Agent output here.
	command := fmt.Sprintf("kill -TERM -- -%s 2>/dev/null || true; kill -TERM %s 2>/dev/null || true; sleep 1; kill -KILL -- -%s 2>/dev/null || true; kill -KILL %s 2>/dev/null || true", pid, pid, pid, pid)
	_, _ = a.ExecuteRaw(command, 10*time.Second)
}

func tempScriptProcessIsDead(output string, err error) bool {
	return err == nil && strings.TrimSpace(output) == "dead"
}

// tempScriptProcessStateCommand distinguishes a live process from a zombie.
// kill -0 succeeds for zombies, which otherwise makes a completed script look
// alive until the full caller timeout expires. The ps check is optional so the
// command remains usable on minimal provider images.
func tempScriptProcessStateCommand(pid string) string {
	quotedPID := utils.ShellSingleQuote(pid)
	return fmt.Sprintf("if ! kill -0 %s 2>/dev/null; then echo dead; else state=$(ps -o stat= -p %s 2>/dev/null || true); case \"$state\" in *Z*|*X*) echo dead ;; '') echo alive ;; *) echo alive ;; esac; fi", quotedPID, quotedPID)
}

// shellEscapeArg escapes a shell argument using single quotes.
func shellEscapeArg(s string) string {
	if !strings.ContainsAny(s, " \t\n\r'\"$`\\*?[]{}|&;<>()~#!") {
		return s
	}
	escaped := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + escaped + "'"
}

func parseAgentPID(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 0 {
		return "", fmt.Errorf("Agent returned an invalid process ID")
	}
	return strconv.Itoa(pid), nil
}

// UploadContent writes file content to the remote agent host using a base64 round-trip.
func (a *AgentShellExecutor) UploadContent(content, remotePath string, perm os.FileMode) error {
	return a.UploadContentContext(context.Background(), content, remotePath, perm)
}

// UploadContentContext ties the transfer to its owning operation so cancelling
// a task cannot leave a long base64 upload occupying an Agent command slot.
func (a *AgentShellExecutor) UploadContentContext(ctx context.Context, content, remotePath string, perm os.FileMode) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	directory := filepath.Dir(remotePath)
	encodedContent := base64.StdEncoding.EncodeToString([]byte(content))
	command := fmt.Sprintf(
		"mkdir -p %s && base64 -d > %s <<'EOF'\n%s\nEOF\nchmod %o %s",
		utils.ShellSingleQuote(directory), utils.ShellSingleQuote(remotePath), encodedContent, perm, utils.ShellSingleQuote(remotePath),
	)
	_, err := a.ExecuteContext(ctx, command)
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

var _ utils.ShellExecutor = (*AgentShellExecutor)(nil)
var _ utils.ContextTempScriptExecutor = (*AgentShellExecutor)(nil)
