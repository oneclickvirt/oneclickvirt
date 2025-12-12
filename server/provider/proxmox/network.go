package proxmox

import (
	"context"
	"strconv"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// NetworkConfig Proxmox网络配置结构
type NetworkConfig struct {
	SSHPort               int
	NATStart              int
	NATEnd                int
	InSpeed               int    // 入站速度（Mbps）- 从Provider配置或用户等级获取
	OutSpeed              int    // 出站速度（Mbps）- 从Provider配置或用户等级获取
	NetworkType           string // 网络配置类型：nat_ipv4, nat_ipv4_ipv6, dedicated_ipv4, dedicated_ipv4_ipv6, ipv6_only
	IPv4PortMappingMethod string // IPv4端口映射方式：iptables, native
	IPv6PortMappingMethod string // IPv6端口映射方式：iptables, native
}

// parseNetworkConfigFromInstanceConfig 从实例配置中解析网络配置
func (p *ProxmoxProvider) parseNetworkConfigFromInstanceConfig(config provider.InstanceConfig) NetworkConfig {
	// 获取用户等级（从Metadata中，如果没有则默认为1）
	userLevel := 1
	if config.Metadata != nil {
		if levelStr, ok := config.Metadata["user_level"]; ok {
			if level, err := strconv.Atoi(levelStr); err == nil {
				userLevel = level
			}
		}
	}

	// 获取Provider默认带宽配置
	defaultInSpeed, defaultOutSpeed, err := p.getBandwidthFromProvider(context.Background(), userLevel)
	if err != nil {
		global.APP_LOG.Warn("获取Provider带宽配置失败，使用硬编码默认值", zap.Error(err))
		defaultInSpeed = 300 // 降级到硬编码默认值
		defaultOutSpeed = 300
	}

	// 首先从Provider配置获取默认值（最高优先级）
	_, providerIPv6PortMethod, providerIPv4PortMethod := p.getNetworkConfigFromProvider(context.Background())

	// 获取完整的Provider信息以支持新的NetworkType
	var providerInfo providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerInfo).Error; err != nil {
		global.APP_LOG.Warn("无法获取Provider配置，使用默认值",
			zap.String("provider", p.config.Name),
			zap.Error(err))
	}

	// 获取网络类型（优先从Metadata中读取，如果没有则从Provider配置中读取）
	networkType := providerInfo.NetworkType
	if config.Metadata != nil {
		if metaNetworkType, ok := config.Metadata["network_type"]; ok {
			networkType = metaNetworkType
			global.APP_LOG.Info("使用实例级别的网络类型配置",
				zap.String("instance", config.Name),
				zap.String("networkType", networkType))
		}
	}

	networkConfig := NetworkConfig{
		SSHPort:               22001,                  // 默认SSH端口
		InSpeed:               defaultInSpeed,         // 使用Provider配置和用户等级的带宽
		OutSpeed:              defaultOutSpeed,        // 使用Provider配置和用户等级的带宽
		NetworkType:           networkType,            // 优先从实例Metadata读取，否则从Provider配置中读取网络类型
		IPv4PortMappingMethod: providerIPv4PortMethod, // 从Provider配置读取IPv4端口映射方法
		IPv6PortMappingMethod: providerIPv6PortMethod, // 从Provider配置读取IPv6端口映射方法
	}

	// 根据NetworkType调整端口映射方式
	switch networkType {
	case "nat_ipv4":
		networkConfig.IPv4PortMappingMethod = "iptables"
	case "nat_ipv4_ipv6":
		networkConfig.IPv4PortMappingMethod = "iptables"
		networkConfig.IPv6PortMappingMethod = providerIPv6PortMethod
	case "dedicated_ipv4":
		networkConfig.IPv4PortMappingMethod = "native"
	case "dedicated_ipv4_ipv6":
		networkConfig.IPv4PortMappingMethod = "native"
		networkConfig.IPv6PortMappingMethod = providerIPv6PortMethod
	case "ipv6_only":
		networkConfig.IPv4PortMappingMethod = ""
		networkConfig.IPv6PortMappingMethod = providerIPv6PortMethod
	}

	global.APP_LOG.Debug("初始化Proxmox网络配置（从Provider读取网络配置）",
		zap.String("instanceName", config.Name),
		zap.String("networkType", networkConfig.NetworkType),
		zap.String("providerIPv6PortMappingMethod", providerIPv6PortMethod),
		zap.String("providerIPv4PortMappingMethod", providerIPv4PortMethod))

	// 从Metadata中解析端口信息
	if config.Metadata != nil {
		if sshPort, ok := config.Metadata["ssh_port"]; ok {
			if port, err := strconv.Atoi(sshPort); err == nil {
				networkConfig.SSHPort = port
			}
		}

		if natStart, ok := config.Metadata["nat_start"]; ok {
			if port, err := strconv.Atoi(natStart); err == nil {
				networkConfig.NATStart = port
			}
		}

		if natEnd, ok := config.Metadata["nat_end"]; ok {
			if port, err := strconv.Atoi(natEnd); err == nil {
				networkConfig.NATEnd = port
			}
		}

		// 带宽配置：取 Provider配置、用户等级配置、实例Metadata配置中的最小值
		if inSpeed, ok := config.Metadata["in_speed"]; ok {
			if speed, err := strconv.Atoi(inSpeed); err == nil && speed > 0 {
				// 取更小的值
				if speed < networkConfig.InSpeed {
					networkConfig.InSpeed = speed
					global.APP_LOG.Info("使用更低的实例级别入站带宽限制",
						zap.String("instance", config.Name),
						zap.Int("metadataInSpeed", speed),
						zap.Int("finalInSpeed", networkConfig.InSpeed))
				}
			}
		}

		if outSpeed, ok := config.Metadata["out_speed"]; ok {
			if speed, err := strconv.Atoi(outSpeed); err == nil && speed > 0 {
				// 取更小的值
				if speed < networkConfig.OutSpeed {
					networkConfig.OutSpeed = speed
					global.APP_LOG.Info("使用更低的实例级别出站带宽限制",
						zap.String("instance", config.Name),
						zap.Int("metadataOutSpeed", speed),
						zap.Int("finalOutSpeed", networkConfig.OutSpeed))
				}
			}
		}

		// IPv6配置始终以Provider配置为准，不允许实例级别覆盖
		if enableIPv6, ok := config.Metadata["enable_ipv6"]; ok {
			hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" || networkConfig.NetworkType == "ipv6_only"
			global.APP_LOG.Debug("从Metadata中发现enable_ipv6配置，但IPv6配置以Provider为准",
				zap.String("instanceName", config.Name),
				zap.String("metadata_enable_ipv6", enableIPv6),
				zap.Bool("provider_enable_ipv6", hasIPv6))

			global.APP_LOG.Info("IPv6配置以Provider为准，忽略实例Metadata配置",
				zap.String("instanceName", config.Name),
				zap.String("metadata_value", enableIPv6),
				zap.Bool("final_enable_ipv6", hasIPv6))
		} else {
			hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" || networkConfig.NetworkType == "ipv6_only"
			global.APP_LOG.Debug("Metadata中未找到enable_ipv6配置，使用Provider配置",
				zap.String("instanceName", config.Name),
				zap.Bool("provider_enable_ipv6", hasIPv6))
		}

		// IPv4端口映射方法以Provider配置为准，不允许实例级别覆盖
		if ipv4PortMethod, ok := config.Metadata["ipv4_port_mapping_method"]; ok {
			global.APP_LOG.Debug("从Metadata中发现ipv4_port_mapping_method配置，但IPv4端口映射方法以Provider为准",
				zap.String("instanceName", config.Name),
				zap.String("metadata_ipv4_port_method", ipv4PortMethod),
				zap.String("provider_ipv4_port_method", networkConfig.IPv4PortMappingMethod))

			global.APP_LOG.Info("IPv4端口映射方法以Provider为准，忽略实例Metadata配置",
				zap.String("instanceName", config.Name),
				zap.String("metadata_value", ipv4PortMethod),
				zap.String("final_ipv4_port_method", networkConfig.IPv4PortMappingMethod))
		} else {
			global.APP_LOG.Debug("Metadata中未找到ipv4_port_mapping_method配置，使用Provider配置",
				zap.String("instanceName", config.Name),
				zap.String("provider_ipv4_port_method", networkConfig.IPv4PortMappingMethod))
		}

		// IPv6端口映射方法以Provider配置为准，不允许实例级别覆盖
		if ipv6PortMethod, ok := config.Metadata["ipv6_port_mapping_method"]; ok {
			global.APP_LOG.Debug("从Metadata中发现ipv6_port_mapping_method配置，但IPv6端口映射方法以Provider为准",
				zap.String("instanceName", config.Name),
				zap.String("metadata_ipv6_port_method", ipv6PortMethod),
				zap.String("provider_ipv6_port_method", networkConfig.IPv6PortMappingMethod))

			global.APP_LOG.Info("IPv6端口映射方法以Provider为准，忽略实例Metadata配置",
				zap.String("instanceName", config.Name),
				zap.String("metadata_value", ipv6PortMethod),
				zap.String("final_ipv6_port_method", networkConfig.IPv6PortMappingMethod))
		}
	}

	// 输出最终的网络配置结果
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" || networkConfig.NetworkType == "ipv6_only"
	global.APP_LOG.Info("Proxmox网络配置解析完成",
		zap.String("instanceName", config.Name),
		zap.Int("sshPort", networkConfig.SSHPort),
		zap.Int("inSpeed", networkConfig.InSpeed),
		zap.Int("outSpeed", networkConfig.OutSpeed),
		zap.Bool("enableIPv6", hasIPv6),
		zap.String("networkType", networkConfig.NetworkType),
		zap.String("ipv4PortMappingMethod", networkConfig.IPv4PortMappingMethod),
		zap.String("ipv6PortMappingMethod", networkConfig.IPv6PortMappingMethod))

	return networkConfig
}

// getBandwidthFromProvider 从Provider配置获取带宽设置，并结合用户等级限制
func (p *ProxmoxProvider) getBandwidthFromProvider(ctx context.Context, userLevel int) (inSpeed, outSpeed int, err error) {
	// 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerInfo).Error; err != nil {
		// 如果获取Provider失败，使用默认值
		global.APP_LOG.Warn("无法获取Provider配置，使用默认带宽",
			zap.String("provider", p.config.Name),
			zap.Error(err))
		return 300, 300, nil // 默认300Mbps
	}

	// 基础带宽配置（来自Provider）
	providerInSpeed := providerInfo.DefaultInboundBandwidth
	providerOutSpeed := providerInfo.DefaultOutboundBandwidth

	// 获取用户等级对应的带宽限制
	userBandwidthLimit := p.getUserLevelBandwidth(userLevel)

	// 选择更小的值作为实际带宽限制（用户等级限制 vs Provider默认值）
	inSpeed = providerInSpeed
	if userBandwidthLimit > 0 && userBandwidthLimit < providerInSpeed {
		inSpeed = userBandwidthLimit
	}

	outSpeed = providerOutSpeed
	if userBandwidthLimit > 0 && userBandwidthLimit < providerOutSpeed {
		outSpeed = userBandwidthLimit
	}

	// 设置默认值（如果配置为0）
	if inSpeed <= 0 {
		inSpeed = 300 // 默认300Mbps
	}
	if outSpeed <= 0 {
		outSpeed = 300 // 默认300Mbps
	}

	// 确保不超过Provider的最大限制
	if providerInfo.MaxInboundBandwidth > 0 && inSpeed > providerInfo.MaxInboundBandwidth {
		inSpeed = providerInfo.MaxInboundBandwidth
	}
	if providerInfo.MaxOutboundBandwidth > 0 && outSpeed > providerInfo.MaxOutboundBandwidth {
		outSpeed = providerInfo.MaxOutboundBandwidth
	}

	global.APP_LOG.Info("从Provider配置和用户等级获取带宽设置",
		zap.String("provider", p.config.Name),
		zap.Int("inSpeed", inSpeed),
		zap.Int("outSpeed", outSpeed),
		zap.Int("userLevel", userLevel),
		zap.Int("userBandwidthLimit", userBandwidthLimit),
		zap.Int("providerDefault", providerInSpeed))

	return inSpeed, outSpeed, nil
}

// getUserLevelBandwidth 根据用户等级获取带宽限制
func (p *ProxmoxProvider) getUserLevelBandwidth(userLevel int) int {
	// 从全局配置中获取用户等级对应的带宽限制
	if levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[userLevel]; exists {
		if bandwidth, ok := levelLimits.MaxResources["bandwidth"].(int); ok {
			return bandwidth
		} else if bandwidthFloat, ok := levelLimits.MaxResources["bandwidth"].(float64); ok {
			return int(bandwidthFloat)
		}
	}

	// 如果没有配置，使用等级基础计算方法（每级+100Mbps，从100开始）
	baseBandwidth := 100
	return baseBandwidth + (userLevel-1)*100
}

// getNetworkConfigFromProvider 从Provider配置获取网络设置
func (p *ProxmoxProvider) getNetworkConfigFromProvider(ctx context.Context) (enableIPv6 bool, ipv6PortMethod string, ipv4PortMethod string) {
	// 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerInfo).Error; err != nil {
		global.APP_LOG.Warn("无法获取Provider配置，使用默认值",
			zap.String("provider", p.config.Name),
			zap.Error(err))
		return false, "native", "iptables" // Proxmox默认值
	}

	hasIPv6 := providerInfo.NetworkType == "nat_ipv4_ipv6" || providerInfo.NetworkType == "dedicated_ipv4_ipv6" || providerInfo.NetworkType == "ipv6_only"
	global.APP_LOG.Info("从Provider配置获取网络设置",
		zap.String("provider", p.config.Name),
		zap.Bool("enableIPv6", hasIPv6),
		zap.String("networkType", providerInfo.NetworkType),
		zap.String("ipv6PortMappingMethod", providerInfo.IPv6PortMappingMethod),
		zap.String("ipv4PortMappingMethod", providerInfo.IPv4PortMappingMethod))

	return hasIPv6, providerInfo.IPv6PortMappingMethod, providerInfo.IPv4PortMappingMethod
}
