package firewall

import (
	"fmt"
	"testing"
)

func TestParseDNATRulesScansNftAndIptablesTogether(t *testing.T) {
	nftOutput := `table ip custom_nat {
	chain inbound {
		tcp dport 22022 dnat to 10.20.0.12:22 comment "legacy name with spaces"
		udp dport 30000-30002 counter packets 4 bytes 512 dnat to 10.20.0.12:40000-40002
	}
}`
	iptablesOutput := `*nat
-A PREROUTING -p tcp -m tcp --dport 8080 -j DNAT --to-destination 10.20.0.12:80
-A PREROUTING -p udp -m udp --dport 22022 -j DNAT --to-destination 10.20.0.12:22
COMMIT`

	rules := ParseDNATRules(nftOutput, iptablesOutput)["10.20.0.12"]
	if len(rules) != 6 {
		t.Fatalf("rules = %#v, want 6 rules from both backends", rules)
	}
	wants := map[string]bool{
		"22022/22/tcp":    false,
		"22022/22/udp":    false,
		"30000/40000/udp": false,
		"30001/40001/udp": false,
		"30002/40002/udp": false,
		"8080/80/tcp":     false,
	}
	for _, rule := range rules {
		key := discoveryRuleTestKey(rule)
		if _, exists := wants[key]; exists {
			wants[key] = true
		}
		if rule.GuestPort == 22 && !rule.IsSSH {
			t.Fatalf("SSH rule not marked: %#v", rule)
		}
	}
	for key, found := range wants {
		if !found {
			t.Errorf("missing rule %s in %#v", key, rules)
		}
	}
}

func TestParseDNATRulesSupportsHumanIptablesAndNftSets(t *testing.T) {
	nftOutput := `tcp dport { 10080, 10443 } dnat to 192.168.50.9`
	iptablesOutput := `DNAT udp -- 0.0.0.0/0 0.0.0.0/0 udp dpt:1053 to:192.168.50.9:53`
	rules := ParseDNATRules(nftOutput, iptablesOutput)["192.168.50.9"]
	if len(rules) != 3 {
		t.Fatalf("rules = %#v, want 3", rules)
	}
	wants := map[string]bool{"10080/10080/tcp": false, "10443/10443/tcp": false, "1053/53/udp": false}
	for _, rule := range rules {
		wants[discoveryRuleTestKey(rule)] = true
	}
	for key, found := range wants {
		if !found {
			t.Errorf("missing %s in %#v", key, rules)
		}
	}
}

func TestParseDNATRulesRejectsOversizedRange(t *testing.T) {
	rules := ParseDNATRules(`tcp dport 1-65535 dnat to 10.0.0.2`, "")
	if len(rules) != 0 {
		t.Fatalf("oversized range should be ignored: %#v", rules)
	}
}

func TestParseDNATRulesForIdentifierMatchesExactComment(t *testing.T) {
	nftOutput := `tcp dport 22022 dnat to 10.20.0.12:22 comment "vm:odd-name"
tcp dport 22023 dnat to 10.20.0.13:22 comment "vm:odd-name-extra"`
	rules := ParseDNATRulesForIdentifier(nftOutput, "", "odd-name")
	if len(rules) != 1 || rules[0].TargetIP != "10.20.0.12" || rules[0].HostPort != 22022 {
		t.Fatalf("comment-associated rules = %#v, want exact instance match", rules)
	}
}

func discoveryRuleTestKey(rule DiscoveredRule) string {
	return fmt.Sprintf("%d/%d/%s", rule.HostPort, rule.GuestPort, rule.Protocol)
}
