package proxmox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

func TestProxmoxSSHKnownStartFailsWhenGuestRemainsStopped(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	executor := &ipv6CommandExecutor{output: func(command string) string {
		if command == "pct status 100" {
			return "status: stopped"
		}
		return ""
	}}
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.sshClient.SetExecutor(executor)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := p.sshStartKnownInstance(ctx, "100", "container")
	if err == nil {
		t.Fatal("sshStartKnownInstance() accepted a guest that remained stopped")
	}
	if !strings.Contains(err.Error(), "最后状态: status: stopped") {
		t.Fatalf("sshStartKnownInstance() error = %q, want final stopped status", err)
	}
	commands := strings.Join(executor.commands, "\n")
	if !strings.Contains(commands, "pct start 100") {
		t.Fatalf("sshStartKnownInstance() commands = %q, want direct VMID start", commands)
	}
	if strings.Contains(commands, "pct list") || strings.Contains(commands, "qm list") {
		t.Fatalf("sshStartKnownInstance() rediscovered a newly created guest: %q", commands)
	}
}

func TestProxmoxSSHKnownStartPropagatesStartFailure(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	executor := &ipv6CommandExecutor{
		output: func(command string) string {
			if command == "pct status 100" {
				return "status: stopped"
			}
			return ""
		},
		fail: func(command string) error {
			if command == "pct start 100" {
				return errors.New("start rejected")
			}
			return nil
		},
	}
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.sshClient.SetExecutor(executor)

	err := p.sshStartKnownInstance(context.Background(), "100", "container")
	if err == nil || !strings.Contains(err.Error(), "start rejected") {
		t.Fatalf("sshStartKnownInstance() error = %v, want start failure", err)
	}
}
