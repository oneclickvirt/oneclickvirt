package incus

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"sort"
	"strings"

	"go.uber.org/zap"
)

// configurePortMappings 配置端口映射
func (i *IncusProvider) configurePortMappings(ctx context.Context, instanceName string, networkConfig NetworkConfig, instanceIP string) error {
	return i.configurePortMappingsWithIP(ctx, instanceName, networkConfig, instanceIP)
}

// configurePortMappingsWithIP 使用指定的实例IP配置端口映射
func (i *IncusProvider) configurePortMappingsWithIP(ctx context.Context, instanceName string, networkConfig NetworkConfig, instanceIP string) error {
	// 检查是否为独立IP模式或纯IPv6模式，如果是则跳过IPv4端口映射
	// dedicated_ipv4: 独立IPv4，不需要端口映射
	// dedicated_ipv4_ipv6: 独立IPv4 + 独立IPv6，不需要端口映射
	// ipv6_only: 纯IPv6，不允许任何IPv4操作
	if networkConfig.NetworkType == "dedicated_ipv4" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" || networkConfig.NetworkType == "ipv6_only" {
		global.APP_LOG.Info("独立IP模式或纯IPv6模式，跳过IPv4端口映射配置",
			zap.String("instance", instanceName),
			zap.String("networkType", networkConfig.NetworkType))
		return nil
	}

	// 从数据库获取实例的端口映射配置
	// 首先获取Provider ID
	var provider providerModel.Provider
	if err := global.APP_DB.Where("name = ?", i.config.Name).First(&provider).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 使用Provider ID和实例名称查询实例（组合唯一索引）
	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ? AND provider_id = ?", instanceName, provider.ID).First(&instance).Error; err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	// 获取实例的所有端口映射
	var portMappings []providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND status = 'active'", instance.ID).Find(&portMappings).Error; err != nil {
		return fmt.Errorf("获取端口映射失败: %w", err)
	}

	if len(portMappings) == 0 {
		global.APP_LOG.Warn("未找到端口映射配置", zap.String("instance", instanceName))
		return nil
	}

	// 分离SSH端口和其他端口
	var sshPort *providerModel.Port
	var otherPorts []providerModel.Port

	for i := range portMappings {
		if portMappings[i].IsSSH {
			sshPort = &portMappings[i]
		} else {
			otherPorts = append(otherPorts, portMappings[i])
		}
	}

	// 1. 单独配置SSH端口映射
	if sshPort != nil {
		var mappingMethod string
		var targetIP string

		// 根据网络类型选择映射方法和目标IP
		if networkConfig.NetworkType == "ipv6_only" {
			// 纯IPv6模式，获取IPv6地址并使用IPv6映射方法
			ipv6, err := i.GetInstanceIPv6(ctx, instanceName)
			if err != nil {
				global.APP_LOG.Warn("获取IPv6地址失败，跳过SSH端口映射",
					zap.String("instance", instanceName),
					zap.Error(err))
			} else {
				mappingMethod = networkConfig.IPv6PortMappingMethod
				targetIP = ipv6
			}
		} else {
			// 其他模式使用IPv4
			mappingMethod = networkConfig.IPv4PortMappingMethod
			targetIP = instanceIP
		}

		if targetIP != "" && mappingMethod != "" {
			if err := i.setupPortMappingWithIP(instanceName, sshPort.HostPort, sshPort.GuestPort, sshPort.Protocol, mappingMethod, targetIP); err != nil {
				global.APP_LOG.Warn("配置SSH端口映射失败",
					zap.String("instance", instanceName),
					zap.Int("hostPort", sshPort.HostPort),
					zap.Int("guestPort", sshPort.GuestPort),
					zap.String("targetIP", targetIP),
					zap.Error(err))
			}
		}
	}

	// 2. 使用区间映射配置其他端口
	if len(otherPorts) > 0 {
		var mappingMethod string
		var targetIP string

		// 根据网络类型选择映射方法和目标IP
		if networkConfig.NetworkType == "ipv6_only" {
			// 纯IPv6模式，获取IPv6地址并使用IPv6映射方法
			ipv6, err := i.GetInstanceIPv6(ctx, instanceName)
			if err != nil {
				global.APP_LOG.Warn("获取IPv6地址失败，跳过其他端口映射",
					zap.String("instance", instanceName),
					zap.Error(err))
			} else {
				mappingMethod = networkConfig.IPv6PortMappingMethod
				targetIP = ipv6
			}
		} else {
			// 其他模式使用IPv4
			mappingMethod = networkConfig.IPv4PortMappingMethod
			targetIP = instanceIP
		}

		if targetIP != "" && mappingMethod != "" {
			if err := i.setupPortRangeMappingWithIP(instanceName, otherPorts, mappingMethod, targetIP); err != nil {
				global.APP_LOG.Warn("配置端口区间映射失败",
					zap.String("instance", instanceName),
					zap.String("targetIP", targetIP),
					zap.Error(err))
			}
		}
	}

	return nil
}

// configureFirewallPorts 配置防火墙端口
func (i *IncusProvider) configureFirewallPorts(instanceName string) error {
	// 获取实例的端口映射信息
	// 首先获取Provider ID
	var provider providerModel.Provider
	if err := global.APP_DB.Where("name = ?", i.config.Name).First(&provider).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 使用Provider ID和实例名称查询实例（组合唯一索引）
	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ? AND provider_id = ?", instanceName, provider.ID).First(&instance).Error; err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	var portMappings []providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND status = 'active'", instance.ID).Find(&portMappings).Error; err != nil {
		return fmt.Errorf("获取端口映射失败: %w", err)
	}

	if len(portMappings) == 0 {
		return nil
	}

	// 检查防火墙类型并配置
	if i.hasFirewalld() {
		return i.configureFirewalldPorts(portMappings)
	} else if i.hasUfw() {
		return i.configureUfwPorts(portMappings)
	}

	return nil
}

// hasFirewalld 检查是否有firewalld
func (i *IncusProvider) hasFirewalld() bool {
	_, err := i.sshClient.Execute("command -v firewall-cmd")
	return err == nil
}

// hasUfw 检查是否有ufw
func (i *IncusProvider) hasUfw() bool {
	_, err := i.sshClient.Execute("command -v ufw")
	return err == nil
}

// configureFirewalldPorts 配置firewalld端口
func (i *IncusProvider) configureFirewalldPorts(portMappings []providerModel.Port) error {
	for _, port := range portMappings {
		protocols := []string{port.Protocol}
		if port.Protocol == "both" {
			protocols = []string{"tcp", "udp"}
		}

		for _, proto := range protocols {
			cmd := fmt.Sprintf("firewall-cmd --permanent --add-port=%d/%s", port.HostPort, strings.ToLower(proto))
			_, err := i.sshClient.Execute(cmd)
			if err != nil {
				global.APP_LOG.Warn("配置firewalld端口失败",
					zap.Int("port", port.HostPort),
					zap.String("protocol", proto),
					zap.Error(err))
			}
		}
	}

	// 重新加载firewall配置
	_, err := i.sshClient.Execute("firewall-cmd --reload")
	return err
}

// configureUfwPorts 配置ufw端口
func (i *IncusProvider) configureUfwPorts(portMappings []providerModel.Port) error {
	for _, port := range portMappings {
		protocols := []string{port.Protocol}
		if port.Protocol == "both" {
			protocols = []string{"tcp", "udp"}
		}

		for _, proto := range protocols {
			cmd := fmt.Sprintf("ufw allow %d/%s", port.HostPort, strings.ToLower(proto))
			_, err := i.sshClient.Execute(cmd)
			if err != nil {
				global.APP_LOG.Warn("配置ufw端口失败",
					zap.Int("port", port.HostPort),
					zap.String("protocol", proto),
					zap.Error(err))
			}
		}
	}

	// 重新加载ufw配置
	_, err := i.sshClient.Execute("ufw reload")
	return err
}

// setupPortMappingWithIP 使用指定的实例IP设置端口映射
func (i *IncusProvider) setupPortMappingWithIP(instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error {
	global.APP_LOG.Info("设置端口映射(使用已知IP)",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol),
		zap.String("method", method),
		zap.String("instanceIP", instanceIP))

	switch method {
	case "device_proxy":
		return i.setupDeviceProxyMappingWithIP(instanceName, hostPort, guestPort, protocol)
	case "iptables":
		return i.setupIptablesMappingWithIP(instanceName, hostPort, guestPort, protocol, instanceIP)
	case "native":
		// 独立IPv4模式下使用native方法，跳过端口映射
		global.APP_LOG.Info("独立IPv4模式，跳过端口映射",
			zap.String("instance", instanceName),
			zap.Int("hostPort", hostPort),
			zap.Int("guestPort", guestPort),
			zap.String("protocol", protocol))
		return nil
	default:
		// 默认使用device proxy方式
		return i.setupDeviceProxyMappingWithIP(instanceName, hostPort, guestPort, protocol)
	}
}

// setupDeviceProxyMappingWithIP 使用Incus device proxy设置端口映射
func (i *IncusProvider) setupDeviceProxyMappingWithIP(instanceName string, hostPort, guestPort int, protocol string) error {
	hostIP, err := i.getHostIP()
	if err != nil {
		hostIP = "0.0.0.0" // 回退方案
	}

	// 如果协议是both，需要创建两个设备（TCP和UDP）
	if protocol == "both" {
		// 创建TCP设备
		tcpDeviceName := fmt.Sprintf("proxy-tcp-%d", hostPort)
		tcpCmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s:%s:%d connect=%s:0.0.0.0:%d nat=true",
			instanceName, tcpDeviceName, "tcp", hostIP, hostPort, "tcp", guestPort)

		_, err = i.sshClient.Execute(tcpCmd)
		if err != nil {
			return fmt.Errorf("设置TCP device proxy映射失败: %w", err)
		}

		// 创建UDP设备
		udpDeviceName := fmt.Sprintf("proxy-udp-%d", hostPort)
		udpCmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s:%s:%d connect=%s:0.0.0.0:%d nat=true",
			instanceName, udpDeviceName, "udp", hostIP, hostPort, "udp", guestPort)

		_, err = i.sshClient.Execute(udpCmd)
		if err != nil {
			return fmt.Errorf("设置UDP device proxy映射失败: %w", err)
		}

		global.APP_LOG.Info("device proxy端口映射配置成功(TCP+UDP)",
			zap.String("instanceName", instanceName),
			zap.String("tcpDeviceName", tcpDeviceName),
			zap.String("udpDeviceName", udpDeviceName),
			zap.Int("hostPort", hostPort),
			zap.Int("guestPort", guestPort))
	} else {
		// 单一协议
		deviceName := fmt.Sprintf("proxy-%s-%d", protocol, hostPort)
		cmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s:%s:%d connect=%s:0.0.0.0:%d nat=true",
			instanceName, deviceName, strings.ToLower(protocol), hostIP, hostPort, strings.ToLower(protocol), guestPort)

		_, err = i.sshClient.Execute(cmd)
		if err != nil {
			return fmt.Errorf("设置device proxy映射失败: %w", err)
		}

		global.APP_LOG.Info("device proxy端口映射配置成功",
			zap.String("instanceName", instanceName),
			zap.String("deviceName", deviceName),
			zap.Int("hostPort", hostPort),
			zap.Int("guestPort", guestPort))
	}

	return nil
}

// setupIptablesMappingWithIP 使用iptables设置端口映射
func (i *IncusProvider) setupIptablesMappingWithIP(instanceName string, hostPort, guestPort int, protocol, instanceIP string) error {
	global.APP_LOG.Info("使用iptables设置端口映射",
		zap.String("instanceName", instanceName),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol),
		zap.String("instanceIP", instanceIP))

	// 如果协议是both，需要同时创建TCP和UDP规则
	protocols := []string{protocol}
	if protocol == "both" {
		protocols = []string{"tcp", "udp"}
	}

	for _, proto := range protocols {
		// DNAT规则
		dnatCmd := fmt.Sprintf("iptables -t nat -A PREROUTING -p %s --dport %d -j DNAT --to-destination %s:%d",
			proto, hostPort, instanceIP, guestPort)
		_, err := i.sshClient.Execute(dnatCmd)
		if err != nil {
			return fmt.Errorf("添加%s DNAT规则失败: %w", proto, err)
		}

		// FORWARD规则
		forwardCmd := fmt.Sprintf("iptables -A FORWARD -p %s -d %s --dport %d -j ACCEPT",
			proto, instanceIP, guestPort)
		_, err = i.sshClient.Execute(forwardCmd)
		if err != nil {
			return fmt.Errorf("添加%s FORWARD规则失败: %w", proto, err)
		}

		// MASQUERADE规则
		masqueradeCmd := fmt.Sprintf("iptables -t nat -A POSTROUTING -p %s -s %s --sport %d -j MASQUERADE",
			proto, instanceIP, guestPort)
		_, err = i.sshClient.Execute(masqueradeCmd)
		if err != nil {
			return fmt.Errorf("添加%s MASQUERADE规则失败: %w", proto, err)
		}

		global.APP_LOG.Info("Iptables端口映射设置成功",
			zap.String("instanceName", instanceName),
			zap.String("protocol", proto),
			zap.String("target", fmt.Sprintf("%s:%d", instanceIP, guestPort)))
	}

	return nil
}

// SaveIptablesRules 保存iptables规则到文件（公开方法）
func (i *IncusProvider) SaveIptablesRules() error {
	// 创建iptables目录
	_, err := i.sshClient.Execute("mkdir -p /etc/iptables")
	if err != nil {
		global.APP_LOG.Warn("创建iptables目录失败", zap.Error(err))
	}

	// 保存IPv4规则
	saveCmd := "iptables-save > /etc/iptables/rules.v4"
	_, err = i.sshClient.Execute(saveCmd)
	if err != nil {
		return fmt.Errorf("保存iptables规则失败: %w", err)
	}

	global.APP_LOG.Info("Incus iptables规则保存成功")
	return nil
}

// setupPortRangeMappingWithIP 设置端口范围映射
func (i *IncusProvider) setupPortRangeMappingWithIP(instanceName string, ports []providerModel.Port, method string, instanceIP string) error {
	if len(ports) == 0 {
		return nil
	}

	// 按端口号排序
	sort.Slice(ports, func(i, j int) bool {
		return ports[i].HostPort < ports[j].HostPort
	})

	// 寻找连续的端口范围
	ranges := i.findPortRanges(ports)

	for _, portRange := range ranges {
		if len(portRange) == 1 {
			// 单个端口
			port := portRange[0]
			if port.Protocol == "both" {
				// 分别映射 tcp 和 udp
				tcpPort := port
				tcpPort.Protocol = "tcp"
				err := i.setupPortMappingWithIP(instanceName, tcpPort.HostPort, tcpPort.GuestPort, "tcp", method, instanceIP)
				if err != nil {
					global.APP_LOG.Warn("单个端口映射失败(tcp)",
						zap.Int("port", tcpPort.HostPort),
						zap.Error(err))
				}
				udpPort := port
				udpPort.Protocol = "udp"
				err = i.setupPortMappingWithIP(instanceName, udpPort.HostPort, udpPort.GuestPort, "udp", method, instanceIP)
				if err != nil {
					global.APP_LOG.Warn("单个端口映射失败(udp)",
						zap.Int("port", udpPort.HostPort),
						zap.Error(err))
				}
			} else {
				err := i.setupPortMappingWithIP(instanceName, port.HostPort, port.GuestPort, port.Protocol, method, instanceIP)
				if err != nil {
					global.APP_LOG.Warn("单个端口映射失败",
						zap.Int("port", port.HostPort),
						zap.Error(err))
				}
			}
		} else {
			// 端口范围
			startPort := portRange[0]
			endPort := portRange[len(portRange)-1]
			if startPort.Protocol == "both" {
				// 分别映射 tcp 和 udp
				err := i.setupPortRangeMapping(instanceName, startPort.HostPort, endPort.HostPort, "tcp")
				if err != nil {
					global.APP_LOG.Warn("端口范围映射失败(tcp)",
						zap.Int("startPort", startPort.HostPort),
						zap.Int("endPort", endPort.HostPort),
						zap.Error(err))
				}
				err = i.setupPortRangeMapping(instanceName, startPort.HostPort, endPort.HostPort, "udp")
				if err != nil {
					global.APP_LOG.Warn("端口范围映射失败(udp)",
						zap.Int("startPort", startPort.HostPort),
						zap.Int("endPort", endPort.HostPort),
						zap.Error(err))
				}
			} else {
				err := i.setupPortRangeMapping(instanceName, startPort.HostPort, endPort.HostPort, startPort.Protocol)
				if err != nil {
					global.APP_LOG.Warn("端口范围映射失败",
						zap.Int("startPort", startPort.HostPort),
						zap.Int("endPort", endPort.HostPort),
						zap.Error(err))
				}
			}
		}
	}

	return nil
}

// findPortRanges 查找连续的端口范围
func (i *IncusProvider) findPortRanges(ports []providerModel.Port) [][]providerModel.Port {
	if len(ports) == 0 {
		return nil
	}

	var ranges [][]providerModel.Port
	currentRange := []providerModel.Port{ports[0]}

	for i := 1; i < len(ports); i++ {
		// 检查是否是连续端口且协议相同
		if ports[i].HostPort == ports[i-1].HostPort+1 &&
			ports[i].Protocol == ports[i-1].Protocol {
			currentRange = append(currentRange, ports[i])
		} else {
			ranges = append(ranges, currentRange)
			currentRange = []providerModel.Port{ports[i]}
		}
	}
	ranges = append(ranges, currentRange)

	return ranges
}

// setupPortRangeMapping 设置端口范围映射
func (i *IncusProvider) setupPortRangeMapping(instanceName string, startPort, endPort int, protocol string) error {
	hostIP, err := i.getHostIP()
	if err != nil {
		hostIP = "0.0.0.0" // 回退方案
	}

	// 如果协议是both，需要创建两个设备（TCP和UDP）
	if protocol == "both" {
		// 创建TCP范围映射
		tcpDeviceName := fmt.Sprintf("proxy-tcp-%d-%d", startPort, endPort)
		tcpCmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s:%s:%d-%d connect=%s:0.0.0.0:%d-%d nat=true",
			instanceName, tcpDeviceName, "tcp", hostIP, startPort, endPort, "tcp", startPort, endPort)

		_, err = i.sshClient.Execute(tcpCmd)
		if err != nil {
			return fmt.Errorf("设置TCP端口范围映射失败: %w", err)
		}

		// 创建UDP范围映射
		udpDeviceName := fmt.Sprintf("proxy-udp-%d-%d", startPort, endPort)
		udpCmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s:%s:%d-%d connect=%s:0.0.0.0:%d-%d nat=true",
			instanceName, udpDeviceName, "udp", hostIP, startPort, endPort, "udp", startPort, endPort)

		_, err = i.sshClient.Execute(udpCmd)
		if err != nil {
			return fmt.Errorf("设置UDP端口范围映射失败: %w", err)
		}

		global.APP_LOG.Info("端口范围映射配置成功(TCP+UDP)",
			zap.String("instanceName", instanceName),
			zap.String("tcpDeviceName", tcpDeviceName),
			zap.String("udpDeviceName", udpDeviceName),
			zap.Int("startPort", startPort),
			zap.Int("endPort", endPort))
	} else {
		// 单一协议
		deviceName := fmt.Sprintf("proxy-%s-%d-%d", protocol, startPort, endPort)
		cmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s:%s:%d-%d connect=%s:0.0.0.0:%d-%d nat=true",
			instanceName, deviceName, strings.ToLower(protocol), hostIP, startPort, endPort, strings.ToLower(protocol), startPort, endPort)

		_, err = i.sshClient.Execute(cmd)
		if err != nil {
			return fmt.Errorf("设置端口范围映射失败: %w", err)
		}

		global.APP_LOG.Info("端口范围映射配置成功",
			zap.String("instanceName", instanceName),
			zap.String("deviceName", deviceName),
			zap.Int("startPort", startPort),
			zap.Int("endPort", endPort))
	}

	return nil
}

// removePortMapping 移除端口映射
func (i *IncusProvider) removePortMapping(instanceName string, hostPort int, protocol string, method string) error {
	global.APP_LOG.Info("移除端口映射",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.String("protocol", protocol),
		zap.String("method", method))

	switch method {
	case "device_proxy":
		return i.removeDeviceProxyMapping(instanceName, hostPort, protocol)
	case "iptables":
		return i.removeIptablesMappingByPort(instanceName, hostPort, protocol)
	default:
		// 默认使用device proxy方式
		return i.removeDeviceProxyMapping(instanceName, hostPort, protocol)
	}
}

// removeDeviceProxyMapping 移除Incus device proxy映射
func (i *IncusProvider) removeDeviceProxyMapping(instanceName string, hostPort int, protocol string) error {
	// 如果是both协议，需要删除TCP和UDP两个设备
	if protocol == "both" {
		// 删除TCP设备
		tcpDeviceName := fmt.Sprintf("proxy-tcp-%d", hostPort)
		tcpRemoveCmd := fmt.Sprintf("incus config device remove %s %s", instanceName, tcpDeviceName)
		_, err := i.sshClient.Execute(tcpRemoveCmd)
		if err != nil {
			global.APP_LOG.Warn("移除TCP proxy设备失败",
				zap.String("instance", instanceName),
				zap.String("device", tcpDeviceName),
				zap.Error(err))
		}

		// 删除UDP设备
		udpDeviceName := fmt.Sprintf("proxy-udp-%d", hostPort)
		udpRemoveCmd := fmt.Sprintf("incus config device remove %s %s", instanceName, udpDeviceName)
		_, err = i.sshClient.Execute(udpRemoveCmd)
		if err != nil {
			global.APP_LOG.Warn("移除UDP proxy设备失败",
				zap.String("instance", instanceName),
				zap.String("device", udpDeviceName),
				zap.Error(err))
		}

		global.APP_LOG.Info("Device proxy端口映射移除成功(TCP+UDP)",
			zap.String("instance", instanceName),
			zap.Int("hostPort", hostPort))
	} else {
		// 单一协议
		deviceName := fmt.Sprintf("proxy-%s-%d", protocol, hostPort)
		removeCmd := fmt.Sprintf("incus config device remove %s %s", instanceName, deviceName)
		_, err := i.sshClient.Execute(removeCmd)
		if err != nil {
			return fmt.Errorf("移除proxy设备失败: %w", err)
		}

		global.APP_LOG.Info("Device proxy端口映射移除成功",
			zap.String("instance", instanceName),
			zap.String("device", deviceName))
	}

	return nil
}

// removeIptablesMappingByPort 移除iptables端口映射（通过端口号）
func (i *IncusProvider) removeIptablesMappingByPort(instanceName string, hostPort int, protocol string) error {
	// 获取实例IP
	instanceIP, err := i.getInstanceIP(instanceName)
	if err != nil {
		return fmt.Errorf("获取实例IP失败: %w", err)
	}

	// 如果是both协议，需要删除TCP和UDP规则
	protocols := []string{protocol}
	if protocol == "both" {
		protocols = []string{"tcp", "udp"}
	}

	for _, proto := range protocols {
		// 移除DNAT规则
		dnatCmd := fmt.Sprintf("iptables -t nat -D PREROUTING -p %s --dport %d -j DNAT --to-destination %s",
			proto, hostPort, instanceIP)

		_, err = i.sshClient.Execute(dnatCmd)
		if err != nil {
			global.APP_LOG.Warn("移除DNAT规则失败",
				zap.String("instance", instanceName),
				zap.String("protocol", proto),
				zap.Error(err))
		}

		// 移除FORWARD规则
		forwardCmd := fmt.Sprintf("iptables -D FORWARD -p %s -d %s --dport %d -j ACCEPT",
			proto, instanceIP, hostPort)

		_, err = i.sshClient.Execute(forwardCmd)
		if err != nil {
			global.APP_LOG.Warn("移除FORWARD规则失败",
				zap.String("instance", instanceName),
				zap.String("protocol", proto),
				zap.Error(err))
		}
	}

	global.APP_LOG.Info("Iptables端口映射移除成功",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.String("protocol", protocol))

	return nil
}
