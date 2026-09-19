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
	if port.HostPortEnd >= port.HostPort && port.GuestPortEnd >= port.GuestPort &&
		port.HostPortEnd > 0 && port.GuestPortEnd > 0 {
		hostCount := port.HostPortEnd - port.HostPort + 1
		guestCount := port.GuestPortEnd - port.GuestPort + 1
		if hostCount == guestCount {
			return hostCount
		}
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
	if port.HostPortEnd > 0 && port.HostPortEnd != port.HostPort+count-1 {
		return nil, fmt.Errorf("宿主机端口范围与端口数量不一致")
	}
	if port.GuestPortEnd > 0 && port.GuestPortEnd != port.GuestPort+count-1 {
		return nil, fmt.Errorf("实例端口范围与端口数量不一致")
	}

	endpoints := make([]portEndpoint, count)
	for i := 0; i < count; i++ {
		endpoints[i] = portEndpoint{host: port.HostPort + i, guest: port.GuestPort + i}
	}
	return endpoints, nil
}

// mappingTarget selects the address family from the persisted mapping row.
// IPv6-only instances legitimately have no PrivateIP; treating that as a
// generic missing IPv4 address used to make repair/delete tasks fail before
// they reached the provider implementation.
func mappingTarget(instance *providerModel.Instance, port *providerModel.Port) (string, bool, error) {
	if instance == nil || port == nil {
		return "", false, fmt.Errorf("实例或端口映射为空")
	}
	if address := strings.TrimSpace(port.IPv6Address); address != "" {
		return address, true, nil
	}
	if port.IPv6Enabled || strings.TrimSpace(instance.PrivateIP) == "" {
		if address := strings.TrimSpace(instance.IPv6Address); address != "" {
			return address, true, nil
		}
		if port.IPv6Enabled || strings.TrimSpace(instance.PrivateIP) == "" {
			return "", true, fmt.Errorf("IPv6端口映射缺少实例IPv6地址")
		}
	}
	return strings.TrimSpace(instance.PrivateIP), false, nil
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
	ctx                     context.Context
	providerInstance        providerCore.Provider
	providerInfo            *providerModel.Provider
	firewallManager         *firewall.Manager
	providerFirewallChanged bool
}

func newPortMappingApplier(ctx context.Context, providerInstance providerCore.Provider, providerInfo *providerModel.Provider) *portMappingApplier {
	return &portMappingApplier{ctx: ctx, providerInstance: providerInstance, providerInfo: providerInfo}
}

func (a *portMappingApplier) Apply(instance *providerModel.Instance, port *providerModel.Port, replace bool) error {
	if port == nil || a.providerInfo == nil {
		return fmt.Errorf("端口映射或Provider配置为空")
	}
	if port.MappingType == "controller" {
		resolved := providerModel.ExpandPortMappingFamilies(*port, a.providerInfo.NetworkType, a.providerInfo.IPv4PortMappingMethod, a.providerInfo.IPv6PortMappingMethod)
		port = &resolved[0]
		if effectivePortCount(*port) != 1 {
			return fmt.Errorf("控制端转发不支持单条记录包含多个端口")
		}
		targetAddress, _, targetErr := mappingTarget(instance, port)
		if targetErr != nil && strings.TrimSpace(port.InternalHost) == "" {
			return targetErr
		}
		targetHost, _ := agentService.ResolveControllerPortTarget(port.InternalHost, targetAddress)
		if targetHost == "" {
			return fmt.Errorf("控制端转发缺少目标地址")
		}
		if replace {
			return agentService.RestartControllerPortForward(port.ID, port.ProviderID, port.HostPort, targetHost, port.GuestPort)
		}
		return agentService.StartControllerPortForward(port.ID, port.ProviderID, port.HostPort, targetHost, port.GuestPort)
	}
	mappings := providerModel.ExpandPortMappingFamilies(*port, a.providerInfo.NetworkType, a.providerInfo.IPv4PortMappingMethod, a.providerInfo.IPv6PortMappingMethod)
	if replace && len(mappings) > 1 {
		// Legacy proxy removal removes both families. Clear old devices once,
		// before creating either family, so IPv6 repair cannot remove fresh IPv4.
		if err := a.Remove(instance, port); err != nil {
			return err
		}
		replace = false
	}
	for _, mapping := range mappings {
		if normalizePortMappingMethod(mapping.MappingMethod) == "native" {
			continue
		}
		if err := a.applyNodeMapping(instance, &mapping, replace); err != nil {
			return err
		}
	}
	return nil
}

func (a *portMappingApplier) applyNodeMapping(instance *providerModel.Instance, port *providerModel.Port, replace bool) error {
	if a.providerInstance == nil {
		return fmt.Errorf("Provider连接不可用")
	}
	if utils.IsDockerFamilyProvider(a.providerInfo.Type) {
		return fmt.Errorf("容器运行时原生映射必须按实例重启")
	}
	targetAddress, ipv6, err := mappingTarget(instance, port)
	if err != nil {
		return err
	}

	endpoints, err := expandPortEndpoints(*port)
	if err != nil {
		return err
	}
	method := normalizePortMappingMethod(port.MappingMethod)
	if method == "" {
		if ipv6 {
			method = normalizePortMappingMethod(a.providerInfo.IPv6PortMappingMethod)
		} else {
			method = normalizePortMappingMethod(a.providerInfo.IPv4PortMappingMethod)
		}
	}

	for _, endpoint := range endpoints {
		if err := a.applyEndpoint(instance, port, endpoint, method, targetAddress, replace); err != nil {
			return fmt.Errorf("端口 %d -> %d 修复失败: %w", endpoint.host, endpoint.guest, err)
		}
	}
	return nil
}

func (a *portMappingApplier) Remove(instance *providerModel.Instance, port *providerModel.Port) error {
	if port == nil || a.providerInfo == nil {
		return fmt.Errorf("端口映射或Provider配置为空")
	}
	if port.MappingType == "controller" {
		agentService.StopControllerPortForward(port.ID)
		return nil
	}
	for _, mapping := range providerModel.ExpandPortMappingFamilies(*port, a.providerInfo.NetworkType, a.providerInfo.IPv4PortMappingMethod, a.providerInfo.IPv6PortMappingMethod) {
		if normalizePortMappingMethod(mapping.MappingMethod) == "native" {
			continue
		}
		if err := a.removeNodeMapping(instance, &mapping); err != nil {
			return err
		}
	}
	return nil
}

func (a *portMappingApplier) removeNodeMapping(instance *providerModel.Instance, port *providerModel.Port) error {
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
	targetAddress, ipv6, err := mappingTarget(instance, port)
	if err != nil {
		// Removing a named proxy or an exactly tagged firewall rule does not
		// require a running guest. Keep the row's family when both IPs are absent.
		ipv6 = port.IPv6Enabled || strings.TrimSpace(port.IPv6Address) != ""
	}
	method := normalizePortMappingMethod(port.MappingMethod)
	if method == "" {
		if ipv6 {
			method = normalizePortMappingMethod(a.providerInfo.IPv6PortMappingMethod)
		} else {
			method = normalizePortMappingMethod(a.providerInfo.IPv4PortMappingMethod)
		}
	}
	for _, endpoint := range endpoints {
		if err := a.removeEndpoint(instance, port, endpoint, method, targetAddress, ipv6); err != nil {
			return fmt.Errorf("端口 %d -> %d 删除失败: %w", endpoint.host, endpoint.guest, err)
		}
	}
	return nil
}

func (a *portMappingApplier) removeEndpoint(instance *providerModel.Instance, port *providerModel.Port, endpoint portEndpoint, method, targetAddress string, ipv6 bool) error {
	providerInstanceID := instance.ProviderInstanceIdentifier()
	switch providerInstance := a.providerInstance.(type) {
	case *lxdProvider.LXDProvider:
		return providerInstance.RemovePortMappingForFamily(providerInstanceID, endpoint.host, endpoint.guest, endpoint.host, endpoint.guest, 1, port.Protocol, method, targetAddress, ipv6)
	case *incusProvider.IncusProvider:
		return providerInstance.RemovePortMappingForFamily(providerInstanceID, endpoint.host, endpoint.guest, endpoint.host, endpoint.guest, 1, port.Protocol, method, targetAddress, ipv6)
	case *proxmoxProvider.ProxmoxProvider:
		return providerInstance.RemovePortMapping(a.ctx, providerInstanceID, endpoint.host, port.Protocol, method)
	default:
		manager, err := a.getFirewallManager()
		if err != nil {
			return err
		}
		comment := fmt.Sprintf("pm:%s:%d:%d", instance.Name, endpoint.host, endpoint.guest)
		return manager.RemoveSingleDNATForFamily(targetAddress, endpoint.host, endpoint.guest, port.Protocol, comment, ipv6)
	}
}

func (a *portMappingApplier) applyEndpoint(instance *providerModel.Instance, port *providerModel.Port, endpoint portEndpoint, method, targetAddress string, replace bool) error {
	if replace {
		if err := a.removeEndpoint(instance, port, endpoint, method, targetAddress, strings.Contains(targetAddress, ":")); err != nil {
			return err
		}
	}
	providerInstanceID := instance.ProviderInstanceIdentifier()
	switch providerInstance := a.providerInstance.(type) {
	case *lxdProvider.LXDProvider:
		a.providerFirewallChanged = a.providerFirewallChanged || method == "iptables"
		return providerInstance.SetupPortMappingWithIP(a.ctx, providerInstanceID, endpoint.host, endpoint.guest, port.Protocol, method, targetAddress)
	case *incusProvider.IncusProvider:
		a.providerFirewallChanged = a.providerFirewallChanged || method == "iptables"
		return providerInstance.SetupPortMappingWithIP(a.ctx, providerInstanceID, endpoint.host, endpoint.guest, port.Protocol, method, targetAddress)
	case *proxmoxProvider.ProxmoxProvider:
		return providerInstance.SetupPortMappingWithIP(a.ctx, providerInstanceID, endpoint.host, endpoint.guest, port.Protocol, method, targetAddress)
	default:
		manager, err := a.getFirewallManager()
		if err != nil {
			return err
		}
		comment := fmt.Sprintf("pm:%s:%d:%d", instance.Name, endpoint.host, endpoint.guest)
		return manager.AddSingleDNAT(targetAddress, endpoint.host, endpoint.guest, port.Protocol, comment)
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

func (a *portMappingApplier) Finish() error {
	if a.firewallManager != nil {
		return a.firewallManager.SaveRules()
	}
	if a.providerFirewallChanged {
		if saver, ok := a.providerInstance.(interface{ SaveIptablesRules() error }); ok {
			return saver.SaveIptablesRules()
		}
	}
	return nil
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
