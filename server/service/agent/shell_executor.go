package agent

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
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
	conn, ok := a.hub.GetConn(a.providerID)
	if !ok || conn == nil {
		return nil, fmt.Errorf("agent not connected for provider %d", a.providerID)
	}
	return conn, nil
}

// Execute runs a command on the remote agent with a default 300s timeout.
func (a *AgentShellExecutor) Execute(command string) (string, error) {
	conn, err := a.getConn()
	if err != nil {
		return "", err
	}
	return conn.ExecuteWithTimeout(command, 300*time.Second)
}

// ExecuteWithTimeout runs a command on the remote agent with a custom timeout.
func (a *AgentShellExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	conn, err := a.getConn()
	if err != nil {
		return "", err
	}
	return conn.ExecuteWithTimeout(command, timeout)
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
