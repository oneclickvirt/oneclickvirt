package podman

import (
	"context"
	"oneclickvirt/provider"
	"strings"
	"testing"
)

func TestCreatePreservesExistingContainer(t *testing.T) {
	e := &routedPodmanExecutor{outputs: []string{"guest\n"}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(e)
	err := p.sshCreateInstanceWithProgress(context.Background(), provider.InstanceConfig{Name: "guest"}, nil)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate create: %v", err)
	}
	if len(e.commands) != 1 || strings.Contains(e.commands[0], "rm ") {
		t.Fatalf("existing guest changed: %v", e.commands)
	}
	e.commands = nil
	err = p.sshCreateInstanceWithProgress(context.Background(), provider.InstanceConfig{Name: "guest", CopyMode: true, CopySourceName: "guest"}, nil)
	if err == nil || len(e.commands) != 0 {
		t.Fatalf("source=target changed source: %v, %v", err, e.commands)
	}
}
func TestCancelledStartDoesNotRestoreNetworkOrStopGuest(t *testing.T) {
	e := &routedPodmanExecutor{}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.sshStartInstance(ctx, "guest"); err == nil {
		t.Fatal("cancelled start reported success")
	}
	if len(e.commands) != 0 {
		t.Fatalf("cancelled task changed guest: %v", e.commands)
	}
}
