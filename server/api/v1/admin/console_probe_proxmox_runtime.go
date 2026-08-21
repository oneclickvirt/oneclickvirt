package admin

// console_probe_proxmox_runtime.go contains the PVE-specific runtime checks.
// PVE discovers the actual qemu/lxc object first, then validates each console
// protocol independently instead of trusting the panel's instance type.

import (
	"encoding/json"
	"fmt"
	"strings"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
)

func probeProxmoxConsoleRuntime(inst providerModel.Instance, p providerModel.Provider) consoleRuntimeProbe {
	runtime, err := resolveProxmoxConsoleRuntime(inst, p)
	if err != nil {
		return consoleRuntimeProbe{runtimeID: inst.ProviderInstanceIdentifier(), reason: "无法读取 PVE 实际运行态: " + err.Error()}
	}
	probe := consoleRuntimeProbe{
		runtimeID: runtime.ID, runtimeKind: runtime.Type, node: runtime.Node, observed: true,
	}
	probe.running = strings.EqualFold(strings.TrimSpace(runtime.Status), "running")
	if !probe.running {
		status := strings.TrimSpace(runtime.Status)
		if status == "" {
			probe.reason = "PVE 未返回实例运行态，拒绝推断控制台能力"
		} else {
			probe.reason = "PVE 实例当前状态为 " + status + "，暂不提供宿主机控制台"
		}
		return probe
	}
	executor, cleanup, err := consoleProbeExecutor(p)
	if err != nil {
		probe.reason = "无法读取 PVE 控制台配置: " + err.Error()
		return probe
	}
	defer cleanup()

	switch runtime.Type {
	case "qemu":
		// A PVE qemu display is exposed through a short-lived vncproxy rather
		// than a stable TCP port. Validate that proxy with an RFB handshake so
		// the capability result reflects the live node, not only the stored
		// vga setting. The proxy is closed immediately and no browser session is
		// created by the capability request.
		probe.proxmoxVNCChecked = true
		// `vga` is only an observed configuration hint, not capability proof.
		// PVE can expose a working vncproxy through devices that older config
		// parsers do not recognize, so always verify the live proxy/RFB exchange.
		target := consoleTarget{
			protocol: consoleProtocolVNC, proxmoxVNC: true,
			runtimeID: runtime.ID, runtimeNode: runtime.Node, provider: p,
		}
		target.transport, probe.proxmoxVNCReason = proxmoxConsoleVNCTransport(p)
		if probe.proxmoxVNCReason == "" {
			probe.proxmoxVNC, probe.proxmoxVNCReason = probeProxmoxVNCConnection(target)
		}

		// PVE's vncproxy does not depend on successfully reading the guest
		// config. Keep configuration as a serial candidate hint only: a transient
		// pvesh/config ACL failure must not hide a VNC endpoint that just completed
		// its real RFB authentication probe.
		serialInterfaces := []string{"serial0"}
		configReason := ""
		command, commandErr := proxmoxConsoleConfigCommand(runtime)
		if commandErr != nil {
			configReason = commandErr.Error()
		} else if output, configErr := executor.ExecuteWithTimeout(command, consoleRuntimeProbeTimeout); configErr != nil {
			configReason = fmt.Sprintf("读取 PVE QEMU 配置失败: %v；远端输出: %s", configErr, utils.TruncateString(strings.TrimSpace(output), 400))
		} else if config, parseErr := parseProxmoxConsoleConfig(output); parseErr != nil {
			configReason = "解析 PVE QEMU 控制台配置失败: " + parseErr.Error()
		} else {
			serialInterfaces = proxmoxSerialProbeInterfaces(config)
		}
		serialReasons := make([]string, 0, 1)
		if configReason != "" {
			serialReasons = append(serialReasons, configReason)
		}
		for _, serialInterface := range serialInterfaces {
			if available, reason := probeProxmoxQEMUSerial(executor, runtime.ID, serialInterface); available {
				probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
					Protocol: consoleProtocolSerial, Command: fmt.Sprintf("qm terminal %s --iface %s", utils.ShellSingleQuote(runtime.ID), utils.ShellSingleQuote(serialInterface)),
				})
				serialReasons = nil
				break
			} else {
				serialReasons = append(serialReasons, reason)
			}
		}
		if len(serialReasons) > 0 {
			probe.terminalFailures = append(probe.terminalFailures, consoleTerminalFailure{
				protocol: consoleProtocolSerial, reason: strings.Join(serialReasons, "；"),
			})
		}
	case "lxc":
		if available, reason := probeProxmoxLXCExec(executor, runtime.ID); available {
			probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
				Protocol: consoleProtocolExec, Command: fmt.Sprintf("pct enter %s", utils.ShellSingleQuote(runtime.ID)),
			})
		} else {
			probe.terminalFailures = append(probe.terminalFailures, consoleTerminalFailure{protocol: consoleProtocolExec, reason: reason})
		}
		// A tty=0 row is not enough to suppress the protocol: imported and
		// manually modified containers can still expose a console. Let the live
		// command be the authority and keep its diagnostic for failed probes.
		if available, reason := probeProxmoxLXCConsole(executor, runtime.ID); available {
			probe.terminalPlans = append(probe.terminalPlans, InstanceConsoleTerminalPlan{
				Protocol: consoleProtocolSerial, Command: fmt.Sprintf("pct console %s", utils.ShellSingleQuote(runtime.ID)),
			})
		} else {
			probe.terminalFailures = append(probe.terminalFailures, consoleTerminalFailure{protocol: consoleProtocolSerial, reason: reason})
		}
	}
	return probe
}

func proxmoxConsoleConfigCommand(runtime proxmoxConsoleRuntime) (string, error) {
	if !isProxmoxConsoleRuntimeID(runtime.ID) {
		return "", fmt.Errorf("PVE 控制台缺少有效 VMID/CTID")
	}
	runtimeType := normalizeProxmoxConsoleRuntimeType(runtime.Type)
	if runtimeType == "" {
		return "", fmt.Errorf("PVE 控制台运行时类型无效")
	}
	node := strings.TrimSpace(runtime.Node)
	if node == "" {
		return fmt.Sprintf("pvesh get \"/nodes/$(hostname)/%s/%s/config\" --output-format json", runtimeType, runtime.ID), nil
	}
	for _, char := range node {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '.' && char != '-' && char != '_' {
			return "", fmt.Errorf("PVE 节点名包含不支持的字符")
		}
	}
	return fmt.Sprintf("pvesh get %s --output-format json", utils.ShellSingleQuote(fmt.Sprintf("/nodes/%s/%s/%s/config", node, runtimeType, runtime.ID))), nil
}

func parseProxmoxConsoleConfig(raw string) (map[string]interface{}, error) {
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &config); err != nil {
		return nil, err
	}
	if nested, ok := config["data"].(map[string]interface{}); ok {
		return nested, nil
	}
	return config, nil
}

func proxmoxConfigString(config map[string]interface{}, key string) string {
	value, ok := config[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

// proxmoxSerialProbeInterfaces derives small candidate set from the live PVE
// config only to choose commands. Every candidate is then started through
// qm terminal; config alone never makes the Serial option available. Probe the
// normal default serial0 too when configuration has no explicit serial device.
func proxmoxSerialProbeInterfaces(config map[string]interface{}) []string {
	interfaces := make([]string, 0, 4)
	for index := 0; index < 4; index++ {
		value := strings.ToLower(proxmoxConfigString(config, fmt.Sprintf("serial%d", index)))
		if value != "" && value != "none" {
			interfaces = append(interfaces, fmt.Sprintf("serial%d", index))
		}
	}
	if len(interfaces) == 0 {
		return []string{"serial0"}
	}
	return interfaces
}
