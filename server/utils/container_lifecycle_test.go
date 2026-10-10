package utils

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type runtimeLifecycleExecutor struct {
	ShellExecutor
	commands  []string
	outputs   []string
	errors    []error
	onCommand func(string)
}

func (e *runtimeLifecycleExecutor) ExecuteContext(ctx context.Context, command string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	i := len(e.commands)
	e.commands = append(e.commands, command)
	if e.onCommand != nil {
		e.onCommand(command)
	}
	var out string
	var err error
	if i < len(e.outputs) {
		out = e.outputs[i]
	}
	if i < len(e.errors) {
		err = e.errors[i]
	}
	return out, err
}

func TestRuntimeStartDoesNotRestartExistingGuest(t *testing.T) {
	for _, cli := range []string{"docker", "podman", "nerdctl"} {
		for _, state := range []string{"\x1b[32mrunning\x1b[0m\r\n", "created", "stopped", "exited", "paused", "restarting"} {
			t.Run(cli+"/"+state, func(t *testing.T) {
				e := &runtimeLifecycleExecutor{outputs: []string{state, "", "running"}}
				if state == "restarting" {
					e.outputs = []string{state, "running"}
				}
				if err := EnsureRuntimeContainerRunning(context.Background(), e, cli, "guest"); err != nil {
					t.Fatal(err)
				}
				joined := strings.Join(e.commands, "\n")
				if strings.Contains(joined, cli+" restart ") {
					t.Fatalf("start restarted a guest: %s", joined)
				}
				if strings.Contains(state, "running") && len(e.commands) != 1 {
					t.Fatalf("running guest changed: %v", e.commands)
				}
				if state == "paused" && !strings.Contains(joined, cli+" unpause 'guest'") {
					t.Fatalf("paused guest not resumed: %s", joined)
				}
			})
		}
	}
}

func TestRuntimeStopMustConfirmState(t *testing.T) {
	for _, state := range []string{"created", "stopped", "exited"} {
		e := &runtimeLifecycleExecutor{outputs: []string{state}}
		if err := StopRuntimeContainer(context.Background(), e, "podman", "guest"); err != nil || len(e.commands) != 1 {
			t.Fatalf("idempotent stop: %v, %v", err, e.commands)
		}
	}
	e := &runtimeLifecycleExecutor{outputs: []string{"running", "", "running"}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := StopRuntimeContainer(ctx, e, "docker", "guest"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("running guest reported stopped: %v", err)
	}
	e = &runtimeLifecycleExecutor{outputs: []string{"running", "already stopped", "stopped"}, errors: []error{nil, errors.New("exit 1")}}
	if err := StopRuntimeContainer(context.Background(), e, "nerdctl", "guest"); err != nil {
		t.Fatalf("verified idempotent stop: %v", err)
	}
}

func TestRuntimeCancellationStopsFollowupMutations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	e := &runtimeLifecycleExecutor{outputs: []string{"exited"}, onCommand: func(string) { cancel() }}
	if err := EnsureRuntimeContainerRunning(ctx, e, "docker", "guest"); !errors.Is(err, context.Canceled) || len(e.commands) != 1 {
		t.Fatalf("cancelled start: %v, %v", err, e.commands)
	}
	e = &runtimeLifecycleExecutor{}
	if err := StopRuntimeContainer(ctx, e, "docker", "guest"); !errors.Is(err, context.Canceled) || len(e.commands) != 0 {
		t.Fatalf("cancelled stop: %v, %v", err, e.commands)
	}
}

func TestRuntimeCreateAndCleanupProtectExistingResources(t *testing.T) {
	for _, cli := range []string{"docker", "podman", "nerdctl"} {
		e := &runtimeLifecycleExecutor{outputs: []string{"other\n\x1b[32mguest\x1b[0m\n"}}
		if err := RequireRuntimeContainerAbsent(context.Background(), e, cli, "guest"); err == nil {
			t.Fatal("existing target accepted")
		}
		if len(e.commands) != 1 || strings.Contains(e.commands[0], "rm ") {
			t.Fatalf("existing target changed: %v", e.commands)
		}
		e = &runtimeLifecycleExecutor{outputs: []string{"immutable-id\tguest\n", "other-attempt"}}
		if err := RemoveOwnedRuntimeContainer(context.Background(), e, cli, "guest", "my-attempt"); err == nil || len(e.commands) != 2 {
			t.Fatalf("unowned cleanup: %v, %v", err, e.commands)
		}
		e = &runtimeLifecycleExecutor{outputs: []string{"immutable-id\tguest\n", "my-attempt"}}
		if err := RemoveOwnedRuntimeContainer(context.Background(), e, cli, "guest", "my-attempt"); err != nil {
			t.Fatal(err)
		}
		if e.commands[2] != cli+" rm -f 'immutable-id'" {
			t.Fatalf("cleanup must use verified ID: %v", e.commands)
		}
	}
}

func TestRuntimeUnreadableStateFailsClosed(t *testing.T) {
	for _, output := range []string{"not running", "running\nexited", "läuft", "en cours", "运行中", ""} {
		e := &runtimeLifecycleExecutor{outputs: []string{output}}
		if err := EnsureRuntimeContainerRunning(context.Background(), e, "docker", "guest"); err == nil || len(e.commands) != 1 {
			t.Fatalf("ambiguous state mutated guest: %q, %v", output, e.commands)
		}
	}
	e := &runtimeLifecycleExecutor{errors: []error{errors.New("daemon unreachable")}}
	if err := RequireRuntimeContainerAbsent(context.Background(), e, "docker", "guest"); err == nil {
		t.Fatal("unreadable inventory treated as absent")
	}
}
