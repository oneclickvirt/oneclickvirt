package firewall

import (
	"encoding/json"
	"fmt"
	"time"
)

// JSON input preserves literal comments, including quotes and backslashes.
// nft's textual grammar does not accept every Go-quoted string. Submit all
// rules together so a rejected expression cannot leave half a NAT mapping.
func (m *Manager) addSingleDNATNft(address string, hostPort, guestPort int, protocol, comment string, ipv6 bool) error {
	family, table := "ip", m.tableName
	if ipv6 {
		family, table = "ip6", m.ipv6NftTable()
	}
	type object = map[string]interface{}
	match := func(protocol, field string, value interface{}) object {
		return object{"match": object{"op": "==", "left": object{"payload": object{"protocol": protocol, "field": field}}, "right": value}}
	}
	rules := []object{}
	add := func(chain string, expressions []object) {
		rule := object{"family": family, "table": table, "chain": chain, "expr": expressions}
		if comment != "" {
			rule["comment"] = comment
		}
		rules = append(rules, object{"add": object{"rule": rule}})
	}
	for _, proto := range expandProtocol(protocol) {
		add("prerouting", []object{match(proto, "dport", hostPort), {"dnat": object{"addr": normalizeFirewallIP(address), "port": guestPort}}})
		if ipv6 {
			add("forward", []object{match(family, "daddr", normalizeFirewallIP(address)), match(proto, "dport", guestPort), {"accept": nil}})
			add("postrouting", []object{match(family, "saddr", normalizeFirewallIP(address)), match(proto, "sport", guestPort), {"masquerade": nil}})
		}
	}
	content, err := json.Marshal(object{"nftables": rules})
	if err != nil {
		return err
	}
	if _, err := m.sshClient.ExecuteWithTimeout("printf '%s' "+shellQuote(string(content))+" | nft -j -f -", 20*time.Second); err != nil {
		return fmt.Errorf("nft添加端口映射失败: %w", err)
	}
	return nil
}
