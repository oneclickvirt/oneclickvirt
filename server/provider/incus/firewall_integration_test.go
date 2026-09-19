//go:build firewall_integration

package incus

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"oneclickvirt/internal/testutil"
	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

func TestFirewallIntegrationIncusDualStackRetriesAndRemoval(t *testing.T) {
	executor := testutil.NewFirewallExecutor(t)
	i := &IncusProvider{connected: true, config: provider.NodeConfig{ExecutionRule: "ssh_only"}, sshClient: utils.NewSafeShellExecutor(executor)}
	for attempt := 0; attempt < 2; attempt++ {
		for _, address := range []string{"192.0.2.10", "2001:db8::10"} {
			if err := i.setupIptablesMappingWithIP("guest", 22000, 22, "both", address); err != nil {
				t.Fatal(err)
			}
		}
	}
	count := func(ipv6 bool) int {
		command := "nft list table ip incus"
		if ipv6 {
			command = "ip6tables -t nat -S PREROUTING"
		}
		return strings.Count(executor.MustExecute(t, command), "pm:guest:22000:22")
	}
	if count(false) != 2 || count(true) != 2 {
		t.Fatal("retry duplicated a rule or lost one address family")
	}
	// Missing saved IPv6 during rollback must not delete the IPv4 mapping.
	if err := i.RemovePortMappingForFamily("guest", 22000, 22, 22000, 22, 1, "both", "iptables", "", true); err != nil {
		t.Fatal(err)
	}
	if count(false) != 2 || count(true) != 0 {
		t.Fatal("IPv6 cleanup affected IPv4 or left stale rules")
	}
	if err := i.RemovePortMappingForFamily("guest", 22000, 22, 22000, 22, 1, "both", "iptables", "", false); err != nil {
		t.Fatal(err)
	}
	if count(false) != 0 {
		t.Fatal("IPv4 mapping survived cleanup")
	}
	// Reusing the released host port leaves only the replacement instance.
	if err := i.setupIptablesMappingWithIP("replacement", 22000, 22, "both", "192.0.2.20"); err != nil {
		t.Fatal(err)
	}
	rules := executor.MustExecute(t, "nft list table ip incus")
	if strings.Contains(rules, "192.0.2.10") || strings.Count(rules, "pm:replacement:22000:22") != 2 {
		t.Fatalf("recycled host port still points to old guest: %s", rules)
	}
}

func TestFirewallIntegrationIncusFailedReadStopsReplacement(t *testing.T) {
	executor := testutil.NewFirewallExecutor(t)
	i := &IncusProvider{connected: true, config: provider.NodeConfig{ExecutionRule: "ssh_only"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := i.setupIptablesMappingWithIP("guest", 22000, 22, "tcp", "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	i.sshClient = utils.NewSafeShellExecutor(&incusFirewallReadFailure{FirewallExecutor: executor})
	if err := i.setupIptablesMappingWithIP("guest", 22000, 22, "tcp", "192.0.2.11"); err == nil {
		t.Fatal("replacement ignored cleanup read failure")
	}
	rules := executor.MustExecute(t, "nft list table ip incus")
	if strings.Count(rules, "pm:guest:22000:22") != 1 || strings.Contains(rules, "192.0.2.11") {
		t.Fatalf("failed cleanup added conflicting rules: %s", rules)
	}
}

type incusFirewallReadFailure struct{ *testutil.FirewallExecutor }

func (e *incusFirewallReadFailure) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	if command == "nft -j -a list ruleset" {
		return "", fmt.Errorf("injected transport failure")
	}
	return e.FirewallExecutor.ExecuteWithTimeout(command, timeout)
}
