package admin

// console_probe.go contains bounded, non-interactive runtime discovery for the
// instance console. The database instance_type is intentionally absent from
// these decisions: imported and legacy records can be stale, while a live
// provider object is the only useful authority for console capabilities.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	providerModel "oneclickvirt/model/provider"
	consoleService "oneclickvirt/service/console"
	"oneclickvirt/utils"

	"golang.org/x/sync/singleflight"
)

const (
	consoleRuntimeProbeTTL     = 15 * time.Second
	consoleRuntimeProbeTimeout = 10 * time.Second
)

type consoleDiscoveredEndpoint struct {
	protocol  string
	host      string
	port      int
	transport string
}

// consoleRuntimeProbe is deliberately small and process-local. It represents a
// short-lived observation of the real provider object; it is not persisted as
// desired state and therefore cannot create a controller/provider drift.
type consoleRuntimeProbe struct {
	runtimeID          string
	runtimeKind        string
	node               string
	observed           bool
	running            bool
	reason             string
	spiceReason        string
	terminalPlans      []InstanceConsoleTerminalPlan
	terminalFailures   []consoleTerminalFailure
	proxmoxVNC         bool
	proxmoxVNCChecked  bool
	proxmoxVNCReason   string
	kubeVirtVNC        bool
	kubeVirtVNCChecked bool
	kubeVirtVNCReason  string
	spiceRepairable    bool
	vncEndpoints       []consoleDiscoveredEndpoint
	nativeTargets      []nativeConsoleTarget
	endpointCandidates []consoleEndpointCandidate
}

// consoleRuntimeProbeAllowlistedProviders have an implementation-specific
// runtime probe. Providers outside this set can still expose a valid VNC/RDP/
// SPICE/SSH/Telnet endpoint through the instance's live mappings; the generic
// endpoint probe is intentionally retained for those imports instead of using
// InstanceType as a visibility switch.
func consoleRuntimeProbeAllowsGenericEndpointDiscovery(inst providerModel.Instance, p providerModel.Provider) consoleRuntimeProbe {
	return consoleRuntimeProbe{
		runtimeID: inst.ProviderInstanceIdentifier(),
		reason:    fmt.Sprintf("%s 节点未提供宿主机运行态探测器；仅检查该实例已暴露端口的实际协议", p.Type),
	}
}

type consoleTerminalFailure struct {
	protocol string
	reason   string
}

type consoleRuntimeProbeCacheEntry struct {
	probe      consoleRuntimeProbe
	resolvedAt time.Time
}

var (
	consoleRuntimeProbeMu    sync.Mutex
	consoleRuntimeProbeCache = make(map[string]consoleRuntimeProbeCacheEntry)
	consoleRuntimeProbeGroup singleflight.Group
	consoleRuntimeProbeSlots = make(chan struct{}, 6)
)

func init() {
	consoleService.RegisterInstanceConsoleCacheInvalidator(invalidateInstanceConsoleProbeState)
}

// invalidateInstanceConsoleProbeState is called only after a lifecycle or
// mapping transaction has committed. No remote work or database query occurs
// here, so invalidating a batch cannot create a long transaction or N+1 reads.
func invalidateInstanceConsoleProbeState(instanceID uint) {
	invalidateConsoleRuntimeProbe(instanceID)
	invalidateConsoleEndpointProbe(instanceID)
	invalidateProxmoxConsoleRuntime(instanceID)
	invalidateSPICEHealth(instanceID)
}

func consoleRuntimeProbeCacheKey(inst providerModel.Instance, p providerModel.Provider) string {
	return fmt.Sprintf("%d:%d:%s:%s:%s:%s:%s:%s:%s", inst.ID, p.ID, strings.ToLower(strings.TrimSpace(p.Type)),
		strings.TrimSpace(inst.ProviderInstanceIdentifier()), strings.TrimSpace(inst.Name), strings.TrimSpace(p.HostName),
		strings.TrimSpace(p.StoragePool), strings.TrimSpace(p.StoragePoolPath), strings.ToLower(strings.TrimSpace(inst.Status)))
}

func cachedConsoleRuntimeProbe(key string, now time.Time) (consoleRuntimeProbe, bool) {
	consoleRuntimeProbeMu.Lock()
	defer consoleRuntimeProbeMu.Unlock()
	for cacheKey, entry := range consoleRuntimeProbeCache {
		if now.Sub(entry.resolvedAt) > consoleRuntimeProbeTTL {
			delete(consoleRuntimeProbeCache, cacheKey)
		}
	}
	entry, ok := consoleRuntimeProbeCache[key]
	return entry.probe, ok
}

func cacheConsoleRuntimeProbe(key string, probe consoleRuntimeProbe) {
	consoleRuntimeProbeMu.Lock()
	consoleRuntimeProbeCache[key] = consoleRuntimeProbeCacheEntry{probe: probe, resolvedAt: time.Now()}
	consoleRuntimeProbeMu.Unlock()
}

// invalidateConsoleRuntimeProbe is used after a repair or explicit lifecycle
// action. It is intentionally local because every cache entry expires quickly
// and the controller never treats a cached observation as persisted truth.
func invalidateConsoleRuntimeProbe(instanceID uint) {
	consoleRuntimeProbeMu.Lock()
	for key := range consoleRuntimeProbeCache {
		if strings.HasPrefix(key, strconv.FormatUint(uint64(instanceID), 10)+":") {
			delete(consoleRuntimeProbeCache, key)
		}
	}
	consoleRuntimeProbeMu.Unlock()
}

// resolveConsoleRuntimeProbe coalesces an actual provider check for callers
// opening the same dialog concurrently. It never runs under a database
// transaction and never executes console/attach commands that could block.
func resolveConsoleRuntimeProbe(inst providerModel.Instance, p providerModel.Provider) consoleRuntimeProbe {
	key := consoleRuntimeProbeCacheKey(inst, p)
	if probe, ok := cachedConsoleRuntimeProbe(key, time.Now()); ok {
		return probe
	}

	value, _, _ := consoleRuntimeProbeGroup.Do(key, func() (interface{}, error) {
		if probe, ok := cachedConsoleRuntimeProbe(key, time.Now()); ok {
			return probe, nil
		}
		select {
		case consoleRuntimeProbeSlots <- struct{}{}:
			defer func() { <-consoleRuntimeProbeSlots }()
		default:
			probe := consoleRuntimeProbe{runtimeID: inst.ProviderInstanceIdentifier(), reason: "控制台能力探测队列繁忙，请稍后重试"}
			cacheConsoleRuntimeProbe(key, probe)
			return probe, nil
		}
		probe := performConsoleRuntimeProbe(inst, p)
		cacheConsoleRuntimeProbe(key, probe)
		return probe, nil
	})
	probe, ok := value.(consoleRuntimeProbe)
	if !ok {
		return consoleRuntimeProbe{runtimeID: inst.ProviderInstanceIdentifier(), reason: "控制台能力探测未返回有效结果"}
	}
	return probe
}

func performConsoleRuntimeProbe(inst providerModel.Instance, p providerModel.Provider) consoleRuntimeProbe {
	providerType := strings.ToLower(strings.TrimSpace(p.Type))
	switch providerType {
	case "proxmox", "proxmoxve", "pve":
		return probeProxmoxConsoleRuntime(inst, p)
	case "lxd", "incus":
		return probeLXDLikeConsoleRuntime(inst, p, providerType)
	case "docker", "orbstack", "podman", "containerd":
		return probeContainerRuntimeConsole(inst, p, providerType)
	case "qemu", "libvirt", "kvm":
		return probeLibvirtConsoleRuntime(inst, p)
	case "kubevirt":
		return probeKubeVirtConsoleRuntime(inst, p)
	case "vmware":
		return probeVMwareConsoleRuntime(inst, p)
	case "virtualbox":
		return probeVirtualBoxConsoleRuntime(inst, p)
	case "multipass":
		return probeMultipassConsoleRuntime(inst, p)
	case "vagrant":
		return probeVagrantConsoleRuntime(inst, p)
	default:
		return consoleRuntimeProbeAllowsGenericEndpointDiscovery(inst, p)
	}
}

func consoleProbeExecutor(p providerModel.Provider) (utils.ShellExecutor, func(), error) {
	executor, cleanup, err := newConsoleExecutor(p)
	if err != nil {
		return nil, nil, err
	}
	return executor, cleanup, nil
}

func probeLXDLikeConsoleRuntime(inst providerModel.Instance, p providerModel.Provider, cli string) consoleRuntimeProbe {
	identifier := strings.TrimSpace(inst.ProviderInstanceIdentifier())
	probe := consoleRuntimeProbe{runtimeID: identifier}
	if identifier == "" {
		probe.reason = "实例缺少运行时标识，无法探测控制台"
		return probe
	}
	executor, cleanup, err := consoleProbeExecutor(p)
	if err != nil {
		probe.reason = "无法连接节点以探测控制台: " + err.Error()
		return probe
	}
	defer cleanup()
	output, err := executor.ExecuteWithTimeout(fmt.Sprintf("%s info %s --format json", cli, utils.ShellSingleQuote(identifier)), consoleRuntimeProbeTimeout)
	if err != nil {
		probe.reason = fmt.Sprintf("读取 %s 实例运行态失败: %v；远端输出: %s", strings.ToUpper(cli), err, utils.TruncateString(strings.TrimSpace(output), 400))
		return probe
	}
	kind, running, err := parseLXDLikeConsoleInfo(output)
	if err != nil {
		probe.reason = "解析实例运行态失败: " + err.Error()
		return probe
	}
	probe.runtimeKind = kind
	probe.observed = true
	probe.running = running
	if !running {
		probe.reason = "实例当前未运行，暂不提供宿主机控制台"
		return probe
	}
	proxyEndpoints, proxyReason := probeLXDLikeProxyConsoleEndpoints(executor, cli, identifier, p)
	probe.endpointCandidates = append(probe.endpointCandidates, proxyEndpoints...)

	// A running LXD/Incus object alone is not sufficient: containers, VMs, and
	// imported instances can all have a disabled or unavailable serial console.
	// Start the real console briefly and require it to remain attached until the
	// node-side timeout. `--show-log` is deliberately not enough here: it can
	// return successfully without proving that an interactive console works.
	if available, reason := probeLXDLikeSerialConsole(executor, cli, identifier); available {
		probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
			Protocol: consoleProtocolSerial, Command: fmt.Sprintf("%s console %s", cli, utils.ShellSingleQuote(identifier)),
		})
	} else {
		probe.terminalFailures = append(probe.terminalFailures, consoleTerminalFailure{protocol: consoleProtocolSerial, reason: reason})
		probe.reason = reason
	}

	// Exec support differs by guest agent and guest OS. Probe a zero-effect shell
	// command once, cached and time-bounded, before presenting an Exec button.
	if shell := probeLXDLikeGuestShell(executor, cli, identifier); shell != "" {
		probe.terminalPlans = append([]InstanceConsoleTerminalPlan{{
			Protocol: consoleProtocolExec, Command: lxdLikeExecConsoleCommand(cli, identifier, shell),
		}}, probe.terminalPlans...)
	}
	// The actual qemu.spice socket is the authority. Do not use the live object
	// type as a visibility gate: an imported or atypical instance may expose a
	// graphical endpoint despite stale or vendor-specific type metadata.
	probe.spiceRepairable, probe.spiceReason = probeLXDLikeSPICESocket(executor, identifier)
	if probe.reason == "" && len(probe.terminalPlans) == 0 && len(probe.endpointCandidates) == 0 && !probe.spiceRepairable && proxyReason != "" {
		probe.reason = proxyReason
	}
	return probe
}

func parseLXDLikeConsoleInfo(raw string) (string, bool, error) {
	var decoded interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &decoded); err != nil {
		return "", false, err
	}
	value, ok := decoded.(map[string]interface{})
	if !ok {
		if list, listOK := decoded.([]interface{}); listOK && len(list) > 0 {
			value, ok = list[0].(map[string]interface{})
		}
	}
	if !ok || value == nil {
		return "", false, fmt.Errorf("实例信息不是对象")
	}
	if nested, nestedOK := value["metadata"].(map[string]interface{}); nestedOK {
		value = nested
	}
	typeValue := strings.ToLower(strings.TrimSpace(fmt.Sprint(value["type"])))
	kind := ""
	switch typeValue {
	case "virtual-machine", "virtual_machine", "vm":
		kind = "vm"
	case "container":
		kind = "container"
	}
	status := strings.ToLower(strings.TrimSpace(fmt.Sprint(value["status"])))
	return kind, status == "running", nil
}

func probeLXDLikeGuestShell(executor utils.ShellExecutor, cli, identifier string) string {
	quoted := utils.ShellSingleQuote(identifier)
	unixProbe := fmt.Sprintf("%s exec %s -- sh -c ':'", cli, quoted)
	windowsProbe := fmt.Sprintf("%s exec %s -- cmd.exe /c exit", cli, quoted)
	command := fmt.Sprintf("%s >/dev/null 2>&1 && printf sh || %s >/dev/null 2>&1 && printf cmd", consoleProbeBoundedCommand(unixProbe), consoleProbeBoundedCommand(windowsProbe))
	output, err := executor.ExecuteWithTimeout(command, consoleRuntimeProbeTimeout)
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(output) {
	case "sh", "cmd":
		return strings.TrimSpace(output)
	default:
		return ""
	}
}

func probeLXDLikeSerialConsole(executor utils.ShellExecutor, cli, identifier string) (bool, string) {
	return probeInteractiveConsole(executor,
		fmt.Sprintf("%s console %s --type=console", cli, utils.ShellSingleQuote(identifier)),
		strings.ToUpper(cli)+" 串口")
}

// lxdLikeSPICESocketDiscoveryLines is shared by the passive capability probe
// and the explicit repair action. Keep the paths identical so a socket that is
// advertised as repairable is exactly the socket the repair will use.
func lxdLikeSPICESocketDiscoveryLines() []string {
	return []string{
		`SOCKET=""`,
		`for candidate in "/run/incus/${INSTANCE}/qemu.spice" "/var/lib/incus/containers/${INSTANCE}/qemu.spice" "/var/lib/incus/logs/${INSTANCE}/qemu.spice" "/var/snap/lxd/common/lxd/logs/${INSTANCE}/qemu.spice" "/var/lib/lxd/containers/${INSTANCE}/qemu.spice" "/var/lib/lxd/containers/${INSTANCE}/logs/qemu.spice"; do`,
		`  if [ -S "$candidate" ]; then SOCKET="$candidate"; break; fi`,
		`done`,
		`if [ -z "$SOCKET" ]; then`,
		`  for root in /run/incus /var/lib/incus /var/snap/lxd/common/lxd/logs /var/lib/lxd/containers; do`,
		`    if [ -d "$root" ]; then`,
		`      found=$(find "$root" -maxdepth 10 -type s -name qemu.spice -print 2>/dev/null | awk -v instance="$INSTANCE" -F/ '{ for (index = 1; index < NF; index++) if ($index == instance) { print; exit } }')`,
		`      if [ -n "$found" ]; then SOCKET="$found"; break; fi`,
		`    fi`,
		`  done`,
		`fi`,
	}
}

func isLXDLikeConsoleInstanceName(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) <= 128 && consoleSimpleResourceName(value)
}

func lxdLikeSPICESocketProbeCommand(instanceName string) string {
	lines := []string{
		"set -u",
		fmt.Sprintf("INSTANCE=%s", utils.ShellSingleQuote(instanceName)),
	}
	lines = append(lines, lxdLikeSPICESocketDiscoveryLines()...)
	lines = append(lines,
		`if [ -z "$SOCKET" ]; then echo "未找到运行中的 qemu.spice Unix socket" >&2; exit 20; fi`,
		`printf 'ONECLICKVIRT_SPICE_SOCKET\t%s\n' "$SOCKET"`,
	)
	return consoleProbeBoundedCommand(strings.Join(lines, "\n"))
}

func probeLXDLikeSPICESocket(executor utils.ShellExecutor, identifier string) (bool, string) {
	if !isLXDLikeConsoleInstanceName(identifier) {
		return false, "实例运行时标识不适合安全探测 qemu.spice Unix socket"
	}
	output, err := executor.ExecuteWithTimeout(lxdLikeSPICESocketProbeCommand(identifier), consoleRuntimeProbeTimeout)
	if err == nil {
		for _, line := range strings.Split(output, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "ONECLICKVIRT_SPICE_SOCKET\t/") {
				return true, ""
			}
		}
		err = fmt.Errorf("节点未返回 qemu.spice socket 探测标记")
	}
	reason := "未检测到可用的 qemu.spice Unix socket: " + err.Error()
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		reason += "；远端输出: " + utils.TruncateString(trimmed, 240)
	}
	return false, reason
}

// consoleProbeBoundedCommand keeps feature probes from waiting on an unhealthy
// guest agent. BusyBox/minimal nodes sometimes lack timeout; the executor's
// own deadline remains the fallback in that case.
func consoleProbeBoundedCommand(command string) string {
	quoted := utils.ShellSingleQuote(command)
	return "( if command -v timeout >/dev/null 2>&1; then timeout 4s sh -c " + quoted + "; else sh -c " + quoted + "; fi )"
}

func lxdLikeExecConsoleCommand(cli, identifier, shell string) string {
	quoted := utils.ShellSingleQuote(identifier)
	if shell == "cmd" {
		return fmt.Sprintf("%s exec %s -- cmd.exe", cli, quoted)
	}
	return fmt.Sprintf("%s exec %s -- %s", cli, quoted, consoleTerminalShell())
}

func probeContainerRuntimeConsole(inst providerModel.Instance, p providerModel.Provider, providerType string) consoleRuntimeProbe {
	identifier := strings.TrimSpace(inst.ProviderInstanceIdentifier())
	probe := consoleRuntimeProbe{runtimeID: identifier, runtimeKind: "container"}
	if identifier == "" {
		probe.reason = "实例缺少运行时标识，无法探测控制台"
		return probe
	}
	runtime := providerType
	if runtime == "orbstack" {
		runtime = "docker"
	}
	if runtime == "containerd" {
		runtime = "nerdctl"
	}
	executor, cleanup, err := consoleProbeExecutor(p)
	if err != nil {
		probe.reason = "无法连接节点以探测控制台: " + err.Error()
		return probe
	}
	defer cleanup()
	output, err := executor.ExecuteWithTimeout(fmt.Sprintf("%s inspect %s --format '{{.State.Running}} {{.State.Pid}} {{.Config.OpenStdin}}'", runtime, utils.ShellSingleQuote(identifier)), consoleRuntimeProbeTimeout)
	if err != nil {
		probe.reason = fmt.Sprintf("读取容器运行态失败: %v；远端输出: %s", err, utils.TruncateString(strings.TrimSpace(output), 400))
		return probe
	}
	running, pid := parseContainerRuntimeState(output)
	probe.observed = true
	probe.running = running
	if !running {
		probe.reason = "容器当前未运行，暂不提供宿主机控制台"
		return probe
	}
	if portOutput, portErr := executor.ExecuteWithTimeout(fmt.Sprintf("%s port %s", runtime, utils.ShellSingleQuote(identifier)), consoleRuntimeProbeTimeout); portErr == nil {
		// The runtime's port table contains the actual node-side mappings, which
		// can differ from panel rows after an import or manual change. Do not
		// classify them by guest port: the common resolver performs the protocol
		// handshake before any control method reaches the UI.
		probe.endpointCandidates = append(probe.endpointCandidates, collectContainerRuntimeConsoleEndpoints(portOutput, p)...)
	}
	if shell := probeContainerGuestShell(executor, runtime, identifier); shell != "" {
		probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
			Protocol: consoleProtocolExec, Command: containerRuntimeExecConsoleCommand(runtime, identifier, shell),
		})
	}
	// Attach is a terminal-control method only when the inspected container has
	// an open stdin. A running log-only container must not get a misleading
	// interactive Attach button.
	if containerRuntimeAttachEnabled(output) {
		if available, reason := probeContainerRuntimeAttach(executor, runtime, identifier); available {
			probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
				Protocol: consoleProtocolAttach, Command: fmt.Sprintf("%s attach %s", runtime, utils.ShellSingleQuote(identifier)),
			})
		} else {
			probe.terminalFailures = append(probe.terminalFailures, consoleTerminalFailure{protocol: consoleProtocolAttach, reason: reason})
		}
	}
	if pid > 0 && probeContainerNamespace(executor, runtime, identifier) {
		probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
			Protocol: consoleProtocolNamespace, Command: containerNamespaceTerminalCommand(runtime, utils.ShellSingleQuote(identifier)),
		})
	}
	return probe
}

func parseContainerRuntimeState(raw string) (bool, int) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 || !strings.EqualFold(fields[0], "true") {
		return false, 0
	}
	pid := 0
	if len(fields) > 1 {
		pid, _ = strconv.Atoi(fields[1])
	}
	return true, pid
}

func containerRuntimeAttachEnabled(raw string) bool {
	fields := strings.Fields(strings.TrimSpace(raw))
	return len(fields) >= 3 && strings.EqualFold(fields[0], "true") && strings.EqualFold(fields[2], "true")
}

func probeContainerGuestShell(executor utils.ShellExecutor, runtime, identifier string) string {
	quoted := utils.ShellSingleQuote(identifier)
	unixProbe := fmt.Sprintf("%s exec %s sh -c ':'", runtime, quoted)
	windowsProbe := fmt.Sprintf("%s exec %s cmd.exe /c exit", runtime, quoted)
	command := fmt.Sprintf("%s >/dev/null 2>&1 && printf sh || %s >/dev/null 2>&1 && printf cmd", consoleProbeBoundedCommand(unixProbe), consoleProbeBoundedCommand(windowsProbe))
	output, err := executor.ExecuteWithTimeout(command, consoleRuntimeProbeTimeout)
	if err != nil {
		return ""
	}
	shell := strings.TrimSpace(output)
	if shell == "sh" || shell == "cmd" {
		return shell
	}
	return ""
}

func containerRuntimeExecConsoleCommand(runtime, identifier, shell string) string {
	quoted := utils.ShellSingleQuote(identifier)
	if shell == "cmd" {
		return fmt.Sprintf("%s exec -it %s cmd.exe", runtime, quoted)
	}
	return fmt.Sprintf("%s exec -it %s %s", runtime, quoted, consoleTerminalShell())
}

func probeContainerNamespace(executor utils.ShellExecutor, runtime, identifier string) bool {
	quoted := utils.ShellSingleQuote(identifier)
	command := fmt.Sprintf(`PID=$(%s inspect %s --format '{{.State.Pid}}' 2>/dev/null)
case "$PID" in ''|*[!0-9]*) exit 1;; esac
command -v nsenter >/dev/null 2>&1 || exit 1
exec nsenter -t "$PID" -m -u -i -n -p -- sh -c ':'`, runtime, quoted)
	_, err := executor.ExecuteWithTimeout(consoleProbeBoundedCommand(command), consoleRuntimeProbeTimeout)
	return err == nil
}

func probeKubeVirtConsoleRuntime(inst providerModel.Instance, p providerModel.Provider) consoleRuntimeProbe {
	identifier := strings.TrimSpace(inst.ProviderInstanceIdentifier())
	probe := consoleRuntimeProbe{runtimeID: identifier}
	if identifier == "" {
		probe.reason = "实例缺少运行时标识，无法探测控制台"
		return probe
	}
	executor, cleanup, err := consoleProbeExecutor(p)
	if err != nil {
		probe.reason = "无法连接节点以探测控制台: " + err.Error()
		return probe
	}
	defer cleanup()
	quoted := utils.ShellSingleQuote(identifier)
	vmOutput, vmErr := executor.ExecuteWithTimeout(fmt.Sprintf("KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl get vmi -n kubevirt-vms %s -o json", quoted), consoleRuntimeProbeTimeout)
	if vmErr == nil && kubeVirtVMIIsRunning(vmOutput) {
		probe.observed = true
		probe.running = true
		probe.runtimeKind = "vm"
		probe.kubeVirtVNCChecked = true
		probe.kubeVirtVNC, probe.kubeVirtVNCReason = kubeVirtConsoleSubresourceReason(executor, "vnc")
		if probe.kubeVirtVNC {
			target := consoleTarget{
				protocol: consoleProtocolVNC, kubeVirtVNC: true,
				runtimeID: identifier, provider: p,
			}
			target.transport, probe.kubeVirtVNCReason = consoleTerminalTransport(p)
			if probe.kubeVirtVNCReason == "" {
				probe.kubeVirtVNC, probe.kubeVirtVNCReason = probeKubeVirtVNCConnection(target)
			}
		}
		serialAvailable, serialReason := kubeVirtConsoleSubresourceReason(executor, "console")
		if serialAvailable {
			if available, reason := probeKubeVirtSerialConsole(executor, identifier); available {
				probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
					Protocol: consoleProtocolSerial, Command: fmt.Sprintf("KUBECONFIG=/etc/rancher/k3s/k3s.yaml virtctl console -n kubevirt-vms %s", quoted),
				})
			} else {
				probe.terminalFailures = append(probe.terminalFailures, consoleTerminalFailure{protocol: consoleProtocolSerial, reason: reason})
			}
		} else {
			probe.terminalFailures = append(probe.terminalFailures, consoleTerminalFailure{protocol: consoleProtocolSerial, reason: serialReason})
		}
		if !probe.kubeVirtVNC && len(probe.terminalPlans) == 0 {
			probe.reason = "KubeVirt VMI 正在运行，但当前节点未通过 VNC 或串口 CLI/RBAC 实际校验"
		}
		return probe
	}
	pod, podReason := probeKubeVirtRunningPod(executor, identifier)
	if podReason == "" {
		probe.observed = true
		probe.running = true
		probe.runtimeKind = "container"
		if shell := probeKubeVirtPodShell(executor, pod.name); shell != "" {
			probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
				Protocol: consoleProtocolExec, Command: kubeVirtPodTerminalCommand(pod.name, shell),
			})
		}
		if pod.stdin {
			if available, reason := probeKubeVirtPodAttach(executor, pod); available {
				probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
					Protocol: consoleProtocolAttach, Command: kubeVirtPodAttachCommandForContainer(pod.name, pod.attachContainer),
				})
			} else {
				probe.terminalFailures = append(probe.terminalFailures, consoleTerminalFailure{protocol: consoleProtocolAttach, reason: reason})
			}
		}
		return probe
	}
	probe.reason = fmt.Sprintf("未在 KubeVirt 中发现运行中的 VMI 或容器 Pod；VMI 探测: %s；Pod 探测: %s", shortProbeError(vmErr, vmOutput), podReason)
	return probe
}

type kubeVirtPodSelector struct {
	labelKey string
	value    string
}

func kubeVirtPodSelectors(identifier string) []kubeVirtPodSelector {
	return []kubeVirtPodSelector{
		{labelKey: "oneclickvirt.io/instance", value: identifier},
		{labelKey: "app", value: identifier},
	}
}

// probeKubeVirtRunningPod prefers the controller's per-instance label. Older
// installations used app=<id>, so it is retained as a bounded fallback only
// after the authoritative label found no running pod.
func probeKubeVirtRunningPod(executor utils.ShellExecutor, identifier string) (kubeVirtPodConsoleInfo, string) {
	diagnostics := make([]string, 0, 2)
	for _, selector := range kubeVirtPodSelectors(identifier) {
		label := selector.labelKey + "=" + selector.value
		command := kubeVirtPodListCommand(label)
		output, err := executor.ExecuteWithTimeout(command, consoleRuntimeProbeTimeout)
		if err == nil {
			if pod, ok := kubeVirtRunningPodForLabel(output, selector.labelKey, selector.value); ok {
				return pod, ""
			}
		}
		diagnostics = append(diagnostics, label+": "+shortProbeError(err, output))
	}
	return kubeVirtPodConsoleInfo{}, strings.Join(diagnostics, "；")
}

func kubeVirtPodListCommand(label string) string {
	return fmt.Sprintf("KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl get pod -n %s -l %s -o json", kubeVirtConsoleNamespace, utils.ShellSingleQuote(label))
}

func kubeVirtVMIIsRunning(raw string) bool {
	var value struct {
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	}
	return json.Unmarshal([]byte(strings.TrimSpace(raw)), &value) == nil && strings.EqualFold(strings.TrimSpace(value.Status.Phase), "Running")
}

func kubeVirtPodIsRunning(raw string) bool {
	_, ok := kubeVirtRunningPod(raw)
	return ok
}

type kubeVirtPodConsoleInfo struct {
	name            string
	stdin           bool
	attachContainer string
}

func kubeVirtRunningPod(raw string) (kubeVirtPodConsoleInfo, bool) {
	return kubeVirtRunningPodMatching(raw, "", "", false)
}

// kubeVirtRunningPodForLabel refuses an ambiguous selector result. The
// provider query is expected to return one pod for an instance; choosing an
// arbitrary matching Pod could attach a user to a different workload.
func kubeVirtRunningPodForLabel(raw, labelKey, labelValue string) (kubeVirtPodConsoleInfo, bool) {
	return kubeVirtRunningPodMatching(raw, labelKey, labelValue, true)
}

func kubeVirtRunningPodMatching(raw, labelKey, labelValue string, rejectAmbiguity bool) (kubeVirtPodConsoleInfo, bool) {
	var value struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
			Spec struct {
				Containers []struct {
					Name  string `json:"name"`
					Stdin bool   `json:"stdin"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &value) != nil {
		return kubeVirtPodConsoleInfo{}, false
	}
	var result kubeVirtPodConsoleInfo
	matched := 0
	for _, item := range value.Items {
		if strings.EqualFold(strings.TrimSpace(item.Status.Phase), "Running") && strings.TrimSpace(item.Metadata.Name) != "" {
			if labelKey != "" && strings.TrimSpace(item.Metadata.Labels[labelKey]) != labelValue {
				continue
			}
			stdin := false
			attachContainer := ""
			for _, container := range item.Spec.Containers {
				stdin = stdin || container.Stdin
				if container.Stdin && attachContainer == "" {
					attachContainer = strings.TrimSpace(container.Name)
				}
			}
			matched++
			if rejectAmbiguity && matched > 1 {
				return kubeVirtPodConsoleInfo{}, false
			}
			result = kubeVirtPodConsoleInfo{
				name:            strings.TrimSpace(item.Metadata.Name),
				stdin:           stdin,
				attachContainer: attachContainer,
			}
		}
	}
	return result, matched == 1
}

func probeKubeVirtPodShell(executor utils.ShellExecutor, podName string) string {
	quoted := utils.ShellSingleQuote(podName)
	prefix := "KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl exec -n kubevirt-vms " + quoted + " -- "
	return probeConsoleGuestShell(executor, prefix+"sh -c ':'", prefix+"cmd.exe /c exit")
}

func shortProbeError(err error, output string) string {
	if err == nil {
		return "未返回运行态"
	}
	message := err.Error()
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		message += " (" + utils.TruncateString(trimmed, 160) + ")"
	}
	return message
}
