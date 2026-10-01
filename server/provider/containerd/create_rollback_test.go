package containerd

import (
	"strings"
	"testing"
)

func TestCleanupFailedCreateContainerTargetsOnlyRequestedName(t *testing.T) {
	executor := &routedContainerdExecutor{}
	provider := NewContainerdProvider().(*ContainerdProvider)
	provider.sshClient.SetExecutor(executor)

	provider.cleanupFailedCreateContainer("instance-a")
	if len(executor.commands) != 1 {
		t.Fatalf("cleanup commands = %#v, want one command", executor.commands)
	}
	if want := "nerdctl rm -f 'instance-a'"; !strings.Contains(executor.commands[0], want) {
		t.Fatalf("cleanup command = %q, want %q", executor.commands[0], want)
	}

	executor.commands = nil
	provider.cleanupFailedCreateContainer("instance-a; rm -rf /")
	if len(executor.commands) != 0 {
		t.Fatalf("unsafe container name was sent to nerdctl: %#v", executor.commands)
	}
}
