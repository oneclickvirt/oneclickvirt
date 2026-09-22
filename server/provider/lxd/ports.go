package lxd

import (
	"context"
	"fmt"
	"net"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider/firewall"
	"oneclickvirt/utils"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

func lxdProxyEndpoint(protocol, address string, port int) string {
	address = strings.Trim(strings.TrimSpace(address), "[]")
	if strings.Contains(address, ":") {
		return fmt.Sprintf("%s:[%s]:%d", protocol, address, port)
	}
	return fmt.Sprintf("%s:%s:%d", protocol, address, port)
}

func lxdProxyEndpointRange(protocol, address string, startPort, endPort int) string {
	address = strings.Trim(strings.TrimSpace(address), "[]")
	if strings.Contains(address, ":") {
		return fmt.Sprintf("%s:[%s]:%d-%d", protocol, address, startPort, endPort)
	}
	return fmt.Sprintf("%s:%s:%d-%d", protocol, address, startPort, endPort)
}

func lxdFamilyLabel(ipv6 bool) string {
	if ipv6 {
		return "IPv6"
	}
	return "IPv4"
}

// lxdHostFirewallProtocols converts the controller's "both" protocol into
// the two protocols understood by firewalld and UFW. Keep this validation at
// the command boundary too: old or manually edited database rows must never
// become shell fragments.
func lxdHostFirewallProtocols(protocol string) ([]string, bool) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "tcp":
		return []string{"tcp"}, true
	case "udp":
		return []string{"udp"}, true
	case "both":
		return []string{"tcp", "udp"}, true
	default:
		return nil, false
	}
}

func lxdHostFirewallPortRange(port providerModel.Port) (int, int, bool) {
	start := port.HostPort
	if start < 1 || start > 65535 {
		return 0, 0, false
	}
	count := port.PortCount
	if count <= 0 {
		count = 1
	}
	if count > 1500 || start+count-1 > 65535 {
		return 0, 0, false
	}
	end := start + count - 1
	if port.HostPortEnd > 0 {
		if port.HostPortEnd < start || port.HostPortEnd > 65535 {
			return 0, 0, false
		}
		if port.PortCount > 0 && port.HostPortEnd != end {
			return 0, 0, false
		}
		end = port.HostPortEnd
	}
	return start, end, true
}

func (l *LXDProvider) applyFirewalldPorts(portMappings []providerModel.Port, remove bool) error {
	operation := "--add-port"
	if remove {
		operation = "--remove-port"
	}
	for _, port := range portMappings {
		start, end, validRange := lxdHostFirewallPortRange(port)
		protocols, ok := lxdHostFirewallProtocols(port.Protocol)
		if !ok || !validRange {
			global.APP_LOG.Warn("跳过无效的firewalld端口规则",
				zap.Int("port", port.HostPort),
				zap.String("protocol", port.Protocol))
			continue
		}
		portSpec := fmt.Sprintf("%d", start)
		if end > start {
			portSpec = fmt.Sprintf("%d-%d", start, end)
		}
		for _, protocol := range protocols {
			if _, err := l.sshClient.Execute(fmt.Sprintf("firewall-cmd --permanent %s=%s/%s", operation, portSpec, protocol)); err != nil {
				global.APP_LOG.Warn("配置firewalld端口失败",
					zap.Int("port", port.HostPort),
					zap.String("protocol", protocol),
					zap.Bool("remove", remove),
					zap.Error(err))
			}
		}
	}
	_, err := l.sshClient.Execute("firewall-cmd --reload")
	return err
}

func (l *LXDProvider) applyUfwPorts(portMappings []providerModel.Port, remove bool) error {
	for _, port := range portMappings {
		start, end, validRange := lxdHostFirewallPortRange(port)
		protocols, ok := lxdHostFirewallProtocols(port.Protocol)
		if !ok || !validRange {
			global.APP_LOG.Warn("跳过无效的ufw端口规则",
				zap.Int("port", port.HostPort),
				zap.String("protocol", port.Protocol))
			continue
		}
		portSpec := fmt.Sprintf("%d", start)
		if end > start {
			portSpec = fmt.Sprintf("%d:%d", start, end)
		}
		for _, protocol := range protocols {
			command := fmt.Sprintf("ufw allow %s/%s", portSpec, protocol)
			if remove {
				command = fmt.Sprintf("ufw --force delete allow %s/%s", portSpec, protocol)
			}
			if _, err := l.sshClient.Execute(command); err != nil {
				global.APP_LOG.Warn("配置ufw端口失败",
					zap.Int("port", port.HostPort),
					zap.String("protocol", protocol),
					zap.Bool("remove", remove),
					zap.Error(err))
			}
		}
	}
	_, err := l.sshClient.Execute("ufw reload")
	return err
}

// removeHostFirewallPorts mirrors configureFirewallPorts for whole-instance
// deletion. Host access rules outlive LXD proxy devices, so leaving them behind
// both grows the ruleset and can accidentally expose a later port reuse.
// Configuration was historically best effort, therefore cleanup remains best
// effort as well and must not make instance deletion impossible.
func (l *LXDProvider) removeHostFirewallPorts(portMappings []providerModel.Port) {
	if _, err := l.sshClient.Execute("command -v firewall-cmd"); err == nil {
		if err := l.applyFirewalldPorts(portMappings, true); err != nil {
			global.APP_LOG.Warn("清理firewalld端口规则失败", zap.Error(err))
		}
		return
	}
	if _, err := l.sshClient.Execute("command -v ufw"); err == nil {
		if err := l.applyUfwPorts(portMappings, true); err != nil {
			global.APP_LOG.Warn("清理ufw端口规则失败", zap.Error(err))
		}
	}
}

// configurePortMappings 配置端口映射
func (l *LXDProvider) configurePortMappings(instanceName string, networkConfig NetworkConfig, instanceIP string) error {
	return l.configurePortMappingsWithIP(instanceName, networkConfig, instanceIP)
}

// configurePortMappingsWithIP 使用指定的实例IP配置端口映射
func (l *LXDProvider) configurePortMappingsWithIP(instanceName string, networkConfig NetworkConfig, instanceIP string) error {
	return l.configurePortMappingFamiliesWithIP(instanceName, networkConfig, instanceIP, true, true)
}

// configureInitialPortMappingsWithIP installs only IPv4 for managed
// dual-stack NAT. The guest ULA and its IPv6 proxy are created in the second
// ordered phase after the bridge lease exists.
func (l *LXDProvider) configureInitialPortMappingsWithIP(instanceName string, networkConfig NetworkConfig, instanceIP string) error {
	if networkConfig.NetworkType == "nat_ipv4_ipv6" {
		return l.configurePortMappingFamiliesWithIP(instanceName, networkConfig, instanceIP, true, false)
	}
	return l.configurePortMappingsWithIP(instanceName, networkConfig, instanceIP)
}

func (l *LXDProvider) configurePortMappingFamiliesWithIP(instanceName string, networkConfig NetworkConfig, instanceIP string, includeIPv4, includeIPv6 bool) error {
	// 独立IPv4模式不需要端口映射；纯IPv6模式必须继续进入IPv6映射
	// 分支，不能在入口处提前返回。
	// dedicated_ipv4: 独立IPv4，不需要端口映射
	// dedicated_ipv4_ipv6: 独立IPv4 + 独立IPv6，不需要端口映射
	if networkConfig.NetworkType == "dedicated_ipv4" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" {
		global.APP_LOG.Debug("独立IPv4模式，跳过端口映射配置",
			zap.String("instance", instanceName),
			zap.String("networkType", networkConfig.NetworkType))
		return nil
	}

	// 从数据库获取实例的端口映射配置
	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ? AND provider_id = ?", instanceName, l.config.ID).First(&instance).Error; err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	// 获取实例的所有端口映射
	var portMappings []providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND status = 'active'", instance.ID).Find(&portMappings).Error; err != nil {
		return fmt.Errorf("获取端口映射失败: %w", err)
	}

	// Controller mappings are handled by the Agent/tunnel service and native
	// mappings are provided by the guest network. Only node-owned mappings may
	// create an LXD proxy or firewall rule here.
	var providerConfig providerModel.Provider
	if err := global.APP_DB.Select("ipv4_port_mapping_method, ipv6_port_mapping_method").First(&providerConfig, l.config.ID).Error; err != nil {
		_ = global.APP_DB.Select("ipv4_port_mapping_method").First(&providerConfig, l.config.ID).Error
	}
	// Keep the SSH path consistent with the API path. An automatic range row
	// with IPv6Enabled=true on a dual-stack NAT instance represents two family
	// mappings; treating it as IPv4-only leaves every IPv6 proxy missing.
	nodeMappings := make([]providerModel.Port, 0, len(portMappings)*2)
	for _, port := range portMappings {
		mappings := providerModel.ExpandPortMappingFamilies(port, networkConfig.NetworkType,
			normalizeLXDMappingMethod(providerConfig.IPv4PortMappingMethod),
			normalizeLXDMappingMethod(providerConfig.IPv6PortMappingMethod))
		for _, mapping := range mappings {
			ipv6 := mapping.IPv6Enabled || strings.TrimSpace(mapping.IPv6Address) != ""
			method := normalizeLXDMappingMethod(mapping.MappingMethod)
			if strings.TrimSpace(mapping.MappingMethod) == "" {
				if ipv6 {
					method = normalizeLXDMappingMethod(providerConfig.IPv6PortMappingMethod)
				} else {
					method = normalizeLXDMappingMethod(providerConfig.IPv4PortMappingMethod)
				}
			}
			if mapping.MappingType == "controller" || method == "native" {
				continue
			}
			if mapping.HostPort < 1 || mapping.HostPort > 65535 || mapping.GuestPort < 1 || mapping.GuestPort > 65535 {
				return fmt.Errorf("端口映射 %d -> %d 超出有效范围", mapping.HostPort, mapping.GuestPort)
			}
			nodeMappings = append(nodeMappings, mapping)
		}
	}
	if len(nodeMappings) == 0 {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("未找到端口映射配置", zap.String("instance", instanceName))
		}
		return nil
	}

	usedIptables := false
	for _, wantIPv6 := range []bool{false, true} {
		if (wantIPv6 && !includeIPv6) || (!wantIPv6 && !includeIPv4) {
			continue
		}
		familyPorts := make([]providerModel.Port, 0, len(nodeMappings))
		for _, mapping := range nodeMappings {
			isIPv6 := mapping.IPv6Enabled || strings.TrimSpace(mapping.IPv6Address) != ""
			if isIPv6 == wantIPv6 {
				familyPorts = append(familyPorts, mapping)
			}
		}
		if len(familyPorts) == 0 {
			continue
		}
		methodDefault := networkConfig.IPv4PortMappingMethod
		targetIP := instanceIP
		if wantIPv6 {
			methodDefault = networkConfig.IPv6PortMappingMethod
			targetIP = strings.TrimSpace(instance.IPv6Address)
			if targetIP == "" {
				var err error
				targetIP, err = l.getIPv6PortMappingTarget(instanceName)
				if err != nil {
					return fmt.Errorf("获取IPv6地址失败，无法配置端口映射: %w", err)
				}
			}
		}
		if targetIP == "" || methodDefault == "" {
			return fmt.Errorf("端口映射缺少目标地址或映射方式 (ipv6=%t target=%q method=%q)", wantIPv6, targetIP, methodDefault)
		}
		sshPorts := make([]providerModel.Port, 0, 1)
		otherPorts := make([]providerModel.Port, 0, len(familyPorts))
		for _, mapping := range familyPorts {
			if mapping.IsSSH {
				sshPorts = append(sshPorts, mapping)
			} else {
				otherPorts = append(otherPorts, mapping)
			}
		}
		for _, sshPort := range sshPorts {
			method := normalizeLXDMappingMethod(sshPort.MappingMethod)
			if method == "" {
				method = normalizeLXDMappingMethod(methodDefault)
			}
			if err := l.setupPortMappingWithIP(instanceName, sshPort.HostPort, sshPort.GuestPort, sshPort.Protocol, method, targetIP); err != nil {
				return fmt.Errorf("配置%s SSH端口映射失败 (host=%d guest=%d): %w", lxdFamilyLabel(wantIPv6), sshPort.HostPort, sshPort.GuestPort, err)
			}
			usedIptables = usedIptables || method == "iptables"
		}
		if len(otherPorts) > 0 {
			method := normalizeLXDMappingMethod(otherPorts[0].MappingMethod)
			if method == "" {
				method = normalizeLXDMappingMethod(methodDefault)
			}
			if err := l.setupPortRangeMappingWithIP(instanceName, otherPorts, method, targetIP); err != nil {
				return fmt.Errorf("配置%s端口区间映射失败: %w", lxdFamilyLabel(wantIPv6), err)
			}
			usedIptables = usedIptables || method == "iptables"
		}
	}
	if usedIptables {
		return l.SaveIptablesRules()
	}
	return nil
}

// getIPv6PortMappingTarget also reads the controller-owned address file. The
// file remains available while an instance is stopped, whereas state.network
// is often empty during the stop/start window used to add proxy devices.
func (l *LXDProvider) getIPv6PortMappingTarget(instanceName string) (string, error) {
	if output, err := l.sshClient.Execute(fmt.Sprintf("cat %s 2>/dev/null", shellSingleQuote(instanceName+"_v6"))); err == nil {
		if address, parseErr := utils.ParseFirstIPv6AddressOutput(output); parseErr == nil {
			return address, nil
		}
	}
	return l.GetInstanceIPv6(instanceName)
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
		// Keep each logical both mapping together so its TCP device can be
		// rolled back when creating the UDP device fails.
		if err := l.setupPortRangeByProtocol(instanceName, bothPorts, "both", method, instanceIP); err != nil {
			return fmt.Errorf("设置TCP/UDP端口范围失败: %w", err)
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
		switch method {
		case "device_proxy", "":
			return l.setupDeviceProxyRangeMapping(instanceName, startHostPort, endPort, protocol, instanceIP)
		case "iptables":
			for _, port := range ports {
				if err := l.setupPortMappingWithIP(instanceName, port.HostPort, port.GuestPort, protocol, method, instanceIP); err != nil {
					return fmt.Errorf("设置端口 %d/%s 映射失败: %w", port.HostPort, protocol, err)
				}
			}
			return nil
		case "native":
			global.APP_LOG.Debug("native模式跳过端口范围映射",
				zap.String("instance", instanceName),
				zap.Int("startPort", startHostPort),
				zap.Int("endPort", endPort),
				zap.String("protocol", protocol))
			return nil
		default:
			return l.setupDeviceProxyRangeMapping(instanceName, startHostPort, endPort, protocol, instanceIP)
		}
	} else {
		// 端口不连续或不是1:1映射，逐个设置
		for _, port := range ports {
			if err := l.setupPortMappingWithIP(instanceName, port.HostPort, port.GuestPort, port.Protocol, method, instanceIP); err != nil {
				return fmt.Errorf("设置单个端口映射失败 (host=%d guest=%d): %w", port.HostPort, port.GuestPort, err)
			}
		}
	}

	return nil
}

// setupDeviceProxyRangeMapping 使用LXD device proxy设置端口范围映射
func (l *LXDProvider) setupDeviceProxyRangeMapping(instanceName string, startPort, endPort int, protocol, instanceIP string) error {
	global.APP_LOG.Debug("设置LXD端口区间映射",
		zap.String("instance", instanceName),
		zap.Int("startPort", startPort),
		zap.Int("endPort", endPort),
		zap.String("protocol", protocol))

	instanceIP = normalizeLXDMappingIP(instanceIP)
	if instanceIP == "" {
		return fmt.Errorf("实例端口映射目标地址无效")
	}
	if l.sshClient == nil || !l.sshClient.HasExecutor() {
		return fmt.Errorf("SSH client不可用，无法固定device proxy目标地址")
	}
	if strings.Contains(instanceIP, ":") {
		if err := utils.SetLXCAddressBinding(l.sshClient, "lxc", instanceName, instanceIP, true); err != nil {
			return fmt.Errorf("固定实例IPv6地址失败: %w", err)
		}
	} else {
		if err := utils.SetLXCIPv4Binding(l.sshClient, "lxc", instanceName, instanceIP); err != nil {
			return fmt.Errorf("固定实例IPv4地址失败: %w", err)
		}
	}

	// NAT mode also supports VMs and requires a concrete host listener.
	hostIP, err := l.getNATProxyListenIP(context.Background(), strings.Contains(instanceIP, ":"))
	if err != nil {
		return fmt.Errorf("获取主机IP失败: %w", err)
	}
	devicePrefix := ""
	if strings.Contains(instanceIP, ":") {
		devicePrefix = "v6-"
	}

	// 如果协议是both，需要创建两个设备（TCP和UDP）
	if protocol == "both" {
		// 创建TCP区间映射
		tcpDeviceName := fmt.Sprintf("%stcp-range-%d-%d", devicePrefix, startPort, endPort)
		tcpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(tcpDeviceName), shellSingleQuote(lxdProxyEndpointRange("tcp", hostIP, startPort, endPort)), shellSingleQuote(lxdProxyEndpointRange("tcp", instanceIP, startPort, endPort)))

		global.APP_LOG.Debug("执行TCP端口区间映射命令",
			zap.String("command", tcpProxyCmd))

		_, err = l.sshClient.Execute(tcpProxyCmd)
		if err != nil {
			return fmt.Errorf("创建TCP端口区间映射失败: %w", err)
		}

		// 创建UDP区间映射
		udpDeviceName := fmt.Sprintf("%sudp-range-%d-%d", devicePrefix, startPort, endPort)
		udpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(udpDeviceName), shellSingleQuote(lxdProxyEndpointRange("udp", hostIP, startPort, endPort)), shellSingleQuote(lxdProxyEndpointRange("udp", instanceIP, startPort, endPort)))

		global.APP_LOG.Debug("执行UDP端口区间映射命令",
			zap.String("command", udpProxyCmd))

		_, err = l.sshClient.Execute(udpProxyCmd)
		if err != nil {
			// 回滚：删除已创建的TCP区间设备
			rollbackCmd := fmt.Sprintf("lxc config device remove %s %s", shellSingleQuote(instanceName), shellSingleQuote(tcpDeviceName))
			if _, rollbackErr := l.sshClient.Execute(rollbackCmd); rollbackErr != nil {
				global.APP_LOG.Warn("回滚TCP区间proxy设备失败",
					zap.String("instance", instanceName),
					zap.String("device", tcpDeviceName),
					zap.Error(rollbackErr))
			}
			return fmt.Errorf("创建UDP端口区间映射失败: %w", err)
		}

		global.APP_LOG.Debug("LXD端口区间映射设置成功(TCP+UDP)",
			zap.String("instance", instanceName),
			zap.String("tcpDevice", tcpDeviceName),
			zap.String("udpDevice", udpDeviceName),
			zap.Int("startPort", startPort),
			zap.Int("endPort", endPort))
	} else {
		// 单一协议
		deviceName := fmt.Sprintf("%s%s-range-%d-%d", devicePrefix, protocol, startPort, endPort)

		// 创建LXD device proxy区间映射
		// 格式：lxc config device add <instance> <device-name> proxy listen=tcp:<host-ip>:<start-port>-<end-port> connect=tcp:<guest-ip>:<start-port>-<end-port>
		proxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(deviceName), shellSingleQuote(lxdProxyEndpointRange(protocol, hostIP, startPort, endPort)), shellSingleQuote(lxdProxyEndpointRange(protocol, instanceIP, startPort, endPort)))

		global.APP_LOG.Debug("执行LXD端口区间映射命令",
			zap.String("command", proxyCmd))

		_, err = l.sshClient.Execute(proxyCmd)
		if err != nil {
			return fmt.Errorf("创建LXD端口区间映射失败: %w", err)
		}

		global.APP_LOG.Debug("LXD端口区间映射设置成功",
			zap.String("instance", instanceName),
			zap.String("device", deviceName),
			zap.Int("startPort", startPort),
			zap.Int("endPort", endPort))
	}

	return nil
}

// setupNATPortRangeMappingWithIP 使用指定的实例IP设置NAT端口范围映射
func (l *LXDProvider) setupNATPortRangeMappingWithIP(instanceName string, startPort, endPort int, method, instanceIP string) error {
	global.APP_LOG.Debug("设置NAT端口范围映射",
		zap.String("instance", instanceName),
		zap.Int("startPort", startPort),
		zap.Int("endPort", endPort),
		zap.String("method", method),
		zap.String("instanceIP", instanceIP))
	switch method {
	case "device_proxy":
		return l.setupNATPortRangeDeviceProxyWithIP(instanceName, startPort, endPort, instanceIP)
	case "iptables":
		for port := startPort; port <= endPort; port++ {
			if err := l.setupIptablesMappingWithIP(instanceName, port, port, "both", instanceIP); err != nil {
				return fmt.Errorf("设置NAT端口 %d 映射失败: %w", port, err)
			}
		}
		return nil
	default:
		// 默认使用device proxy方式
		return l.setupNATPortRangeDeviceProxyWithIP(instanceName, startPort, endPort, instanceIP)
	}
}

// setupNATPortRangeDeviceProxyWithIP 使用device proxy设置NAT端口范围映射
func (l *LXDProvider) setupNATPortRangeDeviceProxyWithIP(instanceName string, startPort, endPort int, instanceIP string) error {
	// 从instanceIP中提取纯IP地址（去除接口名称等信息）
	cleanInstanceIP := strings.TrimSpace(instanceIP)
	cleanInstanceIP = strings.Trim(cleanInstanceIP, "[]")
	if strings.Contains(cleanInstanceIP, " ") {
		cleanInstanceIP = strings.Split(cleanInstanceIP, " ")[0]
	}
	if cleanInstanceIP == "" || net.ParseIP(cleanInstanceIP) == nil {
		return fmt.Errorf("实例IP无效: %q", instanceIP)
	}
	if l.sshClient == nil || !l.sshClient.HasExecutor() {
		return fmt.Errorf("SSH client不可用，无法固定device proxy目标地址")
	}
	if strings.Contains(cleanInstanceIP, ":") {
		if err := utils.SetLXCAddressBinding(l.sshClient, "lxc", instanceName, cleanInstanceIP, true); err != nil {
			return fmt.Errorf("固定实例IPv6地址失败: %w", err)
		}
	} else {
		if err := utils.SetLXCIPv4Binding(l.sshClient, "lxc", instanceName, cleanInstanceIP); err != nil {
			return fmt.Errorf("固定实例IPv4地址失败: %w", err)
		}
	}

	// 获取主机IP地址
	hostIP, err := l.getNATProxyListenIP(context.Background(), strings.Contains(cleanInstanceIP, ":"))
	if err != nil {
		return fmt.Errorf("获取主机IP失败: %w", err)
	}
	// Use the guest's reserved address as the explicit NAT destination.
	tcpDeviceName := "nattcp-ports"
	tcpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s connect=%s nat=true",
		shellSingleQuote(instanceName), shellSingleQuote(tcpDeviceName), shellSingleQuote(lxdProxyEndpointRange("tcp", hostIP, startPort, endPort)), shellSingleQuote(lxdProxyEndpointRange("tcp", cleanInstanceIP, startPort, endPort)))

	global.APP_LOG.Debug("执行TCP NAT端口范围映射命令",
		zap.String("command", tcpProxyCmd))

	_, err = l.sshClient.Execute(tcpProxyCmd)
	if err != nil {
		return fmt.Errorf("创建TCP NAT端口范围proxy设备失败: %w", err)
	}

	// UDP 也必须使用同一个真实实例地址，并与 TCP 保持原子回滚语义。
	udpDeviceName := "natudp-ports"
	udpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s connect=%s nat=true",
		shellSingleQuote(instanceName), shellSingleQuote(udpDeviceName), shellSingleQuote(lxdProxyEndpointRange("udp", hostIP, startPort, endPort)), shellSingleQuote(lxdProxyEndpointRange("udp", cleanInstanceIP, startPort, endPort)))

	global.APP_LOG.Debug("执行UDP NAT端口范围映射命令",
		zap.String("command", udpProxyCmd))

	_, err = l.sshClient.Execute(udpProxyCmd)
	if err != nil {
		// 回滚：删除已创建的TCP设备
		rollbackCmd := fmt.Sprintf("lxc config device remove %s %s", shellSingleQuote(instanceName), shellSingleQuote(tcpDeviceName))
		if _, rollbackErr := l.sshClient.Execute(rollbackCmd); rollbackErr != nil {
			global.APP_LOG.Warn("回滚TCP NAT proxy设备失败",
				zap.String("instance", instanceName),
				zap.String("device", tcpDeviceName),
				zap.Error(rollbackErr))
		}
		return fmt.Errorf("创建UDP NAT端口范围proxy设备失败: %w", err)
	}

	global.APP_LOG.Debug("创建NAT端口范围映射成功",
		zap.String("instance", instanceName),
		zap.Int("startPort", startPort),
		zap.Int("endPort", endPort))

	return nil
}

// setupPortMapping 设置端口映射
func (l *LXDProvider) setupPortMapping(instanceName string, hostPort, guestPort int, protocol, method string) error {
	global.APP_LOG.Debug("设置端口映射",
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
	instanceIP = normalizeLXDMappingIP(instanceIP)
	if instanceIP == "" {
		return fmt.Errorf("实例端口映射目标地址无效")
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "udp" && protocol != "both" {
		return fmt.Errorf("不支持的端口协议 %q", protocol)
	}
	global.APP_LOG.Debug("设置端口映射(使用已知IP)",
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
		global.APP_LOG.Debug("独立IPv4模式，跳过端口映射",
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

func normalizeLXDMappingIP(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, ":") {
		if ip, err := utils.ParseFirstIPv6AddressOutput(value); err == nil {
			return ip
		}
	} else if ip, err := utils.ParseFirstIPv4AddressOutput(value); err == nil {
		return ip
	}
	return ""
}

// setupDeviceProxyMapping 使用LXD device proxy设置端口映射
func (l *LXDProvider) setupDeviceProxyMapping(instanceName string, hostPort, guestPort int, protocol string) error {
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

	return l.setupDeviceProxyMappingWithIP(instanceName, hostPort, guestPort, protocol, instanceIP)
}

// setupDeviceProxyMappingWithIP 使用指定的实例IP设置LXD device proxy端口映射
func (l *LXDProvider) setupDeviceProxyMappingWithIP(instanceName string, hostPort, guestPort int, protocol, instanceIP string) error {
	if l.sshClient == nil || !l.sshClient.HasExecutor() {
		return fmt.Errorf("SSH client不可用，无法固定device proxy目标地址")
	}
	if strings.Contains(instanceIP, ":") {
		if err := utils.SetLXCAddressBinding(l.sshClient, "lxc", instanceName, instanceIP, true); err != nil {
			return fmt.Errorf("固定实例IPv6地址失败: %w", err)
		}
	} else {
		if err := utils.SetLXCIPv4Binding(l.sshClient, "lxc", instanceName, instanceIP); err != nil {
			return fmt.Errorf("固定实例IPv4地址失败: %w", err)
		}
	}
	global.APP_LOG.Debug("设置Device proxy端口映射(使用已知IP)",
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
	hostIP, err := l.getNATProxyListenIP(context.Background(), strings.Contains(cleanInstanceIP, ":"))
	if err != nil {
		return fmt.Errorf("获取主机IP失败: %w", err)
	}
	devicePrefix := "proxy"
	if strings.Contains(cleanInstanceIP, ":") {
		devicePrefix = "proxy-v6"
	}

	// 如果协议是both，需要创建两个设备（TCP和UDP）
	if protocol == "both" {
		// 创建TCP设备
		tcpDeviceName := fmt.Sprintf("%s-tcp-%d", devicePrefix, hostPort)
		tcpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(tcpDeviceName), shellSingleQuote(lxdProxyEndpoint("tcp", hostIP, hostPort)), shellSingleQuote(lxdProxyEndpoint("tcp", cleanInstanceIP, guestPort)))

		global.APP_LOG.Debug("执行TCP端口映射命令",
			zap.String("command", tcpProxyCmd))

		_, err = l.sshClient.Execute(tcpProxyCmd)
		if err != nil {
			return fmt.Errorf("创建TCP proxy设备失败: %w", err)
		}

		// 创建UDP设备
		udpDeviceName := fmt.Sprintf("%s-udp-%d", devicePrefix, hostPort)
		udpProxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(udpDeviceName), shellSingleQuote(lxdProxyEndpoint("udp", hostIP, hostPort)), shellSingleQuote(lxdProxyEndpoint("udp", cleanInstanceIP, guestPort)))

		global.APP_LOG.Debug("执行UDP端口映射命令",
			zap.String("command", udpProxyCmd))

		_, err = l.sshClient.Execute(udpProxyCmd)
		if err != nil {
			// 回滚：删除已创建的TCP设备
			rollbackCmd := fmt.Sprintf("lxc config device remove %s %s", shellSingleQuote(instanceName), shellSingleQuote(tcpDeviceName))
			if _, rollbackErr := l.sshClient.Execute(rollbackCmd); rollbackErr != nil {
				global.APP_LOG.Warn("回滚TCP proxy设备失败",
					zap.String("instance", instanceName),
					zap.String("device", tcpDeviceName),
					zap.Error(rollbackErr))
			}
			return fmt.Errorf("创建UDP proxy设备失败: %w", err)
		}

		global.APP_LOG.Debug("Device proxy端口映射设置成功(TCP+UDP)",
			zap.String("instance", instanceName),
			zap.String("tcpDevice", tcpDeviceName),
			zap.String("udpDevice", udpDeviceName))
	} else {
		// 单一协议
		deviceName := fmt.Sprintf("%s-%s-%d", devicePrefix, protocol, hostPort)

		// 创建proxy设备 - 使用与buildct.sh脚本相同的格式
		proxyCmd := fmt.Sprintf("lxc config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(deviceName), shellSingleQuote(lxdProxyEndpoint(protocol, hostIP, hostPort)), shellSingleQuote(lxdProxyEndpoint(protocol, cleanInstanceIP, guestPort)))

		global.APP_LOG.Debug("执行端口映射命令",
			zap.String("command", proxyCmd))

		_, err = l.sshClient.Execute(proxyCmd)
		if err != nil {
			return fmt.Errorf("创建proxy设备失败: %w", err)
		}

		global.APP_LOG.Debug("Device proxy端口映射设置成功",
			zap.String("instance", instanceName),
			zap.String("device", deviceName))
	}

	return nil
}

// setupIptablesMapping 使用防火墙设置端口映射（nft优先，iptables回退）
func (l *LXDProvider) setupIptablesMapping(instanceName string, hostPort, guestPort int, protocol string) error {
	instanceIP, err := l.getInstanceIP(instanceName)
	if err != nil {
		return fmt.Errorf("获取实例IP失败: %w", err)
	}
	return l.setupIptablesMappingWithIP(instanceName, hostPort, guestPort, protocol, instanceIP)
}

// setupIptablesMappingWithIP 使用指定的实例IP设置防火墙端口映射（nft优先，iptables回退）
func (l *LXDProvider) setupIptablesMappingWithIP(instanceName string, hostPort, guestPort int, protocol, instanceIP string) error {
	global.APP_LOG.Debug("设置防火墙端口映射(使用已知IP)",
		zap.String("instance", instanceName),
		zap.String("instanceIP", instanceIP),
		zap.String("protocol", protocol),
		zap.String("target", fmt.Sprintf("%s:%d", instanceIP, guestPort)))

	fwMgr := firewall.NewManager(l.sshClient, "lxd", "")
	if _, err := fwMgr.DetectBackend("/usr/local/bin/lxd_fw_backend"); err != nil {
		return fmt.Errorf("防火墙后端检测失败: %w", err)
	}
	if err := fwMgr.InitTable(); err != nil {
		return fmt.Errorf("防火墙初始化失败: %w", err)
	}

	comment := fmt.Sprintf("pm:%s:%d:%d", instanceName, hostPort, guestPort)
	if err := fwMgr.RemoveSingleDNAT(instanceIP, hostPort, guestPort, protocol, comment); err != nil {
		return fmt.Errorf("清理旧防火墙规则失败: %w", err)
	}
	if err := fwMgr.AddSingleDNAT(instanceIP, hostPort, guestPort, protocol, comment); err != nil {
		return fmt.Errorf("添加防火墙规则失败: %w", err)
	}

	global.APP_LOG.Debug("防火墙端口映射设置成功",
		zap.String("instance", instanceName),
		zap.String("protocol", protocol),
		zap.String("target", fmt.Sprintf("%s:%d", instanceIP, guestPort)))

	return nil
}

// SaveIptablesRules 保存防火墙规则到文件（公开方法）
func (l *LXDProvider) SaveIptablesRules() error {
	fwMgr := firewall.NewManager(l.sshClient, "lxd", "")
	if _, err := fwMgr.DetectBackend("/usr/local/bin/lxd_fw_backend"); err != nil {
		return err
	}
	return fwMgr.SaveRules()
}

// removePortMapping 移除端口映射
func (l *LXDProvider) removePortMapping(instanceName string, hostPort int, protocol string, method string) error {
	global.APP_LOG.Info("移除端口映射",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.String("protocol", protocol),
		zap.String("method", method))

	guestPort, instanceIP := l.lookupPortMappingDetails(instanceName, hostPort, protocol)
	start, end := l.lookupPortMappingRange(instanceName, hostPort, protocol)
	return l.removePortMappingWithRange(instanceName, hostPort, guestPort, end, guestPort+end-start, end-start+1, protocol, method, instanceIP)
}

// lxdRemoveDeviceCommand is idempotent only for an already absent device.
// A blanket "|| true" hides permission, daemon, and transport-side command
// failures and leaves a live port mapping behind while the task reports
// success.
func lxdRemoveDeviceCommand(instanceName, deviceName string) string {
	return fmt.Sprintf(`set -eu
if output=$(lxc config device remove %s %s 2>&1); then
    exit 0
fi
status=$?
case "$output" in
    *"not found"*|*"does not exist"*|*"doesn't exist"*|*"No such device"*) exit 0 ;;
esac
printf 'lxc device removal failed: %%s\n' "$output" >&2
exit "$status"`, shellSingleQuote(instanceName), shellSingleQuote(deviceName))
}

func (l *LXDProvider) removeDeviceProxyRangeMapping(instanceName string, hostPort, hostPortEnd int, protocol string) error {
	for _, ipv6 := range []bool{false, true} {
		if err := l.removeDeviceProxyRangeMappingForFamily(instanceName, hostPort, hostPortEnd, protocol, ipv6); err != nil {
			return err
		}
	}
	return nil
}

func (l *LXDProvider) removeDeviceProxyRangeMappingForFamily(instanceName string, hostPort, hostPortEnd int, protocol string, ipv6 bool) error {
	protocols := []string{strings.ToLower(strings.TrimSpace(protocol))}
	if protocols[0] == "" {
		protocols[0] = "tcp"
	}
	if protocols[0] == "both" {
		protocols = []string{"tcp", "udp"}
	}
	for _, proto := range protocols {
		prefix, rangePrefix := "proxy-", ""
		if ipv6 {
			prefix, rangePrefix = "proxy-v6-", "v6-"
		}
		deviceNames := []string{fmt.Sprintf("%s%s-%d", prefix, proto, hostPort)}
		if hostPortEnd > hostPort {
			deviceNames = append(deviceNames,
				fmt.Sprintf("%s%s-range-%d-%d", rangePrefix, proto, hostPort, hostPortEnd),
				fmt.Sprintf("%s%s-%d-%d", prefix, proto, hostPort, hostPortEnd))
			if !ipv6 {
				deviceNames = append(deviceNames, fmt.Sprintf("nat%s-ports", proto))
			}
		}
		for _, deviceName := range deviceNames {
			cmd := lxdRemoveDeviceCommand(instanceName, deviceName)
			if _, err := l.sshClient.Execute(cmd); err != nil {
				return fmt.Errorf("移除proxy设备失败: %w", err)
			}
		}
	}
	return nil
}

// removeDeviceProxyMapping 移除LXD device proxy映射
func (l *LXDProvider) removeDeviceProxyMapping(instanceName string, hostPort int, protocol string) error {
	protocols := []string{protocol}
	if protocol == "both" {
		protocols = []string{"tcp", "udp"}
	}
	for _, proto := range protocols {
		for _, deviceName := range []string{fmt.Sprintf("proxy-%s-%d", proto, hostPort), fmt.Sprintf("proxy-v6-%s-%d", proto, hostPort)} {
			removeCmd := lxdRemoveDeviceCommand(instanceName, deviceName)
			if _, err := l.sshClient.Execute(removeCmd); err != nil {
				return fmt.Errorf("移除proxy设备失败: %w", err)
			}
		}
	}

	global.APP_LOG.Debug("Device proxy端口映射移除成功",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.String("protocol", protocol))

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

	global.APP_LOG.Debug("Iptables端口映射移除成功",
		zap.String("instance", instanceName))

	return nil
}

// configureFirewallPorts 配置防火墙端口 - 根据实际的端口映射配置（非阻塞式）
func (l *LXDProvider) configureFirewallPorts(instanceName string) error {
	// 从数据库获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ? AND provider_id = ?", instanceName, l.config.ID).First(&instance).Error; err != nil {
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

	global.APP_LOG.Debug("配置防火墙端口",
		zap.String("instance", instanceName),
		zap.Int("portCount", len(portMappings)))

	// 检查firewall-cmd是否可用
	_, err := l.sshClient.Execute("command -v firewall-cmd")
	if err == nil {
		global.APP_LOG.Debug("使用firewall-cmd配置防火墙")
		err = l.applyFirewalldPorts(portMappings, false)
		if err != nil {
			global.APP_LOG.Warn("重新加载防火墙规则失败", zap.Error(err))
		}

		return nil
	}

	// 检查ufw是否可用
	_, err = l.sshClient.Execute("command -v ufw")
	if err == nil {
		global.APP_LOG.Debug("使用ufw配置防火墙")
		err = l.applyUfwPorts(portMappings, false)
		if err != nil {
			global.APP_LOG.Warn("重新加载ufw规则失败", zap.Error(err))
		}

		return nil
	}

	global.APP_LOG.Debug("未找到支持的防火墙管理工具，跳过防火墙配置")
	return nil
}
