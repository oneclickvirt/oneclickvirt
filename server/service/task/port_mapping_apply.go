package task

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"
	"oneclickvirt/provider/firewall"
	incusProvider "oneclickvirt/provider/incus"
	lxdProvider "oneclickvirt/provider/lxd"
	proxmoxProvider "oneclickvirt/provider/proxmox"
	qemuProvider "oneclickvirt/provider/qemu"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/utils"
)

type portEndpoint struct {
	host  int
	guest int
}

func effectivePortCount(port providerModel.Port) int {
	if port.PortCount > 0 {
		return port.PortCount
	}
	return 1
}

func expandPortEndpoints(port providerModel.Port) ([]portEndpoint, error) {
	count := effectivePortCount(port)
	if count < 1 || count > 1500 {
		return nil, fmt.Errorf("端口数量 %d 超出允许范围", count)
	}
	if port.HostPort < 1 || port.GuestPort < 1 || port.HostPort+count-1 > 65535 || port.GuestPort+count-1 > 65535 {
		return nil, fmt.Errorf("端口范围超出1-65535")
	}

	endpoints := make([]portEndpoint, count)
	for i := 0; i < count; i++ {
		endpoints[i] = portEndpoint{host: port.HostPort + i, guest: port.GuestPort + i}
	}
	return endpoints, nil
}

func normalizePortMappingMethod(method string) string {
	method = strings.ToLower(strings.TrimSpace(method))
	switch {
	case method == "":
		return ""
	case strings.Contains(method, "device") || strings.Contains(method, "proxy"):
		return "device_proxy"
	case strings.Contains(method, "iptables") || strings.Contains(method, "nft") || strings.Contains(method, "firewall"):
		return "iptables"
	case strings.Contains(method, "native"):
		return "native"
	default:
		return method
	}
}

type providerCommandExecutor struct {
	ctx      context.Context
	provider providerCore.Provider
}

func (e *providerCommandExecutor) execute(command string, timeout time.Duration) (string, error) {
	ctx := e.ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return e.provider.ExecuteSSHCommand(ctx, command)
}

func (e *providerCommandExecutor) Execute(command string) (string, error) {
	return e.execute(command, 0)
}

func (e *providerCommandExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	return e.execute(command, timeout)
}

func (e *providerCommandExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.execute(command, 0)
}

func (e *providerCommandExecutor) ExecuteRaw(command string, timeout time.Duration) (string, error) {
	return e.execute(command, timeout)
}

func (e *providerCommandExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", fmt.Errorf("端口映射修复执行器不支持临时脚本")
}

func (e *providerCommandExecutor) UploadContent(string, string, os.FileMode) error {
	return fmt.Errorf("端口映射修复执行器不支持文件上传")
}

func (e *providerCommandExecutor) IsHealthy() bool { return e.provider.IsConnected() }
func (e *providerCommandExecutor) Reconnect() error {
	if e.provider.IsConnected() {
		return nil
	}
	return fmt.Errorf("Provider连接不可用")
}
func (e *providerCommandExecutor) Close() error { return nil }

type portMappingApplier struct {
	ctx              context.Context
	providerInstance providerCore.Provider
	providerInfo     *providerModel.Provider
	firewallManager  *firewall.Manager
}

func newPortMappingApplier(ctx context.Context, providerInstance providerCore.Provider, providerInfo *providerModel.Provider) *portMappingApplier {
	return &portMappingApplier{ctx: ctx, providerInstance: providerInstance, providerInfo: providerInfo}
}

func (a *portMappingApplier) Apply(instance *providerModel.Instance, port *providerModel.Port, replace bool) error {
	if port.MappingType == "controller" {
		if effectivePortCount(*port) != 1 {
			return fmt.Errorf("控制端转发不支持单条记录包含多个端口")
		}
		targetHost, _ := agentService.ResolveControllerPortTarget(port.InternalHost, instance.PrivateIP)
		if targetHost == "" {
			return fmt.Errorf("控制端转发缺少目标地址")
		}
		if replace {
			return agentService.RestartControllerPortForward(port.ID, port.ProviderID, port.HostPort, targetHost, port.GuestPort)
		}
		return agentService.StartControllerPortForward(port.ID, port.ProviderID, port.HostPort, targetHost, port.GuestPort)
	}

	if a.providerInstance == nil {
		return fmt.Errorf("Provider连接不可用")
	}
	if utils.IsDockerFamilyProvider(a.providerInfo.Type) {
		return fmt.Errorf("容器运行时原生映射必须按实例重启")
	}
	if instance.PrivateIP == "" {
		return fmt.Errorf("实例缺少内网IP")
	}

	endpoints, err := expandPortEndpoints(*port)
	if err != nil {
		return err
	}
	method := normalizePortMappingMethod(port.MappingMethod)
	if method == "" {
		method = normalizePortMappingMethod(a.providerInfo.IPv4PortMappingMethod)
	}

	for _, endpoint := range endpoints {
		if err := a.applyEndpoint(instance, port, endpoint, method, replace); err != nil {
			return fmt.Errorf("端口 %d -> %d 修复失败: %w", endpoint.host, endpoint.guest, err)
		}
	}
	return nil
}

func (a *portMappingApplier) Remove(instance *providerModel.Instance, port *providerModel.Port) error {
	if port.MappingType == "controller" {
		agentService.StopControllerPortForward(port.ID)
		return nil
	}
	if a.providerInstance == nil {
		return fmt.Errorf("Provider连接不可用")
	}
	if utils.IsDockerFamilyProvider(a.providerInfo.Type) {
		return fmt.Errorf("容器运行时原生端口不能通过手动删除任务移除")
	}
	if instance == nil || instance.Name == "" {
		return fmt.Errorf("实例不存在，无法定位节点侧规则")
	}

	endpoints, err := expandPortEndpoints(*port)
	if err != nil {
		return err
	}
	method := normalizePortMappingMethod(port.MappingMethod)
	if method == "" {
		method = normalizePortMappingMethod(a.providerInfo.IPv4PortMappingMethod)
	}
	for _, endpoint := range endpoints {
		if err := a.removeEndpoint(instance, port, endpoint, method); err != nil {
			return fmt.Errorf("端口 %d -> %d 删除失败: %w", endpoint.host, endpoint.guest, err)
		}
	}
	return nil
}

func (a *portMappingApplier) removeEndpoint(instance *providerModel.Instance, port *providerModel.Port, endpoint portEndpoint, method string) error {
	switch providerInstance := a.providerInstance.(type) {
	case *lxdProvider.LXDProvider:
		return providerInstance.RemovePortMapping(instance.Name, endpoint.host, port.Protocol, method)
	case *incusProvider.IncusProvider:
		return providerInstance.RemovePortMapping(instance.Name, endpoint.host, port.Protocol, method)
	case *proxmoxProvider.ProxmoxProvider:
		return providerInstance.RemovePortMapping(a.ctx, instance.Name, endpoint.host, port.Protocol, method)
	default:
		manager, err := a.getFirewallManager()
		if err != nil {
			return err
		}
		comment := fmt.Sprintf("pm:%s:%d:%d", instance.Name, endpoint.host, endpoint.guest)
		if manager.GetBackend() == firewall.BackendNft {
			return manager.DeleteRulesByComment(comment)
		}
		return manager.RemoveSingleDNAT(instance.PrivateIP, endpoint.host, endpoint.guest, port.Protocol, comment)
	}
}

func (a *portMappingApplier) applyEndpoint(instance *providerModel.Instance, port *providerModel.Port, endpoint portEndpoint, method string, replace bool) error {
	switch providerInstance := a.providerInstance.(type) {
	case *lxdProvider.LXDProvider:
		if replace {
			_ = providerInstance.RemovePortMapping(instance.Name, endpoint.host, port.Protocol, method)
		}
		return providerInstance.SetupPortMappingWithIP(a.ctx, instance.Name, endpoint.host, endpoint.guest, port.Protocol, method, instance.PrivateIP)
	case *incusProvider.IncusProvider:
		if replace {
			_ = providerInstance.RemovePortMapping(instance.Name, endpoint.host, port.Protocol, method)
		}
		return providerInstance.SetupPortMappingWithIP(a.ctx, instance.Name, endpoint.host, endpoint.guest, port.Protocol, method, instance.PrivateIP)
	case *proxmoxProvider.ProxmoxProvider:
		if replace {
			_ = providerInstance.RemovePortMapping(a.ctx, instance.Name, endpoint.host, port.Protocol, method)
		}
		return providerInstance.SetupPortMappingWithIP(a.ctx, instance.Name, endpoint.host, endpoint.guest, port.Protocol, method, instance.PrivateIP)
	default:
		manager, err := a.getFirewallManager()
		if err != nil {
			return err
		}
		comment := fmt.Sprintf("pm:%s:%d:%d", instance.Name, endpoint.host, endpoint.guest)
		if replace {
			if manager.GetBackend() == firewall.BackendNft {
				_ = manager.DeleteRulesByComment(comment)
			} else {
				_ = manager.RemoveSingleDNAT(instance.PrivateIP, endpoint.host, endpoint.guest, port.Protocol, comment)
			}
		}
		return manager.AddSingleDNAT(instance.PrivateIP, endpoint.host, endpoint.guest, port.Protocol, comment)
	}
}

func (a *portMappingApplier) getFirewallManager() (*firewall.Manager, error) {
	if a.firewallManager != nil {
		return a.firewallManager, nil
	}
	tableName, markerFile, subnet := firewallConfigForProvider(a.providerInfo.Type)
	executor := &providerCommandExecutor{ctx: a.ctx, provider: a.providerInstance}
	manager := firewall.NewManager(executor, tableName, subnet)
	if _, err := manager.DetectBackend(markerFile); err != nil {
		return nil, err
	}
	if err := manager.InitTable(); err != nil {
		return nil, err
	}
	a.firewallManager = manager
	return manager, nil
}

func (a *portMappingApplier) Finish() {
	if a.firewallManager != nil {
		a.firewallManager.SaveRules()
	}
}

func firewallConfigForProvider(providerType string) (tableName, markerFile, subnet string) {
	switch providerType {
	case "pve", "proxmox", "proxmoxve":
		return "proxmox", "/usr/local/bin/proxmox_fw_backend", ""
	case "qemu":
		return qemuProvider.NFTTableName, qemuProvider.FWBackendFile, qemuProvider.InternalSubnet
	case "kubevirt":
		return "kubevirt", "/usr/local/bin/kubevirt_fw_backend", ""
	case "vmware", "virtualbox", "multipass", "vagrant":
		return providerType, "/usr/local/bin/" + providerType + "_fw_backend", ""
	default:
		return "portmap", "", ""
	}
}
