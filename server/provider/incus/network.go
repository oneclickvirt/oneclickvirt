package incus

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// NetworkConfig Incus网络配置结构
type NetworkConfig struct {
	SSHPort               int
	NATStart              int
	NATEnd                int
	InSpeed               int    // 入站速度（Mbps）- 从Provider配置或用户等级获取
	OutSpeed              int    // 出站速度（Mbps）- 从Provider配置或用户等级获取
	NetworkType           string // 网络配置类型：nat_ipv4, nat_ipv4_ipv6, dedicated_ipv4, dedicated_ipv4_ipv6, ipv6_only
	IPv4PortMappingMethod string // IPv4端口映射方式：device_proxy, iptables, native
	IPv6PortMappingMethod string // IPv6端口映射方式：device_proxy, iptables, native
}

// parseNetworkConfigFromInstanceConfig 从实例配置中解析网络配置
func (i *IncusProvider) parseNetworkConfigFromInstanceConfig(config provider.InstanceConfig) NetworkConfig {
	// 获取用户等级（从Metadata中，如果没有则默认为1）
	userLevel := 1
	if config.Metadata != nil {
		if levelStr, ok := config.Metadata["user_level"]; ok {
			if level, err := strconv.Atoi(levelStr); err == nil {
				userLevel = level
			}
		}
	}

	// 获取Provider配置信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.Where("name = ?", i.config.Name).First(&providerInfo).Error; err != nil {
		global.APP_LOG.Warn("无法获取Provider配置，使用默认值",
			zap.String("provider", i.config.Name),
			zap.Error(err))
	}

	// 获取Provider默认带宽配置
	defaultInSpeed, defaultOutSpeed, err := i.getBandwidthFromProvider(userLevel)
	if err != nil {
		global.APP_LOG.Warn("获取Provider带宽配置失败，使用硬编码默认值", zap.Error(err))
		defaultInSpeed = 300 // 降级到硬编码默认值
		defaultOutSpeed = 300
	}

	// 设置默认的IPv4和IPv6端口映射方法（如果Provider配置为空则使用默认值）
	ipv4Method := providerInfo.IPv4PortMappingMethod
	if ipv4Method == "" {
		ipv4Method = "device_proxy" // Incus默认使用device_proxy
	}

	ipv6Method := providerInfo.IPv6PortMappingMethod
	if ipv6Method == "" {
		ipv6Method = "device_proxy" // Incus默认使用device_proxy
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
		SSHPort:               0,               // SSH端口将从实例的端口映射中获取
		InSpeed:               defaultInSpeed,  // 使用Provider配置和用户等级的带宽
		OutSpeed:              defaultOutSpeed, // 使用Provider配置和用户等级的带宽
		NetworkType:           networkType,     // 优先从实例Metadata读取，否则从Provider配置中读取网络类型
		IPv4PortMappingMethod: ipv4Method,      // 从Provider配置中读取IPv4端口映射方式
		IPv6PortMappingMethod: ipv6Method,      // 从Provider配置中读取IPv6端口映射方式
		NATStart:              0,               // 默认值，可被metadata覆盖
		NATEnd:                0,               // 默认值，可被metadata覆盖
	}

	// 根据NetworkType调整端口映射方式
	switch networkType {
	case "dedicated_ipv4", "dedicated_ipv4_ipv6":
		networkConfig.IPv4PortMappingMethod = "native"
	case "ipv6_only":
		networkConfig.IPv4PortMappingMethod = ""
	}

	global.APP_LOG.Info("从Provider配置读取网络设置",
		zap.String("provider", i.config.Name),
		zap.String("networkType", networkType),
		zap.String("ipv4PortMethod", networkConfig.IPv4PortMappingMethod),
		zap.String("ipv6PortMethod", networkConfig.IPv6PortMappingMethod))

	// 从Metadata中解析端口信息（允许实例级别的配置覆盖Provider级别的配置）
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

		// 允许实例级别的带宽配置覆盖Provider和用户等级的配置
		if inSpeed, ok := config.Metadata["in_speed"]; ok {
			if speed, err := strconv.Atoi(inSpeed); err == nil {
				networkConfig.InSpeed = speed
				global.APP_LOG.Info("实例级别带宽配置覆盖Provider配置",
					zap.String("instance", config.Name),
					zap.Int("customInSpeed", speed))
			}
		}

		if outSpeed, ok := config.Metadata["out_speed"]; ok {
			if speed, err := strconv.Atoi(outSpeed); err == nil {
				networkConfig.OutSpeed = speed
				global.APP_LOG.Info("实例级别带宽配置覆盖Provider配置",
					zap.String("instance", config.Name),
					zap.Int("customOutSpeed", speed))
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

	return networkConfig
}

// configureInstanceNetwork 配置实例网络
func (i *IncusProvider) configureInstanceNetwork(ctx context.Context, config provider.InstanceConfig, networkConfig NetworkConfig) error {
	// 对于独立IPv4模式，预先检查并确保该IPv4地址已绑定到宿主机网络接口
	if networkConfig.NetworkType == "dedicated_ipv4" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" {
		if config.Metadata != nil {
			if staticIPv4, ok := config.Metadata["static_ipv4"]; ok && staticIPv4 != "" {
				if err := i.ensureIPv4OnHostInterface(staticIPv4); err != nil {
					global.APP_LOG.Warn("独立IPv4宿主机接口绑定检查失败，继续执行",
						zap.String("instanceName", config.Name),
						zap.String("ipv4", staticIPv4),
						zap.Error(err))
				}
			}
		}
	}

	// 重启实例以获取IP地址（增强容错）
	if err := i.restartInstanceForNetwork(config.Name); err != nil {
		global.APP_LOG.Warn("重启实例获取网络配置失败，尝试直接获取现有网络配置",
			zap.String("instanceName", config.Name),
			zap.Error(err))

		// 如果重启失败，尝试直接使用现有网络配置继续
		if err := i.tryUseExistingNetworkConfig(ctx, config, networkConfig); err != nil {
			return fmt.Errorf("重启实例获取网络配置失败且无法使用现有配置: %w", err)
		}
		global.APP_LOG.Info("使用现有网络配置继续",
			zap.String("instanceName", config.Name))
		return nil
	}

	// 获取实例IP地址
	instanceIP, err := i.getInstanceIP(config.Name)
	if err != nil {
		return fmt.Errorf("获取实例IP地址失败: %w", err)
	}

	// 获取主机IP地址
	hostIP, err := i.getHostIP()
	if err != nil {
		return fmt.Errorf("获取主机IP地址失败: %w", err)
	}

	global.APP_LOG.Info("开始配置实例网络",
		zap.String("instanceName", config.Name),
		zap.String("instanceIP", instanceIP),
		zap.String("hostIP", hostIP))

	// 停止实例进行网络配置
	if err := i.stopInstanceForConfig(config.Name); err != nil {
		return fmt.Errorf("停止实例进行配置失败: %w", err)
	}

	// 配置网络限速
	if err := i.configureNetworkLimits(config.Name, networkConfig); err != nil {
		global.APP_LOG.Warn("配置网络限速失败", zap.Error(err))
	}

	// 设置IP地址绑定
	if err := i.setIPAddressBinding(config.Name, instanceIP); err != nil {
		global.APP_LOG.Warn("设置IP地址绑定失败", zap.Error(err))
	}

	// 配置端口映射 - 在实例停止时添加 proxy 设备
	// LXD/Incus 的 proxy 设备必须在容器停止时添加，然后启动容器时才能正确初始化
	if err := i.configurePortMappingsWithIP(ctx, config.Name, networkConfig, instanceIP); err != nil {
		global.APP_LOG.Warn("配置端口映射失败", zap.Error(err))
	}

	// 启动实例 - 在配置完端口映射后启动，让 proxy 设备正确初始化
	if err := i.sshStartInstance(config.Name); err != nil {
		return fmt.Errorf("启动实例失败: %w", err)
	}

	// 等待实例完全启动并获取IP地址
	if err := i.waitForInstanceReady(config.Name); err != nil {
		global.APP_LOG.Warn("等待实例就绪超时，但继续配置", zap.Error(err))
	}

	// 配置防火墙端口
	if err := i.configureFirewallPorts(config.Name); err != nil {
		global.APP_LOG.Warn("配置防火墙端口失败", zap.Error(err))
	}

	// 配置IPv6网络（如果启用）
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" || networkConfig.NetworkType == "ipv6_only"
	if hasIPv6 {
		if err := i.configureIPv6Network(ctx, config.Name, hasIPv6, networkConfig.IPv6PortMappingMethod); err != nil {
			global.APP_LOG.Warn("配置IPv6网络失败", zap.Error(err))
		}
	}

	global.APP_LOG.Info("实例网络配置完成",
		zap.String("instanceName", config.Name),
		zap.String("instanceIP", instanceIP))

	return nil
}

// tryUseExistingNetworkConfig 尝试使用现有的网络配置继续
func (i *IncusProvider) tryUseExistingNetworkConfig(ctx context.Context, config provider.InstanceConfig, networkConfig NetworkConfig) error {
	global.APP_LOG.Info("尝试使用现有网络配置",
		zap.String("instanceName", config.Name))

	// 检查实例是否仍在运行
	statusCmd := fmt.Sprintf("incus info %s | grep \"Status:\" | awk '{print $2}'", config.Name)
	output, err := i.sshClient.Execute(statusCmd)
	if err != nil {
		return fmt.Errorf("检查实例状态失败: %w", err)
	}

	status := utils.CleanCommandOutput(output)
	if status != "RUNNING" {
		global.APP_LOG.Warn("实例未运行，尝试启动",
			zap.String("instanceName", config.Name),
			zap.String("status", status))

		// 尝试启动实例
		startCmd := fmt.Sprintf("incus start %s", config.Name)
		_, err := i.sshClient.Execute(startCmd)
		if err != nil {
			return fmt.Errorf("启动实例失败: %w", err)
		}

		// 等待实例网络就绪（根据实例类型选择合适的等待方法）
		global.APP_LOG.Info("等待实例网络就绪后再配置端口映射",
			zap.String("instanceName", config.Name))

		// 判断实例类型
		typeCmd := fmt.Sprintf("incus info %s | grep \"Type:\" | awk '{print $2}'", config.Name)
		typeOutput, err := i.sshClient.Execute(typeCmd)
		instanceType := strings.TrimSpace(typeOutput)

		if err == nil && (instanceType == "virtual-machine" || instanceType == "vm") {
			// 虚拟机需要更长的等待时间
			if err := i.waitForVMNetworkReady(config.Name); err != nil {
				global.APP_LOG.Warn("等待虚拟机网络就绪超时，继续尝试配置",
					zap.String("instanceName", config.Name),
					zap.Error(err))
			}
		} else {
			// 容器使用较短的等待时间
			if err := i.waitForContainerNetworkReady(config.Name); err != nil {
				global.APP_LOG.Warn("等待容器网络就绪超时，继续尝试配置",
					zap.String("instanceName", config.Name),
					zap.Error(err))
			}
		}
	}

	// 尝试获取现有IP地址
	instanceIP, err := i.getInstanceIP(config.Name)
	if err != nil {
		global.APP_LOG.Warn("无法获取实例IP地址，跳过网络配置",
			zap.String("instanceName", config.Name),
			zap.Error(err))
		return fmt.Errorf("无法获取实例IP地址: %w", err)
	}

	global.APP_LOG.Info("成功获取现有实例IP地址",
		zap.String("instanceName", config.Name),
		zap.String("instanceIP", instanceIP))

	// 获取主机IP地址
	hostIP, err := i.getHostIP()
	if err != nil {
		global.APP_LOG.Warn("无法获取主机IP地址，使用默认配置",
			zap.Error(err))
		hostIP = "0.0.0.0" // 使用默认值
	}

	global.APP_LOG.Info("使用现有网络配置继续配置",
		zap.String("instanceName", config.Name),
		zap.String("instanceIP", instanceIP),
		zap.String("hostIP", hostIP))

	// 为了确保 proxy 设备正确初始化，停止容器后添加设备再启动
	// 这是 LXD/Incus 的最佳实践，特别是在 Ubuntu 24 上
	global.APP_LOG.Info("停止实例以配置端口映射",
		zap.String("instanceName", config.Name))

	if err := i.stopInstanceForConfig(config.Name); err != nil {
		global.APP_LOG.Warn("停止实例失败，尝试直接配置",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	} else {
		// 尝试配置端口映射（容器停止状态）
		if err := i.configurePortMappingsWithIP(ctx, config.Name, networkConfig, instanceIP); err != nil {
			global.APP_LOG.Warn("配置端口映射失败，但继续",
				zap.String("instanceName", config.Name),
				zap.Error(err))
		}

		// 重新启动实例
		if err := i.sshStartInstance(config.Name); err != nil {
			global.APP_LOG.Warn("启动实例失败",
				zap.String("instanceName", config.Name),
				zap.Error(err))
		}
	}

	// 尝试配置防火墙端口（如果失败只记录警告）
	if err := i.configureFirewallPorts(config.Name); err != nil {
		global.APP_LOG.Warn("配置防火墙端口失败，但继续",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	}

	return nil
}
