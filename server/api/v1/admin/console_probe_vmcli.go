package admin

// This file contains bounded runtime probes for providers whose normal control
// plane is a host-side CLI rather than a long-lived API. None of these probes
// infer a console from Instance.InstanceType: every entry is emitted only after
// its provider reports a running resource and its own configuration exposes a
// usable channel.

import (
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
)

// collectContainerRuntimeConsoleEndpoints turns the runtime's live port table
// into protocol-agnostic candidates. Unlike the database projection, this also
// sees ports allocated by imported containers or node-side dynamic mappings.
// Guest port numbers are intentionally ignored: the shared resolver identifies
// VNC/RDP/SPICE/SSH only by a real protocol handshake.
func collectContainerRuntimeConsoleEndpoints(raw string, p providerModel.Provider) []consoleEndpointCandidate {
	candidates := make([]consoleEndpointCandidate, 0, 2)
	for _, line := range strings.Split(raw, "\n") {
		if len(candidates) >= consoleMappedEndpointProbeLimit {
			break
		}
		line = strings.TrimSpace(line)
		left, right, ok := strings.Cut(line, "->")
		if !ok {
			continue
		}
		left = strings.TrimSpace(left)
		_, protocol, ok := strings.Cut(left, "/")
		if !ok || !strings.EqualFold(strings.TrimSpace(protocol), "tcp") {
			continue
		}
		host, hostPort := utils.ParseEndpoint(strings.TrimSpace(right), 0)
		if strings.HasPrefix(strings.TrimSpace(right), ":::") {
			host, hostPort = "::", 0
			if port, parseErr := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(right), ":::")); parseErr == nil {
				hostPort = port
			}
		}
		if hostPort < 1 || hostPort > 65535 {
			continue
		}
		transport := "direct"
		switch {
		case host == "" || host == "0.0.0.0" || host == "::" || host == "*":
			host = consoleHost(p)
		case isConsoleLoopbackHost(host):
			host = "127.0.0.1"
			transport = normalizeConsoleTransport(p, "")
			if transport != "ssh" && transport != "agent" && transport != "local" {
				continue
			}
		}
		if host == "" {
			continue
		}
		candidates = appendConsoleEndpointCandidate(candidates, consoleEndpointCandidate{
			host: host, port: hostPort, transport: transport, provider: p,
		})
	}
	return candidates
}

func consoleProviderStoragePath(p providerModel.Provider, providerType string) string {
	if value := strings.TrimRight(strings.TrimSpace(p.StoragePoolPath), "/"); value != "" {
		return value
	}
	pool := strings.TrimSpace(p.StoragePool)
	if pool == "" || strings.EqualFold(pool, "local") {
		return path.Join("/var/lib/oneclickvirt", providerType)
	}
	if strings.HasPrefix(pool, "/") || strings.HasPrefix(pool, "~") {
		return strings.TrimRight(pool, "/")
	}
	return path.Join("/var/lib/oneclickvirt", providerType, pool)
}

func consoleSimpleResourceName(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." &&
		!strings.ContainsAny(value, "/\\\r\n\x00")
}

// nodeLocalConsoleEndpoint represents a listener that the provider reports on
// its host. Prefer the configured SSH/Agent/local channel for loopback-only
// listeners; a direct provider is used only when the node has an actual public
// console address configured. refreshVNCConsoleTarget validates the RFB banner
// before this candidate reaches the UI.
func nodeLocalConsoleEndpoint(p providerModel.Provider, protocol string, port int) (consoleDiscoveredEndpoint, bool) {
	if port < 1 || port > 65535 {
		return consoleDiscoveredEndpoint{}, false
	}
	transport := normalizeConsoleTransport(p, "")
	switch transport {
	case "ssh", "agent", "local":
		return consoleDiscoveredEndpoint{protocol: protocol, host: "127.0.0.1", port: port, transport: transport}, true
	case "direct", "":
		host := consoleHost(p)
		if host == "" {
			return consoleDiscoveredEndpoint{}, false
		}
		return consoleDiscoveredEndpoint{protocol: protocol, host: host, port: port, transport: "direct"}, true
	default:
		return consoleDiscoveredEndpoint{}, false
	}
}

func probeVMwareConsoleRuntime(inst providerModel.Instance, p providerModel.Provider) consoleRuntimeProbe {
	identifier := strings.TrimSpace(inst.ProviderInstanceIdentifier())
	probe := consoleRuntimeProbe{runtimeID: identifier, runtimeKind: "vm"}
	if identifier == "" {
		probe.reason = "实例缺少运行时标识，无法探测 VMware 控制台"
		return probe
	}
	executor, cleanup, err := consoleProbeExecutor(p)
	if err != nil {
		probe.reason = "无法连接节点以探测 VMware 控制台: " + err.Error()
		return probe
	}
	defer cleanup()

	output, err := executor.ExecuteWithTimeout(vmwareConsoleProbeCommand(identifier, consoleProviderStoragePath(p, "vmware")), consoleRuntimeProbeTimeout)
	if err != nil {
		probe.reason = fmt.Sprintf("读取 VMware 实例运行态失败: %v；远端输出: %s", err, utils.TruncateString(strings.TrimSpace(output), 400))
		return probe
	}
	probe.observed = true
	state, vncPort := parseVMwareConsoleProbe(output)
	probe.running = state == "running"
	if !probe.running {
		probe.reason = "VMware 实例当前未运行，暂不提供控制台"
		return probe
	}
	if endpoint, ok := nodeLocalConsoleEndpoint(p, consoleProtocolVNC, vncPort); ok {
		probe.vncEndpoints = append(probe.vncEndpoints, endpoint)
	}
	return probe
}

func vmwareConsoleProbeCommand(identifier, library string) string {
	return "sh -c " + utils.ShellSingleQuote(fmt.Sprintf(`set -eu
ID=%s
LIBRARY=%s
RUNNING="$(vmrun list 2>/dev/null || true)"
VMX=""
case "$ID" in
  *.vmx) [ -f "$ID" ] && VMX="$ID" ;;
esac
if [ -z "$VMX" ]; then
  while IFS= read -r candidate; do
    [ -n "$candidate" ] || continue
    base="${candidate##*/}"; stem="${base%%.vmx}"
	    parent="${candidate%%/*}"; parent="${parent##*/}"
    if [ "$candidate" = "$ID" ] || [ "$stem" = "$ID" ] || [ "$parent" = "$ID" ]; then VMX="$candidate"; break; fi
  done <<EOF
$RUNNING
EOF
fi
if [ -z "$VMX" ] && [ -f "$LIBRARY/instances/$ID/$ID.vmx" ]; then VMX="$LIBRARY/instances/$ID/$ID.vmx"; fi
[ -n "$VMX" ] || { echo "VMware 运行实例未找到" >&2; exit 20; }
STATE=stopped
printf '%%s\n' "$RUNNING" | grep -F -x -- "$VMX" >/dev/null 2>&1 && STATE=running
printf 'ONECLICKVIRT_CONSOLE\tstate\t%%s\n' "$STATE"
[ "$STATE" = running ] || exit 0
	# The VMX flag and port are candidates only. A configured disabled/stale port
	# is harmless here because the shared RFB probe decides whether VNC is really
	# reachable before it is shown to the user.
	PORT="$(awk -F'"' 'tolower($1) ~ /^[[:space:]]*remotedisplay\.vnc\.port[[:space:]]*=/ {print $2; exit}' "$VMX" 2>/dev/null || true)"
case "$PORT" in ''|*[!0-9]*) exit 0 ;; esac
[ "$PORT" -gt 0 ] 2>/dev/null && [ "$PORT" -le 65535 ] 2>/dev/null || exit 0
printf 'ONECLICKVIRT_CONSOLE\tvnc\t%%s\n' "$PORT"`, utils.ShellSingleQuote(identifier), utils.ShellSingleQuote(library)))
}

func parseVMwareConsoleProbe(raw string) (string, int) {
	state, vncPort := "", 0
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(parts) != 3 || parts[0] != "ONECLICKVIRT_CONSOLE" {
			continue
		}
		switch parts[1] {
		case "state":
			state = strings.ToLower(strings.TrimSpace(parts[2]))
		case "vnc":
			if port, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil && port > 0 && port <= 65535 {
				vncPort = port
			}
		}
	}
	return state, vncPort
}

func probeVirtualBoxConsoleRuntime(inst providerModel.Instance, p providerModel.Provider) consoleRuntimeProbe {
	identifier := strings.TrimSpace(inst.ProviderInstanceIdentifier())
	probe := consoleRuntimeProbe{runtimeID: identifier, runtimeKind: "vm"}
	if identifier == "" {
		probe.reason = "实例缺少运行时标识，无法探测 VirtualBox 控制台"
		return probe
	}
	executor, cleanup, err := consoleProbeExecutor(p)
	if err != nil {
		probe.reason = "无法连接节点以探测 VirtualBox 控制台: " + err.Error()
		return probe
	}
	defer cleanup()
	output, err := executor.ExecuteWithTimeout("VBoxManage showvminfo "+utils.ShellSingleQuote(identifier)+" --machinereadable", consoleRuntimeProbeTimeout)
	if err != nil {
		probe.reason = fmt.Sprintf("读取 VirtualBox 实例运行态失败: %v；远端输出: %s", err, utils.TruncateString(strings.TrimSpace(output), 400))
		return probe
	}
	probe.observed = true
	state, _, vrdeHost, vrdePort := parseVirtualBoxConsoleInfo(output)
	probe.running = state == "running"
	if !probe.running {
		probe.reason = "VirtualBox 实例当前未运行，暂不提供控制台"
		return probe
	}
	// VRDE configuration only yields an endpoint candidate. The native RDP
	// health check below is the source of truth, including when imported data
	// says VRDE is disabled while a listener is still present.
	if vrdePort > 0 {
		if host := virtualBoxVRDEHost(vrdeHost, p); host != "" {
			probe.nativeTargets = append(probe.nativeTargets, nativeConsoleTarget{
				protocol: "rdp", url: "rdp://" + joinConsoleURLHostPort(host, vrdePort),
			})
		}
	}
	return probe
}

func parseVirtualBoxConsoleInfo(raw string) (string, bool, string, int) {
	values := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		values[strings.ToLower(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	state := strings.ToLower(strings.TrimSpace(values["vmstate"]))
	vrdeEnabled := strings.EqualFold(values["vrde"], "on") || strings.EqualFold(values["vrde"], "true")
	port := 0
	for _, key := range []string{"vrdeport", "vrdeports"} {
		value := strings.TrimSpace(strings.Split(values[key], ",")[0])
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 && parsed <= 65535 {
			port = parsed
			break
		}
	}
	return state, vrdeEnabled, strings.TrimSpace(values["vrdeaddress"]), port
}

// VRDE is consumed by a local RDP client, so a node-only loopback listener
// cannot be advertised as a browser action. Wildcard binds use the configured
// public node host; an explicit address must itself be a configured node host.
func virtualBoxVRDEHost(address string, p providerModel.Provider) string {
	address = strings.Trim(strings.TrimSpace(address), "[]")
	if address == "" || address == "0.0.0.0" || address == "::" || address == "*" {
		return consoleHost(p)
	}
	if isConsoleLoopbackHost(address) {
		return ""
	}
	for _, trusted := range consoleTrustedURLHosts(p) {
		if consoleHostsEqual(address, trusted) {
			return address
		}
	}
	return ""
}

func probeMultipassConsoleRuntime(inst providerModel.Instance, p providerModel.Provider) consoleRuntimeProbe {
	identifier := strings.TrimSpace(inst.ProviderInstanceIdentifier())
	probe := consoleRuntimeProbe{runtimeID: identifier, runtimeKind: "vm"}
	if identifier == "" {
		probe.reason = "实例缺少运行时标识，无法探测 Multipass 控制台"
		return probe
	}
	executor, cleanup, err := consoleProbeExecutor(p)
	if err != nil {
		probe.reason = "无法连接节点以探测 Multipass 控制台: " + err.Error()
		return probe
	}
	defer cleanup()
	output, err := executor.ExecuteWithTimeout("multipass info "+utils.ShellSingleQuote(identifier)+" --format json", consoleRuntimeProbeTimeout)
	if err != nil {
		probe.reason = fmt.Sprintf("读取 Multipass 实例运行态失败: %v；远端输出: %s", err, utils.TruncateString(strings.TrimSpace(output), 400))
		return probe
	}
	state, err := parseMultipassConsoleState(output)
	if err != nil {
		probe.reason = "解析 Multipass 实例运行态失败: " + err.Error()
		return probe
	}
	probe.observed = true
	probe.running = state == "running"
	if !probe.running {
		probe.reason = "Multipass 实例当前状态为 " + state + "，暂不提供控制台"
		return probe
	}
	if shell := probeMultipassGuestShell(executor, identifier); shell != "" {
		probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
			Protocol: consoleProtocolExec, Command: multipassExecConsoleCommand(identifier, shell),
		})
	}
	return probe
}

func parseMultipassConsoleState(raw string) (string, error) {
	var value interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &value); err != nil {
		return "", err
	}
	states := make([]string, 0, 1)
	var collect func(interface{})
	collect = func(node interface{}) {
		switch current := node.(type) {
		case map[string]interface{}:
			for key, child := range current {
				if strings.EqualFold(strings.TrimSpace(key), "state") {
					if state, ok := child.(string); ok && strings.TrimSpace(state) != "" {
						states = append(states, strings.ToLower(strings.TrimSpace(state)))
					}
				}
				collect(child)
			}
		case []interface{}:
			for _, child := range current {
				collect(child)
			}
		}
	}
	collect(value)
	if len(states) == 0 {
		return "", fmt.Errorf("实例信息未返回 state")
	}
	return states[0], nil
}

func probeMultipassGuestShell(executor utils.ShellExecutor, identifier string) string {
	quoted := utils.ShellSingleQuote(identifier)
	unixProbe := "multipass exec " + quoted + " -- sh -c ':'"
	windowsProbe := "multipass exec " + quoted + " -- cmd.exe /c exit"
	return probeConsoleGuestShell(executor, unixProbe, windowsProbe)
}

func multipassExecConsoleCommand(identifier, shell string) string {
	quoted := utils.ShellSingleQuote(identifier)
	if shell == "cmd" {
		return "multipass exec " + quoted + " -- cmd.exe"
	}
	return "multipass exec " + quoted + " -- " + consoleTerminalShell()
}

func probeVagrantConsoleRuntime(inst providerModel.Instance, p providerModel.Provider) consoleRuntimeProbe {
	identifier := strings.TrimSpace(inst.ProviderInstanceIdentifier())
	probe := consoleRuntimeProbe{runtimeID: identifier, runtimeKind: "vm"}
	if !consoleSimpleResourceName(identifier) {
		probe.reason = "Vagrant 实例标识无效，无法安全探测控制台"
		return probe
	}
	directory := path.Join(consoleProviderStoragePath(p, "vagrant"), identifier)
	executor, cleanup, err := consoleProbeExecutor(p)
	if err != nil {
		probe.reason = "无法连接节点以探测 Vagrant 控制台: " + err.Error()
		return probe
	}
	defer cleanup()
	command := "test -f " + utils.ShellSingleQuote(path.Join(directory, "Vagrantfile")) + " && cd " + utils.ShellSingleQuote(directory) + " && vagrant status --machine-readable"
	output, err := executor.ExecuteWithTimeout(command, consoleRuntimeProbeTimeout)
	if err != nil {
		probe.reason = fmt.Sprintf("读取 Vagrant 实例运行态失败: %v；远端输出: %s", err, utils.TruncateString(strings.TrimSpace(output), 400))
		return probe
	}
	state := parseVagrantConsoleState(output)
	if state == "" {
		probe.reason = "Vagrant 未返回实例运行状态"
		return probe
	}
	probe.observed = true
	probe.running = state == "running"
	if !probe.running {
		probe.reason = "Vagrant 实例当前状态为 " + state + "，暂不提供控制台"
		return probe
	}
	if probeVagrantGuestShell(executor, directory) {
		probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
			Protocol: consoleProtocolExec, Command: "cd " + utils.ShellSingleQuote(directory) + " && exec vagrant ssh",
		})
	}
	return probe
}

func parseVagrantConsoleState(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Split(strings.TrimSpace(line), ",")
		if len(parts) >= 4 && strings.EqualFold(strings.TrimSpace(parts[2]), "state") {
			return strings.ToLower(strings.TrimSpace(parts[3]))
		}
	}
	return ""
}

func probeVagrantGuestShell(executor utils.ShellExecutor, directory string) bool {
	command := "cd " + utils.ShellSingleQuote(directory) + " && vagrant ssh -c ':'"
	_, err := executor.ExecuteWithTimeout(consoleProbeBoundedCommand(command), consoleRuntimeProbeTimeout)
	return err == nil
}

func probeConsoleGuestShell(executor utils.ShellExecutor, unixProbe, windowsProbe string) string {
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
