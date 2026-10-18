package podman

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
	"oneclickvirt/global"
	"oneclickvirt/utils"
)

func TestPodmanIPv6OnlySelectionHasNoIPv4PrimaryNetwork(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"", podmanIPv6NetworkModeManaged, "false", "valid"}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.connected = true
	p.sshClient.SetExecutor(executor)

	selection, err := p.resolvePodmanContainerNetwork("ipv6_only", "2001:db8::20")
	if err != nil {
		t.Fatalf("resolvePodmanContainerNetwork() error = %v", err)
	}
	if selection.Network != ipv6Network || len(selection.AdditionalNetworks) != 0 || !selection.IPv6Only {
		t.Fatalf("IPv6-only selection = %#v, want no primary IPv4 network and one primary IPv6 attachment", selection)
	}
	if !shouldRemovePodmanIPv6IPv4(selection) {
		t.Fatal("IPv6-only selection does not request removal of Netavark's implicit IPv4")
	}
	if command := appendPodmanNetworkOptions("podman run", selection); !strings.Contains(command, "--network='podman-ipv6'") || !strings.Contains(command, "--ip6") || !strings.Contains(command, "--dns=2606:4700:4700::1111") {
		t.Fatalf("initial network command = %q", command)
	}
}

func TestPodmanSecondaryDualStackKeepsPublishedPortDestination(t *testing.T) {
	selection := utils.ContainerNetworkSelection{
		Network: ipv4Network, AdditionalNetworks: []string{ipv6Network},
		IPv6Network: ipv6Network, IPv6: true,
	}
	if shouldRemovePodmanIPv6IPv4(selection) {
		t.Fatal("dual-stack removal would invalidate Netavark's published port destination")
	}
	executor := &routedPodmanExecutor{}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(executor)
	if err := p.connectPodmanAdditionalNetworks("instance-a", selection); err != nil {
		t.Fatal(err)
	}
	if len(executor.commands) != 1 || !strings.Contains(executor.commands[0], "network connect") {
		t.Fatalf("dual-stack attach must preserve Netavark addresses: %#v", executor.commands)
	}
}

func TestPodmanManagedDualStackKeepsItsIPv4(t *testing.T) {
	selection := utils.ContainerNetworkSelection{Network: ipv6Network, IPv6Network: ipv6Network, IPv6: true}
	if shouldRemovePodmanIPv6IPv4(selection) {
		t.Fatal("managed dual-stack primary attachment must keep its IPv4")
	}
}

func TestPodmanIPv4IsolationIsRestoredAfterStart(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"true\npodman-ipv6\n", ""}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(executor)

	if err := p.restoreIPv4IsolationAfterStart("instance-a", context.Background()); err != nil {
		t.Fatalf("restoreIPv4IsolationAfterStart() error = %v", err)
	}
	if len(executor.commands) != 2 || !strings.Contains(executor.commands[1], "route del default") {
		t.Fatalf("restore commands = %#v", executor.commands)
	}
}

func TestPodmanIPv4IsolationRejectsMissingIPv6Attachment(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"true\npodman-net\n"}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(executor)
	if err := p.restoreIPv4IsolationAfterStart("instance-a", context.Background()); err == nil {
		t.Fatal("missing IPv6 attachment did not fail closed")
	}
}

func TestPodmanDiscoverDoesNotPublishSyntheticIPv4(t *testing.T) {
	previousLogger := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previousLogger }()
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(&routedPodmanExecutor{outputs: []string{
		`[{"Id":"ipv6-only","Name":"/only-v6","State":{"Status":"running","Running":true},"Config":{"Labels":{"` + ipv6NoIPv4Label + `":"true"}},"NetworkSettings":{"Networks":{"podman-ipv6":{"IPAddress":"10.89.0.8","GlobalIPv6Address":"2001:db8::8"}}}}]`,
		"",
	}})
	discovered, err := p.sshDiscoverInstances(context.Background())
	if err != nil || len(discovered) != 1 {
		t.Fatalf("sshDiscoverInstances() = %#v, %v", discovered, err)
	}
	if discovered[0].PrivateIP != "" || discovered[0].IPv6Address != "2001:db8::8" {
		t.Fatalf("discovered addresses = IPv4 %q IPv6 %q", discovered[0].PrivateIP, discovered[0].IPv6Address)
	}
}

func TestPodmanDiscoverPrefersIPv4PrimaryOverIPv6Network(t *testing.T) {
	previousLogger := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previousLogger }()
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(&routedPodmanExecutor{outputs: []string{
		`[{"Id":"dual-stack","Name":"/dual","State":{"Status":"running","Running":true},"Config":{"Labels":{"` + ipv6NoIPv4Label + `":"true"}},"NetworkSettings":{"Networks":{"podman-ipv6":{"IPAddress":"10.89.0.8","GlobalIPv6Address":"2001:db8::8"},"podman-net":{"IPAddress":"172.20.0.8"}}}}]`,
		"",
	}})
	discovered, err := p.sshDiscoverInstances(context.Background())
	if err != nil || len(discovered) != 1 {
		t.Fatalf("sshDiscoverInstances() = %#v, %v", discovered, err)
	}
	if discovered[0].PrivateIP != "172.20.0.8" {
		t.Fatalf("discovered IPv4 = %q, want the podman-net address", discovered[0].PrivateIP)
	}
}
