package utils

import "time"

// ShellExecutor abstracts remote command execution.
// Both *SSHClient (direct SSH) and *AgentShellExecutor (WebSocket tunnel) implement this interface,
// allowing provider implementations to be agnostic of the underlying transport.
type ShellExecutor interface {
	Execute(command string) (string, error)
	ExecuteWithTimeout(command string, timeout time.Duration) (string, error)
	ExecuteWithLogging(command string, logPrefix string) (string, error)
	IsHealthy() bool
	Reconnect() error
	Close() error
}
