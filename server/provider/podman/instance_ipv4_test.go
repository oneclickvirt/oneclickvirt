package podman

import (
	"context"
	"go.uber.org/zap"
	"oneclickvirt/global"
	"strings"
	"testing"
)

func TestGetContainerPrivateIPReadsPrimaryIPv4Network(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"true\n", "\x1b[32m172.20.0.7\x1b[0m\n"}}
	provider := NewPodmanProvider().(*PodmanProvider)
	provider.sshClient.SetExecutor(executor)

	got, err := provider.getContainerPrivateIP("ct-test")
	if err != nil || got != "172.20.0.7" {
		t.Fatalf("getContainerPrivateIP() = %q, %v; want 172.20.0.7", got, err)
	}
	if len(executor.commands) != 2 || !strings.Contains(executor.commands[1], `"podman-net"`) {
		t.Fatalf("primary IPv4 network was not queried directly: %#v", executor.commands)
	}
}

func TestGetContainerPrivateIPParsesSeparateIPv4FallbackLines(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{
		"false\n",
		"<no value>",
		"172.20.0.7\n",
	}}
	provider := NewPodmanProvider().(*PodmanProvider)
	provider.sshClient.SetExecutor(executor)

	got, err := provider.getContainerPrivateIP("ct-test")
	if err != nil || got != "172.20.0.7" {
		t.Fatalf("getContainerPrivateIP() = %q, %v; want podman-net IPv4", got, err)
	}
	if len(executor.commands) != 3 {
		t.Fatalf("fallback should query the label and network lines, got %#v", executor.commands)
	}
	if !strings.Contains(executor.commands[2], `podman-ipv6`) || !strings.Contains(executor.commands[2], `ne $net`) {
		t.Fatalf("fallback could report the synthetic IPv4 from the IPv6 network: %q", executor.commands[2])
	}
}

func TestGetContainerPrivateIPDoesNotExposeIPv6NetworkIPv4WhenDisabled(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"true\n", ""}}
	provider := NewPodmanProvider().(*PodmanProvider)
	provider.sshClient.SetExecutor(executor)

	got, err := provider.getContainerPrivateIP("ct-ipv6-only")
	if err == nil || got != "" {
		t.Fatalf("getContainerPrivateIP() = %q, %v; want no IPv4", got, err)
	}
	if len(executor.commands) != 2 {
		t.Fatalf("IPv4-disabled instance triggered fallback inspection: %#v", executor.commands)
	}
}

func TestGetInstancePrefersRecordedRoutedIPv6WithPrimaryIPv4(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{
		"dual|running|debian|container-id", "true", "172.20.0.3", "",
		"podman-net\npodman-ipv6", "fd42::3", "2607:9d00:2000:a6::1001",
	}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.connected = true
	p.sshClient.SetExecutor(executor)
	got, err := p.GetInstance(context.Background(), "dual")
	if err != nil || got.PrivateIP != "172.20.0.3" || got.IPv6Address != "2607:9d00:2000:a6::1001" {
		t.Fatalf("GetInstance = %#v, %v", got, err)
	}
}

func TestPodmanListReadsSingleRecordWithoutHeader(t *testing.T) {
	previous := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previous }()
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(&routedPodmanExecutor{outputs: []string{"/single|exited|localhost/debian:latest|abcdef123456\n"}})
	got, err := p.sshListInstances(context.Background())
	if err != nil || len(got) != 1 || got[0].ID != "abcdef123456" || got[0].Name != "single" || got[0].Status != "stopped" || got[0].Image != "localhost/debian:latest" {
		t.Fatalf("list = %#v, %v", got, err)
	}
}
func TestPodmanListRejectsUnparseableNonEmptyOutput(t *testing.T) {
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(&routedPodmanExecutor{outputs: []string{"unexpected old or failed command output\n"}})
	if _, err := p.sshListInstances(context.Background()); err == nil {
		t.Fatal("malformed output was treated as empty inventory")
	}
}
