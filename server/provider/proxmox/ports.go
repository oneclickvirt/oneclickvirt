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
	"oneclickvirt/provider/firewall"

	"go.uber.org/zap"
)

// configureInstancePortMappings 配置实例端口映射
func (p *ProxmoxProvider) configureInstancePortMappings(ctx context.Context, config provider.InstanceConfig, vmid int) error {
	// 等待实例完全启动
	time.Sleep(3 * time.Second)

	global.APP_LOG.Debug("开始配置PVE实例端口映射",
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

	global.APP_LOG.Debug("获取到实例内网IP",
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

	global.APP_LOG.Debug("PVE实例端口映射配置成功",
		zap.String("instance", config.Name),
		zap.Int("vmid", vmid))

	return nil
}

// cleanupInstancePortMappings 清理实例的端口映射
func (p *ProxmoxProvider) cleanupInstancePortMappings(ctx context.Context, vmid string, instanceType string) error {
	global.APP_LOG.Debug("开始清理实例端口映射",
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
		global.APP_LOG.Debug("找到实例名称，开始清理数据库中的端口映射",
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
						global.APP_LOG.Debug("端口映射清理成功",
							zap.String("instanceName", instanceName),
							zap.Int("hostPort", port.HostPort),
							zap.String("protocol", port.Protocol))
					}
				}
			}
		}
	}

	// 3. 尝试基于推断的IP地址清理iptables规则（使用VMID到IP的映射函数）
	if instanceType == "vm" || instanceType == "container" {
		vmidInt, err := strconv.Atoi(vmid)
		if err == nil && vmidInt >= MinVMID && vmidInt <= MaxVMID {
			inferredIP := VMIDToInternalIP(vmidInt)
			global.APP_LOG.Debug("尝试基于推断IP清理iptables规则",
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

	global.APP_LOG.Debug("实例端口映射清理完成",
		zap.String("vmid", vmid),
		zap.String("instanceType", instanceType))

	return nil
}

// cleanupIptablesRulesForIP 清理指定IP地址的防火墙规则（nft优先，iptables回退）
func (p *ProxmoxProvider) cleanupIptablesRulesForIP(ctx context.Context, ipAddress string) error {
	global.APP_LOG.Debug("清理IP地址的防火墙规则", zap.String("ipAddress", ipAddress))

	fwMgr := firewall.NewManager(p.sshClient, "proxmox", "")
	fwMgr.DetectBackend("/usr/local/bin/proxmox_fw_backend")
	fwMgr.DeleteRulesByIP(ipAddress)
	fwMgr.SaveRules()

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
		global.APP_LOG.Debug("独立IP模式或纯IPv6模式，跳过IPv4端口映射配置",
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
		global.APP_LOG.Debug("未找到端口映射配置", zap.String("instance", instanceName))
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
	global.APP_LOG.Debug("设置端口映射(使用已知IP)",
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
	global.APP_LOG.Debug("设置单个协议端口映射",
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

// setupIptablesMappingWithIP 使用防火墙设置端口映射（nft优先，iptables回退）
func (p *ProxmoxProvider) setupIptablesMappingWithIP(ctx context.Context, instanceName string, hostPort, guestPort int, protocol, instanceIP string) error {
	global.APP_LOG.Debug("设置防火墙端口映射(使用已知IP)",
		zap.String("instance", instanceName),
		zap.String("instanceIP", instanceIP),
		zap.String("target", fmt.Sprintf("%s:%d", instanceIP, guestPort)))

	cleanInstanceIP := strings.TrimSpace(instanceIP)
	if strings.Contains(cleanInstanceIP, "/") {
		cleanInstanceIP = strings.Split(cleanInstanceIP, "/")[0]
	}

	fwMgr := firewall.NewManager(p.sshClient, "proxmox", "")
	if _, err := fwMgr.DetectBackend("/usr/local/bin/proxmox_fw_backend"); err != nil {
		return fmt.Errorf("防火墙后端检测失败: %w", err)
	}
	// 确保 nft table/chain 已初始化（首次使用前可能尚未创建）
	if err := fwMgr.InitTable(); err != nil {
		return fmt.Errorf("防火墙初始化失败: %w", err)
	}

	comment := fmt.Sprintf("pm:%s:%d:%d", instanceName, hostPort, guestPort)
	if err := fwMgr.AddSingleDNAT(cleanInstanceIP, hostPort, guestPort, protocol, comment); err != nil {
		return fmt.Errorf("添加防火墙规则失败: %w", err)
	}

	global.APP_LOG.Debug("防火墙端口映射设置成功",
		zap.String("instance", instanceName),
		zap.String("target", fmt.Sprintf("%s:%d", cleanInstanceIP, guestPort)))

	return nil
}

// removePortMapping 移除端口映射
func (p *ProxmoxProvider) removePortMapping(ctx context.Context, instanceName string, hostPort int, protocol string, method string) error {
	global.APP_LOG.Debug("移除端口映射",
		zap.String("instance", instanceName),
		zap.Int("hostPort", hostPort),
		zap.String("protocol", protocol),
		zap.String("method", method))

	return p.removeIptablesMapping(ctx, instanceName, hostPort, protocol)
}

// removeIptablesMapping 移除防火墙端口映射（nft优先，iptables回退）
func (p *ProxmoxProvider) removeIptablesMapping(ctx context.Context, instanceName string, hostPort int, protocol string) error {
	instanceIP, err := p.getInstancePrivateIP(ctx, instanceName)
	if err != nil {
		return fmt.Errorf("获取实例IP失败: %w", err)
	}

	cleanInstanceIP := strings.TrimSpace(instanceIP)
	if strings.Contains(cleanInstanceIP, "/") {
		cleanInstanceIP = strings.Split(cleanInstanceIP, "/")[0]
	}

	fwMgr := firewall.NewManager(p.sshClient, "proxmox", "")
	fwMgr.DetectBackend("/usr/local/bin/proxmox_fw_backend")

	// 尝试先按注释删除（新规则），再按IP+端口删除（旧规则）
	comment := fmt.Sprintf("pm:%s:%d:", instanceName, hostPort)
	backend := fwMgr.GetBackend()
	if backend == firewall.BackendNft {
		fwMgr.DeleteRulesByComment(comment)
	} else {
		// iptables: 精确删除3条规则
		fwMgr.RemoveSingleDNAT(cleanInstanceIP, hostPort, 0, protocol, "")
	}

	global.APP_LOG.Debug("防火墙端口映射移除成功",
		zap.String("instance", instanceName))

	return nil
}

// saveIptablesRules 保存防火墙规则
func (p *ProxmoxProvider) saveIptablesRules() error {
	fwMgr := firewall.NewManager(p.sshClient, "proxmox", "")
	fwMgr.DetectBackend("/usr/local/bin/proxmox_fw_backend")
	fwMgr.SaveRules()
	global.APP_LOG.Debug("防火墙规则保存成功")
	return nil
}

// SaveIptablesRules 保存防火墙规则（公开方法）
func (p *ProxmoxProvider) SaveIptablesRules() error {
	return p.saveIptablesRules()
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
