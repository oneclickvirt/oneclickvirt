package proxmox

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// configureInstancePortMappings 配置实例端口映射
func (p *ProxmoxProvider) configureInstancePortMappings(ctx context.Context, config provider.InstanceConfig, vmid int) error {
	// 等待实例完全启动
	time.Sleep(3 * time.Second)

	global.APP_LOG.Info("开始配置PVE实例端口映射",
		zap.String("instance", config.Name),
		zap.Int("vmid", vmid))

	// 确定实例类型
	instanceType := config.InstanceType
	if instanceType == "" {
		instanceType = "vm" // 默认为虚拟机
	}

	// 获取实例的内网IP地址，使用vmid而不是名称
	vmidStr := fmt.Sprintf("%d", vmid)
	instanceIP, err := p.getInstanceIPAddress(ctx, vmidStr, instanceType)
	if err != nil {
		global.APP_LOG.Error("获取实例内网IP失败",
			zap.String("instance", config.Name),
			zap.Int("vmid", vmid),
			zap.Error(err))
		return fmt.Errorf("获取实例内网IP失败: %w", err)
	}

	if instanceIP == "" {
		global.APP_LOG.Error("获取到空的实例IP地址",
			zap.String("instance", config.Name),
			zap.Int("vmid", vmid))
		return fmt.Errorf("无法获取实例 %s 的IP地址", config.Name)
	}

	global.APP_LOG.Info("获取到实例内网IP",
		zap.String("instance", config.Name),
		zap.Int("vmid", vmid),
		zap.String("instanceIP", instanceIP))

	// 解析网络配置
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	// 调用现有的端口映射配置函数（使用ports.go中的实现）
	err = p.configurePortMappingsWithIP(ctx, config.Name, networkConfig, instanceIP)
	if err != nil {
		global.APP_LOG.Error("配置端口映射失败",
			zap.String("instance", config.Name),
			zap.Error(err))
		return fmt.Errorf("配置端口映射失败: %w", err)
	}

	global.APP_LOG.Info("PVE实例端口映射配置成功",
		zap.String("instance", config.Name),
		zap.Int("vmid", vmid))

	return nil
}

// cleanupInstancePortMappings 清理实例的端口映射
func (p *ProxmoxProvider) cleanupInstancePortMappings(ctx context.Context, vmid string, instanceType string) error {
	global.APP_LOG.Info("开始清理实例端口映射",
		zap.String("vmid", vmid),
		zap.String("instanceType", instanceType))

	// 1. 查找通过vmid对应的实例名称
	instances, err := p.ListInstances(ctx)
	if err != nil {
		global.APP_LOG.Warn("获取实例列表失败，尝试通过vmid清理端口映射", zap.String("vmid", vmid), zap.Error(err))
		// 即使获取实例列表失败，也要尝试清理端口映射
	}

	var instanceName string
	for _, instance := range instances {
		// 从实例ID中提取vmid（假设ID格式是vmid或包含vmid）
		if instance.ID == vmid || strings.Contains(instance.ID, vmid) {
			instanceName = instance.Name
			break
		}
	}

	// 2. 如果找到了实例名称，尝试从数据库获取端口映射进行清理
	if instanceName != "" {
		global.APP_LOG.Info("找到实例名称，开始清理数据库中的端口映射",
			zap.String("vmid", vmid),
			zap.String("instanceName", instanceName))

		// 从数据库获取实例的端口映射
		var instance providerModel.Instance
		if err := global.APP_DB.Where("name = ?", instanceName).First(&instance).Error; err != nil {
			global.APP_LOG.Warn("从数据库获取实例信息失败", zap.String("instanceName", instanceName), zap.Error(err))
		} else {
			// 获取实例的所有端口映射
			var portMappings []providerModel.Port
			if err := global.APP_DB.Where("instance_id = ? AND status = 'active'", instance.ID).Find(&portMappings).Error; err != nil {
				global.APP_LOG.Warn("获取端口映射失败", zap.String("instanceName", instanceName), zap.Error(err))
			} else {
				// 清理每个端口映射
				for _, port := range portMappings {
					if err := p.removePortMapping(ctx, instanceName, port.HostPort, port.Protocol, port.MappingMethod); err != nil {
						global.APP_LOG.Warn("移除端口映射失败",
							zap.String("instanceName", instanceName),
							zap.Int("hostPort", port.HostPort),
							zap.String("protocol", port.Protocol),
							zap.Error(err))
					} else {
						global.APP_LOG.Info("端口映射清理成功",
							zap.String("instanceName", instanceName),
							zap.Int("hostPort", port.HostPort),
							zap.String("protocol", port.Protocol))
					}
				}
			}
		}
	}

	// 3. 尝试基于推断的IP地址清理iptables规则（统一的IP分配规则）
	// VM和Container都使用相同的IP分配规则: 172.16.1.{VMID}, VMID范围: 10-255
	if instanceType == "vm" || instanceType == "container" {
		vmidInt, err := strconv.Atoi(vmid)
		if err == nil && vmidInt >= MinVMID && vmidInt <= MaxVMID {
			inferredIP := fmt.Sprintf("%s.%d", InternalIPPrefix, vmidInt)
			global.APP_LOG.Info("尝试基于推断IP清理iptables规则",
				zap.String("vmid", vmid),
				zap.String("inferredIP", inferredIP))

			// 清理常见的端口映射规则
			if err := p.cleanupIptablesRulesForIP(ctx, inferredIP); err != nil {
				global.APP_LOG.Warn("清理推断IP的iptables规则失败",
					zap.String("inferredIP", inferredIP),
					zap.Error(err))
			}
		}
	}

	global.APP_LOG.Info("实例端口映射清理完成",
		zap.String("vmid", vmid),
		zap.String("instanceType", instanceType))

	return nil
}

// cleanupIptablesRulesForIP 清理指定IP地址的iptables规则
func (p *ProxmoxProvider) cleanupIptablesRulesForIP(ctx context.Context, ipAddress string) error {
	global.APP_LOG.Info("清理IP地址的iptables规则", zap.String("ipAddress", ipAddress))

	// 清理DNAT规则
	dnatCmd := fmt.Sprintf("iptables -t nat -S PREROUTING | grep 'DNAT.*%s' | sed 's/^-A /-D /' | while read line; do iptables -t nat $line 2>/dev/null || true; done", ipAddress)
	_, err := p.sshClient.Execute(dnatCmd)
	if err != nil {
		global.APP_LOG.Warn("清理DNAT规则失败", zap.String("ipAddress", ipAddress), zap.Error(err))
	}

	// 清理FORWARD规则
	forwardCmd := fmt.Sprintf("iptables -S FORWARD | grep '%s' | sed 's/^-A /-D /' | while read line; do iptables $line 2>/dev/null || true; done", ipAddress)
	_, err = p.sshClient.Execute(forwardCmd)
	if err != nil {
		global.APP_LOG.Warn("清理FORWARD规则失败", zap.String("ipAddress", ipAddress), zap.Error(err))
	}

	// 清理MASQUERADE规则
	masqueradeCmd := fmt.Sprintf("iptables -t nat -S POSTROUTING | grep '%s' | sed 's/^-A /-D /' | while read line; do iptables -t nat $line 2>/dev/null || true; done", ipAddress)
	_, err = p.sshClient.Execute(masqueradeCmd)
	if err != nil {
		global.APP_LOG.Warn("清理MASQUERADE规则失败", zap.String("ipAddress", ipAddress), zap.Error(err))
	}

	// 保存iptables规则
	_, err = p.sshClient.Execute("iptables-save > /etc/iptables/rules.v4 2>/dev/null || true")
	if err != nil {
		global.APP_LOG.Warn("保存iptables规则失败", zap.Error(err))
	}

	return nil
}

// GetInstanceIPv4 获取实例的内网IPv4地址 (公开方法)
func (p *ProxmoxProvider) GetInstanceIPv4(ctx context.Context, instanceName string) (string, error) {
	// 复用已有的getInstanceIPAddress方法来获取内网IPv4地址
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, instanceName)
	if err != nil {
		return "", fmt.Errorf("failed to find instance %s: %w", instanceName, err)
	}

	return p.getInstanceIPAddress(ctx, vmid, instanceType)
}

// configurePortMappingsWithIP 使用指定的实例IP配置端口映射
func (p *ProxmoxProvider) configurePortMappingsWithIP(ctx context.Context, instanceName string, networkConfig NetworkConfig, instanceIP string) error {
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
	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ?", instanceName).First(&instance).Error; err != nil {
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
		if err := p.setupPortMappingWithIP(ctx, instanceName, sshPort.HostPort, sshPort.GuestPort, sshPort.Protocol, networkConfig.IPv4PortMappingMethod, instanceIP); err != nil {
			global.APP_LOG.Warn("配置SSH端口映射失败",
				zap.String("instance", instanceName),
				zap.Int("hostPort", sshPort.HostPort),
				zap.Int("guestPort", sshPort.GuestPort),
				zap.Error(err))
		}
	}

	// 2. 配置其他端口（主要使用IPv4映射方法）
	for _, port := range otherPorts {
		if err := p.setupPortMappingWithIP(ctx, instanceName, port.HostPort, port.GuestPort, port.Protocol, networkConfig.IPv4PortMappingMethod, instanceIP); err != nil {
			global.APP_LOG.Warn("配置端口映射失败",
				zap.String("instance", instanceName),
				zap.Int("hostPort", port.HostPort),
				zap.Int("guestPort", port.GuestPort),
				zap.Error(err))
		}
	}

	// 保存iptables规则
	if err := p.saveIptablesRules(); err != nil {
		global.APP_LOG.Warn("保存iptables规则失败", zap.Error(err))
	}

	return nil
}

// setupPortMappingWithIP 使用指定的实例IP设置端口映射
func (p *ProxmoxProvider) setupPortMappingWithIP(ctx context.Context, instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error {
	global.APP_LOG.Info("设置端口映射(使用已知IP)",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol),
		zap.String("method", method),
		zap.String("instanceIP", instanceIP))

	// 如果协议是both，需要同时创建TCP和UDP规则
	protocols := []string{protocol}
	if protocol == "both" {
		protocols = []string{"tcp", "udp"}
	}

	for _, proto := range protocols {
		if err := p.setupSinglePortMapping(ctx, instanceName, hostPort, guestPort, proto, method, instanceIP); err != nil {
			return fmt.Errorf("设置%s端口映射失败: %w", proto, err)
		}
	}

	return nil
}

// setupSinglePortMapping 设置单个协议的端口映射
func (p *ProxmoxProvider) setupSinglePortMapping(ctx context.Context, instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error {
	global.APP_LOG.Info("设置单个协议端口映射",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol),
		zap.String("method", method),
		zap.String("instanceIP", instanceIP))

	switch method {
	case "iptables":
		return p.setupIptablesMappingWithIP(ctx, instanceName, hostPort, guestPort, protocol, instanceIP)
	case "native":
		// Proxmox原生端口映射（暂时使用iptables实现）
		return p.setupIptablesMappingWithIP(ctx, instanceName, hostPort, guestPort, protocol, instanceIP)
	default:
		// 默认使用iptables方式
		return p.setupIptablesMappingWithIP(ctx, instanceName, hostPort, guestPort, protocol, instanceIP)
	}
}

// setupIptablesMappingWithIP 使用指定的实例IP设置iptables端口映射
func (p *ProxmoxProvider) setupIptablesMappingWithIP(ctx context.Context, instanceName string, hostPort, guestPort int, protocol, instanceIP string) error {
	global.APP_LOG.Info("设置Iptables端口映射(使用已知IP)",
		zap.String("instance", instanceName),
		zap.String("instanceIP", instanceIP),
		zap.String("target", fmt.Sprintf("%s:%d", instanceIP, guestPort)))

	// 确保instanceIP是纯IP地址
	cleanInstanceIP := strings.TrimSpace(instanceIP)
	if strings.Contains(cleanInstanceIP, "/") {
		cleanInstanceIP = strings.Split(cleanInstanceIP, "/")[0]
	}

	// DNAT规则 - 将外部请求转发到内部实例
	dnatCmd := fmt.Sprintf("iptables -t nat -A PREROUTING -i vmbr0 -p %s --dport %d -j DNAT --to-destination %s:%d",
		protocol, hostPort, cleanInstanceIP, guestPort)

	_, err := p.sshClient.Execute(dnatCmd)
	if err != nil {
		return fmt.Errorf("添加DNAT规则失败: %w", err)
	}

	// FORWARD规则 - 允许转发流量
	forwardCmd := fmt.Sprintf("iptables -A FORWARD -d %s -p %s --dport %d -j ACCEPT",
		cleanInstanceIP, protocol, guestPort)

	_, err = p.sshClient.Execute(forwardCmd)
	if err != nil {
		return fmt.Errorf("添加FORWARD规则失败: %w", err)
	}

	// MASQUERADE规则 - 处理返回流量
	masqueradeCmd := fmt.Sprintf("iptables -t nat -A POSTROUTING -s %s -p %s --sport %d -j MASQUERADE",
		cleanInstanceIP, protocol, guestPort)

	_, err = p.sshClient.Execute(masqueradeCmd)
	if err != nil {
		return fmt.Errorf("添加MASQUERADE规则失败: %w", err)
	}

	global.APP_LOG.Info("Iptables端口映射设置成功",
		zap.String("instance", instanceName),
		zap.String("target", fmt.Sprintf("%s:%d", cleanInstanceIP, guestPort)))

	return nil
}

// removePortMapping 移除端口映射
func (p *ProxmoxProvider) removePortMapping(ctx context.Context, instanceName string, hostPort int, protocol string, method string) error {
	global.APP_LOG.Info("移除端口映射",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.String("protocol", protocol),
		zap.String("method", method))

	switch method {
	case "iptables":
		return p.removeIptablesMapping(ctx, instanceName, hostPort, protocol)
	case "native":
		// Proxmox原生端口映射移除（暂时使用iptables实现）
		return p.removeIptablesMapping(ctx, instanceName, hostPort, protocol)
	default:
		// 默认使用iptables方式
		return p.removeIptablesMapping(ctx, instanceName, hostPort, protocol)
	}
}

// removeIptablesMapping 移除iptables端口映射
func (p *ProxmoxProvider) removeIptablesMapping(ctx context.Context, instanceName string, hostPort int, protocol string) error {
	// 获取实例IP
	instanceIP, err := p.getInstancePrivateIP(ctx, instanceName)
	if err != nil {
		return fmt.Errorf("获取实例IP失败: %w", err)
	}

	// 确保instanceIP是纯IP地址
	cleanInstanceIP := strings.TrimSpace(instanceIP)
	if strings.Contains(cleanInstanceIP, "/") {
		cleanInstanceIP = strings.Split(cleanInstanceIP, "/")[0]
	}

	// 移除DNAT规则
	dnatCmd := fmt.Sprintf("iptables -t nat -D PREROUTING -i vmbr0 -p %s --dport %d -j DNAT --to-destination %s",
		protocol, hostPort, cleanInstanceIP)

	_, err = p.sshClient.Execute(dnatCmd)
	if err != nil {
		global.APP_LOG.Warn("移除DNAT规则失败",
			zap.String("instance", instanceName),
			zap.Error(err))
	}

	// 移除FORWARD规则
	forwardCmd := fmt.Sprintf("iptables -D FORWARD -d %s -p %s --dport %d -j ACCEPT",
		cleanInstanceIP, protocol, hostPort)

	_, err = p.sshClient.Execute(forwardCmd)
	if err != nil {
		global.APP_LOG.Warn("移除FORWARD规则失败",
			zap.String("instance", instanceName),
			zap.Error(err))
	}

	// 移除MASQUERADE规则
	masqueradeCmd := fmt.Sprintf("iptables -t nat -D POSTROUTING -s %s -p %s --sport %d -j MASQUERADE",
		cleanInstanceIP, protocol, hostPort)

	_, err = p.sshClient.Execute(masqueradeCmd)
	if err != nil {
		global.APP_LOG.Warn("移除MASQUERADE规则失败",
			zap.String("instance", instanceName),
			zap.Error(err))
	}

	global.APP_LOG.Info("Iptables端口映射移除成功",
		zap.String("instance", instanceName))

	return nil
}

// saveIptablesRules 保存iptables规则
func (p *ProxmoxProvider) saveIptablesRules() error {
	// 创建iptables目录
	_, err := p.sshClient.Execute("mkdir -p /etc/iptables")
	if err != nil {
		global.APP_LOG.Warn("创建iptables目录失败", zap.Error(err))
	}

	// 保存IPv4规则
	saveCmd := "iptables-save > /etc/iptables/rules.v4"
	_, err = p.sshClient.Execute(saveCmd)
	if err != nil {
		return fmt.Errorf("保存iptables规则失败: %w", err)
	}

	global.APP_LOG.Info("iptables规则保存成功")
	return nil
}

// getInstancePrivateIP 获取实例的内网IP地址
func (p *ProxmoxProvider) getInstancePrivateIP(ctx context.Context, instanceName string) (string, error) {
	// 尝试从SSH命令获取实例列表并匹配
	output, err := p.sshClient.Execute("pct list")
	if err != nil {
		return "", fmt.Errorf("获取容器列表失败: %w", err)
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 3 && strings.Contains(line, instanceName) {
			// 找到匹配的实例，从字段中提取IP
			for i, field := range fields {
				// 查找IP地址模式
				if strings.Contains(field, "172.16.1.") {
					return field, nil
				}
				// 如果是最后一个字段，可能包含IP信息
				if i == len(fields)-1 && strings.Contains(field, ".") {
					return field, nil
				}
			}
		}
	}

	// 如果上述方法失败，尝试根据实例名称推断IP
	// 假设实例名称格式包含vmid信息
	vmid, _, err := p.findVMIDByNameOrID(ctx, instanceName)
	if err == nil {
		// 根据vmid构造IP地址
		var vmidInt int
		if n, err := fmt.Sscanf(vmid, "%d", &vmidInt); n == 1 && err == nil {
			return fmt.Sprintf("172.16.1.%d", vmidInt), nil
		}
	}

	return "", fmt.Errorf("无法获取实例 %s 的IP地址", instanceName)
}

// SetupPortMappingWithIP 公开的方法：在远程服务器上创建端口映射（用于手动添加端口）
// 保持与LXD/Incus的API一致性
func (p *ProxmoxProvider) SetupPortMappingWithIP(ctx context.Context, instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error {
	return p.setupPortMappingWithIP(ctx, instanceName, hostPort, guestPort, protocol, method, instanceIP)
}
