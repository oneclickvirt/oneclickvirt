package lxd

import (
	"fmt"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

// configurePortMappings 配置端口映射
func (l *LXDProvider) configurePortMappings(instanceName string, networkConfig NetworkConfig, instanceIP string) error {
	return l.configurePortMappingsWithIP(instanceName, networkConfig, instanceIP)
}

// configurePortMappingsWithIP 使用指定的实例IP配置端口映射
func (l *LXDProvider) configurePortMappingsWithIP(instanceName string, networkConfig NetworkConfig, instanceIP string) error {
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
	if err := global.APP_DB.Where("name = ?", l.config.Name).First(&provider).Error; err != nil {
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

	// 1. 单独配置SSH端口映射（使用IPv4映射方法）
	if sshPort != nil {
		if err := l.setupPortMappingWithIP(instanceName, sshPort.HostPort, sshPort.GuestPort, sshPort.Protocol, networkConfig.IPv4PortMappingMethod, instanceIP); err != nil {
			global.APP_LOG.Warn("配置SSH端口映射失败",
				zap.String("instance", instanceName),
				zap.Int("hostPort", sshPort.HostPort),
				zap.Int("guestPort", sshPort.GuestPort),
				zap.Error(err))
		}
	}

	// 2. 使用区间映射配置其他端口（主要使用IPv4映射方法）
	if len(otherPorts) > 0 {
		if err := l.setupPortRangeMappingWithIP(instanceName, otherPorts, networkConfig.IPv4PortMappingMethod, instanceIP); err != nil {
			global.APP_LOG.Warn("配置端口区间映射失败",
				zap.String("instance", instanceName),
				zap.Error(err))
		}
	}

	return nil
}

// setupPortRangeMappingWithIP 使用区间映射配置多个端口
func (l *LXDProvider) setupPortRangeMappingWithIP(instanceName string, ports []providerModel.Port, method string, instanceIP string) error {
	if len(ports) == 0 {
		return nil
	}
	// 按协议和端口号排序，尝试找到连续的端口范围
	var tcpPorts []providerModel.Port
	var udpPorts []providerModel.Port
	var bothPorts []providerModel.Port
	for _, port := range ports {
		if port.Protocol == "tcp" {
			tcpPorts = append(tcpPorts, port)
		} else if port.Protocol == "udp" {
			udpPorts = append(udpPorts, port)
		} else if port.Protocol == "both" {
			bothPorts = append(bothPorts, port)
		}
	}
	// 处理TCP端口
	if len(tcpPorts) > 0 {
		if err := l.setupPortRangeByProtocol(instanceName, tcpPorts, "tcp", method, instanceIP); err != nil {
			return fmt.Errorf("设置TCP端口范围失败: %w", err)
		}
	}

	// 处理UDP端口
	if len(udpPorts) > 0 {
		if err := l.setupPortRangeByProtocol(instanceName, udpPorts, "udp", method, instanceIP); err != nil {
			return fmt.Errorf("设置UDP端口范围失败: %w", err)
		}
	}

	// 处理Both端口 - 同时创建TCP和UDP映射
	if len(bothPorts) > 0 {
		// 拆分为tcp和udp端口组
		tcpVersionPorts := make([]providerModel.Port, len(bothPorts))
		udpVersionPorts := make([]providerModel.Port, len(bothPorts))
		for i, port := range bothPorts {
			tcpVersionPorts[i] = port
			tcpVersionPorts[i].Protocol = "tcp"
			udpVersionPorts[i] = port
			udpVersionPorts[i].Protocol = "udp"
		}
		if err := l.setupPortRangeByProtocol(instanceName, tcpVersionPorts, "tcp", method, instanceIP); err != nil {
			return fmt.Errorf("设置TCP端口范围失败: %w", err)
		}
		if err := l.setupPortRangeByProtocol(instanceName, udpVersionPorts, "udp", method, instanceIP); err != nil {
			return fmt.Errorf("设置UDP端口范围失败: %w", err)
		}
	}

	return nil
}

// setupPortRangeByProtocol 按协议设置端口范围映射
func (l *LXDProvider) setupPortRangeByProtocol(instanceName string, ports []providerModel.Port, protocol string, method string, instanceIP string) error {
	if len(ports) == 0 {
		return nil
	}

	// 按端口号排序
	sort.Slice(ports, func(i, j int) bool {
		return ports[i].HostPort < ports[j].HostPort
	})

	// 如果只有一个端口，使用单端口映射
	if len(ports) == 1 {
		port := ports[0]
		return l.setupPortMappingWithIP(instanceName, port.HostPort, port.GuestPort, port.Protocol, method, instanceIP)
	}

	// 检查是否所有端口都是连续的1:1映射
	isConsecutive := true
	startHostPort := ports[0].HostPort
	startGuestPort := ports[0].GuestPort

	for i, port := range ports {
		expectedHostPort := startHostPort + i
		expectedGuestPort := startGuestPort + i
		if port.HostPort != expectedHostPort || port.GuestPort != expectedGuestPort {
			isConsecutive = false
			break
		}
	}

	if isConsecutive && startHostPort == startGuestPort {
		// 使用区间映射（内外端口相同且连续）
		endPort := startHostPort + len(ports) - 1
		return l.setupDeviceProxyRangeMapping(instanceName, startHostPort, endPort, protocol, instanceIP)
	} else {
		// 端口不连续或不是1:1映射，逐个设置
		for _, port := range ports {
			if err := l.setupPortMappingWithIP(instanceName, port.HostPort, port.GuestPort, port.Protocol, method, instanceIP); err != nil {
				global.APP_LOG.Warn("设置单个端口映射失败",
					zap.String("instance", instanceName),
					zap.Int("hostPort", port.HostPort),
					zap.Int("guestPort", port.GuestPort),
					zap.Error(err))
			}
		}
	}

	return nil
}

// setupDeviceProxyRangeMapping 使用LXD device proxy设置端口范围映射
func (l *LXDProvider) setupDeviceProxyRangeMapping(instanceName string, startPort, endPort int, protocol, instanceIP string) error {
	global.APP_LOG.Info("设置LXD端口区间映射",
		zap.String("instance", instanceName),
		zap.Int("startPort", startPort),
		zap.Int("endPort", endPort),
		zap.String("protocol", protocol))

	// 获取主机IP地址
	hostIP, err := l.getHostIP()
	if err != nil {
		return fmt.Errorf("获取主机IP失败: %w", err)
	}

	// 如果协议是both，需要创建两个设备（TCP和UDP）
	if protocol == "both" {
		// 创建TCP区间映射
		tcpDeviceName := fmt.Sprintf("tcp-range-%d-%d", startPort, endPort)
		tcpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s:%s:%d-%d connect=%s:%s:%d-%d",
			instanceName, tcpDeviceName, "tcp", hostIP, startPort, endPort, "tcp", instanceIP, startPort, endPort)

		global.APP_LOG.Info("执行TCP端口区间映射命令",
			zap.String("command", tcpProxyCmd))

		_, err = l.sshClient.Execute(tcpProxyCmd)
		if err != nil {
			return fmt.Errorf("创建TCP端口区间映射失败: %w", err)
		}

		// 创建UDP区间映射
		udpDeviceName := fmt.Sprintf("udp-range-%d-%d", startPort, endPort)
		udpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s:%s:%d-%d connect=%s:%s:%d-%d",
			instanceName, udpDeviceName, "udp", hostIP, startPort, endPort, "udp", instanceIP, startPort, endPort)

		global.APP_LOG.Info("执行UDP端口区间映射命令",
			zap.String("command", udpProxyCmd))

		_, err = l.sshClient.Execute(udpProxyCmd)
		if err != nil {
			return fmt.Errorf("创建UDP端口区间映射失败: %w", err)
		}

		global.APP_LOG.Info("LXD端口区间映射设置成功(TCP+UDP)",
			zap.String("instance", instanceName),
			zap.String("tcpDevice", tcpDeviceName),
			zap.String("udpDevice", udpDeviceName),
			zap.Int("startPort", startPort),
			zap.Int("endPort", endPort))
	} else {
		// 单一协议
		deviceName := fmt.Sprintf("%s-range-%d-%d", protocol, startPort, endPort)

		// 创建LXD device proxy区间映射
		// 格式：lxc config device add <instance> <device-name> proxy listen=tcp:<host-ip>:<start-port>-<end-port> connect=tcp:<guest-ip>:<start-port>-<end-port>
		proxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s:%s:%d-%d connect=%s:%s:%d-%d",
			instanceName, deviceName, protocol, hostIP, startPort, endPort, protocol, instanceIP, startPort, endPort)

		global.APP_LOG.Info("执行LXD端口区间映射命令",
			zap.String("command", proxyCmd))

		_, err = l.sshClient.Execute(proxyCmd)
		if err != nil {
			return fmt.Errorf("创建LXD端口区间映射失败: %w", err)
		}

		global.APP_LOG.Info("LXD端口区间映射设置成功",
			zap.String("instance", instanceName),
			zap.String("device", deviceName),
			zap.Int("startPort", startPort),
			zap.Int("endPort", endPort))
	}

	return nil
}

// setupNATPortRangeMappingWithIP 使用指定的实例IP设置NAT端口范围映射
func (l *LXDProvider) setupNATPortRangeMappingWithIP(instanceName string, startPort, endPort int, method, instanceIP string) error {
	global.APP_LOG.Info("设置NAT端口范围映射",
		zap.String("instance", instanceName),
		zap.Int("startPort", startPort),
		zap.Int("endPort", endPort),
		zap.String("method", method),
		zap.String("instanceIP", instanceIP))
	switch method {
	case "device_proxy":
		return l.setupNATPortRangeDeviceProxyWithIP(instanceName, startPort, endPort, instanceIP)
	case "iptables":
		// TODO: 需要支持iptables的端口范围映射
		return fmt.Errorf("iptables方式的NAT端口范围映射暂未实现")
	default:
		// 默认使用device proxy方式
		return l.setupNATPortRangeDeviceProxyWithIP(instanceName, startPort, endPort, instanceIP)
	}
}

// setupNATPortRangeDeviceProxyWithIP 使用device proxy设置NAT端口范围映射
func (l *LXDProvider) setupNATPortRangeDeviceProxyWithIP(instanceName string, startPort, endPort int, instanceIP string) error {
	// 从instanceIP中提取纯IP地址（去除接口名称等信息）
	cleanInstanceIP := strings.TrimSpace(instanceIP)
	if strings.Contains(cleanInstanceIP, " ") {
		cleanInstanceIP = strings.Split(cleanInstanceIP, " ")[0]
	}

	// 获取主机IP地址
	hostIP, err := l.getHostIP()
	if err != nil {
		return fmt.Errorf("获取主机IP失败: %w", err)
	}

	// 设置TCP端口范围映射 - 使用与buildct.sh脚本相同的格式
	tcpDeviceName := "nattcp-ports"
	tcpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=tcp:%s:%d-%d connect=tcp:0.0.0.0:%d-%d nat=true",
		instanceName, tcpDeviceName, hostIP, startPort, endPort, startPort, endPort)

	global.APP_LOG.Info("执行TCP NAT端口范围映射命令",
		zap.String("command", tcpProxyCmd))

	_, err = l.sshClient.Execute(tcpProxyCmd)
	if err != nil {
		return fmt.Errorf("创建TCP NAT端口范围proxy设备失败: %w", err)
	}

	// 设置UDP端口范围映射 - 使用与buildct.sh脚本相同的格式
	udpDeviceName := "natudp-ports"
	udpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=udp:%s:%d-%d connect=udp:0.0.0.0:%d-%d nat=true",
		instanceName, udpDeviceName, hostIP, startPort, endPort, startPort, endPort)

	global.APP_LOG.Info("执行UDP NAT端口范围映射命令",
		zap.String("command", udpProxyCmd))

	_, err = l.sshClient.Execute(udpProxyCmd)
	if err != nil {
		return fmt.Errorf("创建UDP NAT端口范围proxy设备失败: %w", err)
	}

	global.APP_LOG.Info("NAT端口范围映射设置成功",
		zap.String("instance", instanceName),
		zap.Int("startPort", startPort),
		zap.Int("endPort", endPort))

	return nil
}

// setupPortMapping 设置端口映射
func (l *LXDProvider) setupPortMapping(instanceName string, hostPort, guestPort int, protocol, method string) error {
	global.APP_LOG.Info("设置端口映射",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol),
		zap.String("method", method))

	switch method {
	case "device_proxy":
		return l.setupDeviceProxyMapping(instanceName, hostPort, guestPort, protocol)
	case "iptables":
		return l.setupIptablesMapping(instanceName, hostPort, guestPort, protocol)
	default:
		// 默认使用device proxy方式
		return l.setupDeviceProxyMapping(instanceName, hostPort, guestPort, protocol)
	}
}

// setupPortMappingWithIP 使用指定的实例IP设置端口映射
func (l *LXDProvider) setupPortMappingWithIP(instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error {
	global.APP_LOG.Info("设置端口映射(使用已知IP)",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol),
		zap.String("method", method),
		zap.String("instanceIP", instanceIP))

	switch method {
	case "device_proxy":
		return l.setupDeviceProxyMappingWithIP(instanceName, hostPort, guestPort, protocol, instanceIP)
	case "iptables":
		return l.setupIptablesMappingWithIP(instanceName, hostPort, guestPort, protocol, instanceIP)
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
		return l.setupDeviceProxyMappingWithIP(instanceName, hostPort, guestPort, protocol, instanceIP)
	}
}

// setupDeviceProxyMapping 使用LXD device proxy设置端口映射
func (l *LXDProvider) setupDeviceProxyMapping(instanceName string, hostPort, guestPort int, protocol string) error {
	deviceName := fmt.Sprintf("proxy-%s-%d", protocol, hostPort)

	// 获取实例IP，添加重试逻辑
	var instanceIP string
	var err error

	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		instanceIP, err = l.getInstanceIP(instanceName)
		if err == nil {
			break
		}

		if i < maxRetries-1 {
			global.APP_LOG.Warn("获取实例IP失败，重试中",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", i+1),
				zap.Int("maxRetries", maxRetries),
				zap.Error(err))
			time.Sleep(time.Duration(2*(i+1)) * time.Second) // 递增延迟
		}
	}

	if err != nil {
		return fmt.Errorf("获取实例IP失败: %w", err)
	}

	// 创建proxy设备
	proxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s:%d connect=%s:%d",
		instanceName, deviceName, protocol, hostPort, instanceIP, guestPort)

	_, err = l.sshClient.Execute(proxyCmd)
	if err != nil {
		return fmt.Errorf("创建proxy设备失败: %w", err)
	}

	global.APP_LOG.Info("Device proxy端口映射设置成功",
		zap.String("instance", instanceName),
		zap.String("device", deviceName))

	return nil
}

// setupDeviceProxyMappingWithIP 使用指定的实例IP设置LXD device proxy端口映射
func (l *LXDProvider) setupDeviceProxyMappingWithIP(instanceName string, hostPort, guestPort int, protocol, instanceIP string) error {
	global.APP_LOG.Info("设置Device proxy端口映射(使用已知IP)",
		zap.String("instance", instanceName),
		zap.String("protocol", protocol),
		zap.String("instanceIP", instanceIP))

	// 从instanceIP中提取纯IP地址（去除接口名称等信息）
	cleanInstanceIP := strings.TrimSpace(instanceIP)
	// 提取纯IP地址（移除接口名称等）
	if strings.Contains(cleanInstanceIP, "(") {
		cleanInstanceIP = strings.TrimSpace(strings.Split(cleanInstanceIP, "(")[0])
	}
	// 移除可能的空格和接口名称
	if strings.Contains(cleanInstanceIP, " ") {
		cleanInstanceIP = strings.TrimSpace(strings.Split(cleanInstanceIP, " ")[0])
	}
	// 移除可能的端口号和其他后缀
	if strings.Contains(cleanInstanceIP, "/") {
		cleanInstanceIP = strings.Split(cleanInstanceIP, "/")[0]
	}

	// 获取主机IP地址
	hostIP, err := l.getHostIP()
	if err != nil {
		return fmt.Errorf("获取主机IP失败: %w", err)
	}

	// 如果协议是both，需要创建两个设备（TCP和UDP）
	if protocol == "both" {
		// 创建TCP设备
		tcpDeviceName := fmt.Sprintf("proxy-tcp-%d", hostPort)
		tcpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s:%s:%d connect=%s:%s:%d nat=true",
			instanceName, tcpDeviceName, "tcp", hostIP, hostPort, "tcp", cleanInstanceIP, guestPort)

		global.APP_LOG.Info("执行TCP端口映射命令",
			zap.String("command", tcpProxyCmd))

		_, err = l.sshClient.Execute(tcpProxyCmd)
		if err != nil {
			return fmt.Errorf("创建TCP proxy设备失败: %w", err)
		}

		// 创建UDP设备
		udpDeviceName := fmt.Sprintf("proxy-udp-%d", hostPort)
		udpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s:%s:%d connect=%s:%s:%d nat=true",
			instanceName, udpDeviceName, "udp", hostIP, hostPort, "udp", cleanInstanceIP, guestPort)

		global.APP_LOG.Info("执行UDP端口映射命令",
			zap.String("command", udpProxyCmd))

		_, err = l.sshClient.Execute(udpProxyCmd)
		if err != nil {
			return fmt.Errorf("创建UDP proxy设备失败: %w", err)
		}

		global.APP_LOG.Info("Device proxy端口映射设置成功(TCP+UDP)",
			zap.String("instance", instanceName),
			zap.String("tcpDevice", tcpDeviceName),
			zap.String("udpDevice", udpDeviceName))
	} else {
		// 单一协议
		deviceName := fmt.Sprintf("proxy-%s-%d", protocol, hostPort)

		// 创建proxy设备 - 使用与buildct.sh脚本相同的格式
		proxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s:%s:%d connect=%s:%s:%d nat=true",
			instanceName, deviceName, protocol, hostIP, hostPort, protocol, cleanInstanceIP, guestPort)

		global.APP_LOG.Info("执行端口映射命令",
			zap.String("command", proxyCmd))

		_, err = l.sshClient.Execute(proxyCmd)
		if err != nil {
			return fmt.Errorf("创建proxy设备失败: %w", err)
		}

		global.APP_LOG.Info("Device proxy端口映射设置成功",
			zap.String("instance", instanceName),
			zap.String("device", deviceName))
	}

	return nil
}

// setupIptablesMapping 使用iptables设置端口映射
func (l *LXDProvider) setupIptablesMapping(instanceName string, hostPort, guestPort int, protocol string) error {
	// 获取实例IP
	instanceIP, err := l.getInstanceIP(instanceName)
	if err != nil {
		return fmt.Errorf("获取实例IP失败: %w", err)
	}

	// DNAT规则
	dnatCmd := fmt.Sprintf("iptables -t nat -A PREROUTING -p %s --dport %d -j DNAT --to-destination %s:%d",
		protocol, hostPort, instanceIP, guestPort)

	_, err = l.sshClient.Execute(dnatCmd)
	if err != nil {
		return fmt.Errorf("添加DNAT规则失败: %w", err)
	}

	// FORWARD规则
	forwardCmd := fmt.Sprintf("iptables -A FORWARD -p %s -d %s --dport %d -j ACCEPT",
		protocol, instanceIP, guestPort)

	_, err = l.sshClient.Execute(forwardCmd)
	if err != nil {
		return fmt.Errorf("添加FORWARD规则失败: %w", err)
	}

	// MASQUERADE规则
	masqueradeCmd := fmt.Sprintf("iptables -t nat -A POSTROUTING -p %s -s %s --sport %d -j MASQUERADE",
		protocol, instanceIP, guestPort)

	_, err = l.sshClient.Execute(masqueradeCmd)
	if err != nil {
		return fmt.Errorf("添加MASQUERADE规则失败: %w", err)
	}

	global.APP_LOG.Info("Iptables端口映射设置成功",
		zap.String("instance", instanceName),
		zap.String("target", fmt.Sprintf("%s:%d", instanceIP, guestPort)))

	return nil
}

// setupIptablesMappingWithIP 使用指定的实例IP设置iptables端口映射
func (l *LXDProvider) setupIptablesMappingWithIP(instanceName string, hostPort, guestPort int, protocol, instanceIP string) error {
	global.APP_LOG.Info("设置Iptables端口映射(使用已知IP)",
		zap.String("instance", instanceName),
		zap.String("instanceIP", instanceIP),
		zap.String("protocol", protocol),
		zap.String("target", fmt.Sprintf("%s:%d", instanceIP, guestPort)))

	// 如果协议是both，需要同时创建TCP和UDP规则
	protocols := []string{protocol}
	if protocol == "both" {
		protocols = []string{"tcp", "udp"}
	}

	for _, proto := range protocols {
		// DNAT规则
		dnatCmd := fmt.Sprintf("iptables -t nat -A PREROUTING -p %s --dport %d -j DNAT --to-destination %s:%d",
			proto, hostPort, instanceIP, guestPort)

		_, err := l.sshClient.Execute(dnatCmd)
		if err != nil {
			return fmt.Errorf("添加%s DNAT规则失败: %w", proto, err)
		}

		// FORWARD规则
		forwardCmd := fmt.Sprintf("iptables -A FORWARD -p %s -d %s --dport %d -j ACCEPT",
			proto, instanceIP, guestPort)

		_, err = l.sshClient.Execute(forwardCmd)
		if err != nil {
			return fmt.Errorf("添加%s FORWARD规则失败: %w", proto, err)
		}

		// MASQUERADE规则
		masqueradeCmd := fmt.Sprintf("iptables -t nat -A POSTROUTING -p %s -s %s --sport %d -j MASQUERADE",
			proto, instanceIP, guestPort)

		_, err = l.sshClient.Execute(masqueradeCmd)
		if err != nil {
			return fmt.Errorf("添加%s MASQUERADE规则失败: %w", proto, err)
		}

		global.APP_LOG.Info("Iptables端口映射设置成功",
			zap.String("instance", instanceName),
			zap.String("protocol", proto),
			zap.String("target", fmt.Sprintf("%s:%d", instanceIP, guestPort)))
	}

	return nil
}

// SaveIptablesRules 保存iptables规则到文件（公开方法）
func (l *LXDProvider) SaveIptablesRules() error {
	// 创建iptables目录
	_, err := l.sshClient.Execute("mkdir -p /etc/iptables")
	if err != nil {
		global.APP_LOG.Warn("创建iptables目录失败", zap.Error(err))
	}

	// 保存IPv4规则
	saveCmd := "iptables-save > /etc/iptables/rules.v4"
	_, err = l.sshClient.Execute(saveCmd)
	if err != nil {
		return fmt.Errorf("保存iptables规则失败: %w", err)
	}

	global.APP_LOG.Info("LXD iptables规则保存成功")
	return nil
}

// removePortMapping 移除端口映射
func (l *LXDProvider) removePortMapping(instanceName string, hostPort int, protocol string, method string) error {
	global.APP_LOG.Info("移除端口映射",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.String("protocol", protocol),
		zap.String("method", method))

	switch method {
	case "device_proxy":
		return l.removeDeviceProxyMapping(instanceName, hostPort, protocol)
	case "iptables":
		return l.removeIptablesMapping(instanceName, hostPort, protocol)
	default:
		// 默认使用device proxy方式
		return l.removeDeviceProxyMapping(instanceName, hostPort, protocol)
	}
}

// removeDeviceProxyMapping 移除LXD device proxy映射
func (l *LXDProvider) removeDeviceProxyMapping(instanceName string, hostPort int, protocol string) error {
	deviceName := fmt.Sprintf("proxy-%s-%d", protocol, hostPort)

	removeCmd := fmt.Sprintf("lxc config device remove %s %s", instanceName, deviceName)
	_, err := l.sshClient.Execute(removeCmd)
	if err != nil {
		return fmt.Errorf("移除proxy设备失败: %w", err)
	}

	global.APP_LOG.Info("Device proxy端口映射移除成功",
		zap.String("instance", instanceName),
		zap.String("device", deviceName))

	return nil
}

// removeIptablesMapping 移除iptables端口映射
func (l *LXDProvider) removeIptablesMapping(instanceName string, hostPort int, protocol string) error {
	// 获取实例IP
	instanceIP, err := l.getInstanceIP(instanceName)
	if err != nil {
		return fmt.Errorf("获取实例IP失败: %w", err)
	}

	// 移除DNAT规则
	dnatCmd := fmt.Sprintf("iptables -t nat -D PREROUTING -p %s --dport %d -j DNAT --to-destination %s",
		protocol, hostPort, instanceIP)

	_, err = l.sshClient.Execute(dnatCmd)
	if err != nil {
		global.APP_LOG.Warn("移除DNAT规则失败",
			zap.String("instance", instanceName),
			zap.Error(err))
	}

	// 移除FORWARD规则
	forwardCmd := fmt.Sprintf("iptables -D FORWARD -p %s -d %s --dport %d -j ACCEPT",
		protocol, instanceIP, hostPort)

	_, err = l.sshClient.Execute(forwardCmd)
	if err != nil {
		global.APP_LOG.Warn("移除FORWARD规则失败",
			zap.String("instance", instanceName),
			zap.Error(err))
	}

	global.APP_LOG.Info("Iptables端口映射移除成功",
		zap.String("instance", instanceName))

	return nil
}

// configureFirewallPorts 配置防火墙端口 - 根据实际的端口映射配置（非阻塞式）
func (l *LXDProvider) configureFirewallPorts(instanceName string) error {
	// 从数据库获取实例信息
	// 首先获取Provider ID
	var provider providerModel.Provider
	if err := global.APP_DB.Where("name = ?", l.config.Name).First(&provider).Error; err != nil {
		global.APP_LOG.Warn("获取Provider信息失败，跳过防火墙配置",
			zap.String("instance", instanceName),
			zap.Error(err))
		return nil // 非阻塞，返回 nil
	}

	// 使用Provider ID和实例名称查询实例（组合唯一索引）
	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ? AND provider_id = ?", instanceName, provider.ID).First(&instance).Error; err != nil {
		global.APP_LOG.Warn("获取实例信息失败，跳过防火墙配置",
			zap.String("instance", instanceName),
			zap.Error(err))
		return nil // 非阻塞，返回 nil
	}

	// 获取实例的所有端口映射
	var portMappings []providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND status = 'active'", instance.ID).Find(&portMappings).Error; err != nil {
		global.APP_LOG.Warn("获取端口映射失败，跳过防火墙配置",
			zap.String("instance", instanceName),
			zap.Error(err))
		return nil // 非阻塞，返回 nil
	}

	global.APP_LOG.Info("配置防火墙端口",
		zap.String("instance", instanceName),
		zap.Int("portCount", len(portMappings)))

	// 检查firewall-cmd是否可用
	_, err := l.sshClient.Execute("command -v firewall-cmd")
	if err == nil {
		global.APP_LOG.Info("使用firewall-cmd配置防火墙")

		// 为每个端口映射配置防火墙规则
		for _, port := range portMappings {
			_, err = l.sshClient.Execute(fmt.Sprintf("firewall-cmd --permanent --add-port=%d/%s", port.HostPort, port.Protocol))
			if err != nil {
				global.APP_LOG.Warn("配置端口防火墙规则失败",
					zap.Int("port", port.HostPort),
					zap.String("protocol", port.Protocol),
					zap.Error(err))
			}
		}

		// 重新加载防火墙规则
		_, err = l.sshClient.Execute("firewall-cmd --reload")
		if err != nil {
			global.APP_LOG.Warn("重新加载防火墙规则失败", zap.Error(err))
		}

		return nil
	}

	// 检查ufw是否可用
	_, err = l.sshClient.Execute("command -v ufw")
	if err == nil {
		global.APP_LOG.Info("使用ufw配置防火墙")

		// 为每个端口映射配置ufw规则
		for _, port := range portMappings {
			_, err = l.sshClient.Execute(fmt.Sprintf("ufw allow %d/%s", port.HostPort, port.Protocol))
			if err != nil {
				global.APP_LOG.Warn("配置端口ufw规则失败",
					zap.Int("port", port.HostPort),
					zap.String("protocol", port.Protocol),
					zap.Error(err))
			}
		}

		// 重新加载ufw规则
		_, err = l.sshClient.Execute("ufw reload")
		if err != nil {
			global.APP_LOG.Warn("重新加载ufw规则失败", zap.Error(err))
		}

		return nil
	}

	global.APP_LOG.Info("未找到支持的防火墙管理工具，跳过防火墙配置")
	return nil
}
