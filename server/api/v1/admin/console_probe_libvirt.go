package admin

// Libvirt domains are not assumed to be QEMU VMs. The runtime probe first
// resolves the domain through supported libvirt connections and only then
// tests serial/display channels returned by that live domain.

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
)

type libvirtConsoleObservation struct {
	uri     string
	kind    string
	state   string
	serial  string
	display string
}

func probeLibvirtConsoleRuntime(inst providerModel.Instance, p providerModel.Provider) consoleRuntimeProbe {
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

	output, err := executor.ExecuteWithTimeout(libvirtConsoleProbeCommand(identifier), consoleRuntimeProbeTimeout)
	if err != nil {
		probe.reason = fmt.Sprintf("读取 Libvirt 实例运行态失败: %v；远端输出: %s", err, utils.TruncateString(strings.TrimSpace(output), 400))
		return probe
	}
	observed := parseLibvirtConsoleObservation(output)
	if observed.uri == "" || observed.kind == "" {
		probe.reason = "Libvirt 未返回已验证的 domain 连接，拒绝推断控制台能力"
		return probe
	}
	probe.observed = true
	probe.runtimeKind = observed.kind
	probe.running, probe.reason = libvirtConsoleRunningState(observed.state)
	if !probe.running {
		return probe
	}

	// ttyconsole is only a candidate. The interactive virsh console command
	// must stay attached for the bounded probe window before Serial is shown.
	if available, reason := probeLibvirtSerialConsole(executor, identifier, observed.uri); available {
		probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
			Protocol: consoleProtocolSerial,
			Command:  fmt.Sprintf("virsh -c %s console %s", utils.ShellSingleQuote(observed.uri), utils.ShellSingleQuote(identifier)),
		})
	} else {
		probe.terminalFailures = append(probe.terminalFailures, consoleTerminalFailure{protocol: consoleProtocolSerial, reason: reason})
	}
	if endpoint, ok := parseLibvirtVNCDisplay(observed.display, p); ok {
		probe.vncEndpoints = append(probe.vncEndpoints, endpoint)
	}
	if target, ok := parseLibvirtSPICEDisplay(observed.display, p); ok {
		probe.nativeTargets = append(probe.nativeTargets, target)
	}
	return probe
}

func libvirtConsoleProbeCommand(identifier string) string {
	quoted := utils.ShellSingleQuote(identifier)
	script := fmt.Sprintf(`if virsh -c qemu:///system dominfo %s >/dev/null 2>&1; then
  URI='qemu:///system'; KIND='vm'
elif virsh -c lxc:/// dominfo %s >/dev/null 2>&1; then
  URI='lxc:///'; KIND='container'
else
  echo '未在 qemu:///system 或 lxc:/// 中找到实例' >&2
  exit 20
fi
printf 'ONECLICKVIRT_CONSOLE\turi\t%%s\n' "$URI"
printf 'ONECLICKVIRT_CONSOLE\tkind\t%%s\n' "$KIND"
STATE=$(virsh -c "$URI" domstate %s 2>/dev/null || true)
TTY=$(virsh -c "$URI" ttyconsole %s 2>/dev/null || true)
DISPLAY=$(virsh -c "$URI" domdisplay %s 2>/dev/null || true)
[ -n "$STATE" ] && printf 'ONECLICKVIRT_CONSOLE\tstate\t%%s\n' "$STATE"
[ -n "$TTY" ] && printf 'ONECLICKVIRT_CONSOLE\tserial\t%%s\n' "$TTY"
[ -n "$DISPLAY" ] && printf 'ONECLICKVIRT_CONSOLE\tdisplay\t%%s\n' "$DISPLAY"`, quoted, quoted, quoted, quoted, quoted)
	return "sh -c " + utils.ShellSingleQuote(script)
}

func parseLibvirtConsoleObservation(raw string) libvirtConsoleObservation {
	var result libvirtConsoleObservation
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(parts) != 3 || parts[0] != "ONECLICKVIRT_CONSOLE" {
			continue
		}
		switch parts[1] {
		case "uri":
			result.uri = strings.TrimSpace(parts[2])
		case "kind":
			result.kind = normalizeConsoleRuntimeKind(parts[2])
		case "state":
			result.state = strings.TrimSpace(parts[2])
		case "serial":
			result.serial = strings.TrimSpace(parts[2])
		case "display":
			result.display = strings.TrimSpace(parts[2])
		}
	}
	return result
}

// parseLibvirtConsoleProbe remains a small compatibility helper for existing
// callers/tests that only need the observed state, serial hint and display.
func parseLibvirtConsoleProbe(raw string) (string, string, string) {
	observed := parseLibvirtConsoleObservation(raw)
	return observed.state, observed.serial, observed.display
}

func libvirtConsoleRunningState(state string) (bool, string) {
	state = strings.TrimSpace(state)
	if state == "" {
		return false, "Libvirt 未返回实例运行态，拒绝推断控制台能力"
	}
	if !strings.EqualFold(state, "running") {
		return false, "Libvirt 实例当前状态为 " + state + "，暂不提供宿主机控制台"
	}
	return true, ""
}

func parseLibvirtVNCDisplay(raw string, p providerModel.Provider) (consoleDiscoveredEndpoint, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, "vnc") || parsed.Hostname() == "" || parsed.Port() == "" {
		return consoleDiscoveredEndpoint{}, false
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return consoleDiscoveredEndpoint{}, false
	}
	return consoleDiscoveredEndpoint{
		protocol: consoleProtocolVNC, host: parsed.Hostname(), port: port, transport: normalizeConsoleTransport(p, ""),
	}, true
}

// Libvirt can report a direct SPICE listener instead of VNC. Browsers do not
// speak SPICE natively, so expose it as an external-client action only when
// libvirt reports a non-loopback listener that is also one of this node's
// configured public addresses.
func parseLibvirtSPICEDisplay(raw string, p providerModel.Provider) (nativeConsoleTarget, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, "spice") || parsed.Hostname() == "" || parsed.Port() == "" {
		return nativeConsoleTarget{}, false
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return nativeConsoleTarget{}, false
	}
	host := strings.Trim(strings.TrimSpace(parsed.Hostname()), "[]")
	if host == "0.0.0.0" || host == "::" {
		host = consoleHost(p)
	}
	if host == "" || isConsoleLoopbackHost(host) {
		return nativeConsoleTarget{}, false
	}
	for _, candidate := range consoleTrustedURLHosts(p) {
		if consoleHostsEqual(host, candidate) {
			return nativeConsoleTarget{protocol: consoleProtocolNative, url: "spice://" + joinConsoleURLHostPort(host, port)}, true
		}
	}
	return nativeConsoleTarget{}, false
}
