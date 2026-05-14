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
}

// NewAgentShellExecutor creates an AgentShellExecutor for the given provider.
func NewAgentShellExecutor(providerID uint, hub *AgentHub) *AgentShellExecutor {
	return &AgentShellExecutor{providerID: providerID, hub: hub}
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
	conn, err := a.getConn()
	if err != nil {
		return "", err
	}
	return conn.ExecuteWithTimeout(wrapShellEnv(command), 300*time.Second)
}

// ExecuteWithTimeout runs a command on the remote agent with a custom timeout.
func (a *AgentShellExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
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
