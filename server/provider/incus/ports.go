package incus

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider/firewall"
	"oneclickvirt/utils"
	"sort"
	"strings"

	"go.uber.org/zap"
)

func proxyEndpoint(protocol, address string, port int) string {
	address = strings.TrimSpace(address)
	address = strings.TrimPrefix(address, "[")
	address = strings.TrimSuffix(address, "]")
	if strings.Contains(address, ":") {
		return fmt.Sprintf("%s:[%s]:%d", protocol, address, port)
	}
	return fmt.Sprintf("%s:%s:%d", protocol, address, port)
}

func proxyEndpointRange(protocol, address string, startPort, endPort int) string {
	address = strings.TrimSpace(address)
	address = strings.TrimPrefix(address, "[")
	address = strings.TrimSuffix(address, "]")
	if strings.Contains(address, ":") {
		return fmt.Sprintf("%s:[%s]:%d-%d", protocol, address, startPort, endPort)
	}
	return fmt.Sprintf("%s:%s:%d-%d", protocol, address, startPort, endPort)
}

func familyLabel(ipv6 bool) string {
	if ipv6 {
		return "IPv6"
	}
	return "IPv4"
}

// incusHostFirewallProtocols converts the controller's "both" value into
// concrete protocols and rejects malformed legacy database values before they
// can reach a remote shell command.
func incusHostFirewallProtocols(protocol string) ([]string, bool) {
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

func incusHostFirewallPortRange(port providerModel.Port) (int, int, bool) {
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

// configurePortMappings 配置端口映射
func (i *IncusProvider) configurePortMappings(ctx context.Context, instanceName string, networkConfig NetworkConfig, instanceIP string) error {
	return i.configurePortMappingsWithIP(ctx, instanceName, networkConfig, instanceIP)
}

// configurePortMappingsWithIP 使用指定的实例IP配置端口映射
func (i *IncusProvider) configurePortMappingsWithIP(ctx context.Context, instanceName string, networkConfig NetworkConfig, instanceIP string) error {
	return i.configurePortMappingFamiliesWithIP(ctx, instanceName, networkConfig, instanceIP, true, true)
}

// configureInitialPortMappingsWithIP prevents a dual-stack row from trying to
// install its IPv6 proxy before the managed bridge has assigned the guest ULA.
func (i *IncusProvider) configureInitialPortMappingsWithIP(ctx context.Context, instanceName string, networkConfig NetworkConfig, instanceIP string) error {
	if networkConfig.NetworkType == "nat_ipv4_ipv6" {
		return i.configurePortMappingFamiliesWithIP(ctx, instanceName, networkConfig, instanceIP, true, false)
	}
	return i.configurePortMappingsWithIP(ctx, instanceName, networkConfig, instanceIP)
}

func (i *IncusProvider) configurePortMappingFamiliesWithIP(ctx context.Context, instanceName string, networkConfig NetworkConfig, instanceIP string, includeIPv4, includeIPv6 bool) error {
	// 检查是否为独立IPv4模式，如果是则跳过端口映射。纯IPv6模式
	// 仍然需要走下面的IPv6分支，不能在这里提前返回。
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
	if err := global.APP_DB.Where("name = ? AND provider_id = ?", instanceName, i.config.ID).First(&instance).Error; err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	// 获取实例的所有端口映射
	var portMappings []providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND status = 'active'", instance.ID).Find(&portMappings).Error; err != nil {
		return fmt.Errorf("获取端口映射失败: %w", err)
	}

	// Controller mappings are terminated by the Agent/tunnel service and
	// native mappings are intentionally provided by the guest network itself.
	// Do not try to install a host-side proxy for either kind.  In particular,
	// an Agent provider can have active controller rows even though this node
	// has no SSH-visible forwarding to configure.
	var providerConfig providerModel.Provider
	if err := global.APP_DB.Select("ipv4_port_mapping_method, ipv6_port_mapping_method").First(&providerConfig, i.config.ID).Error; err != nil {
		// Older test fixtures and upgraded databases may not have the IPv6
		// column yet. The runtime default still supplies the IPv6 method, so
		// retain the IPv4 fallback instead of silently losing all mappings.
		_ = global.APP_DB.Select("ipv4_port_mapping_method").First(&providerConfig, i.config.ID).Error
	}
	// A historical automatic range row with IPv6Enabled=true represents both
	// families on dual-stack NAT networks. Expand it before dispatching so the
	// SSH path has the same family semantics as the API-only path. Previously
	// this path treated every row as IPv4 unless the whole instance was
	// ipv6_only, which silently created IPv4 proxy devices only.
	nodeMappings := make([]providerModel.Port, 0, len(portMappings)*2)
	for _, port := range portMappings {
		mappings := providerModel.ExpandPortMappingFamilies(port, networkConfig.NetworkType,
			normalizeIncusMappingMethod(providerConfig.IPv4PortMappingMethod),
			normalizeIncusMappingMethod(providerConfig.IPv6PortMappingMethod))
		for _, mapping := range mappings {
			ipv6 := mapping.IPv6Enabled || strings.TrimSpace(mapping.IPv6Address) != ""
			method := normalizeIncusMappingMethod(mapping.MappingMethod)
			if strings.TrimSpace(mapping.MappingMethod) == "" {
				if ipv6 {
					method = normalizeIncusMappingMethod(providerConfig.IPv6PortMappingMethod)
				} else {
					method = normalizeIncusMappingMethod(providerConfig.IPv4PortMappingMethod)
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
				targetIP, err = i.getIPv6PortMappingTarget(ctx, instanceName)
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
			method := normalizeIncusMappingMethod(sshPort.MappingMethod)
			if method == "" {
				method = normalizeIncusMappingMethod(methodDefault)
			}
			if err := i.setupPortMappingWithIP(instanceName, sshPort.HostPort, sshPort.GuestPort, sshPort.Protocol, method, targetIP); err != nil {
				return fmt.Errorf("配置%s SSH端口映射失败 (host=%d guest=%d): %w", familyLabel(wantIPv6), sshPort.HostPort, sshPort.GuestPort, err)
			}
			usedIptables = usedIptables || method == "iptables"
		}
		if len(otherPorts) > 0 {
			method := normalizeIncusMappingMethod(otherPorts[0].MappingMethod)
			if method == "" {
				method = normalizeIncusMappingMethod(methodDefault)
			}
			if err := i.setupPortRangeMappingWithIP(instanceName, otherPorts, method, targetIP); err != nil {
				return fmt.Errorf("配置%s端口区间映射失败: %w", familyLabel(wantIPv6), err)
			}
			usedIptables = usedIptables || method == "iptables"
		}
	}
	if usedIptables {
		return i.SaveIptablesRules()
	}
	return nil
}

// getIPv6PortMappingTarget also reads the controller-owned address file. The
// file remains available while an instance is stopped, whereas state.network
// is often empty during the stop/start window used to add proxy devices.
func (i *IncusProvider) getIPv6PortMappingTarget(ctx context.Context, instanceName string) (string, error) {
	if output, err := i.sshClient.Execute(fmt.Sprintf("cat %s 2>/dev/null", shellSingleQuote(instanceName+"_v6"))); err == nil {
		if address, parseErr := utils.ParseFirstIPv6AddressOutput(output); parseErr == nil {
			return address, nil
		}
	}
	return i.GetInstanceIPv6(ctx, instanceName)
}

// configureFirewallPorts 配置防火墙端口
func (i *IncusProvider) configureFirewallPorts(instanceName string) error {
	// 获取实例的端口映射信息
	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ? AND provider_id = ?", instanceName, i.config.ID).First(&instance).Error; err != nil {
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
	return i.applyFirewalldPorts(portMappings, false)
}

func (i *IncusProvider) applyFirewalldPorts(portMappings []providerModel.Port, remove bool) error {
	operation := "--add-port"
	if remove {
		operation = "--remove-port"
	}
	for _, port := range portMappings {
		start, end, validRange := incusHostFirewallPortRange(port)
		protocols, ok := incusHostFirewallProtocols(port.Protocol)
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

		for _, proto := range protocols {
			cmd := fmt.Sprintf("firewall-cmd --permanent %s=%s/%s", operation, portSpec, proto)
			_, err := i.sshClient.Execute(cmd)
			if err != nil {
				global.APP_LOG.Warn("配置firewalld端口失败",
					zap.Int("port", port.HostPort),
					zap.String("protocol", proto),
					zap.Bool("remove", remove),
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
	return i.applyUfwPorts(portMappings, false)
}

func (i *IncusProvider) applyUfwPorts(portMappings []providerModel.Port, remove bool) error {
	for _, port := range portMappings {
		start, end, validRange := incusHostFirewallPortRange(port)
		protocols, ok := incusHostFirewallProtocols(port.Protocol)
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

		for _, proto := range protocols {
			cmd := fmt.Sprintf("ufw allow %s/%s", portSpec, proto)
			if remove {
				cmd = fmt.Sprintf("ufw --force delete allow %s/%s", portSpec, proto)
			}
			_, err := i.sshClient.Execute(cmd)
			if err != nil {
				global.APP_LOG.Warn("配置ufw端口失败",
					zap.Int("port", port.HostPort),
					zap.String("protocol", proto),
					zap.Bool("remove", remove),
					zap.Error(err))
			}
		}
	}

	// 重新加载ufw配置
	_, err := i.sshClient.Execute("ufw reload")
	return err
}

// removeHostFirewallPorts mirrors configureFirewallPorts when the whole Incus
// instance is deleted. The rules are host-global and otherwise survive after
// the proxy device and database rows are gone. Keep this best effort because
// their original installation is deliberately non-blocking.
func (i *IncusProvider) removeHostFirewallPorts(portMappings []providerModel.Port) {
	if i.hasFirewalld() {
		if err := i.applyFirewalldPorts(portMappings, true); err != nil {
			global.APP_LOG.Warn("清理firewalld端口规则失败", zap.Error(err))
		}
		return
	}
	if i.hasUfw() {
		if err := i.applyUfwPorts(portMappings, true); err != nil {
			global.APP_LOG.Warn("清理ufw端口规则失败", zap.Error(err))
		}
	}
}

// setupPortMappingWithIP 使用指定的实例IP设置端口映射
func (i *IncusProvider) setupPortMappingWithIP(instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error {
	instanceIP = normalizeIncusMappingIP(instanceIP)
	if instanceIP == "" {
		return fmt.Errorf("实例端口映射目标地址无效: %q", instanceIP)
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
		return i.setupDeviceProxyMappingWithIP(instanceName, hostPort, guestPort, protocol, instanceIP)
	case "iptables":
		return i.setupIptablesMappingWithIP(instanceName, hostPort, guestPort, protocol, instanceIP)
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
		return i.setupDeviceProxyMappingWithIP(instanceName, hostPort, guestPort, protocol, instanceIP)
	}
}

func normalizeIncusMappingIP(value string) string {
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

// setupDeviceProxyMappingWithIP 使用Incus device proxy设置端口映射
func (i *IncusProvider) setupDeviceProxyMappingWithIP(instanceName string, hostPort, guestPort int, protocol, instanceIP string) error {
	if i.sshClient == nil || !i.sshClient.HasExecutor() {
		return fmt.Errorf("SSH client不可用，无法固定device proxy目标地址")
	}
	if strings.Contains(instanceIP, ":") {
		if err := utils.SetLXCAddressBinding(i.sshClient, "incus", instanceName, instanceIP, true); err != nil {
			return fmt.Errorf("固定实例IPv6地址失败: %w", err)
		}
	} else {
		if err := utils.SetLXCIPv4Binding(i.sshClient, "incus", instanceName, instanceIP); err != nil {
			return fmt.Errorf("固定实例IPv4地址失败: %w", err)
		}
	}
	hostIP, err := i.getNATProxyListenIP(context.Background(), strings.Contains(instanceIP, ":"))
	if err != nil {
		return err
	}
	connectIP := strings.Trim(strings.TrimSpace(instanceIP), "[]")
	if strings.Contains(connectIP, "/") {
		connectIP = strings.Split(connectIP, "/")[0]
	}
	if connectIP == "" {
		return fmt.Errorf("device proxy映射缺少实例目标地址")
	}
	devicePrefix := "proxy"
	if strings.Contains(instanceIP, ":") {
		// NAT requires a concrete host address in the same family as the guest.
		connectIP = strings.Trim(strings.TrimSpace(instanceIP), "[]")
		devicePrefix = "proxy-v6"
	}

	// 如果协议是both，需要创建两个设备（TCP和UDP）
	if protocol == "both" {
		// 创建TCP设备
		tcpDeviceName := fmt.Sprintf("%s-tcp-%d", devicePrefix, hostPort)
		tcpCmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(tcpDeviceName), shellSingleQuote(proxyEndpoint("tcp", hostIP, hostPort)), shellSingleQuote(proxyEndpoint("tcp", connectIP, guestPort)))

		_, err = i.sshClient.Execute(tcpCmd)
		if err != nil {
			return fmt.Errorf("设置TCP device proxy映射失败: %w", err)
		}

		// 创建UDP设备
		udpDeviceName := fmt.Sprintf("%s-udp-%d", devicePrefix, hostPort)
		udpCmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(udpDeviceName), shellSingleQuote(proxyEndpoint("udp", hostIP, hostPort)), shellSingleQuote(proxyEndpoint("udp", connectIP, guestPort)))

		_, err = i.sshClient.Execute(udpCmd)
		if err != nil {
			// A `both` mapping is a two-device transaction. Do not leave the
			// TCP half behind when the UDP device fails (for example because the
			// host port was concurrently claimed).
			rollbackCmd := incusRemoveDeviceCommand(instanceName, tcpDeviceName)
			if _, rollbackErr := i.sshClient.Execute(rollbackCmd); rollbackErr != nil {
				global.APP_LOG.Warn("回滚TCP device proxy设备失败",
					zap.String("instance", instanceName),
					zap.String("device", tcpDeviceName),
					zap.Error(rollbackErr))
			}
			return fmt.Errorf("设置UDP device proxy映射失败: %w", err)
		}

		global.APP_LOG.Debug("device proxy端口映射配置成功(TCP+UDP)",
			zap.String("instanceName", instanceName),
			zap.String("tcpDeviceName", tcpDeviceName),
			zap.String("udpDeviceName", udpDeviceName),
			zap.Int("hostPort", hostPort),
			zap.Int("guestPort", guestPort))
	} else {
		// 单一协议
		deviceName := fmt.Sprintf("%s-%s-%d", devicePrefix, protocol, hostPort)
		cmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(deviceName), shellSingleQuote(proxyEndpoint(strings.ToLower(protocol), hostIP, hostPort)), shellSingleQuote(proxyEndpoint(strings.ToLower(protocol), connectIP, guestPort)))

		_, err = i.sshClient.Execute(cmd)
		if err != nil {
			return fmt.Errorf("设置device proxy映射失败: %w", err)
		}

		global.APP_LOG.Debug("device proxy端口映射配置成功",
			zap.String("instanceName", instanceName),
			zap.String("deviceName", deviceName),
			zap.Int("hostPort", hostPort),
			zap.Int("guestPort", guestPort))
	}

	return nil
}

// setupIptablesMappingWithIP 使用防火墙设置端口映射（nft优先，iptables回退）
func (i *IncusProvider) setupIptablesMappingWithIP(instanceName string, hostPort, guestPort int, protocol, instanceIP string) error {
	global.APP_LOG.Debug("使用防火墙设置端口映射",
		zap.String("instanceName", instanceName),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol),
		zap.String("instanceIP", instanceIP))

	fwMgr := firewall.NewManager(i.sshClient, "incus", "")
	if _, err := fwMgr.DetectBackend("/usr/local/bin/incus_fw_backend"); err != nil {
		return fmt.Errorf("防火墙后端检测失败: %w", err)
	}
	if err := fwMgr.InitTable(); err != nil {
		return fmt.Errorf("防火墙初始化失败: %w", err)
	}

	comment := fmt.Sprintf("pm:%s:%d:%d", instanceName, hostPort, guestPort)
	// Make retries idempotent. This also prevents a timed-out create task from
	// adding the same DNAT rule twice.
	if err := fwMgr.RemoveSingleDNAT(instanceIP, hostPort, guestPort, protocol, comment); err != nil {
		return fmt.Errorf("清理旧防火墙规则失败: %w", err)
	}
	if err := fwMgr.AddSingleDNAT(instanceIP, hostPort, guestPort, protocol, comment); err != nil {
		return fmt.Errorf("添加防火墙规则失败: %w", err)
	}

	global.APP_LOG.Info("防火墙端口映射设置成功",
		zap.String("instanceName", instanceName),
		zap.String("protocol", protocol),
		zap.String("target", fmt.Sprintf("%s:%d", instanceIP, guestPort)))

	return nil
}

// SaveIptablesRules 保存防火墙规则到文件（公开方法）
func (i *IncusProvider) SaveIptablesRules() error {
	fwMgr := firewall.NewManager(i.sshClient, "incus", "")
	if _, err := fwMgr.DetectBackend("/usr/local/bin/incus_fw_backend"); err != nil {
		return err
	}
	return fwMgr.SaveRules()
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
					return fmt.Errorf("单个端口映射失败(tcp) %d: %w", tcpPort.HostPort, err)
				}
				udpPort := port
				udpPort.Protocol = "udp"
				err = i.setupPortMappingWithIP(instanceName, udpPort.HostPort, udpPort.GuestPort, "udp", method, instanceIP)
				if err != nil {
					// The TCP half is part of the same logical `both` mapping.
					switch normalizeIncusMappingMethod(method) {
					case "device_proxy":
						_ = i.removeDeviceProxyRangeMappingForFamily(instanceName, tcpPort.HostPort, tcpPort.HostPort, "tcp", strings.Contains(instanceIP, ":"))
					case "iptables":
						_ = i.removeIptablesMapping(instanceName, tcpPort.HostPort, tcpPort.GuestPort, "tcp", instanceIP)
					}
					return fmt.Errorf("单个端口映射失败(udp) %d: %w", udpPort.HostPort, err)
				}
			} else {
				err := i.setupPortMappingWithIP(instanceName, port.HostPort, port.GuestPort, port.Protocol, method, instanceIP)
				if err != nil {
					return fmt.Errorf("单个端口映射失败 %d: %w", port.HostPort, err)
				}
			}
		} else {
			// 端口范围
			startPort := portRange[0]
			endPort := portRange[len(portRange)-1]
			if startPort.Protocol == "both" {
				// 分别映射 tcp 和 udp
				err := i.setupPortRangeByMethodWithIP(instanceName, portRange, method, instanceIP, "tcp")
				if err != nil {
					return fmt.Errorf("端口范围映射失败(tcp) %d-%d: %w", startPort.HostPort, endPort.HostPort, err)
				}
				err = i.setupPortRangeByMethodWithIP(instanceName, portRange, method, instanceIP, "udp")
				if err != nil {
					if normalizeIncusMappingMethod(method) == "device_proxy" {
						_ = i.removeDeviceProxyRangeMappingForFamily(instanceName, startPort.HostPort, endPort.HostPort, "tcp", strings.Contains(instanceIP, ":"))
					}
					return fmt.Errorf("端口范围映射失败(udp) %d-%d: %w", startPort.HostPort, endPort.HostPort, err)
				}
			} else {
				err := i.setupPortRangeByMethodWithIP(instanceName, portRange, method, instanceIP, startPort.Protocol)
				if err != nil {
					return fmt.Errorf("端口范围映射失败 %d-%d: %w", startPort.HostPort, endPort.HostPort, err)
				}
			}
		}
	}

	return nil
}

func (i *IncusProvider) setupPortRangeByMethodWithIP(instanceName string, ports []providerModel.Port, method, instanceIP, protocol string) error {
	if len(ports) == 0 {
		return nil
	}

	startPort := ports[0].HostPort
	endPort := ports[len(ports)-1].HostPort
	guestStart := ports[0].GuestPort
	guestEnd := ports[len(ports)-1].GuestPort
	switch method {
	case "device_proxy", "":
		return i.setupPortRangeMapping(instanceName, startPort, endPort, guestStart, guestEnd, protocol, instanceIP)
	case "iptables":
		for _, port := range ports {
			if err := i.setupPortMappingWithIP(instanceName, port.HostPort, port.GuestPort, protocol, method, instanceIP); err != nil {
				return fmt.Errorf("设置端口 %d/%s 映射失败: %w", port.HostPort, protocol, err)
			}
		}
		return nil
	case "native":
		global.APP_LOG.Debug("native模式跳过端口范围映射",
			zap.String("instance", instanceName),
			zap.Int("startPort", startPort),
			zap.Int("endPort", endPort),
			zap.String("protocol", protocol))
		return nil
	default:
		return i.setupPortRangeMapping(instanceName, startPort, endPort, guestStart, guestEnd, protocol, instanceIP)
	}
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
			ports[i].GuestPort == ports[i-1].GuestPort+1 &&
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
func (i *IncusProvider) setupPortRangeMapping(instanceName string, startPort, endPort, guestStart, guestEnd int, protocol string, instanceIP string) error {
	if startPort < 1 || endPort > 65535 || endPort < startPort || guestStart < 1 || guestEnd > 65535 || guestEnd < guestStart || endPort-startPort != guestEnd-guestStart {
		return fmt.Errorf("端口映射范围无效: host=%d-%d guest=%d-%d", startPort, endPort, guestStart, guestEnd)
	}
	instanceIP = normalizeIncusMappingIP(instanceIP)
	if instanceIP == "" {
		return fmt.Errorf("实例端口映射目标地址无效")
	}
	// Incus NAT proxy requires the connect address to be declared as a static
	// address on the guest NIC, including for range devices.  Single-port
	// mappings do this in setupDeviceProxyMappingWithIP; keep the range path
	// consistent so the first range does not fail while single ports work.
	if i.sshClient == nil || !i.sshClient.HasExecutor() {
		return fmt.Errorf("SSH client不可用，无法固定device proxy目标地址")
	}
	if strings.Contains(instanceIP, ":") {
		if err := utils.SetLXCAddressBinding(i.sshClient, "incus", instanceName, instanceIP, true); err != nil {
			return fmt.Errorf("固定实例IPv6地址失败: %w", err)
		}
	} else {
		if err := utils.SetLXCIPv4Binding(i.sshClient, "incus", instanceName, instanceIP); err != nil {
			return fmt.Errorf("固定实例IPv4地址失败: %w", err)
		}
	}
	hostIP, err := i.getNATProxyListenIP(context.Background(), strings.Contains(instanceIP, ":"))
	if err != nil {
		return err
	}
	connectIP := strings.Trim(strings.TrimSpace(instanceIP), "[]")
	if strings.Contains(connectIP, "/") {
		connectIP = strings.Split(connectIP, "/")[0]
	}
	if connectIP == "" {
		return fmt.Errorf("端口范围映射缺少实例目标地址")
	}
	devicePrefix := "proxy"
	if strings.Contains(instanceIP, ":") {
		connectIP = strings.Trim(strings.TrimSpace(instanceIP), "[]")
		devicePrefix = "proxy-v6"
	}

	// 如果协议是both，需要创建两个设备（TCP和UDP）
	if protocol == "both" {
		// 创建TCP范围映射
		tcpDeviceName := fmt.Sprintf("%s-tcp-%d-%d", devicePrefix, startPort, endPort)
		tcpCmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(tcpDeviceName), shellSingleQuote(proxyEndpointRange("tcp", hostIP, startPort, endPort)), shellSingleQuote(proxyEndpointRange("tcp", connectIP, guestStart, guestEnd)))

		_, err = i.sshClient.Execute(tcpCmd)
		if err != nil {
			return fmt.Errorf("设置TCP端口范围映射失败: %w", err)
		}

		// 创建UDP范围映射
		udpDeviceName := fmt.Sprintf("%s-udp-%d-%d", devicePrefix, startPort, endPort)
		udpCmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(udpDeviceName), shellSingleQuote(proxyEndpointRange("udp", hostIP, startPort, endPort)), shellSingleQuote(proxyEndpointRange("udp", connectIP, guestStart, guestEnd)))

		_, err = i.sshClient.Execute(udpCmd)
		if err != nil {
			rollbackCmd := incusRemoveDeviceCommand(instanceName, tcpDeviceName)
			if _, rollbackErr := i.sshClient.Execute(rollbackCmd); rollbackErr != nil {
				global.APP_LOG.Warn("回滚TCP端口范围device proxy设备失败",
					zap.String("instance", instanceName),
					zap.String("device", tcpDeviceName),
					zap.Error(rollbackErr))
			}
			return fmt.Errorf("设置UDP端口范围映射失败: %w", err)
		}

		global.APP_LOG.Debug("端口范围映射配置成功(TCP+UDP)",
			zap.String("instanceName", instanceName),
			zap.String("tcpDeviceName", tcpDeviceName),
			zap.String("udpDeviceName", udpDeviceName),
			zap.Int("startPort", startPort),
			zap.Int("endPort", endPort))
	} else {
		// 单一协议
		deviceName := fmt.Sprintf("%s-%s-%d-%d", devicePrefix, protocol, startPort, endPort)
		cmd := fmt.Sprintf("incus config device add %s %s proxy listen=%s connect=%s nat=true",
			shellSingleQuote(instanceName), shellSingleQuote(deviceName), shellSingleQuote(proxyEndpointRange(strings.ToLower(protocol), hostIP, startPort, endPort)), shellSingleQuote(proxyEndpointRange(strings.ToLower(protocol), connectIP, guestStart, guestEnd)))

		_, err = i.sshClient.Execute(cmd)
		if err != nil {
			return fmt.Errorf("设置端口范围映射失败: %w", err)
		}

		global.APP_LOG.Debug("端口范围映射配置成功",
			zap.String("instanceName", instanceName),
			zap.String("deviceName", deviceName),
			zap.Int("startPort", startPort),
			zap.Int("endPort", endPort))
	}

	return nil
}

// removePortMapping 移除端口映射
func (i *IncusProvider) removePortMapping(instanceName string, hostPort int, protocol string, method string) error {
	global.APP_LOG.Debug("移除端口映射",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.String("protocol", protocol),
		zap.String("method", method))

	guestPort, instanceIP := i.lookupPortMappingDetails(instanceName, hostPort, protocol)
	start, end := i.lookupPortMappingRange(instanceName, hostPort, protocol)
	return i.removePortMappingWithRange(instanceName, hostPort, guestPort, end, guestPort+end-start, end-start+1, protocol, method, instanceIP)
}

// incusRemoveDeviceCommand is idempotent only for an already absent device.
// A blanket "|| true" hides permission, daemon, and transport-side command
// failures and leaves a live port mapping behind while the task reports
// success.
func incusRemoveDeviceCommand(instanceName, deviceName string) string {
	return fmt.Sprintf(`set -eu
if output=$(incus config device remove %s %s 2>&1); then
    exit 0
fi
status=$?
case "$output" in
    *"not found"*|*"does not exist"*|*"doesn't exist"*|*"No such device"*) exit 0 ;;
esac
printf 'incus device removal failed: %%s\n' "$output" >&2
exit "$status"`, shellSingleQuote(instanceName), shellSingleQuote(deviceName))
}

func (i *IncusProvider) removeDeviceProxyRangeMapping(instanceName string, hostPort, hostPortEnd int, protocol string) error {
	for _, ipv6 := range []bool{false, true} {
		if err := i.removeDeviceProxyRangeMappingForFamily(instanceName, hostPort, hostPortEnd, protocol, ipv6); err != nil {
			return err
		}
	}
	return nil
}

func (i *IncusProvider) removeDeviceProxyRangeMappingForFamily(instanceName string, hostPort, hostPortEnd int, protocol string, ipv6 bool) error {
	protocols := []string{strings.ToLower(strings.TrimSpace(protocol))}
	if protocols[0] == "" {
		protocols[0] = "tcp"
	}
	if protocols[0] == "both" {
		protocols = []string{"tcp", "udp"}
	}
	for _, proto := range protocols {
		prefix := "proxy-"
		if ipv6 {
			prefix = "proxy-v6-"
		}
		deviceNames := []string{fmt.Sprintf("%s%s-%d", prefix, proto, hostPort)}
		if hostPortEnd > hostPort {
			deviceNames = append(deviceNames,
				fmt.Sprintf("%s%s-%d-%d", prefix, proto, hostPort, hostPortEnd))
		}
		for _, deviceName := range deviceNames {
			cmd := incusRemoveDeviceCommand(instanceName, deviceName)
			if _, err := i.sshClient.Execute(cmd); err != nil {
				return fmt.Errorf("移除proxy设备失败: %w", err)
			}
		}
	}
	return nil
}

// removeDeviceProxyMapping 移除Incus device proxy映射
func (i *IncusProvider) removeDeviceProxyMapping(instanceName string, hostPort int, protocol string) error {
	// 如果是both协议，需要删除TCP和UDP两个设备
	if protocol == "both" {
		for _, proto := range []string{"tcp", "udp"} {
			for _, deviceName := range []string{fmt.Sprintf("proxy-%s-%d", proto, hostPort), fmt.Sprintf("proxy-v6-%s-%d", proto, hostPort)} {
				removeCmd := incusRemoveDeviceCommand(instanceName, deviceName)
				if _, err := i.sshClient.Execute(removeCmd); err != nil {
					global.APP_LOG.Warn("移除proxy设备失败", zap.String("instance", instanceName), zap.String("device", deviceName), zap.Error(err))
				}
			}
		}

		global.APP_LOG.Debug("Device proxy端口映射移除成功(TCP+UDP)",
			zap.String("instance", instanceName),
			zap.Int("hostPort", hostPort))
	} else {
		// 单一协议
		for _, deviceName := range []string{fmt.Sprintf("proxy-%s-%d", protocol, hostPort), fmt.Sprintf("proxy-v6-%s-%d", protocol, hostPort)} {
			removeCmd := incusRemoveDeviceCommand(instanceName, deviceName)
			if _, err := i.sshClient.Execute(removeCmd); err != nil {
				return fmt.Errorf("移除proxy设备失败: %w", err)
			}
		}

		global.APP_LOG.Debug("Device proxy端口映射移除成功",
			zap.String("instance", instanceName),
			zap.String("device", fmt.Sprintf("proxy[-v6]-%s-%d", protocol, hostPort)))
	}

	return nil
}

// removeIptablesMappingByPort 移除iptables端口映射（通过端口号）
func (i *IncusProvider) removeIptablesMappingByPort(instanceName string, hostPort int, protocol string) error {
	guestPort, instanceIP := i.lookupPortMappingDetails(instanceName, hostPort, protocol)
	return i.removeIptablesMapping(instanceName, hostPort, guestPort, protocol, instanceIP)
}
