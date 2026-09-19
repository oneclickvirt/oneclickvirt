package firewall

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type cleanupRule struct {
	family, table, chain, tool string
	handle                     uint64
	args                       []string
	comment, protocol          string
	source, destination        string
	dnatIP, action             string
	dport, sport, guestPort    int
	legacySafe                 bool
}

func (r cleanupRule) deleteCommand() string {
	if r.tool == "nft" {
		return fmt.Sprintf("nft delete rule %s %s %s handle %d", r.family, shellQuote(r.table), shellQuote(r.chain), r.handle)
	}
	args := append([]string(nil), r.args...)
	args[0] = "-D"
	for index := range args {
		args[index] = shellQuote(args[index])
	}
	return r.tool + " -w 5 -t " + shellQuote(r.table) + " " + strings.Join(args, " ")
}

func cleanupNumber(value interface{}) int {
	switch value := value.(type) {
	case float64:
		if value == float64(int(value)) {
			return int(value)
		}
	case string:
		number, _ := strconv.Atoi(value)
		return number
	}
	return 0
}

func cleanupHost(value interface{}) string {
	if address, ok := value.(string); ok {
		ip := net.ParseIP(address)
		if ip != nil {
			return ip.String()
		}
		parsed, subnet, err := net.ParseCIDR(address)
		if err == nil {
			ones, bits := subnet.Mask.Size()
			if ones == bits {
				return parsed.String()
			}
		}
		return ""
	}
	if object, ok := value.(map[string]interface{}); ok {
		if prefix, ok := object["prefix"].(map[string]interface{}); ok {
			address, _ := prefix["addr"].(string)
			return cleanupHost(fmt.Sprintf("%s/%d", address, cleanupNumber(prefix["len"])))
		}
	}
	return ""
}

func (m *Manager) readNftCleanupRules(ipv6 bool) ([]cleanupRule, error) {
	output, err := m.sshClient.ExecuteWithTimeout("nft -j -a list ruleset", 20*time.Second)
	if err != nil {
		return nil, fmt.Errorf("读取nft规则失败: %w", err)
	}
	var result struct {
		Nftables []struct {
			Rule *struct {
				Family, Table, Chain, Comment string
				Handle                        uint64
				Expr                          []map[string]interface{}
			}
		}
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("解析nft规则失败: %w", err)
	}
	if result.Nftables == nil {
		return nil, fmt.Errorf("nft响应缺少规则集")
	}
	family, table := "ip", m.tableName
	if ipv6 {
		family, table = "ip6", m.ipv6NftTable()
	}
	var rules []cleanupRule
	for _, item := range result.Nftables {
		raw := item.Rule
		if raw == nil || raw.Family != family || raw.Table != table {
			continue
		}
		if raw.Chain != "prerouting" && raw.Chain != "postrouting" && raw.Chain != "forward" {
			continue
		}
		if raw.Handle == 0 {
			return nil, fmt.Errorf("nft规则缺少有效handle")
		}
		rule := cleanupRule{tool: "nft", family: family, table: table, chain: raw.Chain, handle: raw.Handle, comment: raw.Comment, legacySafe: true}
		for _, expression := range raw.Expr {
			if target, ok := expression["dnat"].(map[string]interface{}); ok {
				rule.action = "DNAT"
				rule.dnatIP = cleanupHost(target["addr"])
				rule.guestPort = cleanupNumber(target["port"])
			}
			if _, ok := expression["accept"]; ok {
				rule.action = "ACCEPT"
			}
			if _, ok := expression["masquerade"]; ok {
				rule.action = "MASQUERADE"
			}
			match, _ := expression["match"].(map[string]interface{})
			if match == nil {
				continue
			}
			if match["op"] != "==" {
				rule.legacySafe = false
				continue
			}
			left, _ := match["left"].(map[string]interface{})
			payload, _ := left["payload"].(map[string]interface{})
			protocol, _ := payload["protocol"].(string)
			field, _ := payload["field"].(string)
			if protocol == "tcp" || protocol == "udp" {
				rule.protocol = protocol
				if field == "dport" {
					rule.dport = cleanupNumber(match["right"])
				}
				if field == "sport" {
					rule.sport = cleanupNumber(match["right"])
				}
			}
			if protocol == "ip" || protocol == "ip6" {
				if field == "daddr" {
					rule.destination = cleanupHost(match["right"])
				}
				if field == "saddr" {
					rule.source = cleanupHost(match["right"])
				}
			}
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// xtables -S uses quoted/escaped words, not executable shell input. Decode
// those words and quote each argument again when deleting a selected rule.
func splitXTablesRule(line string) ([]string, error) {
	var words []string
	var word strings.Builder
	var quote rune
	escaped, present := false, false
	for _, char := range line {
		if escaped {
			word.WriteRune(char)
			escaped = false
			present = true
			continue
		}
		if char == '\\' && quote != '\'' {
			escaped = true
			present = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				word.WriteRune(char)
			}
			continue
		}
		if char == '\'' || char == '"' {
			quote = char
			present = true
			continue
		}
		if unicode.IsSpace(char) {
			if present {
				words = append(words, word.String())
				word.Reset()
				present = false
			}
			continue
		}
		word.WriteRune(char)
		present = true
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("unterminated xtables rule quoting")
	}
	if present {
		words = append(words, word.String())
	}
	return words, nil
}

func (m *Manager) readXTablesCleanupRules(ipv6 bool) ([]cleanupRule, error) {
	tool, family := "iptables", "ip"
	if ipv6 {
		tool, family = "ip6tables", "ip6"
	}
	var rules []cleanupRule
	for _, table := range []string{"nat", "filter"} {
		output, err := m.sshClient.ExecuteWithTimeout(tool+" -w 5 -t "+table+" -S", 20*time.Second)
		if err != nil {
			return nil, fmt.Errorf("读取%s %s规则失败: %w", tool, table, err)
		}
		for _, line := range strings.Split(output, "\n") {
			args, err := splitXTablesRule(line)
			if err != nil {
				return nil, err
			}
			if len(args) < 2 || args[0] != "-A" {
				continue
			}
			chain := args[1]
			if !((table == "nat" && (chain == "PREROUTING" || chain == "POSTROUTING")) || (table == "filter" && chain == "FORWARD")) {
				continue
			}
			rule := cleanupRule{tool: tool, family: family, table: table, chain: chain, args: args, legacySafe: true}
			for index := 2; index+1 < len(args); index++ {
				if args[index] == "!" {
					rule.legacySafe = false
					continue
				}
				value := args[index+1]
				switch args[index] {
				case "--comment":
					rule.comment = value
				case "-p":
					rule.protocol = value
				case "-s":
					rule.source = cleanupHost(value)
				case "-d":
					rule.destination = cleanupHost(value)
				case "--dport":
					rule.dport = cleanupNumber(value)
				case "--sport":
					rule.sport = cleanupNumber(value)
				case "-j":
					rule.action = value
				case "--to-destination":
					host, port, err := net.SplitHostPort(value)
					if err == nil {
						rule.dnatIP, rule.guestPort = cleanupHost(host), cleanupNumber(port)
					} else {
						rule.dnatIP = cleanupHost(value)
					}
				default:
					continue
				}
				index++
			}
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

func (m *Manager) readCleanupRules(ipv6 bool) ([]cleanupRule, error) {
	var rules []cleanupRule
	available := false
	// A previous IPv6 mapping may have used the nft fallback before ip6tables
	// was installed. Inspect both available backends instead of guessing.
	if _, err := m.sshClient.Execute("command -v nft >/dev/null 2>&1"); err == nil || m.backend == BackendNft {
		available = true
		found, err := m.readNftCleanupRules(ipv6)
		if err != nil {
			return nil, err
		}
		rules = append(rules, found...)
	}
	tool := "iptables"
	if ipv6 {
		tool = "ip6tables"
	}
	if _, err := m.sshClient.Execute("command -v " + tool + " >/dev/null 2>&1"); err == nil || (!ipv6 && m.backend == BackendIptables) {
		available = true
		found, err := m.readXTablesCleanupRules(ipv6)
		if err != nil {
			return nil, err
		}
		rules = append(rules, found...)
	}
	if !available {
		return nil, fmt.Errorf("无可用的IPv%d防火墙检查工具", map[bool]int{false: 4, true: 6}[ipv6])
	}
	return rules, nil
}

func (m *Manager) removeSelectedRules(ipv6 bool, selectRule func(cleanupRule) bool, rules []cleanupRule) error {
	for _, rule := range rules {
		if !selectRule(rule) {
			continue
		}
		command := rule.deleteCommand()
		if _, err := m.sshClient.ExecuteWithTimeout(command, 20*time.Second); err != nil {
			current, readErr := m.readCleanupRules(ipv6)
			if readErr != nil {
				return fmt.Errorf("删除防火墙规则失败: %w; 复查失败: %v", err, readErr)
			}
			for _, remaining := range current {
				if selectRule(remaining) && remaining.deleteCommand() == command {
					return fmt.Errorf("删除防火墙规则失败，原规则仍存在: %w", err)
				}
			}
			// A concurrent cleanup already removed exactly this rule.
		}
	}
	current, err := m.readCleanupRules(ipv6)
	if err != nil {
		return err
	}
	for _, remaining := range current {
		if selectRule(remaining) {
			return fmt.Errorf("清理后仍存在属于该端口映射的防火墙规则")
		}
	}
	return nil
}

// RemoveSingleDNATForFamily also handles failed creates whose guest IP was not
// persisted. Exact owner comments remain sufficient; ambiguous untagged DNAT
// rules require the original guest address and are never deleted by host port.
func (m *Manager) RemoveSingleDNATForFamily(instanceIP string, hostPort, guestPort int, protocol, comment string, ipv6 bool) error {
	address := normalizeFirewallIP(instanceIP)
	if address != "" {
		ip := net.ParseIP(address)
		if ip == nil || (ip.To4() == nil) != ipv6 {
			return fmt.Errorf("无效的端口映射目标地址 %q", instanceIP)
		}
		address = ip.String()
	} else if strings.TrimSpace(comment) == "" {
		return fmt.Errorf("缺少目标地址和规则归属标记，拒绝删除端口映射")
	}
	if hostPort < 1 || hostPort > 65535 || guestPort < 0 || guestPort > 65535 {
		return fmt.Errorf("无效的端口映射范围")
	}
	protocols := make(map[string]bool)
	for _, item := range expandProtocol(strings.ToLower(strings.TrimSpace(protocol))) {
		if item != "tcp" && item != "udp" {
			return fmt.Errorf("无效的端口映射协议 %q", protocol)
		}
		protocols[item] = true
	}
	rules, err := m.readCleanupRules(ipv6)
	if err != nil {
		return err
	}
	guestPorts := map[int]bool{}
	if guestPort > 0 {
		guestPorts[guestPort] = true
	}
	for _, rule := range rules {
		if rule.action != "DNAT" || rule.dport != hostPort || !protocols[rule.protocol] {
			continue
		}
		if rule.comment == "" && address == "" {
			return fmt.Errorf("缺少实例IP，端口%d存在未标记归属的DNAT规则，拒绝猜测删除", hostPort)
		}
		if guestPort == 0 && ((comment != "" && rule.comment == comment) || (address != "" && rule.dnatIP == address && rule.comment == "")) {
			guestPorts[rule.guestPort] = true
		}
	}
	selectDNAT := func(rule cleanupRule) bool {
		if rule.action != "DNAT" || !protocols[rule.protocol] || rule.dport != hostPort || !guestPorts[rule.guestPort] {
			return false
		}
		if comment != "" && rule.comment == comment {
			return true
		}
		return rule.legacySafe && rule.comment == "" && address != "" && rule.dport == hostPort && rule.dnatIP == address && guestPorts[rule.guestPort]
	}
	// An untagged FORWARD/MASQUERADE rule may support several host mappings
	// to the same guest service. Keep it while any other DNAT still uses it.
	sharedSupport := func(rule cleanupRule) bool {
		target := rule.destination
		if rule.action == "MASQUERADE" {
			target = rule.source
		}
		for _, other := range rules {
			if other.action == "DNAT" && !selectDNAT(other) && other.dnatIP == target && other.protocol == rule.protocol && (rule.comment == "" || other.comment == rule.comment) {
				if other.guestPort == 0 || other.guestPort == rule.dport || other.guestPort == rule.sport {
					return true
				}
			}
		}
		return false
	}
	selectRule := func(rule cleanupRule) bool {
		if !protocols[rule.protocol] {
			return false
		}
		if comment != "" && rule.comment == comment {
			switch rule.action {
			case "DNAT":
				return selectDNAT(rule)
			case "ACCEPT":
				return guestPorts[rule.dport] && !sharedSupport(rule)
			case "MASQUERADE":
				return guestPorts[rule.sport] && !sharedSupport(rule)
			}
			return false
		}
		if rule.comment != "" || address == "" || !rule.legacySafe {
			return false
		}
		switch rule.action {
		case "DNAT":
			return selectDNAT(rule)
		case "ACCEPT":
			return rule.destination == address && guestPorts[rule.dport] && !sharedSupport(rule)
		case "MASQUERADE":
			return rule.source == address && guestPorts[rule.sport] && !sharedSupport(rule)
		}
		return false
	}
	return m.removeSelectedRules(ipv6, selectRule, rules)
}

// DeleteRulesByCommentForFamily deletes one owner's rules in one address
// family, including rules left behind by a previous firewall backend.
func (m *Manager) DeleteRulesByCommentForFamily(comment string, ipv6 bool) error {
	if strings.TrimSpace(comment) == "" {
		return fmt.Errorf("拒绝按空注释删除防火墙规则")
	}
	rules, err := m.readCleanupRules(ipv6)
	if err != nil {
		return err
	}
	return m.removeSelectedRules(ipv6, func(rule cleanupRule) bool { return rule.comment == comment }, rules)
}
