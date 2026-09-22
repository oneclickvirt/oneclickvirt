package incus

import (
	"context"
	"fmt"
	"net"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider/firewall"

	"go.uber.org/zap"
)

// normalizeIncusMappingMethod accepts both the values stored by the provider
// form and the values returned by the generic port-mapping discovery layer.
func normalizeIncusMappingMethod(method string) string {
	method = strings.ToLower(strings.TrimSpace(method))
	switch {
	case method == "":
		return "device_proxy"
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

// lookupPortMappingDetails supplies the guest port and the last known guest IP
// for callers that only have the public RemovePortMapping signature. The
// database lookup is deliberately best effort: manually removing an old rule
// must still be possible when the controller row has already been removed.
func (i *IncusProvider) lookupPortMappingDetails(instanceName string, hostPort int, protocol string) (int, string) {
	if global.APP_DB == nil || i.config.ID == 0 {
		return 0, ""
	}
	var instance providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ? AND (name = ? OR provider_vm_id = ?)", i.config.ID, instanceName, instanceName).First(&instance).Error; err != nil {
		return 0, ""
	}
	var port providerModel.Port
	query := global.APP_DB.Where("instance_id = ? AND host_port = ?", instance.ID, hostPort)
	if strings.TrimSpace(protocol) != "" {
		query = query.Where("protocol = ? OR protocol = ?", protocol, "both")
	}
	if err := query.Order("id ASC").First(&port).Error; err != nil {
		return 0, instance.PrivateIP
	}
	return port.GuestPort, instance.PrivateIP
}

func (i *IncusProvider) lookupPortMappingRange(instanceName string, hostPort int, protocol string) (int, int) {
	if global.APP_DB == nil || i.config.ID == 0 {
		return hostPort, hostPort
	}
	var instance providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ? AND (name = ? OR provider_vm_id = ?)", i.config.ID, instanceName, instanceName).First(&instance).Error; err != nil {
		return hostPort, hostPort
	}
	query := global.APP_DB.Where("instance_id = ? AND host_port <= ?", instance.ID, hostPort)
	if strings.TrimSpace(protocol) != "" {
		query = query.Where("protocol = ? OR protocol = ?", protocol, "both")
	}
	var ports []providerModel.Port
	if err := query.Find(&ports).Error; err != nil {
		return hostPort, hostPort
	}
	for _, candidate := range ports {
		end := candidate.HostPortEnd
		if end <= candidate.HostPort {
			count := candidate.PortCount
			if count > 1 {
				end = candidate.HostPort + count - 1
			} else {
				end = candidate.HostPort
			}
		}
		if candidate.HostPort <= hostPort && hostPort <= end {
			return candidate.HostPort, end
		}
	}
	return hostPort, hostPort
}

func (i *IncusProvider) removePortMappingWithDetails(instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error {
	switch normalizeIncusMappingMethod(method) {
	case "device_proxy":
		return i.removeDeviceProxyMapping(instanceName, hostPort, protocol)
	case "iptables":
		return i.removeIptablesMapping(instanceName, hostPort, guestPort, protocol, instanceIP)
	case "native":
		return nil
	default:
		return fmt.Errorf("Incus不支持端口映射方式 %q", method)
	}
}

func (i *IncusProvider) removePortMappingWithRange(instanceName string, hostPort, guestPort, hostPortEnd, guestPortEnd, portCount int, protocol, method, instanceIP string) error {
	ip := net.ParseIP(strings.Trim(instanceIP, "[]"))
	return i.RemovePortMappingForFamily(instanceName, hostPort, guestPort, hostPortEnd, guestPortEnd, portCount, protocol, method, instanceIP, ip != nil && ip.To4() == nil)
}

// RemovePortMappingForFamily retains the intended family even when a failed
// create has no saved guest IP. It never removes the other family's proxy.
func (i *IncusProvider) RemovePortMappingForFamily(instanceName string, hostPort, guestPort, hostPortEnd, guestPortEnd, portCount int, protocol, method, instanceIP string, ipv6 bool) error {
	if hostPortEnd <= hostPort && portCount > 1 {
		hostPortEnd = hostPort + portCount - 1
	}
	if hostPortEnd < hostPort {
		hostPortEnd = hostPort
	}
	if guestPortEnd <= guestPort && portCount > 1 {
		guestPortEnd = guestPort + portCount - 1
	}
	if guestPortEnd < guestPort {
		guestPortEnd = guestPort
	}
	if normalizeIncusMappingMethod(method) == "device_proxy" {
		return i.removeDeviceProxyRangeMappingForFamily(instanceName, hostPort, hostPortEnd, protocol, ipv6)
	}
	if normalizeIncusMappingMethod(method) == "iptables" {
		count := hostPortEnd - hostPort + 1
		if guestPortEnd-guestPort+1 < count {
			count = guestPortEnd - guestPort + 1
		}
		for offset := 0; offset < count; offset++ {
			if err := i.removeIptablesMappingForFamily(instanceName, hostPort+offset, guestPort+offset, protocol, instanceIP, ipv6); err != nil {
				return err
			}
		}
		return nil
	}
	return i.removePortMappingWithDetails(instanceName, hostPort, guestPort, protocol, method, instanceIP)
}

// removeIptablesMapping removes the rule from the same backend and managed
// table that setupIptablesMappingWithIP used. The former implementation always
// issued legacy iptables commands, so nft rules silently survived deletion and
// were later encountered again when the host port was reused.
func (i *IncusProvider) removeIptablesMapping(instanceName string, hostPort, guestPort int, protocol, instanceIP string) error {
	ip := net.ParseIP(strings.Trim(instanceIP, "[]"))
	return i.removeIptablesMappingForFamily(instanceName, hostPort, guestPort, protocol, instanceIP, ip != nil && ip.To4() == nil)
}

func (i *IncusProvider) removeIptablesMappingForFamily(instanceName string, hostPort, guestPort int, protocol, instanceIP string, ipv6 bool) error {
	if !i.shouldUseSSH() || i.sshClient == nil || !i.sshClient.HasExecutor() {
		return fmt.Errorf("Incus iptables/nftables端口映射清理需要SSH执行器")
	}

	fwMgr := firewall.NewManager(i.sshClient, "incus", "")
	backend, err := fwMgr.DetectBackend("/usr/local/bin/incus_fw_backend")
	if err != nil {
		return fmt.Errorf("防火墙后端检测失败: %w", err)
	}
	if guestPort <= 0 {
		return fmt.Errorf("缺少端口映射客户机端口，拒绝模糊删除")
	}
	comment := fmt.Sprintf("pm:%s:%d:%d", instanceName, hostPort, guestPort)
	if err := fwMgr.RemoveSingleDNATForFamily(instanceIP, hostPort, guestPort, protocol, comment, ipv6); err != nil {
		return fmt.Errorf("删除端口映射规则失败: %w", err)
	}
	if err := fwMgr.SaveRules(); err != nil {
		return err
	}
	global.APP_LOG.Info("Incus防火墙端口映射移除成功",
		zap.String("instanceName", instanceName),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol),
		zap.String("backend", string(backend)))
	return nil
}

// cleanupInstancePortMappings runs before the Incus instance is deleted. A
// proxy device disappears with its instance, while host firewall rules do not;
// only the latter need explicit cleanup here. The operation is intentionally
// fail-closed for iptables/nftables so a successful delete cannot leave a rule
// which will later capture a recycled host port.
func (i *IncusProvider) cleanupInstancePortMappings(ctx context.Context, providerInstanceID string) error {
	_ = ctx // firewall command execution is synchronous; retain context in API for callers.
	if global.APP_DB == nil || i.config.ID == 0 {
		return nil
	}
	if !i.shouldUseSSH() {
		// API-only deletion removes proxy devices together with the instance,
		// but has no capability to inspect or mutate a host firewall. Do not
		// turn an otherwise valid API delete into a permanent retry loop.
		return nil
	}
	var instance providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ? AND (name = ? OR provider_vm_id = ?)", i.config.ID, providerInstanceID, providerInstanceID).First(&instance).Error; err != nil {
		return nil
	}
	instanceIP := strings.TrimSpace(instance.PrivateIP)
	if instanceIP == "" {
		// A failed create may not have persisted PrivateIP yet. Resolve it while
		// the instance still exists so an iptables rule can still be removed.
		if discoveredIP, discoverErr := i.GetInstanceIPv4(ctx, instance.Name); discoverErr == nil {
			instanceIP = strings.TrimSpace(discoveredIP)
		}
	}
	var ports []providerModel.Port
	// Cleanup is a deletion boundary: include inactive rows too, because a
	// failed/retried lifecycle task may have marked the database row inactive
	// before the host rule was actually removed.
	if err := global.APP_DB.Where("instance_id = ?", instance.ID).Find(&ports).Error; err != nil {
		return fmt.Errorf("读取实例端口映射失败: %w", err)
	}
	var providerConfig providerModel.Provider
	_ = global.APP_DB.First(&providerConfig, i.config.ID).Error
	var mappings []providerModel.Port
	for _, port := range ports {
		mappings = append(mappings, providerModel.ExpandPortMappingFamilies(port, providerConfig.NetworkType,
			providerConfig.IPv4PortMappingMethod, providerConfig.IPv6PortMappingMethod)...)
	}
	for _, port := range mappings {
		targetIP := instanceIP
		isIPv6 := port.IPv6Enabled || strings.TrimSpace(port.IPv6Address) != ""
		if isIPv6 {
			targetIP = strings.TrimSpace(port.IPv6Address)
			if targetIP == "" {
				targetIP = strings.TrimSpace(instance.IPv6Address)
			}
			if targetIP == "" {
				if discoveredIPv6, discoverErr := i.GetInstanceIPv6(ctx, instance.Name); discoverErr == nil {
					targetIP = strings.TrimSpace(discoveredIPv6)
				}
			}
		}
		mappingMethod := port.MappingMethod
		if strings.TrimSpace(mappingMethod) == "" {
			if isIPv6 {
				mappingMethod = providerConfig.IPv6PortMappingMethod
			} else {
				mappingMethod = providerConfig.IPv4PortMappingMethod
			}
		}
		if port.MappingType == "controller" || normalizeIncusMappingMethod(mappingMethod) != "iptables" {
			continue
		}
		if err := i.RemovePortMappingForFamily(instance.Name, port.HostPort, port.GuestPort, port.HostPortEnd, port.GuestPortEnd, port.PortCount, port.Protocol, mappingMethod, targetIP, isIPv6); err != nil {
			return fmt.Errorf("清理实例 %s 的端口 %d 失败: %w", instance.Name, port.HostPort, err)
		}
	}
	i.removeHostFirewallPorts(ports)
	return nil
}
