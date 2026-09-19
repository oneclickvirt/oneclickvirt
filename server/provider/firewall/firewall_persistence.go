package firewall

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/global"
)

var managedTableName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type persistenceLayout struct {
	nftConfig, ipv4, ipv6 string
	services              []string
}

const persistenceDistributionCommand = `# oneclickvirt firewall persistence distribution
set -e
ID= ID_LIKE=
if [ -r /etc/os-release ]; then . /etc/os-release
elif [ -r /usr/lib/os-release ]; then . /usr/lib/os-release
fi
printf '%s\n' "${ID:-}" "${ID_LIKE:-}" complete`

func layoutForDistribution(id, like string) persistenceLayout {
	// Keep the historical defaults on unknown distributions; service hooks are
	// optional and do not turn an otherwise working live rule into a failure.
	layout := persistenceLayout{"/etc/nftables.conf", "/etc/iptables/rules.v4", "/etc/iptables/rules.v6", []string{"netfilter-persistent"}}
	for _, name := range append([]string{strings.ToLower(id)}, strings.Fields(strings.ToLower(like))...) {
		switch name {
		case "debian", "ubuntu":
			return layout
		case "rhel", "centos", "fedora", "rocky", "almalinux", "ol", "amzn":
			return persistenceLayout{"/etc/sysconfig/nftables.conf", "/etc/sysconfig/iptables", "/etc/sysconfig/ip6tables", []string{"iptables", "ip6tables"}}
		case "arch", "manjaro":
			return persistenceLayout{"/etc/nftables.conf", "/etc/iptables/iptables.rules", "/etc/iptables/ip6tables.rules", []string{"iptables", "ip6tables"}}
		case "alpine":
			return persistenceLayout{"/etc/nftables.nft", "/etc/iptables/rules-save", "/etc/iptables/rules6-save", []string{"iptables", "ip6tables"}}
		}
	}
	return layout
}

func (m *Manager) persistenceLayout() (persistenceLayout, error) {
	output, err := m.sshClient.ExecuteWithTimeout(persistenceDistributionCommand, 20*time.Second)
	if err != nil {
		return persistenceLayout{}, fmt.Errorf("检测防火墙持久化发行版失败: %w", err)
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(output, "\r\n", "\n"), "\n"), "\n")
	if len(lines) != 3 || lines[2] != "complete" {
		return persistenceLayout{}, fmt.Errorf("防火墙持久化发行版响应不完整")
	}
	return layoutForDistribution(lines[0], lines[1]), nil
}

// SaveRules persists both native nft tables and compatibility rules. IPv6
// writes may use ip6tables even when the selected IPv4 backend is nft.
// Read failures leave previous snapshots intact and are returned to callers.
func (m *Manager) SaveRules() error {
	if !m.detected {
		return fmt.Errorf("防火墙后端尚未检测")
	}
	if !managedTableName.MatchString(m.tableName) {
		return fmt.Errorf("无效的防火墙表名 %q", m.tableName)
	}
	tools, err := m.availableFirewallTools()
	if err != nil {
		return err
	}
	layout, err := m.persistenceLayout()
	if err != nil {
		return err
	}
	type snapshot struct{ path, content string }
	var snapshots []snapshot
	hasManagedNftTables := false
	if tools["nft"] {
		output, err := m.sshClient.ExecuteWithTimeout("nft -j list tables", 20*time.Second)
		if err != nil {
			return fmt.Errorf("读取nft表失败: %w", err)
		}
		var tables struct {
			Nftables []struct {
				Table *struct{ Family, Name string }
			}
		}
		if err := json.Unmarshal([]byte(output), &tables); err != nil || tables.Nftables == nil {
			return fmt.Errorf("无法解析nft表清单: %v", err)
		}
		content := "# VM port forwarding - managed by oneclickvirt\n"
		for _, item := range tables.Nftables {
			table := item.Table
			if table == nil || !((table.Family == "ip" && table.Name == m.tableName) || (table.Family == "ip6" && table.Name == m.ipv6NftTable())) {
				continue
			}
			hasManagedNftTables = true
			rules, err := m.sshClient.ExecuteWithTimeout("nft list table "+table.Family+" "+shellQuote(table.Name), 20*time.Second)
			if err != nil || strings.TrimSpace(rules) == "" {
				return fmt.Errorf("读取nft表 %s %s 失败: %v", table.Family, table.Name, err)
			}
			// Declare before deleting so restore works on both empty and live
			// rulesets without duplicating rules or flushing unrelated tables.
			content += fmt.Sprintf("table %s %s\ndelete table %s %s\n%s\n", table.Family, table.Name, table.Family, table.Name, rules)
		}
		snapshots = append(snapshots, snapshot{"/etc/nftables.d/" + m.tableName + ".nft", content})
	}
	for _, family := range []struct{ tool, path string }{{"iptables", layout.ipv4}, {"ip6tables", layout.ipv6}} {
		if !tools[family.tool] {
			continue
		}
		content, err := m.sshClient.ExecuteWithTimeout(family.tool+"-save", 20*time.Second)
		if err != nil {
			return fmt.Errorf("读取%s持久化规则失败: %w", family.tool, err)
		}
		snapshots = append(snapshots, snapshot{family.path, content})
	}
	// Gather every snapshot first. A failed IPv6 read must not truncate an
	// existing IPv4 file (or vice versa).
	for _, snapshot := range snapshots {
		if err := m.persistRulesFile(snapshot.path, snapshot.content); err != nil {
			return err
		}
	}
	if hasManagedNftTables {
		command := "set -e\nocv_nft_config=" + shellQuote(layout.nftConfig) + `
mkdir -p -- "$(dirname -- "$ocv_nft_config")"
if [ ! -e "$ocv_nft_config" ]; then
    printf '%s\n' 'include "/etc/nftables.d/*.nft"' > "$ocv_nft_config"
elif [ ! -r "$ocv_nft_config" ]; then
    exit 1
elif ! grep -Eq '^[[:space:]]*include[[:space:]]+"/etc/nftables\.d/\*\.nft"' "$ocv_nft_config"; then
    printf '\n%s\n' 'include "/etc/nftables.d/*.nft"' >> "$ocv_nft_config"
fi`
		if _, err := m.sshClient.ExecuteWithTimeout(command, 20*time.Second); err != nil {
			return fmt.Errorf("配置nft持久化入口失败: %w", err)
		}
	}
	// Preserve the existing distro service integration. Missing services are
	// optional (e.g. OpenRC/minimal hosts); they do not invalidate snapshots.
	commands := []string{}
	if hasManagedNftTables {
		commands = append(commands, "if command -v systemctl >/dev/null 2>&1; then systemctl enable nftables; elif command -v rc-update >/dev/null 2>&1; then rc-update add nftables default; fi")
	}
	if tools["iptables"] || tools["ip6tables"] {
		// Snapshots above already contain both available families. A second
		// global save can truncate those atomic files or overwrite an unavailable
		// family's previous policy. Only enable existing restoration services.
		for _, service := range layout.services {
			if service == "iptables" && !tools["iptables"] || service == "ip6tables" && !tools["ip6tables"] {
				continue
			}
			commands = append(commands, "if command -v systemctl >/dev/null 2>&1; then systemctl enable "+service+
				"; elif command -v rc-update >/dev/null 2>&1 && [ -x /etc/init.d/"+service+" ]; then rc-update add "+service+
				" default; elif command -v chkconfig >/dev/null 2>&1 && [ -x /etc/init.d/"+service+" ]; then chkconfig "+service+" on; fi")
		}
	}
	for _, command := range commands {
		if _, err := m.sshClient.ExecuteWithTimeout(command, 30*time.Second); err != nil && global.APP_LOG != nil {
			global.APP_LOG.Warn("防火墙规则已保存，但系统服务持久化钩子失败", zap.Error(err))
		}
	}
	return nil
}

func (m *Manager) persistRulesFile(path, content string) error {
	command := "set -e\numask 077\nocv_rule_path=" + shellQuote(path) + `
if [ -L "$ocv_rule_path" ]; then
    ocv_rule_path=$(readlink -f -- "$ocv_rule_path")
    [ -f "$ocv_rule_path" ] || exit 1
fi
[ ! -e "$ocv_rule_path" ] || [ -f "$ocv_rule_path" ] || exit 1
mkdir -p -- "$(dirname -- "$ocv_rule_path")"
ocv_rule_tmp=$(mktemp "${ocv_rule_path}.tmp.XXXXXX")
` +
		"trap 'rm -f -- \"$ocv_rule_tmp\"' EXIT\n" +
		"if [ -f \"$ocv_rule_path\" ]; then cp -p -- \"$ocv_rule_path\" \"$ocv_rule_tmp\"; fi\n" +
		"printf '%s' " + shellQuote(content) + " > \"$ocv_rule_tmp\"\n" +
		"mv -f -- \"$ocv_rule_tmp\" \"$ocv_rule_path\""
	if _, err := m.sshClient.ExecuteWithTimeout(command, 20*time.Second); err != nil {
		return fmt.Errorf("保存防火墙文件 %s 失败: %w", path, err)
	}
	return nil
}

// Explicit presence markers distinguish a missing optional binary from a
// failed SSH/Agent request. Missing optional tools must not hide read errors.
func (m *Manager) availableFirewallTools() (map[string]bool, error) {
	output, err := m.sshClient.ExecuteWithTimeout(`for tool in nft iptables ip6tables; do
    if command -v "$tool" >/dev/null 2>&1; then printf '%s\n' "$tool"; fi
done
printf '%s\n' complete`, 20*time.Second)
	if err != nil {
		return nil, fmt.Errorf("检测防火墙工具失败: %w", err)
	}
	available := make(map[string]bool)
	for _, name := range strings.Fields(output) {
		available[name] = true
	}
	if !available["complete"] {
		return nil, fmt.Errorf("防火墙工具检测响应不完整")
	}
	if (m.backend == BackendNft && !available["nft"]) || (m.backend == BackendIptables && !available["iptables"]) {
		return nil, fmt.Errorf("配置的防火墙后端 %s 不可用", m.backend)
	}
	return available, nil
}
