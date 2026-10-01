package utils

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"
)

// ShellExecutor abstracts remote command execution.
// Both *SSHClient (direct SSH) and *AgentShellExecutor (WebSocket tunnel) implement this interface,
// allowing provider implementations to be agnostic of the underlying transport.
type ShellExecutor interface {
	Execute(command string) (string, error)
	ExecuteWithTimeout(command string, timeout time.Duration) (string, error)
	ExecuteWithLogging(command string, logPrefix string) (string, error)
	// ExecuteRaw runs a command WITHOUT shell environment wrapping (no profile sourcing, no PATH export).
	// This is preferred for running local scripts or lightweight commands on the remote host.
	ExecuteRaw(command string, timeout time.Duration) (string, error)
	// ExecuteViaTempScript uploads a shell script to the remote host, executes it with the given arguments,
	// and returns the combined stdout+stderr output. For agent-mode nodes, execution is detached (nohup)
	// to avoid WebSocket timeouts; for SSH nodes, it runs synchronously.
	// This is the RECOMMENDED method for any command that enters a container or VM
	// (e.g. lxc exec, incus exec, docker exec, pct exec, qm guest exec).
	ExecuteViaTempScript(scriptContent string, args []string, timeout time.Duration) (string, error)
	UploadContent(content, remotePath string, perm os.FileMode) error
	IsHealthy() bool
	Reconnect() error
	Close() error
}

// ContextShellExecutor is implemented by command transports that can interrupt
// an in-flight remote command when its owning task is cancelled.
type ContextShellExecutor interface {
	ExecuteContext(context.Context, string) (string, error)
}

// ExecuteViaTempScriptContext runs a temporary script while preserving the
// task's cancellation boundary.  Transports that can terminate a detached
// script may implement ContextTempScriptExecutor; legacy transports are
// allowed to finish their in-flight script before this helper returns, which
// keeps provider locks held and prevents a cancelled operation from racing a
// replacement task.
type ContextTempScriptExecutor interface {
	ExecuteViaTempScriptContext(context.Context, string, []string, time.Duration) (string, error)
}

func ExecuteViaTempScriptContext(ctx context.Context, executor ShellExecutor, script string, args []string, timeout time.Duration) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if contextual, ok := executor.(ContextTempScriptExecutor); ok {
		return contextual.ExecuteViaTempScriptContext(ctx, script, args, timeout)
	}

	type result struct {
		output string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		output, err := executor.ExecuteViaTempScript(script, args, timeout)
		done <- result{output: output, err: err}
	}()
	select {
	case completed := <-done:
		if err := ctx.Err(); err != nil {
			return completed.output, err
		}
		return completed.output, completed.err
	case <-ctx.Done():
		// Wait for the transport call to finish.  Returning immediately would
		// release the task's instance/provider lock while the remote command was
		// still mutating the guest.
		completed := <-done
		return completed.output, ctx.Err()
	}
}

// ExecuteShellCommandContext preserves serialization for legacy executors that
// cannot interrupt commands, while context-aware transports stop promptly.
func ExecuteShellCommandContext(ctx context.Context, executor ShellExecutor, command string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if contextual, ok := executor.(ContextShellExecutor); ok {
		return contextual.ExecuteContext(ctx, command)
	}

	type result struct {
		output string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		output, err := executor.Execute(command)
		done <- result{output: output, err: err}
	}()
	select {
	case completed := <-done:
		if err := ctx.Err(); err != nil {
			return completed.output, err
		}
		return completed.output, completed.err
	case <-ctx.Done():
		completed := <-done
		return completed.output, ctx.Err()
	}
}

// SleepContext waits for the duration or returns as soon as the operation is
// cancelled.
func SleepContext(ctx context.Context, duration time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TempScriptBuilder helps build self-contained shell scripts with timeout+fallback.
// ──────────────────────────────────────────────────────────────────────────────

// TempScriptConfig configures a temp script for container/VM entry operations.
type TempScriptConfig struct {
	// PrimaryCmd is the main command to execute inside the container/VM (e.g. incus exec).
	// It is embedded as-is in a heredoc - no escaping needed.
	PrimaryCmd string
	// FallbackCmd is executed if PrimaryCmd times out or fails (can be empty).
	FallbackCmd string
	// TimeoutSeconds for the primary command (default 30).
	TimeoutSeconds int
	// SuccessMarker is written to the marker file on success (default "PASSWORD_OK").
	SuccessMarker string
}

// TempScriptInterpreter returns the shell required by a temporary script.
// Generated scripts use POSIX sh so Agent containers based on Alpine do not
// need bash. Explicit bash shebangs remain supported for provider scripts that
// genuinely require it.
func TempScriptInterpreter(script string) string {
	firstLine := strings.TrimSpace(strings.SplitN(script, "\n", 2)[0])
	if !strings.HasPrefix(firstLine, "#!") {
		return "bash"
	}

	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(firstLine, "#!")))
	if len(fields) == 0 {
		return "bash"
	}
	if path.Base(fields[0]) == "sh" {
		return "sh"
	}
	if path.Base(fields[0]) == "env" {
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "-") {
				continue
			}
			if path.Base(field) == "sh" {
				return "sh"
			}
			return "bash"
		}
	}
	return "bash"
}

// BuildTempScript creates a self-contained shell script with timeout+fallback logic.
// The script writes its final status to a marker file (SCRIPT_PATH.marker) and
// full logs to SCRIPT_PATH.log. It is designed to be run via nohup for agent mode,
// or synchronously for SSH mode.
//
// Commands are embedded via heredoc so arbitrary characters (quotes, dollars, etc.)
// are preserved literally without escaping issues.
func BuildTempScript(cfg TempScriptConfig) string {
	timeout := cfg.TimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	marker := cfg.SuccessMarker
	if marker == "" {
		marker = "PASSWORD_OK"
	}

	script := `#!/bin/sh
# Auto-generated by oneclickvirt - do not edit
export LC_ALL=C.UTF-8 LANG=C.UTF-8 LANGUAGE=C.UTF-8 2>/dev/null || true
export PATH=` + shellQuote(StandardExtendedPath) + `${PATH:+:$PATH}

SCRIPT_PATH="$0"
MARKER_FILE="${SCRIPT_PATH}.marker"
LOG_FILE="${SCRIPT_PATH}.log"

exec > "$LOG_FILE" 2>&1

run_with_timeout() {
    duration="$1"
    command_text="$2"
    if command -v timeout >/dev/null 2>&1; then
        timeout "$duration" sh -c "$command_text"
    else
        sh -c "$command_text"
    fi
}

echo "=== $(date) Temp script started ==="
`
	if cfg.PrimaryCmd != "" {
		script += fmt.Sprintf(`
# ── Primary command (with timeout) ──
echo "[primary] running command..."
PRIMARY_CMD=$(cat << 'ENDOFCMD'
%s
ENDOFCMD
)
if run_with_timeout %d "$PRIMARY_CMD"; then
    echo "%s"
    echo "%s" > "$MARKER_FILE"
    echo "=== $(date) SUCCESS via primary ==="
    exit 0
else
    PRIMARY_RC=$?
fi
echo "[primary] failed with exit code $PRIMARY_RC"
`, cfg.PrimaryCmd, timeout, marker, marker)
	}

	if cfg.FallbackCmd != "" {
		script += fmt.Sprintf(`
# ── Fallback command ──
echo "[fallback] running command..."
FALLBACK_CMD=$(cat << 'ENDOFCMD'
%s
ENDOFCMD
)
if sh -c "$FALLBACK_CMD"; then
    echo "%s"
    echo "%s" > "$MARKER_FILE"
    echo "=== $(date) SUCCESS via fallback ==="
    exit 0
else
    FALLBACK_RC=$?
fi
echo "[fallback] failed with exit code $FALLBACK_RC"
`, cfg.FallbackCmd, marker, marker)
	}

	script += `
echo "TEMP_SCRIPT_FAILED"
echo "TEMP_SCRIPT_FAILED" > "$MARKER_FILE"
echo "=== $(date) FAILED - all methods exhausted ==="
exit 1
`
	return script
}
