//go:build firewall_integration

package firewall

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/global"
	"oneclickvirt/internal/testutil"
)

func integrationManager(t *testing.T) (*Manager, *testutil.FirewallExecutor) {
	t.Helper()
	if global.APP_LOG == nil {
		global.APP_LOG = zap.NewNop()
	}
	e := testutil.NewFirewallExecutor(t)
	m := NewManager(e, "ocvtest", "")
	if _, err := m.DetectBackend(""); err != nil {
		t.Fatal(err)
	}
	if err := m.InitTable(); err != nil {
		t.Fatal(err)
	}
	return m, e
}

func assertOwnerCount(t *testing.T, m *Manager, ipv6 bool, owner string, want int) {
	t.Helper()
	rules, err := m.readCleanupRules(ipv6)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, rule := range rules {
		if rule.comment == owner {
			count++
		}
	}
	if count != want {
		t.Fatalf("IPv6=%v owner %q: %d rules, want %d: %+v", ipv6, owner, count, want, rules)
	}
}

func TestFirewallIntegrationDualStackMixedBackendsAndDuplicates(t *testing.T) {
	m, _ := integrationManager(t)
	const owner = "pm:guest:22000:22"
	const other = "pm:guest-other:22000:22"
	if err := m.AddSingleDNAT("192.0.2.10", 22000, 22, "both", owner); err != nil {
		t.Fatal(err)
	}
	if err := m.addSingleDNATIPv6Nft("2001:db8::10", 22000, 22, "both", owner); err != nil {
		t.Fatal(err)
	}
	if err := m.addSingleDNATIPv6Nft("2001:db8::11", 22000, 22, "both", other); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		if err := m.AddSingleDNAT("2001:db8::10", 22000, 22, "both", owner); err != nil {
			t.Fatal(err)
		}
	}
	assertOwnerCount(t, m, true, owner, 18)
	assertOwnerCount(t, m, true, other, 6)
	// The failed-create path has no saved guest IP.
	if err := m.RemoveSingleDNATForFamily("", 22000, 22, "both", owner, true); err != nil {
		t.Fatal(err)
	}
	assertOwnerCount(t, m, true, owner, 0)
	assertOwnerCount(t, m, true, other, 6)
	assertOwnerCount(t, m, false, owner, 2)
	if err := m.RemoveSingleDNATForFamily("", 22000, 22, "both", owner, true); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if err := m.DeleteRulesByComment(owner); err != nil {
		t.Fatal(err)
	}
	assertOwnerCount(t, m, false, owner, 0)
	assertOwnerCount(t, m, true, other, 6)
}

func TestFirewallIntegrationLegacySharedGuestService(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "ip6tables", true: "nft"}[native], func(t *testing.T) {
			m, _ := integrationManager(t)
			add := m.AddSingleDNAT
			if native {
				add = m.addSingleDNATIPv6Nft
			}
			if err := add("2001:db8::10", 22000, 22, "tcp", ""); err != nil {
				t.Fatal(err)
			}
			if err := add("2001:db8::10", 22001, 22, "tcp", "other"); err != nil {
				t.Fatal(err)
			}
			if err := m.RemoveSingleDNAT("2001:db8::10", 22000, 22, "tcp", "owner"); err != nil {
				t.Fatal(err)
			}
			rules, err := m.readCleanupRules(true)
			if err != nil {
				t.Fatal(err)
			}
			if len(rules) != 5 {
				t.Fatalf("lost shared service support: %+v", rules)
			}
			for _, rule := range rules {
				if rule.action == "DNAT" && rule.dport == 22000 {
					t.Fatal("legacy DNAT remains")
				}
			}
			if err := m.RemoveSingleDNAT("2001:db8::10", 22001, 22, "tcp", "other"); err != nil {
				t.Fatal(err)
			}
			rules, err = m.readCleanupRules(true)
			if err != nil || len(rules) != 0 {
				t.Fatalf("last reference cleanup: %+v, %v", rules, err)
			}
		})
	}
}

func TestFirewallIntegrationMissingIPRefusesAmbiguousLegacy(t *testing.T) {
	m, e := integrationManager(t)
	if err := m.AddSingleDNAT("192.0.2.10", 22000, 22, "tcp", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.AddSingleDNAT("192.0.2.11", 22000, 22, "tcp", "owner"); err != nil {
		t.Fatal(err)
	}
	before := e.MustExecute(t, "nft -j -a list ruleset")
	if err := m.RemoveSingleDNATForFamily("", 22000, 22, "tcp", "owner", false); err == nil {
		t.Fatal("ambiguous cleanup succeeded")
	}
	if after := e.MustExecute(t, "nft -j -a list ruleset"); before != after {
		t.Fatal("ambiguous cleanup mutated firewall")
	}
}

func TestFirewallIntegrationDeleteByIPUsesExactAddress(t *testing.T) {
	m, e := integrationManager(t)
	for _, address := range []string{"2001:db8::1", "2001:db8::10"} {
		if err := m.addSingleDNATIPv6Nft(address, 22000, 22, "tcp", ""); err != nil {
			t.Fatal(err)
		}
	}
	e.MustExecute(t, "nft 'add rule ip6 ocvtest6 forward ip6 daddr != 2001:db8::1 tcp dport 22 accept'")
	if err := m.DeleteRulesByIP("2001:db8::1"); err != nil {
		t.Fatal(err)
	}
	rules, err := m.readCleanupRules(true)
	if err != nil || len(rules) != 4 {
		t.Fatalf("cleanup affected a neighbor or negated rule: %+v, %v", rules, err)
	}
}

func TestFirewallIntegrationQuotedXTablesOwner(t *testing.T) {
	m, _ := integrationManager(t)
	owner := "pm:quote' and \"double\" \\back $(false) `false`:22000:22"
	if err := m.AddSingleDNAT("2001:db8::10", 22000, 22, "tcp", owner); err != nil {
		t.Fatal(err)
	}
	assertOwnerCount(t, m, true, owner, 3)
	if err := m.DeleteRulesByCommentForFamily(owner, true); err != nil {
		t.Fatal(err)
	}
	rules, err := m.readCleanupRules(true)
	if err != nil || len(rules) != 0 {
		t.Fatalf("quoted owner survived cleanup: %+v %v", rules, err)
	}
	if err := m.AddSingleDNAT("192.0.2.10", 22000, 22, "tcp", owner); err != nil {
		t.Fatal(err)
	}
	if err := m.addSingleDNATIPv6Nft("2001:db8::10", 22000, 22, "tcp", owner); err != nil {
		t.Fatal(err)
	}
	assertOwnerCount(t, m, false, owner, 1)
	assertOwnerCount(t, m, true, owner, 3)
	if err := m.DeleteRulesByComment(owner); err != nil {
		t.Fatal(err)
	}
	assertOwnerCount(t, m, false, owner, 0)
	assertOwnerCount(t, m, true, owner, 0)
}

func TestFirewallIntegrationSharedOwnerKeepsOtherHostPort(t *testing.T) {
	m, _ := integrationManager(t)
	for _, port := range []int{22000, 22001} {
		if err := m.AddSingleDNAT("2001:db8::10", port, 22, "tcp", "vm:guest"); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.RemoveSingleDNAT("2001:db8::10", 22000, 22, "tcp", "vm:guest"); err != nil {
		t.Fatal(err)
	}
	assertOwnerCount(t, m, true, "vm:guest", 5)
	if err := m.RemoveSingleDNAT("2001:db8::10", 22001, 22, "tcp", "vm:guest"); err != nil {
		t.Fatal(err)
	}
	assertOwnerCount(t, m, true, "vm:guest", 0)
}

func TestFirewallIntegrationLegacyVMOwnerAndPersistence(t *testing.T) {
	m, e := integrationManager(t)
	e.MustExecute(t, "nft delete table ip ocvtest")
	m.backend = BackendIptables
	if err := m.AddDNAT("guest", "192.0.2.10", 22000, 30000, 30001); err != nil {
		t.Fatal(err)
	}
	assertOwnerCount(t, m, false, "vm:guest", 6)
	if err := m.SaveRules(); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteRulesByComment("vm:guest"); err != nil {
		t.Fatal(err)
	}
	assertOwnerCount(t, m, false, "vm:guest", 0)
}

func TestFirewallIntegrationProtocolIsolation(t *testing.T) {
	m, _ := integrationManager(t)
	for _, address := range []string{"192.0.2.10", "2001:db8::10"} {
		if err := m.AddSingleDNAT(address, 22000, 22, "both", "owner"); err != nil {
			t.Fatal(err)
		}
		if err := m.RemoveSingleDNAT(address, 22000, 22, "tcp", "owner"); err != nil {
			t.Fatal(err)
		}
		rules, err := m.readCleanupRules(strings.Contains(address, ":"))
		if err != nil || len(rules) == 0 {
			t.Fatalf("UDP rules lost: %+v, %v", rules, err)
		}
		for _, rule := range rules {
			if rule.protocol != "udp" {
				t.Fatalf("TCP rule survived: %+v", rule)
			}
		}
	}
}

func TestFirewallIntegrationPersistenceRoundTripAndReadFailure(t *testing.T) {
	m, e := integrationManager(t)
	const owner = "pm:guest:22000:22"
	if err := m.AddSingleDNAT("192.0.2.10", 22000, 22, "both", owner); err != nil {
		t.Fatal(err)
	}
	if err := m.addSingleDNATIPv6Nft("2001:db8::10", 22000, 22, "both", owner); err != nil {
		t.Fatal(err)
	}
	if err := m.AddSingleDNAT("2001:db8::10", 22000, 22, "both", owner); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveRules(); err != nil {
		t.Fatal(err)
	}
	saved := e.MustExecute(t, "cat /etc/nftables.d/ocvtest.nft /etc/iptables/rules.v4 /etc/iptables/rules.v6")
	e.MustExecute(t, "nft flush ruleset")
	e.MustExecute(t, "nft add table ip unrelated")
	for attempt := 0; attempt < 2; attempt++ {
		e.MustExecute(t, "nft -f /etc/nftables.d/ocvtest.nft")
		e.MustExecute(t, "iptables-restore < /etc/iptables/rules.v4")
		e.MustExecute(t, "ip6tables-restore < /etc/iptables/rules.v6")
		assertOwnerCount(t, m, false, owner, 2)
		assertOwnerCount(t, m, true, owner, 12)
		e.MustExecute(t, "nft list table ip unrelated")
	}
	for _, failOn := range []string{"nft -j list tables", "nft list table ip6", "ip6tables-save"} {
		t.Run(failOn, func(t *testing.T) {
			broken := NewManager(&persistenceFailureExecutor{FirewallExecutor: e, failOn: failOn}, "ocvtest", "")
			broken.backend, broken.detected = BackendNft, true
			if err := broken.SaveRules(); err == nil {
				t.Fatal("snapshot read failure became success")
			}
			if got := e.MustExecute(t, "cat /etc/nftables.d/ocvtest.nft /etc/iptables/rules.v4 /etc/iptables/rules.v6"); got != saved {
				t.Fatal("read failure replaced a saved snapshot")
			}
		})
	}
	broken := NewManager(&persistenceFailureExecutor{FirewallExecutor: e, failOn: "set -e\numask 077"}, "ocvtest", "")
	broken.backend, broken.detected = BackendNft, true
	if err := broken.SaveRules(); err == nil {
		t.Fatal("snapshot write failure became success")
	}
}

type persistenceFailureExecutor struct {
	*testutil.FirewallExecutor
	failOn string
}

type persistenceDistributionExecutor struct {
	*testutil.FirewallExecutor
	id string
}

func (e *persistenceDistributionExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	if command == persistenceDistributionCommand {
		return e.id + "\n\ncomplete\n", nil
	}
	return e.FirewallExecutor.ExecuteWithTimeout(command, timeout)
}

func TestFirewallIntegrationPersistenceDistributionFiles(t *testing.T) {
	for _, id := range []string{"debian", "fedora", "arch", "alpine"} {
		t.Run(id, func(t *testing.T) {
			_, e := integrationManager(t)
			m := NewManager(&persistenceDistributionExecutor{FirewallExecutor: e, id: id}, "ocvtest", "")
			m.backend, m.detected = BackendNft, true
			layout := layoutForDistribution(id, "")
			const owner = "pm:persist:22000:22"
			if err := m.AddSingleDNAT("192.0.2.10", 22000, 22, "tcp", owner); err != nil {
				t.Fatal(err)
			}
			if err := m.addSingleDNATIPv6Nft("2001:db8::10", 22000, 22, "tcp", owner); err != nil {
				t.Fatal(err)
			}
			if err := m.AddSingleDNAT("2001:db8::10", 22000, 22, "udp", owner); err != nil {
				t.Fatal(err)
			}
			e.MustExecute(t, "mkdir -p /etc/sysconfig; printf '%s\\n' '# administrator boot policy' > "+shellQuote(layout.nftConfig))
			if err := m.SaveRules(); err != nil {
				t.Fatal(err)
			}
			bootConfig := e.MustExecute(t, "cat "+shellQuote(layout.nftConfig))
			if !strings.Contains(bootConfig, "# administrator boot policy") || !strings.Contains(bootConfig, `include "/etc/nftables.d/*.nft"`) {
				t.Fatalf("boot configuration not preserved/wired: %s", bootConfig)
			}
			e.MustExecute(t, "nft flush ruleset; nft add table ip unrelated")
			for attempt := 0; attempt < 2; attempt++ {
				e.MustExecute(t, "nft -f "+shellQuote(layout.nftConfig))
				e.MustExecute(t, "iptables-restore < "+shellQuote(layout.ipv4))
				e.MustExecute(t, "ip6tables-restore < "+shellQuote(layout.ipv6))
				assertOwnerCount(t, m, false, owner, 1)
				assertOwnerCount(t, m, true, owner, 6)
				e.MustExecute(t, "nft list table ip unrelated")
			}
		})
	}
}

func TestFirewallIntegrationInitAfterTableAndChainRemoval(t *testing.T) {
	m, e := integrationManager(t)
	for _, command := range []string{"nft delete chain ip ocvtest forward", "nft delete chain ip ocvtest postrouting", "nft delete table ip ocvtest"} {
		e.MustExecute(t, command)
		if err := m.InitTable(); err != nil {
			t.Fatalf("reinitialize after %s: %v", command, err)
		}
		e.MustExecute(t, "nft list chain ip ocvtest forward")
		e.MustExecute(t, "nft list chain ip ocvtest postrouting")
		e.MustExecute(t, "nft list chain ip ocvtest prerouting")
	}
	if err := m.AddSingleDNAT("192.0.2.10", 22000, 22, "tcp", "recovered"); err != nil {
		t.Fatal(err)
	}
	assertOwnerCount(t, m, false, "recovered", 1)
}

func (e *persistenceFailureExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	if strings.HasPrefix(command, e.failOn) {
		return "", fmt.Errorf("injected read/write failure")
	}
	return e.FirewallExecutor.ExecuteWithTimeout(command, timeout)
}
