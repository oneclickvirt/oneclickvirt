package utils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// LocalShellExecutor executes provider commands on the controller host itself.
// It is used by the automatically-created local QEMU/libvirt node and avoids
// requiring SSH credentials for the same machine.
type LocalShellExecutor struct {
	defaultTimeout time.Duration
}

func NewLocalShellExecutor(defaultTimeout time.Duration) *LocalShellExecutor {
	if defaultTimeout <= 0 {
		defaultTimeout = 300 * time.Second
	}
	return &LocalShellExecutor{defaultTimeout: defaultTimeout}
}

func (e *LocalShellExecutor) Execute(command string) (string, error) {
	return e.ExecuteWithTimeout(command, e.defaultTimeout)
}

func (e *LocalShellExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = e.defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	output, err := cmd.CombinedOutput()
	out := string(output)
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("local command timed out after %s", timeout)
	}
	if err != nil {
		return out, err
	}
	return out, nil
}

func (e *LocalShellExecutor) ExecuteWithLogging(command string, logPrefix string) (string, error) {
	return e.Execute(command)
}

func (e *LocalShellExecutor) ExecuteRaw(command string, timeout time.Duration) (string, error) {
	return e.ExecuteWithTimeout(command, timeout)
}

func (e *LocalShellExecutor) ExecuteViaTempScript(scriptContent string, args []string, timeout time.Duration) (string, error) {
	tmp, err := os.CreateTemp("", "oneclickvirt-local-*.sh")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.WriteString(scriptContent); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0700); err != nil {
		return "", err
	}
	quotedArgs := make([]string, 0, len(args)+1)
	quotedArgs = append(quotedArgs, shellQuote(path))
	for _, arg := range args {
		quotedArgs = append(quotedArgs, shellQuote(arg))
	}
	return e.ExecuteWithTimeout(strings.Join(quotedArgs, " "), timeout)
}

func (e *LocalShellExecutor) UploadContent(content, remotePath string, perm os.FileMode) error {
	if err := os.WriteFile(remotePath, []byte(content), perm); err != nil {
		return err
	}
	return os.Chmod(remotePath, perm)
}

func (e *LocalShellExecutor) IsHealthy() bool  { return true }
func (e *LocalShellExecutor) Reconnect() error { return nil }
func (e *LocalShellExecutor) Close() error     { return nil }

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

var _ ShellExecutor = (*LocalShellExecutor)(nil)
