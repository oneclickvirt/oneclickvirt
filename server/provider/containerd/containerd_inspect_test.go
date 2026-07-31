package containerd

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/utils"
)

type containerdInspectExecutor struct {
	command string
}

func (e *containerdInspectExecutor) Execute(command string) (string, error) {
	e.command = command
	return "/example|exited|docker.io/library/debian:12|0123456789abcdef", nil
}

func (e *containerdInspectExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func (e *containerdInspectExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}

func (e *containerdInspectExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func (e *containerdInspectExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}

func (e *containerdInspectExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e *containerdInspectExecutor) IsHealthy() bool                                 { return true }
func (e *containerdInspectExecutor) Reconnect() error                                { return nil }
func (e *containerdInspectExecutor) Close() error                                    { return nil }

func TestGetInstanceUsesNerdctlIDField(t *testing.T) {
	executor := &containerdInspectExecutor{}
	provider := NewContainerdProvider().(*ContainerdProvider)
	provider.connected = true
	provider.sshClient = utils.NewSafeShellExecutor(executor)

	instance, err := provider.GetInstance(context.Background(), "example")
	if err != nil {
		t.Fatalf("GetInstance() error = %v", err)
	}
	if instance.ID != "0123456789abcdef" || instance.Name != "example" || instance.Status != "stopped" {
		t.Fatalf("instance = %#v", instance)
	}
	if !strings.Contains(executor.command, "{{.ID}}") || strings.Contains(executor.command, "{{.Id}}") {
		t.Fatalf("nerdctl inspect command uses an incompatible ID field: %s", executor.command)
	}
}
