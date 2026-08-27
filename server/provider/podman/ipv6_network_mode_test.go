package podman

import (
	"errors"
	"strings"
	"testing"

	"oneclickvirt/utils"
)

func TestParsePodmanIPv6NetworkMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
		valid bool
	}{
		{input: "", want: podmanIPv6NetworkModeManaged, valid: true},
		{input: "  managed\n", want: podmanIPv6NetworkModeManaged, valid: true},
		{input: "UNMANAGED", want: podmanIPv6NetworkModeUnmanaged, valid: true},
		{input: "manual", want: podmanIPv6NetworkModeManual, valid: true},
		{input: "nat", want: podmanIPv6NetworkModeNAT, valid: true},
		{input: "unsupported", valid: false},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, valid := parsePodmanIPv6NetworkMode(test.input)
			if got != test.want || valid != test.valid {
				t.Fatalf("parsePodmanIPv6NetworkMode(%q) = (%q, %v), want (%q, %v)", test.input, got, valid, test.want, test.valid)
			}
		})
	}
}

func TestPodmanNAT66NetworkIsAvailableWithoutNDPResponder(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"", "nat", "fd42:5339:296f:1d00::/64"}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.connected = true
	p.sshClient.SetExecutor(executor)

	mode, available := p.podmanIPv6NetworkAvailability()
	if mode != podmanIPv6NetworkModeNAT || !available {
		t.Fatalf("podmanIPv6NetworkAvailability() = (%q, %v), want NAT66 availability", mode, available)
	}
	if len(executor.commands) != 3 {
		t.Fatalf("NAT66 availability commands = %#v, want network and state checks only", executor.commands)
	}
	for _, command := range executor.commands {
		if strings.Contains(command, "ndpresponder") {
			t.Fatalf("NAT66 availability unexpectedly checked NDP responder: %q", command)
		}
	}
}

func TestPodmanNAT66NetworkRequiresULA64(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"", "nat", "2a14:6781:a::/64"}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.connected = true
	p.sshClient.SetExecutor(executor)

	mode, available := p.podmanIPv6NetworkAvailability()
	if mode != "" || available {
		t.Fatalf("public subnet with NAT66 marker = (%q, %v), want unavailable", mode, available)
	}
	for _, command := range executor.commands {
		if strings.Contains(command, "ndpresponder") {
			t.Fatalf("invalid NAT66 state unexpectedly fell through to NDP: %q", command)
		}
	}
}

func TestResolvePodmanNAT66RejectsPublicStaticIPv6(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"", "nat", "fd42:5339:296f:1d00::/64"}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.connected = true
	p.sshClient.SetExecutor(executor)

	_, err := p.resolvePodmanContainerNetwork("nat_ipv4_ipv6", "2a14:6781:a::20/64")
	if err == nil || !strings.Contains(err.Error(), "ULA NAT66") {
		t.Fatalf("public static IPv6 on Podman NAT66 = %v, want capability error", err)
	}
}

func TestResolvePodmanNAT66AllowsULAStaticIPv6(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"", "nat", "fd42:5339:296f:1d00::/64"}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.connected = true
	p.sshClient.SetExecutor(executor)

	selection, err := p.resolvePodmanContainerNetwork("nat_ipv4_ipv6", "fd42:5339:296f:1d00::20")
	if err != nil {
		t.Fatalf("ULA static IPv6 on Podman NAT66 = %v", err)
	}
	if selection.Network != ipv6Network || selection.StaticIPv6 != "fd42:5339:296f:1d00::20" {
		t.Fatalf("unexpected NAT66 selection: %#v", selection)
	}
}

func TestPodmanIPv6NetworkAvailabilityRequiresIPv4PrimaryForUnmanagedMode(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		errors    []error
		wantMode  string
		available bool
	}{
		{
			name:      "missing mode marker defaults to managed",
			mode:      "",
			wantMode:  podmanIPv6NetworkModeManaged,
			available: true,
		},
		{
			name:      "unmanaged requires ipv4 primary network",
			mode:      podmanIPv6NetworkModeUnmanaged,
			errors:    []error{nil, nil, nil, nil, errors.New("podman-net not found")},
			available: false,
		},
		{
			name:      "manual requires routed helper and ipv4 primary network",
			mode:      podmanIPv6NetworkModeManual,
			wantMode:  podmanIPv6NetworkModeManual,
			available: true,
		},
		{
			name:      "invalid mode is unavailable",
			mode:      "unexpected",
			available: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &routedPodmanExecutor{
				outputs: []string{"", test.mode, "running", "valid", "", ""},
				errors:  test.errors,
			}
			p := NewPodmanProvider().(*PodmanProvider)
			p.connected = true
			p.sshClient.SetExecutor(executor)

			mode, available := p.podmanIPv6NetworkAvailability()
			if mode != test.wantMode || available != test.available {
				t.Fatalf("podmanIPv6NetworkAvailability() = (%q, %v), want (%q, %v)", mode, available, test.wantMode, test.available)
			}
			if test.mode == podmanIPv6NetworkModeUnmanaged && !strings.Contains(strings.Join(executor.commands, "\n"), "network inspect 'podman-net'") {
				t.Fatalf("unmanaged mode did not verify podman-net: %#v", executor.commands)
			}
			if test.mode == podmanIPv6NetworkModeManual {
				commands := strings.Join(executor.commands, "\n")
				if !strings.Contains(commands, "podman-ipv6-attach.sh") || !strings.Contains(commands, "network inspect 'podman-net'") {
					t.Fatalf("manual mode did not verify helper and podman-net: %#v", executor.commands)
				}
			}
		})
	}
}

func TestResolvePodmanContainerNetworkUsesDualAttachmentForUnmanagedMode(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"", podmanIPv6NetworkModeUnmanaged, "running", "valid", ""}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.connected = true
	p.sshClient.SetExecutor(executor)

	selection, err := p.resolvePodmanContainerNetwork("nat_ipv4_ipv6", "2001:db8::20")
	if err != nil {
		t.Fatalf("resolvePodmanContainerNetwork() error = %v", err)
	}
	if selection.Network != ipv4Network || selection.IPv6Network != ipv6Network || selection.StaticIPv6 != "2001:db8::20" || len(selection.AdditionalNetworks) != 1 || selection.AdditionalNetworks[0] != ipv6Network {
		t.Fatalf("unexpected unmanaged selection: %#v", selection)
	}
}

func TestPodmanUnmanagedIPv6UsesIPv4PrimaryThenConnectsStaticIPv6(t *testing.T) {
	selection := utils.ContainerNetworkSelection{
		Network:            ipv4Network,
		AdditionalNetworks: []string{ipv6Network},
		StaticIPv6:         "2001:db8::20",
		IPv6Network:        ipv6Network,
		IPv6:               true,
	}

	command := appendPodmanNetworkOptions("podman run", selection)
	if !strings.Contains(command, "--network='podman-net'") || strings.Contains(command, "--ip6") {
		t.Fatalf("primary command = %q, want IPv4-only primary attachment", command)
	}

	executor := &routedPodmanExecutor{}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(executor)
	if err := p.connectPodmanAdditionalNetworks("instance-a", selection); err != nil {
		t.Fatalf("connectPodmanAdditionalNetworks() error = %v", err)
	}
	if len(executor.commands) != 1 {
		t.Fatalf("commands = %#v, want exactly one network connect", executor.commands)
	}
	for _, fragment := range []string{"podman network connect", "--ip6='2001:db8::20'", "'podman-ipv6'", "'instance-a'"} {
		if !strings.Contains(executor.commands[0], fragment) {
			t.Fatalf("network connect command = %q, missing %q", executor.commands[0], fragment)
		}
	}
}

func TestPodmanManualIPv6UsesHelperAfterULAAttachment(t *testing.T) {
	selection := utils.ContainerNetworkSelection{
		Network:            ipv4Network,
		AdditionalNetworks: []string{ipv6Network},
		StaticIPv6:         "2001:db8::20",
		IPv6Network:        ipv6Network,
		IPv6:               true,
		ManualIPv6:         true,
	}

	command := appendPodmanNetworkOptions("podman run", selection)
	if !strings.Contains(command, "--network='podman-net'") || strings.Contains(command, "--ip6") {
		t.Fatalf("primary command = %q, want IPv4-only primary attachment", command)
	}

	executor := &routedPodmanExecutor{}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(executor)
	if err := p.connectPodmanAdditionalNetworks("instance-a", selection); err != nil {
		t.Fatalf("connectPodmanAdditionalNetworks() error = %v", err)
	}
	if len(executor.commands) != 2 {
		t.Fatalf("commands = %#v, want network attachment and helper invocation", executor.commands)
	}
	if strings.Contains(executor.commands[0], "--ip6") || !strings.Contains(executor.commands[0], "network connect") {
		t.Fatalf("manual network command = %q, want ULA attachment without public --ip6", executor.commands[0])
	}
	for _, fragment := range []string{"podman-ipv6-attach.sh", "'instance-a'", "'2001:db8::20'"} {
		if !strings.Contains(executor.commands[1], fragment) {
			t.Fatalf("manual helper command = %q, missing %q", executor.commands[1], fragment)
		}
	}
}
