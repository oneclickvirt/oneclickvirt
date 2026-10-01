package podman

import (
	"strings"
	"testing"
)

func TestGetContainerPrivateIPReadsPrimaryIPv4Network(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"\x1b[32m172.20.0.7\x1b[0m\n"}}
	provider := NewPodmanProvider().(*PodmanProvider)
	provider.sshClient.SetExecutor(executor)

	got, err := provider.getContainerPrivateIP("ct-test")
	if err != nil || got != "172.20.0.7" {
		t.Fatalf("getContainerPrivateIP() = %q, %v; want 172.20.0.7", got, err)
	}
	if len(executor.commands) != 1 || !strings.Contains(executor.commands[0], `"podman-net"`) {
		t.Fatalf("primary IPv4 network was not queried directly: %#v", executor.commands)
	}
}

func TestGetContainerPrivateIPParsesSeparateIPv4FallbackLines(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{
		"<no value>",
		"10.89.0.2\n172.20.0.7\n",
	}}
	provider := NewPodmanProvider().(*PodmanProvider)
	provider.sshClient.SetExecutor(executor)

	got, err := provider.getContainerPrivateIP("ct-test")
	if err != nil || got != "10.89.0.2" {
		t.Fatalf("getContainerPrivateIP() = %q, %v; want first parsed fallback IPv4", got, err)
	}
	if len(executor.commands) != 2 {
		t.Fatalf("fallback should query separate network lines, got %#v", executor.commands)
	}
}
