package admin

// console_terminal_probe.go contains short-lived probes for interactive
// provider consoles. A configuration flag, an OpenStdin bit, or an RBAC rule
// only makes a channel a candidate; the channel is advertised only after its
// own command has actually started on the live provider and remained alive
// until the bounded probe window expires.

import (
	"fmt"
	"strings"
	"time"

	"oneclickvirt/utils"
)

const (
	consoleInteractiveProbeTimeout  = 6 * time.Second
	consoleInteractiveProbeMarker   = "ONECLICKVIRT_CONSOLE_PROBE_STARTED"
	consoleInteractiveProbeTimedOut = "ONECLICKVIRT_CONSOLE_PROBE_TIMED_OUT"
)

// consoleInteractiveProbeCommand starts an interactive command with a marker
// before it is launched. A node-side timeout after the marker is a successful
// capability result: the command is expected to remain attached to the guest.
// Immediate usage/configuration/connection failures are unavailable. Requiring
// the node-side timeout prevents an Agent executor deadline from disconnecting
// its shared control channel merely because a healthy console is interactive.
//
// Some minimal provider images do not ship GNU coreutils' timeout command. The
// fallback uses a short-lived child plus a watchdog, so timeout availability is
// not treated as a console capability gate. The executor deadline remains the
// outer bound if a provider shell cannot start either form.
func consoleInteractiveProbeCommand(command string) string {
	quoted := utils.ShellSingleQuote(command)
	script := fmt.Sprintf(`run_console_probe() {
  if command -v timeout >/dev/null 2>&1; then
    timeout 4s sh -c %s
    return $?
  fi
  sh -c %s &
  child=$!
  (
    sleep 4
    if kill -0 "$child" 2>/dev/null; then
      kill "$child" >/dev/null 2>&1 || true
      printf '%s\n'
    fi
  ) &
  watchdog=$!
  wait "$child"
  status=$?
  kill "$watchdog" >/dev/null 2>&1 || true
  return "$status"
}
printf '%s\n'
run_console_probe
status=$?
case "$status" in
  124|137) printf '%s\n'; exit 0 ;;
esac
	exit "$status"`, quoted, quoted, consoleInteractiveProbeTimedOut, consoleInteractiveProbeMarker, consoleInteractiveProbeTimedOut)
	return "sh -c " + utils.ShellSingleQuote(script)
}

func probeInteractiveConsole(executor utils.ShellExecutor, command, label string) (bool, string) {
	output, err := executor.ExecuteWithTimeout(consoleInteractiveProbeCommand(command), consoleInteractiveProbeTimeout)
	started := strings.Contains(output, consoleInteractiveProbeMarker)
	remote := strings.TrimSpace(strings.ReplaceAll(output, consoleInteractiveProbeMarker, ""))
	if probeOutputHasImmediateFailure(remote) {
		return false, formatConsoleProbeFailure(label, err, remote)
	}
	if started && strings.Contains(output, consoleInteractiveProbeTimedOut) {
		return true, ""
	}
	if !started && err == nil {
		return false, label + "未返回启动标记"
	}
	if started && err == nil {
		return false, label + "启动后立即退出，未保持交互式控制台会话"
	}
	return false, formatConsoleProbeFailure(label, err, remote)
}

func probeNonInteractiveConsole(executor utils.ShellExecutor, command, label string) (bool, string) {
	output, err := executor.ExecuteWithTimeout(consoleProbeBoundedCommand(command), consoleInteractiveProbeTimeout)
	remote := strings.TrimSpace(output)
	if err == nil && !probeOutputHasImmediateFailure(remote) {
		return true, ""
	}
	return false, formatConsoleProbeFailure(label, err, remote)
}

// Keep this list specific. Generic words such as "error" can legitimately be
// printed by a guest application after an otherwise successful console start.
func probeOutputHasImmediateFailure(output string) bool {
	value := strings.ToLower(strings.TrimSpace(output))
	if value == "" {
		return false
	}
	for _, marker := range []string{
		"unknown option", "invalid option", "usage:", "command not found", " not found",
		"no such", "does not exist", "unable to", "cannot connect", "connection refused",
		"permission denied", "not permitted", "not configured", "not running", "no serial",
		"serial device", "can't open", "could not", "failed to", "failure", "parameter verification",
		"type check", "vmid:", "ctid:", "exit status 2", "exit status 1",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func formatConsoleProbeFailure(label string, err error, output string) string {
	reason := label + "实际探测失败"
	if err != nil {
		reason += ": " + err.Error()
	}
	if output != "" {
		reason += "；远端输出: " + utils.TruncateString(output, 320)
	}
	return reason
}

func probeProxmoxQEMUSerial(executor utils.ShellExecutor, runtimeID, serialInterface string) (bool, string) {
	return probeInteractiveConsole(executor,
		fmt.Sprintf("qm terminal %s --iface %s", utils.ShellSingleQuote(runtimeID), utils.ShellSingleQuote(serialInterface)),
		"PVE QEMU "+serialInterface+" 串口")
}

func probeProxmoxLXCExec(executor utils.ShellExecutor, runtimeID string) (bool, string) {
	return probeNonInteractiveConsole(executor,
		fmt.Sprintf("pct exec %s -- sh -c ':'", utils.ShellSingleQuote(runtimeID)),
		"PVE LXC Exec")
}

func probeProxmoxLXCConsole(executor utils.ShellExecutor, runtimeID string) (bool, string) {
	return probeInteractiveConsole(executor,
		fmt.Sprintf("pct console %s", utils.ShellSingleQuote(runtimeID)),
		"PVE LXC 串口")
}

func probeLibvirtSerialConsole(executor utils.ShellExecutor, identifier, uri string) (bool, string) {
	if uri != "qemu:///system" && uri != "lxc:///" {
		return false, "Libvirt domain 连接无效"
	}
	return probeInteractiveConsole(executor,
		fmt.Sprintf("virsh -c %s console %s", utils.ShellSingleQuote(uri), utils.ShellSingleQuote(identifier)),
		"Libvirt 串口")
}

func probeKubeVirtSerialConsole(executor utils.ShellExecutor, identifier string) (bool, string) {
	return probeInteractiveConsole(executor,
		fmt.Sprintf("KUBECONFIG=/etc/rancher/k3s/k3s.yaml virtctl console -n %s %s", kubeVirtConsoleNamespace, utils.ShellSingleQuote(identifier)),
		"KubeVirt 串口")
}

func probeContainerRuntimeAttach(executor utils.ShellExecutor, runtime, identifier string) (bool, string) {
	// stdin is deliberately closed for the probe. It proves the runtime can
	// attach without sending any guest input; the real browser session gets a
	// PTY and forwards input after the user explicitly selects Attach.
	return probeInteractiveConsole(executor,
		fmt.Sprintf("%s attach --no-stdin %s", runtime, utils.ShellSingleQuote(identifier)),
		runtime+" Attach")
}

func probeKubeVirtPodAttach(executor utils.ShellExecutor, pod kubeVirtPodConsoleInfo) (bool, string) {
	// Do not send guest input while probing. The attach subresource must still
	// open a live stream and remain attached until the bounded node-side timeout
	// before the browser receives an Attach option.
	return probeInteractiveConsole(executor,
		kubeVirtPodAttachProbeCommand(pod.name, pod.attachContainer),
		"KubeVirt Pod Attach")
}
