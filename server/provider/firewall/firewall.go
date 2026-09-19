package firewall

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// Backend 防火墙后端类型
type Backend string

const (
	BackendNft      Backend = "nft"
	BackendIptables Backend = "iptables"
)

// Manager 防火墙管理器，封装 nft-first + iptables-fallback 双后端
type Manager struct {
	sshClient *utils.SafeShellExecutor // 永不为nil，所有方法安全调用
	backend   Backend
	tableName string // nft table 名称，如 "qemu" "kubevirt" "docker"
	subnet    string // 内网网段，如 "192.168.122.0/24"
	detected  bool
	initMu    sync.Mutex
	// Cache belongs to this manager, never a recyclable address in a global
	// map. Check remote chains before reuse because a daemon/host can restart.
	nftInitialized bool
}

var managedCommentName = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,128}$`)

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// NewManager 创建防火墙管理器
// tableName: nft table 名称（如 "qemu"）
// subnet: 需要做 NAT 的内网网段（如 "192.168.122.0/24"），为空则跳过基础 NAT 规则
func NewManager(sshClient utils.ShellExecutor, tableName, subnet string) *Manager {
	return &Manager{
		sshClient: utils.NewSafeShellExecutor(sshClient),
		tableName: tableName,
		subnet:    subnet,
	}
}

// DetectBackend 检测防火墙后端，优先读取标记文件，再自动探测
// markerFile: 后端标记文件路径，如 "/usr/local/bin/qemu_fw_backend"，为空则自动探测
func (m *Manager) DetectBackend(markerFile string) (Backend, error) {
	if m.detected {
		return m.backend, nil
	}

	// 1. 从标记文件读取
	if markerFile != "" {
		output, err := m.sshClient.Execute(fmt.Sprintf("cat %s 2>/dev/null", utils.ShellSingleQuote(markerFile)))
		if err == nil {
			b := strings.TrimSpace(output)
			if b == "nft" || b == "iptables" {
				m.backend = Backend(b)
				m.detected = true
				return m.backend, nil
			}
		}
	}

	// 2. 自动探测
	_, err := m.sshClient.Execute("command -v nft >/dev/null 2>&1")
	if err == nil {
		m.backend = BackendNft
		m.detected = true
		return m.backend, nil
	}

	_, err = m.sshClient.Execute("command -v iptables >/dev/null 2>&1")
	if err == nil {
		m.backend = BackendIptables
		m.detected = true
		return m.backend, nil
	}

	return "", fmt.Errorf("no firewall tool available (nft or iptables)")
}

// GetBackend 返回已检测到的后端
func (m *Manager) GetBackend() Backend {
	return m.backend
}

// InitTable 初始化 nft table / iptables 基础规则
func (m *Manager) InitTable() error {
	m.initMu.Lock()
	defer m.initMu.Unlock()
	if !m.detected {
		return fmt.Errorf("firewall backend not detected, call DetectBackend first")
	}
	if !managedTableName.MatchString(m.tableName) {
		return fmt.Errorf("invalid firewall table name %q", m.tableName)
	}
	if m.subnet != "" {
		parsed, _, err := net.ParseCIDR(strings.TrimSpace(m.subnet))
		if err != nil || parsed.To4() == nil {
			return fmt.Errorf("invalid IPv4 firewall subnet %q", m.subnet)
		}
		m.subnet = strings.TrimSpace(m.subnet)
	}

	if m.backend == BackendIptables {
		return m.initIptablesBase()
	}

	if m.nftInitialized {
		if output, err := m.sshClient.Execute(m.nftChainProbe()); err == nil && strings.TrimSpace(output) == "ok" {
			return nil
		}
		m.nftInitialized = false
	}

	if err := m.initNftTable(); err != nil {
		return err
	}
	m.nftInitialized = true
	return nil
}

func (m *Manager) nftChainProbe() string {
	return fmt.Sprintf("nft list chain ip %s prerouting >/dev/null 2>&1 && nft list chain ip %s postrouting >/dev/null 2>&1 && nft list chain ip %s forward >/dev/null 2>&1 && echo 'ok'", m.tableName, m.tableName, m.tableName)
}

func (m *Manager) initNftTable() error {
	cmds := []string{
		fmt.Sprintf("nft add table ip %s 2>/dev/null || true", m.tableName),
		fmt.Sprintf("nft 'add chain ip %s prerouting { type nat hook prerouting priority dstnat; policy accept; }' 2>/dev/null || true", m.tableName),
		fmt.Sprintf("nft 'add chain ip %s postrouting { type nat hook postrouting priority srcnat; policy accept; }' 2>/dev/null || true", m.tableName),
		fmt.Sprintf("nft 'add chain ip %s forward { type filter hook forward priority 0; policy accept; }' 2>/dev/null || true", m.tableName),
	}

	for _, cmd := range cmds {
		if _, err := m.sshClient.Execute(cmd); err != nil && global.APP_LOG != nil {
			global.APP_LOG.Warn("nft init command failed", zap.String("cmd", utils.TruncateString(cmd, 200)), zap.Error(err))
		}
	}

	// All chains are required; a partially initialized table is not ready.
	verifyCmd := m.nftChainProbe()
	verifyOutput, verifyErr := m.sshClient.Execute(verifyCmd)
	if verifyErr != nil || strings.TrimSpace(verifyOutput) != "ok" {
		return fmt.Errorf("nft chain verification failed for table %s: init commands may have silently failed (check nft/kernel support)", m.tableName)
	}

	// 添加基础 NAT/FORWARD 规则（仅在 subnet 非空时）
	if m.subnet != "" {
		baseCmds := []string{
			// MASQUERADE: 内网出外网
			fmt.Sprintf("nft list chain ip %s postrouting 2>/dev/null | grep -q masquerade || nft add rule ip %s postrouting ip saddr %s ip daddr != %s masquerade 2>/dev/null || true",
				m.tableName, m.tableName, m.subnet, m.subnet),
			// conntrack
			fmt.Sprintf("nft list chain ip %s forward 2>/dev/null | grep -q 'ct state' || nft add rule ip %s forward ct state established,related accept 2>/dev/null || true",
				m.tableName, m.tableName),
			// 目标子网转发
			fmt.Sprintf("nft list chain ip %s forward 2>/dev/null | grep -q 'ip daddr %s' || nft add rule ip %s forward ip daddr %s accept 2>/dev/null || true",
				m.tableName, m.subnet, m.tableName, m.subnet),
			// 源子网转发
			fmt.Sprintf("nft list chain ip %s forward 2>/dev/null | grep -q 'ip saddr %s' || nft add rule ip %s forward ip saddr %s accept 2>/dev/null || true",
				m.tableName, m.subnet, m.tableName, m.subnet),
		}
		for _, cmd := range baseCmds {
			if _, err := m.sshClient.Execute(cmd); err != nil && global.APP_LOG != nil {
				global.APP_LOG.Warn("nft base rule failed", zap.String("cmd", utils.TruncateString(cmd, 200)), zap.Error(err))
			}
		}
	}

	return nil
}

func (m *Manager) initIptablesBase() error {
	if m.subnet == "" {
		return nil
	}

	cmds := []string{
		// MASQUERADE
		fmt.Sprintf("iptables -t nat -C POSTROUTING -s %s ! -d %s -j MASQUERADE 2>/dev/null || iptables -t nat -I POSTROUTING -s %s ! -d %s -j MASQUERADE 2>/dev/null || true",
			m.subnet, m.subnet, m.subnet, m.subnet),
		// conntrack FORWARD
		"iptables -C FORWARD -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null || iptables -I FORWARD -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null || true",
		// dest subnet FORWARD
		fmt.Sprintf("iptables -C FORWARD -d %s -j ACCEPT 2>/dev/null || iptables -I FORWARD -d %s -j ACCEPT 2>/dev/null || true",
			m.subnet, m.subnet),
		// source subnet FORWARD
		fmt.Sprintf("iptables -C FORWARD -s %s -j ACCEPT 2>/dev/null || iptables -I FORWARD -s %s -j ACCEPT 2>/dev/null || true",
			m.subnet, m.subnet),
	}

	for _, cmd := range cmds {
		if _, err := m.sshClient.Execute(cmd); err != nil {
			global.APP_LOG.Warn("iptables init command failed", zap.String("cmd", utils.TruncateString(cmd, 200)), zap.Error(err))
		}
	}

	return nil
}

// AddDNAT 添加 DNAT 转发规则（SSH 端口和端口范围）
// vmName: VM 名称（用于 nft comment）
// vmIP: VM 内网 IP
// sshPort: SSH 映射的宿主机端口 → 转到 vmIP:22
// startPort, endPort: 端口范围映射（宿主机端口 identity 转发到 vmIP 对应端口）
func (m *Manager) AddDNAT(vmName, vmIP string, sshPort, startPort, endPort int) error {
	if !managedTableName.MatchString(m.tableName) {
		return fmt.Errorf("invalid firewall table name %q", m.tableName)
	}
	address := net.ParseIP(normalizeFirewallIP(vmIP))
	if address == nil || address.To4() == nil {
		return fmt.Errorf("invalid IPv4 DNAT target %q", vmIP)
	}
	if !managedCommentName.MatchString(vmName) {
		return fmt.Errorf("invalid DNAT instance name %q", vmName)
	}
	if sshPort < 1 || sshPort > 65535 {
		return fmt.Errorf("invalid SSH port %d", sshPort)
	}
	if (startPort == 0) != (endPort == 0) || startPort < 0 || endPort < 0 || startPort > 65535 || endPort > 65535 || (startPort > 0 && startPort > endPort) {
		return fmt.Errorf("invalid DNAT port range %d-%d", startPort, endPort)
	}
	vmIP = address.To4().String()
	if m.backend == BackendNft {
		return m.addDNATNft(vmName, vmIP, sshPort, startPort, endPort)
	}
	return m.addDNATIptables(vmName, vmIP, sshPort, startPort, endPort)
}

func (m *Manager) addDNATNft(vmName, vmIP string, sshPort, startPort, endPort int) error {
	// 使用单引号包裹整个nft表达式，确保双引号的comment值不被SSH shell解析
	cmds := []string{
		// SSH DNAT tcp + udp
		fmt.Sprintf("nft 'add rule ip %s prerouting tcp dport %d dnat to %s:22 comment \"vm:%s\"'",
			m.tableName, sshPort, vmIP, vmName),
		fmt.Sprintf("nft 'add rule ip %s prerouting udp dport %d dnat to %s:22 comment \"vm:%s\"'",
			m.tableName, sshPort, vmIP, vmName),
	}

	// 端口范围 DNAT（identity mapping）
	if startPort > 0 && endPort > 0 && startPort <= endPort {
		cmds = append(cmds,
			fmt.Sprintf("nft 'add rule ip %s prerouting tcp dport %d-%d dnat to %s comment \"vm:%s\"'",
				m.tableName, startPort, endPort, vmIP, vmName),
			fmt.Sprintf("nft 'add rule ip %s prerouting udp dport %d-%d dnat to %s comment \"vm:%s\"'",
				m.tableName, startPort, endPort, vmIP, vmName),
		)
	}

	for _, cmd := range cmds {
		if _, err := m.sshClient.Execute(cmd); err != nil {
			global.APP_LOG.Error("nft DNAT rule failed", zap.String("cmd", utils.TruncateString(cmd, 200)), zap.Error(err))
			return fmt.Errorf("nft DNAT failed: %w", err)
		}
	}
	return nil
}

func (m *Manager) addDNATIptables(vmName, vmIP string, sshPort, startPort, endPort int) error {
	owner := shellQuote("vm:" + vmName)
	cmds := []string{
		// SSH DNAT tcp + udp
		fmt.Sprintf("iptables -t nat -I PREROUTING -p tcp --dport %d -m comment --comment %s -j DNAT --to %s:22", sshPort, owner, vmIP),
		fmt.Sprintf("iptables -t nat -I PREROUTING -p udp --dport %d -m comment --comment %s -j DNAT --to %s:22", sshPort, owner, vmIP),
	}

	// 端口范围
	if startPort > 0 && endPort > 0 && startPort <= endPort {
		for port := startPort; port <= endPort; port++ {
			cmds = append(cmds,
				fmt.Sprintf("iptables -t nat -I PREROUTING -p tcp --dport %d -m comment --comment %s -j DNAT --to %s:%d", port, owner, vmIP, port),
				fmt.Sprintf("iptables -t nat -I PREROUTING -p udp --dport %d -m comment --comment %s -j DNAT --to %s:%d", port, owner, vmIP, port),
			)
		}
	}

	for _, cmd := range cmds {
		if _, err := m.sshClient.Execute(cmd); err != nil {
			global.APP_LOG.Error("iptables DNAT rule failed", zap.String("cmd", utils.TruncateString(cmd, 200)), zap.Error(err))
			return fmt.Errorf("iptables DNAT failed: %w", err)
		}
	}
	return nil
}

// AddSingleDNAT 添加单个端口映射（用于 portmapping 层的 CRUD 操作）
// hostPort → instanceIP:guestPort, protocol = "tcp"/"udp"/"both"
// comment: nft comment（如 "vm:xxx" 或 "inst:xxx"），为空则不添加 comment
func (m *Manager) AddSingleDNAT(instanceIP string, hostPort, guestPort int, protocol, comment string) error {
	if !managedTableName.MatchString(m.tableName) {
		return fmt.Errorf("invalid firewall table name %q", m.tableName)
	}
	address := net.ParseIP(normalizeFirewallIP(instanceIP))
	if address == nil {
		return fmt.Errorf("无效的端口映射目标地址 %q", instanceIP)
	}
	if hostPort < 1 || hostPort > 65535 || guestPort < 1 || guestPort > 65535 {
		return fmt.Errorf("无效的端口映射范围")
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol != "" && protocol != "tcp" && protocol != "udp" && protocol != "both" {
		return fmt.Errorf("无效的端口映射协议 %q", protocol)
	}
	instanceIP = address.String()
	if isIPv6Address(instanceIP) {
		return m.addSingleDNATIPv6(instanceIP, hostPort, guestPort, protocol, comment)
	}
	protocols := expandProtocol(protocol)
	commentArgs := ""
	if strings.TrimSpace(comment) != "" {
		commentArgs = fmt.Sprintf(" -m comment --comment %s", shellQuote(comment))
	}

	if m.backend == BackendNft {
		return m.addSingleDNATNft(instanceIP, hostPort, guestPort, protocol, comment, false)
	}
	for _, proto := range protocols {
		cmd := fmt.Sprintf("iptables -t nat -A PREROUTING -p %s --dport %d%s -j DNAT --to-destination %s:%d",
			proto, hostPort, commentArgs, instanceIP, guestPort)
		if _, err := m.sshClient.Execute(cmd); err != nil {
			return fmt.Errorf("iptables add DNAT failed: %w", err)
		}
		// FORWARD
		fwd := fmt.Sprintf("iptables -A FORWARD -p %s -d %s --dport %d%s -j ACCEPT",
			proto, instanceIP, guestPort, commentArgs)
		if _, err := m.sshClient.Execute(fwd); err != nil {
			global.APP_LOG.Warn("iptables FORWARD failed", zap.Error(err))
		}
		// MASQUERADE
		masq := fmt.Sprintf("iptables -t nat -A POSTROUTING -p %s -s %s --sport %d%s -j MASQUERADE",
			proto, instanceIP, guestPort, commentArgs)
		if _, err := m.sshClient.Execute(masq); err != nil {
			global.APP_LOG.Warn("iptables MASQUERADE failed", zap.Error(err))
		}
	}
	return nil
}

// RemoveSingleDNAT deletes a mapping in the target address family. Call
// RemoveSingleDNATForFamily when a failed create has no saved target address.
func (m *Manager) RemoveSingleDNAT(instanceIP string, hostPort, guestPort int, protocol, comment string) error {
	return m.RemoveSingleDNATForFamily(instanceIP, hostPort, guestPort, protocol, comment, isIPv6Address(instanceIP))
}

func normalizeFirewallIP(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "[]")
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		value = value[:slash]
	}
	return strings.TrimSpace(value)
}

func isIPv6Address(value string) bool {
	parsed := net.ParseIP(normalizeFirewallIP(value))
	return parsed != nil && parsed.To4() == nil
}

func ip6Target(value string, port int) string {
	return fmt.Sprintf("[%s]:%d", normalizeFirewallIP(value), port)
}

// addSingleDNATIPv6 uses ip6tables for IPv6 targets even when the selected
// write backend is nftables.  This keeps the existing IPv4 nft table intact
// and works on both legacy iptables and iptables-nft installations, while
// avoiding accidental IPv4 `table ip` rules for an IPv6 guest.
func (m *Manager) addSingleDNATIPv6(instanceIP string, hostPort, guestPort int, protocol, comment string) error {
	if !isIPv6Address(instanceIP) {
		return fmt.Errorf("invalid IPv6 target %q", instanceIP)
	}
	// nft-only hosts (common on recent minimal distributions) may not ship the
	// ip6tables compatibility binary. Keep IPv6 mappings usable by using a
	// dedicated ip6 nft table when that capability is absent.
	if _, err := m.sshClient.Execute("command -v ip6tables >/dev/null 2>&1"); err != nil {
		return m.addSingleDNATIPv6Nft(instanceIP, hostPort, guestPort, protocol, comment)
	}
	commentArgs := ""
	if strings.TrimSpace(comment) != "" {
		commentArgs = fmt.Sprintf(" -m comment --comment %s", shellQuote(comment))
	}
	for _, proto := range expandProtocol(protocol) {
		commands := []string{
			fmt.Sprintf("ip6tables -t nat -A PREROUTING -p %s --dport %d%s -j DNAT --to-destination %s", proto, hostPort, commentArgs, ip6Target(instanceIP, guestPort)),
			fmt.Sprintf("ip6tables -A FORWARD -p %s -d %s --dport %d%s -j ACCEPT", proto, normalizeFirewallIP(instanceIP), guestPort, commentArgs),
			fmt.Sprintf("ip6tables -t nat -A POSTROUTING -p %s -s %s --sport %d%s -j MASQUERADE", proto, normalizeFirewallIP(instanceIP), guestPort, commentArgs),
		}
		for _, command := range commands {
			if _, err := m.sshClient.Execute(command); err != nil {
				return fmt.Errorf("ip6tables add DNAT failed: %w", err)
			}
		}
	}
	return nil
}

func (m *Manager) ipv6NftTable() string {
	name := strings.TrimSpace(m.tableName)
	if name == "" {
		name = "portmap"
	}
	return name + "6"
}

func (m *Manager) ensureIPv6NftTable() error {
	table := m.ipv6NftTable()
	commands := []string{
		fmt.Sprintf("nft 'add table ip6 %s' 2>/dev/null || true", table),
		fmt.Sprintf("nft 'add chain ip6 %s prerouting { type nat hook prerouting priority dstnat; policy accept; }' 2>/dev/null || true", table),
		fmt.Sprintf("nft 'add chain ip6 %s postrouting { type nat hook postrouting priority srcnat; policy accept; }' 2>/dev/null || true", table),
		fmt.Sprintf("nft 'add chain ip6 %s forward { type filter hook forward priority 0; policy accept; }' 2>/dev/null || true", table),
	}
	for _, command := range commands {
		if _, err := m.sshClient.Execute(command); err != nil {
			return fmt.Errorf("初始化IPv6 nft表失败: %w", err)
		}
	}
	verify := fmt.Sprintf("nft list chain ip6 %s prerouting >/dev/null 2>&1 && echo ok", table)
	output, err := m.sshClient.Execute(verify)
	if err != nil || strings.TrimSpace(output) != "ok" {
		return fmt.Errorf("IPv6 nft prerouting链不可用")
	}
	return nil
}

func (m *Manager) addSingleDNATIPv6Nft(instanceIP string, hostPort, guestPort int, protocol, comment string) error {
	if _, err := m.sshClient.Execute("command -v nft >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("IPv6映射需要ip6tables或nft")
	}
	if err := m.ensureIPv6NftTable(); err != nil {
		return err
	}
	return m.addSingleDNATNft(instanceIP, hostPort, guestPort, protocol, comment, true)
}

// DeleteRulesByComment removes an exact owner in both address families.
// Individual port updates must use the family-specific cleanup methods.
func (m *Manager) DeleteRulesByComment(comment string) error {
	if err := m.DeleteRulesByCommentForFamily(comment, false); err != nil {
		return err
	}
	// IPv4-only legacy installations need not provide an IPv6 rules tool.
	output, err := m.sshClient.Execute("if command -v nft >/dev/null 2>&1 || command -v ip6tables >/dev/null 2>&1; then echo present; else echo absent; fi")
	if err != nil {
		return fmt.Errorf("检测IPv6防火墙工具失败: %w", err)
	}
	if strings.TrimSpace(output) == "absent" {
		return nil
	}
	if strings.TrimSpace(output) != "present" {
		return fmt.Errorf("无效的IPv6防火墙工具检测响应")
	}
	return m.DeleteRulesByCommentForFamily(comment, true)
}

// DeleteRulesByIP deletes only rules referring to the exact guest address.
// Subnets, negated matches and textual address prefixes are not ownership.
func (m *Manager) DeleteRulesByIP(address string) error {
	if strings.TrimSpace(address) == "" {
		return nil
	}
	ip := net.ParseIP(normalizeFirewallIP(address))
	if ip == nil {
		return fmt.Errorf("无效的实例地址 %q", address)
	}
	ipv6 := ip.To4() == nil
	rules, err := m.readCleanupRules(ipv6)
	if err != nil {
		return err
	}
	return m.removeSelectedRules(ipv6, func(rule cleanupRule) bool {
		if !rule.legacySafe {
			return false
		}
		switch rule.action {
		case "DNAT":
			return rule.dnatIP == ip.String()
		case "ACCEPT":
			return rule.destination == ip.String()
		case "MASQUERADE":
			return rule.source == ip.String()
		}
		return false
	}, rules)
}

// DiscoverDNATRules 发现指向指定 IP 的所有 DNAT 规则，返回 (hostPort, guestPort, protocol, isSSH) 列表
func (m *Manager) DiscoverDNATRules(vmIP string) []DiscoveredRule {
	nftOutput, iptablesOutput := m.readDNATRuleset()
	if ip := normalizeRuleIP(vmIP); ip != "" {
		return ParseDNATRules(nftOutput, iptablesOutput)[ip]
	}
	return ParseDNATRulesForIdentifier(nftOutput, iptablesOutput, vmIP)
}

// DiscoveredRule 发现的 DNAT 规则
type DiscoveredRule struct {
	TargetIP  string
	HostPort  int
	GuestPort int
	Protocol  string
	IsSSH     bool
}

// DiscoverAllDNATRules scans both nftables and iptables instead of trusting the
// selected write backend. Hosts commonly have nft installed while legacy or
// Docker/PVE rules still live behind iptables-nft, and third-party rules may be
// stored in a table other than the one managed by OneClickVirt.
func (m *Manager) DiscoverAllDNATRules() map[string][]DiscoveredRule {
	nftOutput, iptablesOutput := m.readDNATRuleset()
	return ParseDNATRules(nftOutput, iptablesOutput)
}

func (m *Manager) readDNATRuleset() (string, string) {
	nftOutput, _ := m.sshClient.Execute("nft -a list ruleset 2>/dev/null || true")
	iptablesOutput, _ := m.sshClient.Execute("iptables-save -t nat 2>/dev/null || iptables -t nat -S 2>/dev/null || iptables -t nat -L PREROUTING -n 2>/dev/null || true")
	return nftOutput, iptablesOutput
}

const maxDiscoveredPortRange = 4096

var (
	nftDNATTargetPattern = regexp.MustCompile(`(?i)\bdnat(?:\s+ip)?\s+to\s+([0-9]+(?:\.[0-9]+){3})(?::([0-9]+)(?:-([0-9]+))?)?`)
	nftDPortPattern      = regexp.MustCompile(`(?i)\bdport\s+(?:\{\s*)?([0-9][0-9,\-\s]*)(?:\})?`)
	iptDNATTargetPattern = regexp.MustCompile(`(?i)(?:--to-destination|--to)\s+([0-9]+(?:\.[0-9]+){3})(?::([0-9]+)(?:-([0-9]+))?)?`)
	iptListTargetPattern = regexp.MustCompile(`(?i)\bto:([0-9]+(?:\.[0-9]+){3})(?::([0-9]+)(?:-([0-9]+))?)?`)
	iptDPortPattern      = regexp.MustCompile(`(?i)--dports?\s+([0-9][0-9,:-]*)`)
	iptListDPortPattern  = regexp.MustCompile(`(?i)\bdpt:([0-9]+(?:[:-][0-9]+)?)`)
	protocolPattern      = regexp.MustCompile(`(?i)(?:^|\s)(?:-p\s+)?(tcp|udp)(?:\s|$)`)
	ruleCommentPattern   = regexp.MustCompile(`(?i)(?:\bcomment\s+|--comment\s+)(?:"([^"]*)"|'([^']*)'|([^\s#]+))`)
)

// ParseDNATRulesForIdentifier recovers rules associated with an instance
// comment when the caller has no guest IP yet. This keeps KubeVirt and legacy
// nft rules discoverable without reverting to substring grep matching.
func ParseDNATRulesForIdentifier(nftOutput, iptablesOutput, identifier string) []DiscoveredRule {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil
	}
	filter := func(output string) string {
		matched := make([]string, 0)
		for _, line := range strings.Split(output, "\n") {
			if ruleCommentMatchesIdentifier(line, identifier) {
				matched = append(matched, line)
			}
		}
		return strings.Join(matched, "\n")
	}
	rulesByIP := ParseDNATRules(filter(nftOutput), filter(iptablesOutput))
	rules := make([]DiscoveredRule, 0)
	for _, ipRules := range rulesByIP {
		rules = append(rules, ipRules...)
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].HostPort != rules[j].HostPort {
			return rules[i].HostPort < rules[j].HostPort
		}
		if rules[i].GuestPort != rules[j].GuestPort {
			return rules[i].GuestPort < rules[j].GuestPort
		}
		if rules[i].Protocol != rules[j].Protocol {
			return rules[i].Protocol < rules[j].Protocol
		}
		return rules[i].TargetIP < rules[j].TargetIP
	})
	return rules
}

func ruleCommentMatchesIdentifier(line, identifier string) bool {
	for _, match := range ruleCommentPattern.FindAllStringSubmatch(line, -1) {
		comment := ""
		for index := 1; index < len(match); index++ {
			if match[index] != "" {
				comment = match[index]
				break
			}
		}
		if comment == identifier || comment == "vm:"+identifier || comment == "inst:"+identifier || strings.HasPrefix(comment, "pm:"+identifier+":") {
			return true
		}
	}
	return false
}

// ParseDNATRules parses nft list-ruleset and iptables-save/-S/-L output. It is
// intentionally table/chain-name agnostic so imported instances also recover
// mappings created by older versions or external tooling.
func ParseDNATRules(nftOutput, iptablesOutput string) map[string][]DiscoveredRule {
	result := make(map[string][]DiscoveredRule)
	seen := make(map[string]struct{})
	appendRules := func(rules []DiscoveredRule) {
		for _, rule := range rules {
			rule.TargetIP = normalizeRuleIP(rule.TargetIP)
			if rule.TargetIP == "" || rule.HostPort <= 0 || rule.HostPort > 65535 || rule.GuestPort <= 0 || rule.GuestPort > 65535 {
				continue
			}
			if rule.Protocol != "udp" {
				rule.Protocol = "tcp"
			}
			rule.IsSSH = rule.GuestPort == 22
			key := fmt.Sprintf("%s\x00%d\x00%d\x00%s", rule.TargetIP, rule.HostPort, rule.GuestPort, rule.Protocol)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result[rule.TargetIP] = append(result[rule.TargetIP], rule)
		}
	}

	for _, line := range strings.Split(nftOutput, "\n") {
		appendRules(parseNftDNATRulesLine(strings.TrimSpace(line)))
	}
	for _, line := range strings.Split(iptablesOutput, "\n") {
		appendRules(parseIptablesDNATRulesLine(strings.TrimSpace(line)))
	}
	for ip := range result {
		sort.Slice(result[ip], func(i, j int) bool {
			if result[ip][i].HostPort != result[ip][j].HostPort {
				return result[ip][i].HostPort < result[ip][j].HostPort
			}
			if result[ip][i].GuestPort != result[ip][j].GuestPort {
				return result[ip][i].GuestPort < result[ip][j].GuestPort
			}
			return result[ip][i].Protocol < result[ip][j].Protocol
		})
	}
	return result
}

func parseNftDNATRulesLine(line string) []DiscoveredRule {
	if line == "" || !strings.Contains(strings.ToLower(line), "dnat") {
		return nil
	}
	target := nftDNATTargetPattern.FindStringSubmatch(line)
	if len(target) == 0 {
		return nil
	}
	dnatIndex := strings.Index(strings.ToLower(line), "dnat")
	portMatch := nftDPortPattern.FindStringSubmatch(line[:dnatIndex])
	if len(portMatch) == 0 {
		return nil
	}
	hostPorts := expandPortSpec(portMatch[1])
	return buildDiscoveredRules(target[1], hostPorts, target[2], target[3], discoverProtocol(line))
}

func parseIptablesDNATRulesLine(line string) []DiscoveredRule {
	if line == "" || !strings.Contains(strings.ToUpper(line), "DNAT") {
		return nil
	}
	target := iptDNATTargetPattern.FindStringSubmatch(line)
	if len(target) == 0 {
		target = iptListTargetPattern.FindStringSubmatch(line)
	}
	if len(target) == 0 {
		return nil
	}
	portMatch := iptDPortPattern.FindStringSubmatch(line)
	if len(portMatch) == 0 {
		portMatch = iptListDPortPattern.FindStringSubmatch(line)
	}
	if len(portMatch) == 0 {
		return nil
	}
	hostPorts := expandPortSpec(strings.ReplaceAll(portMatch[1], ":", "-"))
	return buildDiscoveredRules(target[1], hostPorts, target[2], target[3], discoverProtocol(line))
}

func buildDiscoveredRules(targetIP string, hostPorts []int, guestStartText, guestEndText, protocol string) []DiscoveredRule {
	if len(hostPorts) == 0 {
		return nil
	}
	guestStart, _ := strconv.Atoi(guestStartText)
	guestEnd, _ := strconv.Atoi(guestEndText)
	rules := make([]DiscoveredRule, 0, len(hostPorts))
	for index, hostPort := range hostPorts {
		guestPort := guestStart
		switch {
		case guestStart == 0:
			guestPort = hostPort
		case guestEnd >= guestStart && len(hostPorts) == guestEnd-guestStart+1:
			guestPort = guestStart + index
		}
		rules = append(rules, DiscoveredRule{TargetIP: targetIP, HostPort: hostPort, GuestPort: guestPort, Protocol: protocol})
	}
	return rules
}

func expandPortSpec(spec string) []int {
	spec = strings.NewReplacer("{", "", "}", "", " ", "", "\t", "").Replace(spec)
	if spec == "" {
		return nil
	}
	ports := make([]int, 0)
	seen := make(map[int]struct{})
	for _, item := range strings.Split(spec, ",") {
		if item == "" {
			continue
		}
		startText, endText, ranged := strings.Cut(item, "-")
		start, err := strconv.Atoi(startText)
		if err != nil || start <= 0 || start > 65535 {
			continue
		}
		end := start
		if ranged {
			parsedEnd, parseErr := strconv.Atoi(endText)
			if parseErr != nil || parsedEnd < start || parsedEnd > 65535 || parsedEnd-start+1 > maxDiscoveredPortRange {
				continue
			}
			end = parsedEnd
		}
		for port := start; port <= end && len(ports) < maxDiscoveredPortRange; port++ {
			if _, exists := seen[port]; exists {
				continue
			}
			seen[port] = struct{}{}
			ports = append(ports, port)
		}
	}
	return ports
}

func discoverProtocol(line string) string {
	match := protocolPattern.FindStringSubmatch(line)
	if len(match) > 1 && strings.EqualFold(match[1], "udp") {
		return "udp"
	}
	return "tcp"
}

func normalizeRuleIP(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "[]"))
	if ip := net.ParseIP(value); ip != nil && ip.To4() != nil {
		return ip.String()
	}
	return ""
}

// --- helpers ---

func expandProtocol(protocol string) []string {
	switch strings.ToLower(protocol) {
	case "both", "":
		return []string{"tcp", "udp"}
	case "tcp":
		return []string{"tcp"}
	case "udp":
		return []string{"udp"}
	default:
		return []string{protocol}
	}
}

func parseHandles(output string) []string {
	var handles []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		h := strings.TrimSpace(line)
		if h != "" {
			handles = append(handles, h)
		}
	}
	return handles
}
