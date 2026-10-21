package lxd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func TestRestartRecoveryPreservesUnrelatedFailures(t *testing.T) {
	previous := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previous }()
	for _, output := range []string{"permission denied", "storage unavailable", "connection reset"} {
		t.Run(output, func(t *testing.T) {
			executor := &recordingLXDIPv6Executor{
				outputs: []string{output, "RUNNING"},
				errors:  []error{errors.New("restart rejected")},
			}
			p := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}
			if err := p.sshRestartInstance(context.Background(), "guest"); err == nil {
				t.Fatal("the unchanged running instance masked a failed restart")
			}
			if len(executor.commands) != 1 || !strings.Contains(executor.commands[0], "timeout -k 5 150") {
				t.Fatalf("unexpected recovery or missing command budget: %#v", executor.commands)
			}
		})
	}
}

func TestRestartRecoveryStartsOnlyAConfirmedStoppedInstance(t *testing.T) {
	previous := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previous }()
	for _, state := range []string{"STOPPED", "RUNNING"} {
		t.Run(state, func(t *testing.T) {
			executor := &recordingLXDIPv6Executor{
				outputs: []string{"The instance cannot be started as in Error status", state},
				errors:  []error{errors.New("start half rejected")},
			}
			p := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}
			if err := p.sshRestartInstance(context.Background(), "guest"); err != nil {
				t.Fatal(err)
			}
			want := 2
			if state == "STOPPED" {
				want = 3
				if !strings.Contains(executor.commands[2], "timeout -k 5 40 lxc start 'guest'") {
					t.Fatalf("unbounded or incorrect recovery: %#v", executor.commands)
				}
			}
			if len(executor.commands) != want {
				t.Fatalf("unexpected mutation of %s instance: %#v", state, executor.commands)
			}
		})
	}
}

func TestRestartRecoveryCancelsWithoutStartingUnknownInstance(t *testing.T) {
	executor := &recordingLXDIPv6Executor{
		outputs: []string{"The instance cannot be started as in Error status", "OTHER"},
		errors:  []error{errors.New("start half rejected")},
	}
	p := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := p.sshRestartInstance(ctx, "guest"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("recovery ignored cancellation: %v", err)
	}
	for _, command := range executor.commands {
		if strings.Contains(command, "lxc start ") {
			t.Fatalf("unknown state permitted a start: %s", command)
		}
	}
}
