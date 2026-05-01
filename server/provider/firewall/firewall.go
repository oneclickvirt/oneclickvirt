package firewall

import (
	"fmt"
	"strings"
	"sync"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// tableInitCache prevents redundant SSH round-trips for InitTable on the same
// (sshClient, tableName) pair. The table persists on the remote host for the
// lifetime of the process, so one successful InitTable per connection is enough.
// Key: fmt.Sprintf("%p:%s", sshClient, tableName), Value: struct{}{}
var tableInitCache sync.Map

// Backend 防火墙后端类型
type Backend string

const (
	BackendNft      Backend = "nft"
	BackendIptables Backend = "iptables"
)

// Manager 防火墙管理器，封装 nft-first + iptables-fallback 双后端
type Manager struct {
	sshClient *utils.SSHClient
	backend   Backend
	tableName string // nft table 名称，如 "qemu" "kubevirt" "docker"
	subnet    string // 内网网段，如 "192.168.122.0/24"
	detected  bool
}

// NewManager 创建防火墙管理器
// tableName: nft table 名称（如 "qemu"）
// subnet: 需要做 NAT 的内网网段（如 "192.168.122.0/24"），为空则跳过基础 NAT 规则
func NewManager(sshClient *utils.SSHClient, tableName, subnet string) *Manager {
	return &Manager{
		sshClient: sshClient,
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
		output, err := m.sshClient.Execute(fmt.Sprintf("cat '%s' 2>/dev/null", markerFile))
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
	if !m.detected {
		return fmt.Errorf("firewall backend not detected, call DetectBackend first")
	}

	if m.backend == BackendIptables {
		return m.initIptablesBase()
	}

	// nft: avoid re-running 5 SSH commands on every port mapping call.
	// The table/chain persists on the remote host, so one successful init
	// per (SSH connection, table name) pair per process is sufficient.
	cacheKey := fmt.Sprintf("%p:%s", m.sshClient, m.tableName)
	if _, ok := tableInitCache.Load(cacheKey); ok {
		return nil
	}

	if err := m.initNftTable(); err != nil {
		return err
	}
	tableInitCache.Store(cacheKey, struct{}{})
	return nil
}

func (m *Manager) initNftTable() error {
	cmds := []string{
		fmt.Sprintf("nft add table ip %s 2>/dev/null || true", m.tableName),
		fmt.Sprintf("nft 'add chain ip %s prerouting { type nat hook prerouting priority dstnat; policy accept; }' 2>/dev/null || true", m.tableName),
		fmt.Sprintf("nft 'add chain ip %s postrouting { type nat hook postrouting priority srcnat; policy accept; }' 2>/dev/null || true", m.tableName),
		fmt.Sprintf("nft 'add chain ip %s forward { type filter hook forward priority 0; policy accept; }' 2>/dev/null || true", m.tableName),
	}

	for _, cmd := range cmds {
		if _, err := m.sshClient.Execute(cmd); err != nil {
			global.APP_LOG.Warn("nft init command failed", zap.String("cmd", utils.TruncateString(cmd, 200)), zap.Error(err))
		}
	}

	// 验证 prerouting chain 已创建成功，这是 DNAT 规则的必要前提
	verifyCmd := fmt.Sprintf("nft list chain ip %s prerouting >/dev/null 2>&1 && echo 'ok'", m.tableName)
	verifyOutput, verifyErr := m.sshClient.Execute(verifyCmd)
	if verifyErr != nil || strings.TrimSpace(verifyOutput) != "ok" {
		return fmt.Errorf("nft prerouting chain verification failed for table %s: init commands may have silently failed (check nft/kernel support)", m.tableName)
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
			if _, err := m.sshClient.Execute(cmd); err != nil {
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
	if m.backend == BackendNft {
		return m.addDNATNft(vmName, vmIP, sshPort, startPort, endPort)
	}
	return m.addDNATIptables(vmIP, sshPort, startPort, endPort)
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

func (m *Manager) addDNATIptables(vmIP string, sshPort, startPort, endPort int) error {
	cmds := []string{
		// SSH DNAT tcp + udp
		fmt.Sprintf("iptables -t nat -I PREROUTING -p tcp --dport %d -j DNAT --to %s:22", sshPort, vmIP),
		fmt.Sprintf("iptables -t nat -I PREROUTING -p udp --dport %d -j DNAT --to %s:22", sshPort, vmIP),
	}

	// 端口范围
	if startPort > 0 && endPort > 0 && startPort <= endPort {
		for port := startPort; port <= endPort; port++ {
			cmds = append(cmds,
				fmt.Sprintf("iptables -t nat -I PREROUTING -p tcp --dport %d -j DNAT --to %s:%d", port, vmIP, port),
				fmt.Sprintf("iptables -t nat -I PREROUTING -p udp --dport %d -j DNAT --to %s:%d", port, vmIP, port),
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
	protocols := expandProtocol(protocol)

	for _, proto := range protocols {
		if m.backend == BackendNft {
			// 使用单引号包裹整个nft表达式，确保双引号的comment值不被SSH shell解析
			if comment != "" {
				cmd := fmt.Sprintf("nft 'add rule ip %s prerouting %s dport %d dnat to %s:%d comment \"%s\"'",
					m.tableName, proto, hostPort, instanceIP, guestPort, comment)
				if _, err := m.sshClient.Execute(cmd); err != nil {
					return fmt.Errorf("nft add DNAT failed: %w", err)
				}
			} else {
				cmd := fmt.Sprintf("nft 'add rule ip %s prerouting %s dport %d dnat to %s:%d'",
					m.tableName, proto, hostPort, instanceIP, guestPort)
				if _, err := m.sshClient.Execute(cmd); err != nil {
					return fmt.Errorf("nft add DNAT failed: %w", err)
				}
			}
		} else {
			cmd := fmt.Sprintf("iptables -t nat -A PREROUTING -p %s --dport %d -j DNAT --to-destination %s:%d",
				proto, hostPort, instanceIP, guestPort)
			if _, err := m.sshClient.Execute(cmd); err != nil {
				return fmt.Errorf("iptables add DNAT failed: %w", err)
			}
			// FORWARD
			fwd := fmt.Sprintf("iptables -A FORWARD -p %s -d %s --dport %d -j ACCEPT",
				proto, instanceIP, guestPort)
			if _, err := m.sshClient.Execute(fwd); err != nil {
				global.APP_LOG.Warn("iptables FORWARD failed", zap.Error(err))
			}
			// MASQUERADE
			masq := fmt.Sprintf("iptables -t nat -A POSTROUTING -p %s -s %s --sport %d -j MASQUERADE",
				proto, instanceIP, guestPort)
			if _, err := m.sshClient.Execute(masq); err != nil {
				global.APP_LOG.Warn("iptables MASQUERADE failed", zap.Error(err))
			}
		}
	}
	return nil
}

// RemoveSingleDNAT 删除单个端口映射
func (m *Manager) RemoveSingleDNAT(instanceIP string, hostPort, guestPort int, protocol, comment string) error {
	protocols := expandProtocol(protocol)

	for _, proto := range protocols {
		if m.backend == BackendNft {
			// 通过 handle 删除匹配的规则
			searchCmd := fmt.Sprintf(
				"nft -a list chain ip %s prerouting 2>/dev/null | grep 'dport %d.*dnat to %s:%d' | grep -oP '# handle \\K[0-9]+'",
				m.tableName, hostPort, instanceIP, guestPort)
			output, err := m.sshClient.Execute(searchCmd)
			if err == nil {
				for _, handle := range parseHandles(output) {
					m.sshClient.Execute(fmt.Sprintf("nft delete rule ip %s prerouting handle %s 2>/dev/null || true", m.tableName, handle))
				}
			}
		} else {
			// iptables: 精确删除 3 条规则
			cmds := []string{
				fmt.Sprintf("iptables -t nat -D PREROUTING -p %s --dport %d -j DNAT --to-destination %s:%d 2>/dev/null || true",
					proto, hostPort, instanceIP, guestPort),
				fmt.Sprintf("iptables -D FORWARD -p %s -d %s --dport %d -j ACCEPT 2>/dev/null || true",
					proto, instanceIP, guestPort),
				fmt.Sprintf("iptables -t nat -D POSTROUTING -p %s -s %s --sport %d -j MASQUERADE 2>/dev/null || true",
					proto, instanceIP, guestPort),
			}
			for _, cmd := range cmds {
				m.sshClient.Execute(cmd)
			}
		}
	}
	return nil
}

// DeleteRulesByComment 删除 nft 表中指定 comment 的所有规则
// 仅在 nft 后端有效；iptables 后端使用 DeleteRulesByIP
func (m *Manager) DeleteRulesByComment(comment string) error {
	if m.backend != BackendNft {
		return nil
	}

	chains := []string{"prerouting", "postrouting", "forward"}
	for _, chain := range chains {
		searchCmd := fmt.Sprintf(
			"nft -a list chain ip %s %s 2>/dev/null | grep '\"%s\"' | grep -oP '# handle \\K[0-9]+'",
			m.tableName, chain, comment)
		output, err := m.sshClient.Execute(searchCmd)
		if err != nil {
			continue
		}
		for _, handle := range parseHandles(output) {
			m.sshClient.Execute(fmt.Sprintf("nft delete rule ip %s %s handle %s 2>/dev/null || true", m.tableName, chain, handle))
		}
	}
	return nil
}

// DeleteRulesByIP 删除所有指向指定 IP 的转发规则
func (m *Manager) DeleteRulesByIP(ip string) error {
	if ip == "" {
		return nil
	}

	if m.backend == BackendNft {
		return m.deleteNftRulesByIP(ip)
	}
	return m.deleteIptablesRulesByIP(ip)
}

func (m *Manager) deleteNftRulesByIP(ip string) error {
	chains := []string{"prerouting", "postrouting", "forward"}
	for _, chain := range chains {
		searchCmd := fmt.Sprintf(
			"nft -a list chain ip %s %s 2>/dev/null | grep '%s' | grep -oP '# handle \\K[0-9]+'",
			m.tableName, chain, ip)
		output, err := m.sshClient.Execute(searchCmd)
		if err != nil {
			continue
		}
		for _, handle := range parseHandles(output) {
			m.sshClient.Execute(fmt.Sprintf("nft delete rule ip %s %s handle %s 2>/dev/null || true", m.tableName, chain, handle))
		}
	}
	return nil
}

func (m *Manager) deleteIptablesRulesByIP(ip string) error {
	const maxIterations = 100

	// PREROUTING DNAT rules
	for i := 0; i < maxIterations; i++ {
		output, err := m.sshClient.Execute(fmt.Sprintf(
			"iptables -t nat -S PREROUTING 2>/dev/null | grep 'DNAT.*%s[:/]' | head -1", ip))
		if err != nil || strings.TrimSpace(output) == "" {
			break
		}
		rule := strings.TrimSpace(output)
		deleteRule := strings.Replace(rule, "-A PREROUTING", "-D PREROUTING", 1)
		if _, err := m.sshClient.Execute(fmt.Sprintf("iptables -t nat %s 2>/dev/null", deleteRule)); err != nil {
			break
		}
	}

	// FORWARD rules
	for i := 0; i < maxIterations; i++ {
		output, err := m.sshClient.Execute(fmt.Sprintf(
			"iptables -S FORWARD 2>/dev/null | grep -- '-d %s' | head -1", ip))
		if err != nil || strings.TrimSpace(output) == "" {
			break
		}
		rule := strings.TrimSpace(output)
		deleteRule := strings.Replace(rule, "-A FORWARD", "-D FORWARD", 1)
		if _, err := m.sshClient.Execute(fmt.Sprintf("iptables %s 2>/dev/null", deleteRule)); err != nil {
			break
		}
	}

	// POSTROUTING MASQUERADE rules
	for i := 0; i < maxIterations; i++ {
		output, err := m.sshClient.Execute(fmt.Sprintf(
			"iptables -t nat -S POSTROUTING 2>/dev/null | grep '%s' | grep MASQUERADE | head -1", ip))
		if err != nil || strings.TrimSpace(output) == "" {
			break
		}
		rule := strings.TrimSpace(output)
		deleteRule := strings.Replace(rule, "-A POSTROUTING", "-D POSTROUTING", 1)
		if _, err := m.sshClient.Execute(fmt.Sprintf("iptables -t nat %s 2>/dev/null", deleteRule)); err != nil {
			break
		}
	}

	return nil
}

// SaveRules 持久化防火墙规则
func (m *Manager) SaveRules() {
	if m.backend == BackendNft {
		m.saveNftRules()
	} else {
		m.saveIptablesRules()
	}
}

func (m *Manager) saveNftRules() {
	cmds := []string{
		"mkdir -p /etc/nftables.d",
		fmt.Sprintf(`{
echo "# VM port forwarding - managed by oneclickvirt"
echo "table ip %s"
echo "delete table ip %s"
nft list table ip %s
} > /etc/nftables.d/%s.nft 2>/dev/null || true`, m.tableName, m.tableName, m.tableName, m.tableName),
	}
	for _, cmd := range cmds {
		m.sshClient.Execute(cmd)
	}
}

func (m *Manager) saveIptablesRules() {
	cmds := []string{
		"mkdir -p /etc/iptables",
		"iptables-save > /etc/iptables/rules.v4 2>/dev/null || true",
		"ip6tables-save > /etc/iptables/rules.v6 2>/dev/null || true",
		"service iptables save 2>/dev/null || true",
		"service ip6tables save 2>/dev/null || true",
		"netfilter-persistent save 2>/dev/null || true",
	}
	for _, cmd := range cmds {
		m.sshClient.Execute(cmd)
	}
}

// DiscoverDNATRules 发现指向指定 IP 的所有 DNAT 规则，返回 (hostPort, guestPort, protocol, isSSH) 列表
func (m *Manager) DiscoverDNATRules(vmIP string) []DiscoveredRule {
	if m.backend == BackendNft {
		return m.discoverNftDNAT(vmIP)
	}
	return m.discoverIptablesDNAT(vmIP)
}

// DiscoveredRule 发现的 DNAT 规则
type DiscoveredRule struct {
	HostPort  int
	GuestPort int
	Protocol  string
	IsSSH     bool
}

func (m *Manager) discoverNftDNAT(vmIP string) []DiscoveredRule {
	output, err := m.sshClient.Execute(fmt.Sprintf(
		"nft -a list chain ip %s prerouting 2>/dev/null | grep '%s'", m.tableName, vmIP))
	if err != nil {
		return nil
	}

	var rules []DiscoveredRule
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rule := parseNftDNATLine(line, vmIP)
		if rule != nil {
			rules = append(rules, *rule)
		}
	}
	return rules
}

func (m *Manager) discoverIptablesDNAT(vmIP string) []DiscoveredRule {
	output, err := m.sshClient.Execute(fmt.Sprintf(
		"iptables -t nat -L PREROUTING -n 2>/dev/null | grep 'DNAT' | grep '%s'", vmIP))
	if err != nil {
		return nil
	}

	var rules []DiscoveredRule
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rule := parseIptablesDNATLine(line, vmIP)
		if rule != nil {
			rules = append(rules, *rule)
		}
	}
	return rules
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

// parseNftDNATLine 解析 nft DNAT 规则行
// 格式示例: "tcp dport 25001 dnat to 192.168.122.2:22 comment \"vm:vm1\" # handle 5"
func parseNftDNATLine(line, vmIP string) *DiscoveredRule {
	if !strings.Contains(line, "dnat") {
		return nil
	}

	rule := &DiscoveredRule{Protocol: "tcp"}

	if strings.Contains(line, "udp") {
		rule.Protocol = "udp"
	}

	// 提取 dport
	if idx := strings.Index(line, "dport "); idx >= 0 {
		rest := line[idx+6:]
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			fmt.Sscanf(fields[0], "%d", &rule.HostPort)
		}
	}

	// 提取 dnat to IP:port
	if idx := strings.Index(line, "dnat to "); idx >= 0 {
		rest := line[idx+8:]
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			target := fields[0]
			parts := strings.Split(target, ":")
			if len(parts) == 2 {
				fmt.Sscanf(parts[1], "%d", &rule.GuestPort)
			} else {
				// identity mapping (no :port means same as host port)
				rule.GuestPort = rule.HostPort
			}
		}
	}

	if rule.HostPort == 0 {
		return nil
	}
	if rule.GuestPort == 0 {
		rule.GuestPort = rule.HostPort
	}

	if rule.GuestPort == 22 {
		rule.IsSSH = true
	}

	return rule
}

// parseIptablesDNATLine 解析 iptables DNAT 规则行
// 格式: DNAT tcp -- 0.0.0.0/0 0.0.0.0/0 tcp dpt:10022 to:192.168.122.2:22
func parseIptablesDNATLine(line, vmIP string) *DiscoveredRule {
	if !strings.Contains(line, "DNAT") {
		return nil
	}

	rule := &DiscoveredRule{Protocol: "tcp"}

	if strings.Contains(line, "udp") {
		rule.Protocol = "udp"
	}

	// 提取 dpt:
	if idx := strings.Index(line, "dpt:"); idx >= 0 {
		portStr := line[idx+4:]
		fields := strings.Fields(portStr)
		if len(fields) > 0 {
			fmt.Sscanf(fields[0], "%d", &rule.HostPort)
		}
	}

	// 提取 to:IP:port
	if idx := strings.Index(line, "to:"); idx >= 0 {
		target := line[idx+3:]
		fields := strings.Fields(target)
		if len(fields) > 0 {
			parts := strings.Split(fields[0], ":")
			if len(parts) == 2 {
				fmt.Sscanf(parts[1], "%d", &rule.GuestPort)
			}
		}
	}

	if rule.HostPort == 0 || rule.GuestPort == 0 {
		return nil
	}

	if rule.GuestPort == 22 {
		rule.IsSSH = true
	}

	return rule
}
