package admin

import (
	"fmt"
	"strings"
	"time"

	"oneclickvirt/constant"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
)

// InstanceConsoleTerminalPlan describes a terminal-style console launched on
// the provider host. It intentionally contains only commands generated from
// fixed provider templates and a shell-quoted provider identifier.
type InstanceConsoleTerminalPlan struct {
	Protocol string
	Command  string
}

// InstanceConsoleTerminalTarget is the small, authenticated hand-off between
// the capability resolver and the WebSocket handler. Commands remain private
// to the server; the browser only receives the supported protocol name.
type InstanceConsoleTerminalTarget struct {
	Protocol        string
	Command         string
	ProviderID      uint
	ConnectionType  string
	Provider        providerModel.Provider
	InstanceID      uint
	InstanceName    string
	InstanceType    string
	ProviderRuntime string
}

const (
	consoleProtocolSerial = "serial"
)

func consoleTerminalShell() string {
	return `sh -c 'cd /root 2>/dev/null || cd /; if command -v bash >/dev/null 2>&1; then exec bash; fi; exec sh'`
}

func consoleTerminalPlans(providerType, instanceType, identifier string) []InstanceConsoleTerminalPlan {
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	instanceType = strings.ToLower(strings.TrimSpace(instanceType))
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil
	}
	quotedID := utils.ShellSingleQuote(identifier)
	appendContainerPlans := func(runtime string) []InstanceConsoleTerminalPlan {
		return []InstanceConsoleTerminalPlan{
			{Protocol: consoleProtocolExec, Command: fmt.Sprintf("%s exec -it %s %s", runtime, quotedID, consoleTerminalShell())},
			{Protocol: consoleProtocolAttach, Command: fmt.Sprintf("%s attach %s", runtime, quotedID)},
			{Protocol: consoleProtocolNamespace, Command: containerNamespaceTerminalCommand(runtime, quotedID)},
		}
	}

	if instanceType != "vm" {
		switch providerType {
		case "docker", "orbstack":
			return appendContainerPlans("docker")
		case "podman":
			return appendContainerPlans("podman")
		case "containerd":
			return appendContainerPlans("nerdctl")
		case "lxd":
			return []InstanceConsoleTerminalPlan{
				{Protocol: consoleProtocolExec, Command: fmt.Sprintf("lxc exec %s -- %s", quotedID, consoleTerminalShell())},
				{Protocol: consoleProtocolSerial, Command: fmt.Sprintf("lxc console %s", quotedID)},
			}
		case "incus":
			return []InstanceConsoleTerminalPlan{
				{Protocol: consoleProtocolExec, Command: fmt.Sprintf("incus exec %s -- %s", quotedID, consoleTerminalShell())},
				{Protocol: consoleProtocolSerial, Command: fmt.Sprintf("incus console %s", quotedID)},
			}
		case "proxmox", "proxmoxve", "pve":
			return []InstanceConsoleTerminalPlan{
				{Protocol: consoleProtocolExec, Command: fmt.Sprintf("pct enter %s", quotedID)},
				{Protocol: consoleProtocolSerial, Command: fmt.Sprintf("pct console %s", quotedID)},
			}
		case "kubevirt":
			return []InstanceConsoleTerminalPlan{
				{Protocol: consoleProtocolExec, Command: kubeVirtContainerTerminalCommand(identifier)},
				{Protocol: consoleProtocolAttach, Command: kubeVirtContainerAttachCommand(identifier)},
			}
		}
		return nil
	}

	switch providerType {
	case "proxmox", "proxmoxve", "pve":
		return []InstanceConsoleTerminalPlan{{Protocol: consoleProtocolSerial, Command: fmt.Sprintf("qm terminal %s", quotedID)}}
	case "lxd":
		return []InstanceConsoleTerminalPlan{{Protocol: consoleProtocolSerial, Command: fmt.Sprintf("lxc console %s", quotedID)}}
	case "incus":
		return []InstanceConsoleTerminalPlan{{Protocol: consoleProtocolSerial, Command: fmt.Sprintf("incus console %s", quotedID)}}
	case "qemu", "libvirt", "kvm":
		return []InstanceConsoleTerminalPlan{{Protocol: consoleProtocolSerial, Command: fmt.Sprintf("virsh -c qemu:///system console %s", quotedID)}}
	case "kubevirt":
		return []InstanceConsoleTerminalPlan{{Protocol: consoleProtocolSerial, Command: fmt.Sprintf("KUBECONFIG=/etc/rancher/k3s/k3s.yaml virtctl console -n kubevirt-vms %s", quotedID)}}
	}
	return nil
}

// containerNamespaceTerminalCommand enters only the namespaces belonging to
// the selected runtime object. The PID is read from the runtime and validated
// as decimal before nsenter; the browser never supplies a PID or command.
func containerNamespaceTerminalCommand(runtime, quotedID string) string {
	return fmt.Sprintf("sh -c %s", utils.ShellSingleQuote(fmt.Sprintf(`PID=$(%s inspect %s --format '{{.State.Pid}}' 2>/dev/null)
case "$PID" in ''|*[!0-9]*) echo "未找到运行中的容器 namespace（容器可能已停止，或当前节点不允许 nsenter）" >&2; exit 1;; esac
command -v nsenter >/dev/null 2>&1 || { echo "节点未安装 nsenter" >&2; exit 1; }
exec nsenter -t "$PID" -m -u -i -n -p -- %s`, runtime, quotedID, consoleTerminalShell())))
}

func kubeVirtContainerTerminalCommand(identifier string) string {
	selector := utils.ShellSingleQuote("app=" + identifier)
	script := fmt.Sprintf(`POD=$(KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl get pod -n kubevirt-vms -l %s -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -z "$POD" ]; then echo "未找到对应的 KubeVirt 容器 Pod" >&2; exit 1; fi
exec KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl exec -it -n kubevirt-vms "$POD" -- sh`, selector)
	return "sh -c " + utils.ShellSingleQuote(script)
}

func kubeVirtContainerAttachCommand(identifier string) string {
	selector := utils.ShellSingleQuote("app=" + identifier)
	script := fmt.Sprintf(`POD=$(KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl get pod -n kubevirt-vms -l %s -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -z "$POD" ]; then echo "未找到对应的 KubeVirt 容器 Pod" >&2; exit 1; fi
exec KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl attach -it -n kubevirt-vms "$POD"`, selector)
	return "sh -c " + utils.ShellSingleQuote(script)
}

func consoleTerminalTransport(p providerModel.Provider) (string, string) {
	transport := normalizeConsoleTransport(p, "")
	switch transport {
	case "ssh":
		endpoint := strings.TrimSpace(p.Endpoint)
		if endpoint == "" {
			endpoint = strings.TrimSpace(p.PortIP)
		}
		if utils.ExtractHost(endpoint) == "" || strings.TrimSpace(p.Username) == "" {
			return transport, "节点缺少 SSH 地址或用户名，无法建立宿主机控制台"
		}
		return transport, ""
	case "agent", "local":
		return transport, ""
	default:
		if transport == "" {
			return transport, "节点未配置 SSH、Agent 或本机连接方式"
		}
		return transport, fmt.Sprintf("节点连接方式 %q 尚未提供宿主机控制台代理", p.ConnectionType)
	}
}

func resolveInstanceConsoleTerminal(instanceID, userID uint, admin bool, protocol string) (InstanceConsoleTerminalTarget, error) {
	inst, p, err := loadConsoleRecords(instanceID, userID, admin)
	if err != nil {
		return InstanceConsoleTerminalTarget{}, err
	}
	if constant.IsBusyStatus(inst.Status) {
		return InstanceConsoleTerminalTarget{}, fmt.Errorf("实例正在操作进行中（当前状态：%s），请等待当前任务完成", inst.Status)
	}
	if inst.Status != constant.InstanceStatusRunning {
		return InstanceConsoleTerminalTarget{}, fmt.Errorf("实例未运行")
	}
	if inst.IsFrozen {
		return InstanceConsoleTerminalTarget{}, fmt.Errorf("实例已被冻结，无法进入控制台")
	}
	if inst.ExpiresAt != nil && inst.ExpiresAt.Before(time.Now()) {
		return InstanceConsoleTerminalTarget{}, fmt.Errorf("实例已到期，无法进入控制台")
	}
	plan, err := ResolveInstanceConsoleTerminalPlan(p.Type, inst.InstanceType, inst.ProviderInstanceIdentifier(), protocol)
	if err != nil {
		return InstanceConsoleTerminalTarget{}, err
	}
	transport, reason := consoleTerminalTransport(p)
	if reason != "" {
		return InstanceConsoleTerminalTarget{}, fmt.Errorf("%s", reason)
	}
	return InstanceConsoleTerminalTarget{
		Protocol:        plan.Protocol,
		Command:         plan.Command,
		ProviderID:      p.ID,
		ConnectionType:  transport,
		Provider:        p,
		InstanceID:      inst.ID,
		InstanceName:    inst.Name,
		InstanceType:    inst.InstanceType,
		ProviderRuntime: inst.ProviderInstanceIdentifier(),
	}, nil
}

// ResolveInstanceConsoleTerminalForUser returns a verified, provider-side
// terminal plan for one user-owned running instance.
func ResolveInstanceConsoleTerminalForUser(instanceID, userID uint, protocol string) (InstanceConsoleTerminalTarget, error) {
	return resolveInstanceConsoleTerminal(instanceID, userID, false, protocol)
}

// ResolveInstanceConsoleTerminalPlan resolves a known provider-side terminal
// protocol. It is shared by the capability endpoint and the WebSocket handler
// so a visible option cannot diverge from the executable transport.
func ResolveInstanceConsoleTerminalPlan(providerType, instanceType, identifier, protocol string) (InstanceConsoleTerminalPlan, error) {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		return InstanceConsoleTerminalPlan{}, fmt.Errorf("请指定控制台协议")
	}
	for _, plan := range consoleTerminalPlans(providerType, instanceType, identifier) {
		if plan.Protocol == protocol {
			return plan, nil
		}
	}
	return InstanceConsoleTerminalPlan{}, fmt.Errorf("当前节点/实例不支持 %s 宿主机控制台", protocol)
}
