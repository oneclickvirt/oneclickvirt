package proxmox

import (
	"context"
	"strings"
	"testing"
)

func TestPVEIPv4DiscoveryRequiresValidatedGuestAddress(t *testing.T) {
	for _, tc := range []struct{ name, typ, config, dynamic, want string }{
		{"static public CT", "container", "net0: name=eth0,ip=198.51.100.9/27,gw=198.51.100.1", "", "198.51.100.9"},
		{"static VM", "vm", "ipconfig0: ip=172.16.1.4/24,gw=172.16.1.1", "", "172.16.1.4"},
		{"BusyBox diagnostic", "container", "net0: ip6=2001:db8::9/72", "hostname: unrecognized option: I\nBusyBox usage: hostname", ""},
		{"IPv6-only CT", "container", "net0: ip6=2001:db8::9/72", "", ""},
		{"dynamic CT", "container", "net0: ip=dhcp", "2: eth0    inet 172.16.1.9/24 brd 172.16.1.255 scope global eth0", "172.16.1.9"},
		{"invalid CT output", "container", "description: ip=198.51.100.9", "no IPv4; inet invalid", ""},
		{"IPv6-only VM", "vm", "ipconfig0: ip6=2001:db8::9/72", `[{"name":"lo","ip-addresses":[{"ip-address":"127.0.0.1"}]},{"name":"eth0","ip-addresses":[{"ip-address":"2001:db8::9"}]}]`, ""},
		{"public agent IPv4", "vm", "ipconfig0: ip=dhcp", `[{"name":"eth0","ip-addresses":[{"ip-address":"169.254.1.1"},{"ip-address":"198.51.100.9"}]}]`, "198.51.100.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &ipv6CommandExecutor{output: func(command string) string {
				if command == "pct config 104" || command == "qm config 104" {
					return tc.config
				}
				return tc.dynamic
			}}
			p := NewProxmoxProvider().(*ProxmoxProvider)
			p.sshClient.SetExecutor(executor)
			got, err := p.getInstanceIPAddress(context.Background(), "104", tc.typ)
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatalf("address=%q err=%v, want %q", got, err, tc.want)
			}
			for _, command := range executor.commands {
				if strings.Contains(command, "ping ") || strings.Contains(command, "hostname -I") {
					t.Fatalf("unsafe address fallback: %s", command)
				}
			}
		})
	}
}

func TestPVEAgentIPv6ParsesJSONAndRejectsDiagnostics(t *testing.T) {
	output := `[{"name":"eth0","ip-addresses":[{"ip-address":"fe80::1"},{"ip-address":"2001:db8::a"}]}]`
	if got, err := proxmoxAgentAddress(output, true); err != nil || got != "2001:db8::a" {
		t.Fatalf("address=%q err=%v", got, err)
	}
	for _, output := range []string{"", "unreachable", `{"error":"not ready"}`, `[{"ip-addresses":[{"ip-address":"::"}]}]`} {
		if got, err := proxmoxAgentAddress(output, true); err == nil || got != "" {
			t.Fatalf("accepted invalid agent response: %q", output)
		}
	}
}
