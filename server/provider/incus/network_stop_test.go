package incus

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type networkStopExecutor struct {
	commands         []string
	state            string
	gracefulFailures int
}

func (e *networkStopExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	switch command {
	case incusInstanceStatusCommand("guest"):
		return e.state, nil
	case "incus stop 'guest' --timeout=30":
		if e.gracefulFailures > 0 {
			e.gracefulFailures--
			return "graceful shutdown timed out", errors.New("exit code 1")
		}
		e.state = "STOPPED"
		return "", nil
	case "incus stop 'guest' --force --timeout=30":
		e.state = "STOPPED"
		return "", nil
	default:
		return "", nil
	}
}

func (e *networkStopExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *networkStopExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *networkStopExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (*networkStopExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (*networkStopExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (*networkStopExecutor) IsHealthy() bool                                 { return true }
func (*networkStopExecutor) Reconnect() error                                { return nil }
func (*networkStopExecutor) Close() error                                    { return nil }

func TestStopInstanceForConfigAcceptsAlreadyStopped(t *testing.T) {
	executor := &networkStopExecutor{state: "STOPPED"}
	provider := NewIncusProvider().(*IncusProvider)
	provider.sshClient.SetExecutor(executor)
	if err := provider.stopInstanceForConfig(context.Background(), "guest"); err != nil {
		t.Fatal(err)
	}
	for _, command := range executor.commands {
		if strings.HasPrefix(command, "incus stop ") {
			t.Fatalf("already-stopped guest was stopped again: %s", command)
		}
	}
}

func TestStopInstanceForConfigFallsBackToForceStop(t *testing.T) {
	executor := &networkStopExecutor{state: "RUNNING", gracefulFailures: 1}
	provider := NewIncusProvider().(*IncusProvider)
	provider.sshClient.SetExecutor(executor)
	if err := provider.stopInstanceForConfig(context.Background(), "guest"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(executor.commands, "\n")
	if !strings.Contains(joined, "incus stop 'guest' --force --timeout=30") {
		t.Fatalf("force-stop fallback was not attempted: %v", executor.commands)
	}
}

func TestStopInstanceForConfigHonorsCancellation(t *testing.T) {
	executor := &networkStopExecutor{state: "RUNNING"}
	provider := NewIncusProvider().(*IncusProvider)
	provider.sshClient.SetExecutor(executor)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := provider.stopInstanceForConfig(ctx, "guest"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if len(executor.commands) != 0 {
		t.Fatalf("cancelled stop issued commands: %v", executor.commands)
	}
}
