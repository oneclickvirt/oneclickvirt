package containerd

import (
	"errors"
	"strings"
	"testing"

	"oneclickvirt/utils"
)

func TestContainerdStaticIPv6RunOptions(t *testing.T) {
	tests := []struct {
		name        string
		networkType string
		staticIPv6  string
		available   bool
		want        []string
		wantErr     string
	}{
		{
			name:        "static address is attached",
			networkType: "ipv6_only",
			staticIPv6:  "2001:0db8::30/128",
			available:   true,
			want:        []string{"--network='containerd-ipv6'", "--ip6='2001:db8::30'"},
		},
		{name: "invalid static address", networkType: "nat_ipv4_ipv6", staticIPv6: "not-an-ip", available: true, wantErr: "静态IPv6地址无效"},
		{name: "IPv6 network unavailable", networkType: "nat_ipv4_ipv6", staticIPv6: "2001:db8::30", wantErr: "节点IPv6网络不可用"},
		{name: "IPv4-only network rejects static address", networkType: "nat_ipv4", staticIPv6: "2001:db8::30", available: true, wantErr: "未启用IPv6"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selection, err := utils.ResolveContainerNetwork(test.networkType, test.staticIPv6, ipv4Network, ipv6Network, test.available)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("ResolveContainerNetwork() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveContainerNetwork() error = %v", err)
			}
			command := appendContainerdNetworkOptions("nerdctl run", selection)
			for _, fragment := range test.want {
				if !strings.Contains(command, fragment) {
					t.Fatalf("command = %q, want fragment %q", command, fragment)
				}
			}
		})
	}
}

func TestContainerdNAT66RejectsPublicStaticIPv6(t *testing.T) {
	if err := rejectContainerdNAT66PublicStaticIPv6("2a14:6781:a::20/64", true); err == nil || !strings.Contains(err.Error(), "ULA NAT66") {
		t.Fatalf("public static IPv6 on NAT66 = %v, want capability error", err)
	}
	if err := rejectContainerdNAT66PublicStaticIPv6("fd42:5339:296f:1e00::20", true); err != nil {
		t.Fatalf("ULA static IPv6 on NAT66 = %v, want nil", err)
	}
	if err := rejectContainerdNAT66PublicStaticIPv6("2a14:6781:a::20", false); err != nil {
		t.Fatalf("managed IPv6 must continue accepting a public static address: %v", err)
	}
}

func TestContainerdNAT66NetworkIsAvailableWithoutNDPResponder(t *testing.T) {
	executor := &routedContainerdExecutor{outputs: []string{"", "nat", "fd42:5339:296f:1e00::/64"}}
	c := NewContainerdProvider().(*ContainerdProvider)
	c.connected = true
	c.sshClient.SetExecutor(executor)
	if !c.checkIPv6NetworkAvailable() {
		t.Fatal("installer-managed ULA NAT66 network was not reported as available")
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

func TestContainerdTunnelIPv6SkipsNDPResponder(t *testing.T) {
	executor := &routedContainerdExecutor{outputs: []string{"", "manual", "false", "valid", ""}}
	c := NewContainerdProvider().(*ContainerdProvider)
	c.connected = true
	c.sshClient.SetExecutor(executor)
	if !c.checkIPv6NetworkAvailable() {
		t.Fatal("Containerd tunnel IPv6 was rejected without an NDP responder")
	}
	for _, command := range executor.commands {
		if strings.Contains(command, "inspect -f '{{.State.Status}}' ndpresponder") || strings.Contains(command, "containerd_ipv6_ndp_ready") {
			t.Fatalf("tunnel IPv6 unexpectedly required NDP readiness: %q", command)
		}
	}
}

func TestContainerdManagedIPv6RequiresNDPReadiness(t *testing.T) {
	executor := &routedContainerdExecutor{
		outputs: []string{"", "managed", "true", "running", "valid", ""},
		errors:  []error{nil, nil, nil, nil, nil, errors.New("responder not ready")},
	}
	c := NewContainerdProvider().(*ContainerdProvider)
	c.connected = true
	c.sshClient.SetExecutor(executor)
	if c.checkIPv6NetworkAvailable() {
		t.Fatal("Containerd accepted IPv6 before the responder readiness marker")
	}
	if commands := strings.Join(executor.commands, "\n"); !strings.Contains(commands, "containerd_ipv6_ndp_ready_required") {
		t.Fatalf("Containerd did not inspect the readiness requirement: %#v", executor.commands)
	}
}
