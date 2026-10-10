package containerd

import (
	"context"
	"strings"
	"testing"
)

func TestCleanupFailedCreateContainerTargetsOnlyRequestedName(t *testing.T) {
	executor := &routedContainerdExecutor{outputs: []string{"immutable-id\tinstance-a\n", "owner-token"}}
	provider := NewContainerdProvider().(*ContainerdProvider)
	provider.sshClient.SetExecutor(executor)

	if err := provider.cleanupFailedCreateContainer(context.Background(), "instance-a", "owner-token"); err != nil {
		t.Fatal(err)
	}
	if len(executor.commands) != 3 {
		t.Fatalf("cleanup commands = %#v, want inventory, ownership check, and removal", executor.commands)
	}
	if want := "nerdctl rm -f 'immutable-id'"; !strings.Contains(executor.commands[2], want) {
		t.Fatalf("cleanup command = %q, want %q", executor.commands[2], want)
	}

	executor.commands = nil
	if err := provider.cleanupFailedCreateContainer(context.Background(), "instance-a; rm -rf /", "owner-token"); err == nil {
		t.Fatal("unsafe name accepted")
	}
	if len(executor.commands) != 0 {
		t.Fatalf("unsafe container name was sent to nerdctl: %#v", executor.commands)
	}
}
