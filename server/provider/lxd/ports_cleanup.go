package lxd

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

func normalizeLXDMappingMethod(method string) string {
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

func (l *LXDProvider) lookupPortMappingDetails(instanceName string, hostPort int, protocol string) (int, string) {
	if global.APP_DB == nil || l.config.ID == 0 {
		return 0, ""
	}
	var instance providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ? AND (name = ? OR provider_vm_id = ?)", l.config.ID, instanceName, instanceName).First(&instance).Error; err != nil {
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

func (l *LXDProvider) lookupPortMappingRange(instanceName string, hostPort int, protocol string) (int, int) {
	if global.APP_DB == nil || l.config.ID == 0 {
		return hostPort, hostPort
	}
	var instance providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ? AND (name = ? OR provider_vm_id = ?)", l.config.ID, instanceName, instanceName).First(&instance).Error; err != nil {
		return hostPort, hostPort
	}
	query := global.APP_DB.Where("instance_id = ? AND host_port <= ?", instance.ID, hostPort)
	if strings.TrimSpace(protocol) != "" {
		query = query.Where("protocol = ? OR protocol = ?", protocol, "both")
	}
	var ports []providerModel.Port
	if err := query.Order("id ASC").Find(&ports).Error; err != nil {
		return hostPort, hostPort
	}
	for _, candidate := range ports {
		end := candidate.HostPortEnd
		if end <= candidate.HostPort {
			if candidate.PortCount > 1 {
				end = candidate.HostPort + candidate.PortCount - 1
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

func (l *LXDProvider) removePortMappingWithDetails(instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error {
	switch normalizeLXDMappingMethod(method) {
	case "device_proxy":
		return l.removeDeviceProxyMapping(instanceName, hostPort, protocol)
	case "iptables":
		return l.removeIptablesMappingWithDetails(instanceName, hostPort, guestPort, protocol, instanceIP)
	case "native":
		return nil
	default:
		return fmt.Errorf("LXD不支持端口映射方式 %q", method)
	}
}

func (l *LXDProvider) removePortMappingWithRange(instanceName string, hostPort, guestPort, hostPortEnd, guestPortEnd, portCount int, protocol, method, instanceIP string) error {
	ip := net.ParseIP(strings.Trim(instanceIP, "[]"))
	return l.RemovePortMappingForFamily(instanceName, hostPort, guestPort, hostPortEnd, guestPortEnd, portCount, protocol, method, instanceIP, ip != nil && ip.To4() == nil)
}

// RemovePortMappingForFamily retains the intended family even when a failed
// create has no saved guest IP. It never removes the other family's proxy.
func (l *LXDProvider) RemovePortMappingForFamily(instanceName string, hostPort, guestPort, hostPortEnd, guestPortEnd, portCount int, protocol, method, instanceIP string, ipv6 bool) error {
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
	switch normalizeLXDMappingMethod(method) {
	case "device_proxy":
		return l.removeDeviceProxyRangeMappingForFamily(instanceName, hostPort, hostPortEnd, protocol, ipv6)
	case "iptables":
		count := hostPortEnd - hostPort + 1
		if guestPortEnd-guestPort+1 < count {
			count = guestPortEnd - guestPort + 1
		}
		for offset := 0; offset < count; offset++ {
			if err := l.removeIptablesMappingForFamily(instanceName, hostPort+offset, guestPort+offset, protocol, instanceIP, ipv6); err != nil {
				return err
			}
		}
		return nil
	default:
		return l.removePortMappingWithDetails(instanceName, hostPort, guestPort, protocol, method, instanceIP)
	}
}

func (l *LXDProvider) removeIptablesMappingWithDetails(instanceName string, hostPort, guestPort int, protocol, instanceIP string) error {
	ip := net.ParseIP(strings.Trim(instanceIP, "[]"))
	return l.removeIptablesMappingForFamily(instanceName, hostPort, guestPort, protocol, instanceIP, ip != nil && ip.To4() == nil)
}

func (l *LXDProvider) removeIptablesMappingForFamily(instanceName string, hostPort, guestPort int, protocol, instanceIP string, ipv6 bool) error {
	if !l.shouldUseSSH() || l.sshClient == nil || !l.sshClient.HasExecutor() {
		return fmt.Errorf("LXD iptables/nftables端口映射清理需要SSH执行器")
	}
	fwMgr := firewall.NewManager(l.sshClient, "lxd", "")
	backend, err := fwMgr.DetectBackend("/usr/local/bin/lxd_fw_backend")
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
	global.APP_LOG.Info("LXD防火墙端口映射移除成功",
		zap.String("instanceName", instanceName),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol),
		zap.String("backend", string(backend)))
	return nil
}

func (l *LXDProvider) cleanupInstancePortMappings(ctx context.Context, providerInstanceID string) error {
	_ = ctx
	if global.APP_DB == nil || l.config.ID == 0 {
		return nil
	}
	if !l.shouldUseSSH() {
		// The LXD API removes proxy devices with the instance. An API-only
		// connection cannot inspect a host firewall, so it must not make a
		// normal API deletion impossible solely because of that limitation.
		return nil
	}
	var instance providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ? AND (name = ? OR provider_vm_id = ?)", l.config.ID, providerInstanceID, providerInstanceID).First(&instance).Error; err != nil {
		return nil
	}
	instanceIP := strings.TrimSpace(instance.PrivateIP)
	if instanceIP == "" {
		// A failed create may not have persisted PrivateIP yet. Resolve it while
		// the instance still exists so an iptables rule can still be removed.
		if discoveredIP, discoverErr := l.GetInstanceIPv4(ctx, instance.Name); discoverErr == nil {
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
	_ = global.APP_DB.First(&providerConfig, l.config.ID).Error
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
				if discoveredIPv6, discoverErr := l.GetInstanceIPv6(instance.Name); discoverErr == nil {
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
		if port.MappingType == "controller" || normalizeLXDMappingMethod(mappingMethod) != "iptables" {
			continue
		}
		if err := l.RemovePortMappingForFamily(instance.Name, port.HostPort, port.GuestPort, port.HostPortEnd, port.GuestPortEnd, port.PortCount, port.Protocol, mappingMethod, targetIP, isIPv6); err != nil {
			return fmt.Errorf("清理实例 %s 的端口 %d 失败: %w", instance.Name, port.HostPort, err)
		}
	}
	return nil
}
