package admin

// console.go contains the provider-neutral browser console contract. VNC is
// a raw RFB stream, while Incus/LXD Windows VMs expose a SPICE Unix socket
// which is made browser-compatible by a node-local websockify process.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	consoleProtocolVNC         = "vnc"
	consoleProtocolSPICE       = "spice"
	consoleProtocolExec        = "exec"
	consoleProtocolAttach      = "attach"
	consoleProtocolNamespace   = "namespace"
	consoleProtocolNative      = "native-console"
	consoleProtocolUnsupported = "unsupported"
	consoleRepairStateTTL      = 30 * time.Minute
)

type consoleTarget struct {
	protocol     string
	host         string
	port         int
	webRoot      string
	transport    string
	available    bool
	repairable   bool
	repairStatus string
	reason       string
	nativeURL    string
	terminal     bool
	instanceID   uint
	provider     providerModel.Provider
}

type consoleRepairState struct {
	status    string
	reason    string
	updatedAt time.Time
}

var (
	consoleRepairMu       sync.Mutex
	consoleRepairs        = make(map[uint]consoleRepairState)
	consoleRepairProvider = make(map[uint]bool)
	consoleRepairSlots    = make(chan struct{}, 4)
)

var spiceMarker = regexp.MustCompile(`(?m)^ONECLICKVIRT_SPICE\t([^\t\r\n]+)\t([^\t\r\n]+)\t([0-9]+)\t([^\t\r\n]+)$`)
var spicePathPattern = regexp.MustCompile(`spice_query_var\(\s*["']path["']\s*,\s*["']websockify["']\s*\)`)

func loadConsoleRecords(instanceID, userID uint, admin bool) (providerModel.Instance, providerModel.Provider, error) {
	var inst providerModel.Instance
	query := global.APP_DB.Select("id", "name", "provider_id", "status", "instance_type", "provider_vm_id", "discovered_data", "user_id", "is_frozen", "expires_at")
	if admin {
		query = query.Where("id = ?", instanceID)
	} else {
		query = query.Where("id = ? AND user_id = ?", instanceID, userID)
	}
	if err := query.First(&inst).Error; err != nil {
		return inst, providerModel.Provider{}, err
	}
	var p providerModel.Provider
	if err := global.APP_DB.Select(
		"id", "type", "endpoint", "port_ip", "ssh_port", "username", "password", "ssh_key",
		"connection_type", "agent_status", "enable_vnc", "vnc_base_port", "vnc_host",
	).First(&p, inst.ProviderID).Error; err != nil {
		return inst, p, err
	}
	return inst, p, nil
}

func consoleRepairStatus(instanceID uint) (string, string) {
	consoleRepairMu.Lock()
	defer consoleRepairMu.Unlock()
	pruneConsoleRepairsLocked(time.Now())
	state, ok := consoleRepairs[instanceID]
	if !ok {
		return "not_started", ""
	}
	return state.status, state.reason
}

// pruneConsoleRepairsLocked retains only currently useful repair results. The
// state maps are process-local UI hints, so keeping one entry per historical
// instance would otherwise make a long-running controller grow without bound.
// Callers must hold consoleRepairMu.
func pruneConsoleRepairsLocked(now time.Time) {
	for instanceID, state := range consoleRepairs {
		if state.status != "running" && now.Sub(state.updatedAt) > consoleRepairStateTTL {
			delete(consoleRepairs, instanceID)
		}
	}
}

func parseJSONNumber(value interface{}) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		n, _ := strconv.Atoi(string(v))
		return n
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

type discoveredConsole struct {
	protocol       string
	nativeProtocol string
	host           string
	port           int
	webRoot        string
	transport      string
	spiceHost      string
	spicePort      int
	spiceWebRoot   string
	spiceTransport string
	spiceManaged   bool
	nativeURL      string
	nativeTargets  []nativeConsoleTarget
}

func parseDiscoveredConsole(raw string) discoveredConsole {
	var root map[string]interface{}
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &root) != nil {
		return discoveredConsole{}
	}
	console, _ := root["console"].(map[string]interface{})
	if console == nil {
		console = root
	}
	getString := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := console[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	getNumber := func(keys ...string) int {
		for _, key := range keys {
			if value, ok := console[key]; ok {
				if port := parseJSONNumber(value); port > 0 {
					return port
				}
			}
		}
		return 0
	}
	result := discoveredConsole{
		protocol:  strings.ToLower(getString("protocol", "consoleProtocol", "console_protocol")),
		host:      getString("host", "consoleHost", "console_host"),
		port:      getNumber("port", "consolePort", "console_port"),
		webRoot:   getString("webRoot", "web_root", "spiceWebRoot", "spice_web_root"),
		transport: strings.ToLower(getString("transport", "connectionType", "connection_type")),
	}
	spicePort := getNumber("spicePort", "spice_port", "websockifyPort", "websockify_port")
	if result.protocol == consoleProtocolSPICE {
		result.spiceHost = result.host
		result.spicePort = result.port
		if result.spicePort == 0 {
			result.spicePort = spicePort
		}
		result.spiceWebRoot = result.webRoot
		result.spiceTransport = result.transport
	}
	if result.spicePort == 0 && spicePort > 0 {
		result.spicePort = spicePort
		result.spiceHost = result.host
		result.spiceWebRoot = result.webRoot
		result.spiceTransport = result.transport
		if result.protocol == "" {
			result.protocol = consoleProtocolSPICE
		}
	}
	result.addNativeTarget(result.protocol, getString("url", "nativeURL", "native_url", "consoleURL", "console_url"))
	if spice, ok := console["spice"].(map[string]interface{}); ok {
		for _, key := range []string{"port", "consolePort", "websockifyPort", "spicePort"} {
			if port := parseJSONNumber(spice[key]); port > 0 {
				result.spicePort = port
				break
			}
		}
		if host, ok := spice["host"].(string); ok && strings.TrimSpace(host) != "" {
			result.spiceHost = strings.TrimSpace(host)
		}
		if rootPath, ok := spice["webRoot"].(string); ok && strings.TrimSpace(rootPath) != "" {
			result.spiceWebRoot = strings.TrimSpace(rootPath)
		}
		if transport, ok := spice["transport"].(string); ok && strings.TrimSpace(transport) != "" {
			result.spiceTransport = strings.ToLower(strings.TrimSpace(transport))
		}
		if managed, ok := spice["managed"].(bool); ok {
			result.spiceManaged = managed
		}
		if result.protocol == "" && result.spicePort > 0 {
			result.protocol = consoleProtocolSPICE
		}
	}
	for _, key := range []string{"native", "serial", "rdp", "terminal", "console", "virtio-console", "virtio_console", "vsock", "ssh", "telnet"} {
		entry, exists := console[key]
		if !exists {
			continue
		}
		protocol := strings.ReplaceAll(strings.ToLower(key), "_", "-")
		switch value := entry.(type) {
		case string:
			result.addNativeTarget(protocol, value)
		case map[string]interface{}:
			for _, urlKey := range []string{"url", "nativeURL", "native_url", "consoleURL", "console_url"} {
				if rawURL, ok := value[urlKey].(string); ok {
					result.addNativeTarget(protocol, rawURL)
					break
				}
			}
		}
	}
	if result.protocol == "" && result.spicePort > 0 && (getString("spiceSocket", "spice_socket") != "" || result.spiceWebRoot != "") {
		result.protocol = consoleProtocolSPICE
	}
	if result.spiceHost != "" {
		host, port := utils.ParseEndpoint(result.spiceHost, result.spicePort)
		result.spiceHost = host
		result.spicePort = port
	}
	if result.host != "" {
		host, port := utils.ParseEndpoint(result.host, result.port)
		result.host = host
		result.port = port
	}
	return result
}

func consoleHost(p providerModel.Provider) string {
	for _, candidate := range []string{p.VNCHost, p.PortIP, p.Endpoint} {
		host := utils.ExtractHost(candidate)
		if host != "" {
			return host
		}
	}
	return ""
}

// normalizeConsoleTransport preserves an explicitly discovered transport while
// allowing older SSH-managed providers (which predate connection_type) to use
// their node-local console listeners through SSH instead of the controller.
func normalizeConsoleTransport(p providerModel.Provider, transport string) string {
	transport = strings.ToLower(strings.TrimSpace(transport))
	if transport != "" {
		return transport
	}
	transport = strings.ToLower(strings.TrimSpace(p.ConnectionType))
	if transport != "" {
		return transport
	}
	if p.Username != "" && (strings.TrimSpace(p.Endpoint) != "" || strings.TrimSpace(p.PortIP) != "") {
		return "ssh"
	}
	return ""
}

// normalizeVNCConsoleTransport intentionally keeps legacy VNC behavior: VNC
// ports configured before the multi-protocol console existed were reachable at
// the provider endpoint. Only an explicitly discovered transport opts into a
// node-local SSH/Agent/local proxy. SPICE is different because its websockify
// listener is created on node loopback and always uses normalizeConsoleTransport.
func normalizeVNCConsoleTransport(p providerModel.Provider, transport string) string {
	if strings.TrimSpace(transport) == "" {
		return "direct"
	}
	return normalizeConsoleTransport(p, transport)
}

func consoleProviderTypeSupportsSPICE(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "incus", "lxd":
		return true
	default:
		return false
	}
}

func consoleProviderTypeSupportsVNC(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "proxmox", "proxmoxve", "pve", "qemu", "libvirt", "kvm", "kubevirt",
		"vmware", "virtualbox", "multipass", "vagrant":
		return true
	default:
		return false
	}
}

func consoleProviderTypeSupportsExec(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "docker", "orbstack", "podman", "containerd", "lxd", "incus":
		return true
	default:
		return false
	}
}

func resolveInstanceConsoleTargets(instanceID, userID uint, admin bool) ([]consoleTarget, error) {
	inst, p, err := loadConsoleRecords(instanceID, userID, admin)
	if err != nil {
		return nil, err
	}
	if constant.IsBusyStatus(inst.Status) {
		return nil, fmt.Errorf("实例正在操作进行中（当前状态：%s），请等待当前任务完成", inst.Status)
	}
	if inst.Status != constant.InstanceStatusRunning {
		return nil, fmt.Errorf("实例未运行")
	}
	metadata := parseDiscoveredConsole(inst.DiscoveredData)
	metadata.spiceTransport = normalizeConsoleTransport(p, metadata.spiceTransport)
	targets := make([]consoleTarget, 0, 3)
	add := func(target consoleTarget) {
		for index := range targets {
			existing := &targets[index]
			if existing.protocol == target.protocol {
				if target.available && !existing.available {
					existing.available = true
					existing.reason = ""
					existing.host = target.host
					existing.port = target.port
					existing.webRoot = target.webRoot
					existing.transport = target.transport
				}
				if existing.nativeURL == "" {
					existing.nativeURL = target.nativeURL
				}
				existing.repairable = existing.repairable || target.repairable
				if existing.repairStatus == "" {
					existing.repairStatus = target.repairStatus
				}
				// A native link and an in-panel terminal may describe the same
				// protocol. Prefer the terminal when the selected transport can
				// actually execute it, while retaining the native link as fallback.
				existing.terminal = existing.terminal || (target.terminal && target.available)
				return
			}
		}
		if target.terminal && !target.available {
			target.terminal = false
		}
		targets = append(targets, target)
	}

	for _, native := range metadata.nativeTargets {
		safeURL, nativeErr := validateNativeConsoleURL(native.url, p)
		target := consoleTarget{
			protocol: native.protocol, instanceID: instanceID, provider: p,
		}
		if nativeErr != nil {
			target.reason = nativeErr.Error()
		} else {
			target.nativeURL = safeURL
			target.available = true
		}
		add(target)
	}
	if p.EnableVNC && metadata.spicePort > 0 {
		host := metadata.spiceHost
		if host == "" {
			host = "127.0.0.1"
		}
		host, transport, targetErr := normalizeConsoleProxyTarget(p, host, metadata.spiceTransport)
		target := consoleTarget{
			protocol: consoleProtocolSPICE, host: host, port: metadata.spicePort,
			webRoot: metadata.spiceWebRoot, transport: transport,
			available: targetErr == nil, instanceID: instanceID, provider: p,
		}
		if targetErr != nil {
			target.reason = targetErr.Error()
		} else {
			target = refreshSPICEConsoleTarget(target)
		}
		add(target)
	}
	for _, plan := range consoleTerminalPlans(p.Type, inst.InstanceType, inst.ProviderInstanceIdentifier()) {
		transport, reason := consoleTerminalTransport(p)
		add(consoleTarget{
			protocol: plan.Protocol, transport: transport, terminal: true,
			available: reason == "", reason: reason,
			instanceID: instanceID, provider: p,
		})
	}

	// Incus/LXD VMs expose SPICE through a Unix socket and do not have a stable
	// TCP VNC port. Mark them repairable so the UI can initialize websockify
	// with one explicit idempotent action and display the actual remote error.
	if p.EnableVNC && inst.InstanceType == "vm" && consoleProviderTypeSupportsSPICE(p.Type) && metadata.spicePort == 0 {
		status, reason := consoleRepairStatus(instanceID)
		if reason == "" {
			reason = "未检测到 SPICE 浏览器代理，将自动检查 qemu.spice 并配置 websockify"
		}
		add(consoleTarget{
			protocol: consoleProtocolSPICE, repairable: true,
			repairStatus: status, reason: reason,
			instanceID: instanceID, provider: p,
		})
	}

	// Existing VNC metadata is honoured for VMs and containers alike. Some
	// Docker/special-runtime images intentionally provide a graphical VNC port.
	vncPort := parseVNCDiscoveredPort(inst.DiscoveredData)
	if p.EnableVNC && vncPort > 0 {
		vncTransport := normalizeVNCConsoleTransport(p, metadata.transport)
		host := metadata.host
		if host == "" {
			host = consoleHost(p)
			if vncTransport == "ssh" || vncTransport == "agent" || vncTransport == "local" {
				host = "127.0.0.1"
			}
		}
		if host == "" {
			return nil, fmt.Errorf("已发现 VNC 端口 %d，但节点控制台地址为空", vncPort)
		}
		host, transport, targetErr := normalizeConsoleProxyTarget(p, host, vncTransport)
		target := consoleTarget{
			protocol: consoleProtocolVNC, host: host, port: vncPort,
			transport: transport, available: targetErr == nil,
			instanceID: instanceID, provider: p,
		}
		if targetErr != nil {
			target.reason = targetErr.Error()
		}
		add(target)
	}
	if p.EnableVNC && inst.InstanceType == "vm" && consoleProviderTypeSupportsVNC(p.Type) {
		vncTransport := normalizeVNCConsoleTransport(p, metadata.transport)
		base := p.VNCBasePort
		if base <= 0 {
			base = 5900
		}
		vmid, _ := strconv.Atoi(strings.TrimSpace(inst.ProviderVMID))
		if vmid > 0 {
			base += vmid
		}
		host := consoleHost(p)
		if vncTransport == "ssh" || vncTransport == "agent" || vncTransport == "local" {
			host = "127.0.0.1"
		}
		if host != "" && base <= 65535 {
			host, transport, targetErr := normalizeConsoleProxyTarget(p, host, vncTransport)
			target := consoleTarget{
				protocol: consoleProtocolVNC, host: host, port: base,
				transport: transport, available: targetErr == nil,
				instanceID: instanceID, provider: p,
			}
			if targetErr != nil {
				target.reason = targetErr.Error()
			}
			add(target)
		}
	}
	if len(targets) == 0 {
		reason := fmt.Sprintf("%s 节点未提供可用的 VNC、SPICE 或原生控制台", p.Type)
		if !p.EnableVNC {
			reason = "节点未启用图形控制台，且未发现可用的原生控制台或容器 Exec"
		}
		return []consoleTarget{{
			protocol:   consoleProtocolUnsupported,
			reason:     reason + "；容器不会被隐式安装桌面环境",
			instanceID: instanceID, provider: p,
		}}, nil
	}
	return targets, nil
}

func resolveInstanceConsoleTargetForProtocol(instanceID, userID uint, admin bool, protocol string) (consoleTarget, error) {
	targets, err := resolveInstanceConsoleTargets(instanceID, userID, admin)
	if err != nil {
		return consoleTarget{}, err
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol != "" {
		for _, target := range targets {
			if target.protocol == protocol {
				return target, nil
			}
		}
		return consoleTarget{}, fmt.Errorf("当前实例不支持 %s 控制台", protocol)
	}
	for _, target := range targets {
		if target.available {
			return target, nil
		}
	}
	return targets[0], nil
}

// resolveInstanceConsoleTarget keeps the legacy VNC proxy entrypoint source
// compatible while routing it through the provider-neutral capability model.
// Callers that need a specific protocol should use the protocol-aware helper.
func resolveInstanceConsoleTarget(instanceID, userID uint, admin bool) (consoleTarget, error) {
	return resolveInstanceConsoleTargetForProtocol(instanceID, userID, admin, consoleProtocolVNC)
}

func consoleTargetInfo(targets []consoleTarget) gin.H {
	protocols := make([]string, 0, len(targets))
	capabilities := make([]gin.H, 0, len(targets))
	selected := consoleProtocolUnsupported
	available := false
	repairable := false
	reason := ""
	for _, target := range targets {
		protocols = append(protocols, target.protocol)
		if target.available && !available {
			selected = target.protocol
			available = true
		}
		if target.repairable {
			repairable = true
			if selected == consoleProtocolUnsupported && !available {
				selected = target.protocol
			}
		}
		if reason == "" && target.reason != "" {
			reason = target.reason
		}
		capabilities = append(capabilities, gin.H{
			"protocol": target.protocol, "available": target.available,
			"repairable": target.repairable, "repairStatus": target.repairStatus,
			"reason": target.reason, "nativeURL": target.nativeURL, "terminal": target.terminal,
		})
	}
	if !available {
		for _, target := range targets {
			if target.repairable {
				selected = target.protocol
				break
			}
		}
	}
	if selected == consoleProtocolUnsupported && reason == "" {
		reason = "未发现可用控制台协议"
	}
	return gin.H{
		"enabled": available || repairable, "available": available,
		"protocol": selected, "protocols": protocols, "capabilities": capabilities,
		"repairable": repairable, "reason": reason,
		"repairStatus": func() string {
			for _, target := range targets {
				if target.protocol == selected {
					return target.repairStatus
				}
			}
			return "not_started"
		}(),
		"instanceID": targets[0].instanceID,
	}
}

func buildInstanceConsoleInfo(instanceID, userID uint, admin bool) (gin.H, error) {
	targets, err := resolveInstanceConsoleTargets(instanceID, userID, admin)
	if err != nil {
		return gin.H{
			"enabled": false, "available": false,
			"protocol": consoleProtocolUnsupported, "protocols": []string{consoleProtocolUnsupported},
			"capabilities": []gin.H{{"protocol": consoleProtocolUnsupported, "available": false, "repairable": false, "reason": err.Error()}},
			"repairable":   false, "repairStatus": "not_started", "reason": err.Error(),
		}, nil
	}
	return consoleTargetInfo(targets), nil
}

func newConsoleExecutor(p providerModel.Provider) (utils.ShellExecutor, func(), error) {
	if strings.EqualFold(strings.TrimSpace(p.ConnectionType), "local") {
		exec := utils.NewLocalShellExecutor(240 * time.Second)
		return exec, func() {}, nil
	}
	if strings.EqualFold(strings.TrimSpace(p.ConnectionType), "agent") {
		exec := agentService.NewAgentShellExecutor(p.ID, agentService.GetHub())
		return exec, func() {}, nil
	}
	endpoint := strings.TrimSpace(p.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(p.PortIP)
	}
	host, port := utils.ParseEndpoint(endpoint, p.SSHPort)
	if host == "" || p.Username == "" {
		return nil, nil, fmt.Errorf("节点缺少 SSH 地址或用户名，无法自动配置 SPICE")
	}
	if port <= 0 {
		port = 22
	}
	client, err := utils.NewSSHClient(utils.SSHConfig{
		Host: host, Port: port, Username: p.Username, Password: p.Password, PrivateKey: p.SSHKey,
		ConnectTimeout: 15 * time.Second, ExecuteTimeout: 240 * time.Second,
	})
	if err != nil {
		return nil, nil, err
	}
	return client, func() { _ = client.Close() }, nil
}

func spiceSetupCommand(instanceName string, instanceID uint, p providerModel.Provider) string {
	name := utils.ShellSingleQuote(instanceName)
	base := p.VNCBasePort
	if base < 6000 {
		base = 6100
	}
	base += int(instanceID % 200)
	lines := []string{
		"set -u",
		fmt.Sprintf("INSTANCE=%s", name),
		`SOCKET=""`,
		`for candidate in "/run/incus/${INSTANCE}/qemu.spice" "/var/lib/incus/containers/${INSTANCE}/qemu.spice" "/var/lib/incus/logs/${INSTANCE}/qemu.spice" "/var/snap/lxd/common/lxd/logs/${INSTANCE}/qemu.spice" "/var/lib/lxd/containers/${INSTANCE}/qemu.spice" "/var/lib/lxd/containers/${INSTANCE}/logs/qemu.spice"; do`,
		`  if [ -S "$candidate" ]; then SOCKET="$candidate"; break; fi`,
		`done`,
		`if [ -z "$SOCKET" ]; then`,
		`  for root in /run/incus /var/lib/incus /var/snap/lxd/common/lxd/logs /var/lib/lxd/containers; do`,
		`    if [ -d "$root" ]; then`,
		`      found=$(find "$root" -path "*/${INSTANCE}/*" -type s -name qemu.spice -print -quit 2>/dev/null || true)`,
		`      if [ -n "$found" ]; then SOCKET="$found"; break; fi`,
		`    fi`,
		`  done`,
		`fi`,
		`if [ -z "$SOCKET" ]; then echo "未找到运行中的 qemu.spice Unix socket（实例可能未启动、不是 VM，或 Incus/LXD 未启用 QEMU 驱动）" >&2; exit 20; fi`,
		`if ! command -v websockify >/dev/null 2>&1 || ! find /usr/share /usr/local/share -type f -name spice_auto.html -print -quit 2>/dev/null | grep -q .; then`,
		`  if command -v apt-get >/dev/null 2>&1; then`,
		`    export DEBIAN_FRONTEND=noninteractive`,
		`    apt-get update -y >/dev/null 2>&1 && apt-get install -y spice-html5 websockify >/dev/null 2>&1 || { echo "自动安装 spice-html5/websockify 失败，请确认节点可用 root 权限、APT 源和网络" >&2; exit 21; }`,
		`  else`,
		`    echo "节点未安装 websockify/spice-html5，且没有 apt-get 可用于自动修复" >&2; exit 22`,
		`  fi`,
		`fi`,
		`WEB_ROOT=""`,
		`for root in /usr/share/spice-html5 /usr/share/spice-html5-* /usr/local/share/spice-html5; do if [ -f "$root/spice_auto.html" ]; then WEB_ROOT="$root"; break; fi; done`,
		`if [ -z "$WEB_ROOT" ]; then found_html=$(find /usr/share /usr/local/share -type f -name spice_auto.html -print -quit 2>/dev/null || true); if [ -n "$found_html" ]; then WEB_ROOT=$(dirname "$found_html"); fi; fi`,
		`if [ -z "$WEB_ROOT" ] || [ ! -f "$WEB_ROOT/spice_auto.html" ]; then echo "已找到 websockify，但未找到 spice_auto.html 资源" >&2; exit 23; fi`,
		`mkdir -p /run/oneclickvirt-spice 2>/dev/null || true`,
		fmt.Sprintf(`INSTANCE_ID=%d`, instanceID),
		`RUNTIME_DIR="/run/oneclickvirt-spice"`,
		`if [ ! -w "$RUNTIME_DIR" ]; then RUNTIME_DIR="/tmp/oneclickvirt-spice"; mkdir -p "$RUNTIME_DIR" 2>/dev/null || true; fi`,
		`PIDFILE="$RUNTIME_DIR/${INSTANCE_ID}.pid"`,
		`STATEFILE="$RUNTIME_DIR/${INSTANCE_ID}.state"`,
		`LOGFILE="$RUNTIME_DIR/${INSTANCE_ID}.log"`,
		`LOCKDIR="$RUNTIME_DIR/${INSTANCE_ID}.lock"`,
		`acquire_lock() {`,
		`  attempts=0`,
		`  while ! mkdir "$LOCKDIR" 2>/dev/null; do`,
		`    if [ -f "$LOCKDIR/pid" ]; then lock_pid=$(cat "$LOCKDIR/pid" 2>/dev/null || true); if [ -n "$lock_pid" ] && ! kill -0 "$lock_pid" 2>/dev/null; then rm -rf -- "$LOCKDIR"; continue; fi; fi`,
		`    attempts=$((attempts + 1)); [ "$attempts" -ge 80 ] && { echo "等待 SPICE 修复锁超时" >&2; exit 26; }; sleep 0.25`,
		`  done`,
		`  printf "%s\n" "$$" > "$LOCKDIR/pid"`,
		`  trap 'rm -rf -- "$LOCKDIR"' EXIT`,
		`}`,
		`acquire_lock`,
		`is_websockify_pid() {`,
		`  pid="$1"`,
		`  [ -n "$pid" ] || return 1`,
		`  if [ -r "/proc/$pid/cmdline" ]; then tr "\\000" " " < "/proc/$pid/cmdline" | grep -q "[w]ebsockify"; return $?; fi`,
		`  ps -p "$pid" -o args= 2>/dev/null | grep -q "[w]ebsockify"`,
		`}`,
		`if [ -s "$PIDFILE" ] && [ -s "$STATEFILE" ]; then`,
		`  EXISTING_PID=$(cat "$PIDFILE" 2>/dev/null || true)`,
		`  if is_websockify_pid "$EXISTING_PID"; then`,
		`    EXISTING_SOCKET=""; EXISTING_ROOT=""; EXISTING_PORT=""; EXISTING_HOST=""`,
		`    IFS="$(printf "\\t")" read -r EXISTING_SOCKET EXISTING_ROOT EXISTING_PORT EXISTING_HOST < "$STATEFILE" || true`,
		`    if [ -S "$EXISTING_SOCKET" ] && [ -n "$EXISTING_PORT" ] && (ss -ltn 2>/dev/null || true) | awk '{print $4}' | grep -Eq "[:.]${EXISTING_PORT}$"; then`,
		`      printf "ONECLICKVIRT_SPICE\\t%s\\t%s\\t%s\\t%s\\n" "$EXISTING_SOCKET" "$EXISTING_ROOT" "$EXISTING_PORT" "${EXISTING_HOST:-127.0.0.1}"; exit 0`,
		`    fi`,
		`  fi`,
		`  is_websockify_pid "$EXISTING_PID" && kill "$EXISTING_PID" >/dev/null 2>&1 || true`,
		`  rm -f -- "$PIDFILE" "$STATEFILE"`,
		`fi`,
		`PORT=0`,
		fmt.Sprintf(`for candidate in $(seq %d %d); do if ! (ss -ltn 2>/dev/null || true) | awk '{print $4}' | grep -Eq "[:.]${candidate}$"; then PORT="$candidate"; break; fi; done`, base, base+199),
		`if [ "$PORT" = "0" ]; then echo "没有可用的 websockify 监听端口（候选范围已被占用）" >&2; exit 24; fi`,
		`nohup websockify --web "$WEB_ROOT" "$PORT" --unix-target="$SOCKET" >"$LOGFILE" 2>&1 </dev/null &`,
		`PID=$!; printf "%s\n" "$PID" > "$PIDFILE"`,
		`printf "%s\t%s\t%s\t%s\n" "$SOCKET" "$WEB_ROOT" "$PORT" "127.0.0.1" > "${STATEFILE}.tmp"; mv -f -- "${STATEFILE}.tmp" "$STATEFILE"`,
		`for _ in $(seq 1 20); do if (ss -ltn 2>/dev/null || true) | awk '{print $4}' | grep -Eq "[:.]${PORT}$"; then printf 'ONECLICKVIRT_SPICE\t%s\t%s\t%s\t%s\n' "$SOCKET" "$WEB_ROOT" "$PORT" "127.0.0.1"; exit 0; fi; sleep 0.5; done`,
		`echo "websockify 启动后未监听端口，最近日志：" >&2; tail -n 40 "$LOGFILE" 2>/dev/null || true; exit 25`,
	}
	return strings.Join(lines, "\n")
}

func persistSpiceMetadata(instanceID uint, socket, webRoot string, port int, transport string) error {
	// Discovery/import may update discovered_data while the node-side adapter is
	// starting. Retry a short optimistic compare-and-swap so this small metadata
	// addition cannot erase another controller update.
	for attempt := 0; attempt < 3; attempt++ {
		var inst providerModel.Instance
		if err := global.APP_DB.Select("id", "discovered_data", "updated_at").First(&inst, instanceID).Error; err != nil {
			return err
		}
		root := make(map[string]interface{})
		if strings.TrimSpace(inst.DiscoveredData) != "" {
			_ = json.Unmarshal([]byte(inst.DiscoveredData), &root)
		}
		console := make(map[string]interface{})
		if existing, ok := root["console"].(map[string]interface{}); ok {
			for key, value := range existing {
				console[key] = value
			}
		}
		console["spice"] = map[string]interface{}{
			"protocol": consoleProtocolSPICE, "host": "127.0.0.1", "port": port,
			"webRoot": webRoot, "spiceSocket": socket, "transport": transport,
			"managed": true, "updatedAt": time.Now().UTC().Format(time.RFC3339),
		}
		// SPICE is deliberately nested. A prior top-level VNC record must keep its
		// host/port semantics so the legacy /vnc route and an independently managed
		// VNC proxy never start using the SPICE websockify listener.
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(console["protocol"])), consoleProtocolSPICE) && console["managed"] == true {
			for _, key := range []string{"protocol", "host", "port", "webRoot", "spiceSocket", "transport", "managed", "updatedAt"} {
				delete(console, key)
			}
		}
		root["console"] = console
		data, err := json.Marshal(root)
		if err != nil {
			return err
		}
		now := time.Now()
		result := global.APP_DB.Model(&providerModel.Instance{}).
			Where("id = ? AND status = ? AND updated_at = ?", instanceID, constant.InstanceStatusRunning, inst.UpdatedAt).
			Updates(map[string]interface{}{"discovered_data": string(data), "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			return nil
		}
	}
	return fmt.Errorf("实例状态或发现数据在保存 SPICE 控制台元数据时发生变化，请重试")
}

func setConsoleRepairState(instanceID uint, status, reason string) {
	consoleRepairMu.Lock()
	pruneConsoleRepairsLocked(time.Now())
	consoleRepairs[instanceID] = consoleRepairState{status: status, reason: reason, updatedAt: time.Now()}
	consoleRepairMu.Unlock()
}

// reserveConsoleRepair bounds remote package/process work. A node can have
// many instances, but installing the same SPICE adapter concurrently on one
// node only creates races around apt/dpkg and the websockify port range.
func reserveConsoleRepair(providerID uint) bool {
	select {
	case consoleRepairSlots <- struct{}{}:
	default:
		return false
	}
	consoleRepairMu.Lock()
	if consoleRepairProvider[providerID] {
		consoleRepairMu.Unlock()
		<-consoleRepairSlots
		return false
	}
	consoleRepairProvider[providerID] = true
	consoleRepairMu.Unlock()
	return true
}

func releaseConsoleRepair(providerID uint) {
	consoleRepairMu.Lock()
	delete(consoleRepairProvider, providerID)
	consoleRepairMu.Unlock()
	<-consoleRepairSlots
}

func runSpiceRepair(instanceID uint, instanceName string, p providerModel.Provider) {
	defer releaseConsoleRepair(p.ID)
	exec, cleanup, err := newConsoleExecutor(p)
	if err != nil {
		setConsoleRepairState(instanceID, "failed", err.Error())
		return
	}
	defer cleanup()
	output, execErr := exec.ExecuteWithTimeout(spiceSetupCommand(instanceName, instanceID, p), 240*time.Second)
	match := spiceMarker.FindStringSubmatch(output)
	if execErr != nil || len(match) != 5 {
		reason := strings.TrimSpace(output)
		if execErr != nil {
			reason = fmt.Sprintf("%v", execErr)
			if strings.TrimSpace(output) != "" {
				reason += "; 远端输出: " + strings.TrimSpace(output)
			}
		}
		if reason == "" {
			reason = "远端未返回 SPICE 代理启动结果"
		}
		setConsoleRepairState(instanceID, "failed", utils.TruncateString(reason, 1600))
		return
	}
	port, _ := strconv.Atoi(match[3])
	transport := strings.ToLower(strings.TrimSpace(p.ConnectionType))
	if err := persistSpiceMetadata(instanceID, match[1], match[2], port, transport); err != nil {
		setConsoleRepairState(instanceID, "failed", "SPICE 已启动但保存控制台元数据失败: "+err.Error())
		return
	}
	invalidateSPICEHealth(instanceID)
	setConsoleRepairState(instanceID, "ready", "")
	if global.APP_LOG != nil {
		global.APP_LOG.Info("SPICE 浏览器控制台已配置", zap.Uint("instanceID", instanceID), zap.Int("port", port))
	}
}

func startInstanceConsoleRepair(instanceID, userID uint, admin bool) (gin.H, error) {
	inst, p, err := loadConsoleRecords(instanceID, userID, admin)
	if err != nil {
		return nil, err
	}
	if constant.IsBusyStatus(inst.Status) || inst.Status != constant.InstanceStatusRunning {
		return nil, fmt.Errorf("实例未处于可修复状态（当前状态：%s）", inst.Status)
	}
	if !consoleProviderTypeSupportsSPICE(p.Type) || inst.InstanceType != "vm" {
		return nil, fmt.Errorf("当前节点/实例没有可自动修复的 SPICE 控制台")
	}
	if !p.EnableVNC {
		return nil, fmt.Errorf("节点未启用图形控制台，请先启用 WebVNC/控制台")
	}
	consoleRepairMu.Lock()
	pruneConsoleRepairsLocked(time.Now())
	state := consoleRepairs[instanceID]
	if state.status == "running" {
		consoleRepairMu.Unlock()
		targets, targetsErr := resolveInstanceConsoleTargets(instanceID, userID, admin)
		if targetsErr != nil {
			return nil, targetsErr
		}
		return consoleTargetInfo(targets), nil
	}
	consoleRepairs[instanceID] = consoleRepairState{status: "running", updatedAt: time.Now()}
	consoleRepairMu.Unlock()
	invalidateSPICEHealth(instanceID)
	if !reserveConsoleRepair(p.ID) {
		setConsoleRepairState(instanceID, "failed", "该节点已有控制台修复任务或修复队列已满，请稍后重试")
		targets, targetsErr := resolveInstanceConsoleTargets(instanceID, userID, admin)
		if targetsErr != nil {
			return nil, targetsErr
		}
		return consoleTargetInfo(targets), nil
	}
	go runSpiceRepair(instanceID, inst.ProviderInstanceIdentifier(), p)
	targets, err := resolveInstanceConsoleTargets(instanceID, userID, admin)
	if err != nil {
		return nil, err
	}
	return consoleTargetInfo(targets), nil
}
