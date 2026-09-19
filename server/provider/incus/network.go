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
	if global.APP_DB != nil {
		if err := global.APP_DB.Where("id = ?", i.config.ID).First(&providerInfo).Error; err != nil {
			global.APP_LOG.Warn("无法获取Provider配置，使用默认值",
				zap.Uint("provider_id", i.config.ID),
				zap.String("provider", i.config.Name),
				zap.Error(err))
		}
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
			global.APP_LOG.Debug("使用实例级别的网络类型配置",
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

	global.APP_LOG.Debug("从Provider配置读取网络设置",
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
				global.APP_LOG.Debug("实例级别带宽配置覆盖Provider配置",
					zap.String("instance", config.Name),
					zap.Int("customInSpeed", speed))
			}
		}

		if outSpeed, ok := config.Metadata["out_speed"]; ok {
			if speed, err := strconv.Atoi(outSpeed); err == nil {
				networkConfig.OutSpeed = speed
				global.APP_LOG.Debug("实例级别带宽配置覆盖Provider配置",
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

			global.APP_LOG.Debug("IPv6配置以Provider为准，忽略实例Metadata配置",
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

			global.APP_LOG.Debug("IPv4端口映射方法以Provider为准，忽略实例Metadata配置",
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

			global.APP_LOG.Debug("IPv6端口映射方法以Provider为准，忽略实例Metadata配置",
				zap.String("instanceName", config.Name),
				zap.String("metadata_value", ipv6PortMethod),
				zap.String("final_ipv6_port_method", networkConfig.IPv6PortMappingMethod))
		}
	}

	return networkConfig
}

// configureInstanceNetwork 配置实例网络
func (i *IncusProvider) configureInstanceNetwork(ctx context.Context, config provider.InstanceConfig, networkConfig NetworkConfig) error {
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" || networkConfig.NetworkType == "ipv6_only"
	var routedIPv6 *provider.RoutedIPv6Config
	if routed, present, err := provider.ResolveRoutedIPv6(config); err != nil {
		return err
	} else if present {
		routedIPv6 = &routed
	}
	requestedIPv6 := ""
	if config.Metadata != nil {
		requestedIPv6 = config.Metadata["static_ipv6"]
	}
	if strings.TrimSpace(requestedIPv6) != "" && !hasIPv6 {
		return fmt.Errorf("已分配静态IPv6，但实例网络类型 %s 未启用IPv6", networkConfig.NetworkType)
	}

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
		global.APP_LOG.Debug("使用现有网络配置继续",
			zap.String("instanceName", config.Name))
		if hasIPv6 {
			if err := i.configureIPv6AndPortMappings(ctx, config, networkConfig, requestedIPv6, routedIPv6); err != nil {
				return fmt.Errorf("使用现有网络配置静态IPv6失败: %w", err)
			}
		}
		return nil
	}

	// IPv6-only guests intentionally have no IPv4 address. Do not make their
	// creation depend on an IPv4 DHCP lease; only the IPv4 mapping path needs it.
	instanceIP := ""
	var err error
	if networkConfig.NetworkType != "ipv6_only" {
		instanceIP, err = i.getInstanceIP(config.Name)
		if err != nil {
			return fmt.Errorf("获取实例IPv4地址失败: %w", err)
		}
	}

	// IPv6-only mappings use their own IPv6 endpoint and do not need an IPv4
	// host address. Keep this lookup out of that path so an IPv4-less node can
	// still create a usable guest.
	hostIP := ""
	if networkConfig.NetworkType != "ipv6_only" {
		hostIP, err = i.getHostIP()
		if err != nil {
			return fmt.Errorf("获取主机IPv4地址失败: %w", err)
		}
	}

	global.APP_LOG.Debug("开始配置实例网络",
		zap.String("instanceName", config.Name),
		zap.String("instanceIP", instanceIP),
		zap.String("hostIP", hostIP))

	// Keep bandwidth configuration before the address override, which may
	// copy an inherited NIC into the local device list.
	if err := i.configureNetworkLimits(config.Name, networkConfig); err != nil {
		global.APP_LOG.Warn("配置网络限速失败", zap.Error(err))
	}
	// Read the live NIC address/MAC before stopping. Guest interface names
	// (such as enp5s0 in a VM) need not match the profile device name.
	if instanceIP != "" {
		if err := i.setIPAddressBinding(config.Name, instanceIP); err != nil {
			global.APP_LOG.Warn("设置IP地址绑定失败", zap.Error(err))
		}
	}
	// 停止实例进行网络配置
	if err := i.stopInstanceForConfig(config.Name); err != nil {
		return fmt.Errorf("停止实例进行配置失败: %w", err)
	}

	// 配置端口映射 - 在实例停止时添加 proxy 设备。IPv6-only 实例的
	// IPv6 设备还没有在此阶段创建，先跳过，待 configureIPv6Network
	// 完成后再读取 eth0/eth1 的实际地址配置映射。
	if networkConfig.NetworkType != "ipv6_only" {
		if err := i.configureInitialPortMappingsWithIP(ctx, config.Name, networkConfig, instanceIP); err != nil {
			return fmt.Errorf("配置端口映射失败: %w", err)
		}
	}

	// 启动实例 - 在配置完端口映射后启动，让 proxy 设备正确初始化
	if err := i.sshStartInstance(config.Name); err != nil {
		return fmt.Errorf("启动实例失败: %w", err)
	}

	// 等待实例完全启动并获取IP地址
	if err := i.waitForInstanceReady(config.Name); err != nil {
		global.APP_LOG.Warn("等待实例就绪超时，但继续配置", zap.Error(err))
	}

	if config.InstanceType == "vm" {
		if err := i.ensureVMGuestNetworkUp(config.Name); err != nil {
			global.APP_LOG.Warn("Incus VM网络配置后唤醒Guest网络失败，继续后续流程",
				zap.String("instanceName", config.Name),
				zap.Error(err))
		}
	}

	// 配置防火墙端口
	if err := i.configureFirewallPorts(config.Name); err != nil {
		global.APP_LOG.Warn("配置防火墙端口失败", zap.Error(err))
	}

	// 配置IPv6网络（如果启用）
	if hasIPv6 {
		if err := i.configureIPv6AndPortMappings(ctx, config, networkConfig, requestedIPv6, routedIPv6); err != nil {
			return fmt.Errorf("配置IPv6网络失败: %w", err)
		}
	}

	global.APP_LOG.Debug("实例网络配置完成",
		zap.String("instanceName", config.Name),
		zap.String("instanceIP", instanceIP))

	return nil
}

// configureIPv6AndPortMappings configures the IPv6 device first and only then
// adds IPv6 proxy/firewall mappings.  A routed address does not exist before
// configureIPv6Network. Preserve the existing stop/configure/start sequence
// for these SSH creation paths; NAT proxies also support hotplug. The same ordering is used after the restart
// fallback so a transient restart failure cannot silently lose IPv6 mappings.
func (i *IncusProvider) configureIPv6AndPortMappings(ctx context.Context, config provider.InstanceConfig, networkConfig NetworkConfig, requestedIPv6 string, routed *provider.RoutedIPv6Config) error {
	if networkConfig.NetworkType == "nat_ipv4_ipv6" && routed == nil {
		guestIPv6, err := i.configureNATIPv6Network(ctx, config.Name, requestedIPv6)
		if err != nil {
			return err
		}
		// Port mappings are added while the guest is stopped, when Incus often
		// reports state.network=null. Persist the observed ULA before that stop
		// so mapping never depends on a shell working-directory sidecar file.
		if err := i.persistManagedNATIPv6Target(config.Name, guestIPv6); err != nil {
			return err
		}
	} else {
		if err := i.configureIPv6Network(ctx, config.Name, true, networkConfig.IPv6PortMappingMethod, requestedIPv6, routed, config.InstanceType); err != nil {
			return err
		}
	}
	if networkConfig.NetworkType != "nat_ipv4_ipv6" && networkConfig.NetworkType != "ipv6_only" {
		return nil
	}
	if err := i.stopInstanceForConfig(config.Name); err != nil {
		return fmt.Errorf("停止实例配置IPv6端口映射失败: %w", err)
	}
	ipv6Config := networkConfig
	ipv6Config.NetworkType = "ipv6_only"
	if err := i.configurePortMappingFamiliesWithIP(ctx, config.Name, ipv6Config, "", false, true); err != nil {
		return fmt.Errorf("配置IPv6端口映射失败: %w", err)
	}
	if err := i.sshStartInstance(config.Name); err != nil {
		return fmt.Errorf("启动实例完成IPv6端口映射失败: %w", err)
	}
	return nil
}

func (i *IncusProvider) persistManagedNATIPv6Target(instanceName, guestIPv6 string) error {
	if global.APP_DB == nil {
		return fmt.Errorf("持久化NAT IPv6实例ULA失败: 数据库未初始化")
	}
	result := global.APP_DB.Model(&providerModel.Instance{}).
		Where("name = ? AND provider_id = ?", instanceName, i.config.ID).
		Update("ipv6_address", guestIPv6)
	if result.Error != nil {
		return fmt.Errorf("持久化NAT IPv6实例ULA失败: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("持久化NAT IPv6实例ULA失败: rows=%d", result.RowsAffected)
	}
	return nil
}

// tryUseExistingNetworkConfig 尝试使用现有的网络配置继续
func (i *IncusProvider) tryUseExistingNetworkConfig(ctx context.Context, config provider.InstanceConfig, networkConfig NetworkConfig) error {
	global.APP_LOG.Debug("尝试使用现有网络配置",
		zap.String("instanceName", config.Name))

	// 检查实例是否仍在运行
	statusCmd := fmt.Sprintf("incus info %s | grep \"Status:\" | awk '{print $2}'", shellSingleQuote(config.Name))
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
		startCmd := fmt.Sprintf("incus start %s", shellSingleQuote(config.Name))
		_, err := i.sshClient.Execute(startCmd)
		if err != nil {
			return fmt.Errorf("启动实例失败: %w", err)
		}

		// 等待实例网络就绪（根据实例类型选择合适的等待方法）
		global.APP_LOG.Debug("等待实例网络就绪后再配置端口映射",
			zap.String("instanceName", config.Name))

		// 判断实例类型
		typeCmd := fmt.Sprintf("incus info %s | grep \"Type:\" | awk '{print $2}'", shellSingleQuote(config.Name))
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

	// IPv6-only instances may not expose an IPv4 lease at all. Let the port
	// mapping path resolve their IPv6 address instead of treating that as a
	// failed network configuration.
	instanceIP := ""
	if networkConfig.NetworkType != "ipv6_only" {
		var err error
		instanceIP, err = i.getInstanceIP(config.Name)
		if err != nil {
			global.APP_LOG.Error("无法获取实例IPv4地址，跳过网络配置",
				zap.String("instanceName", config.Name), zap.Error(err))
			return fmt.Errorf("无法获取实例IPv4地址: %w", err)
		}
	}

	global.APP_LOG.Debug("成功获取现有实例IP地址",
		zap.String("instanceName", config.Name),
		zap.String("instanceIP", instanceIP))

	hostIP := ""
	if networkConfig.NetworkType != "ipv6_only" {
		var err error
		hostIP, err = i.getHostIP()
		if err != nil {
			global.APP_LOG.Warn("无法获取主机IPv4地址，使用默认配置", zap.Error(err))
			hostIP = "0.0.0.0"
		}
	}

	global.APP_LOG.Debug("使用现有网络配置继续配置",
		zap.String("instanceName", config.Name),
		zap.String("instanceIP", instanceIP),
		zap.String("hostIP", hostIP))

	// 为了确保 proxy 设备正确初始化，停止容器后添加设备再启动
	// 这是 LXD/Incus 的最佳实践，特别是在 Ubuntu 24 上
	global.APP_LOG.Debug("停止实例以配置端口映射",
		zap.String("instanceName", config.Name))

	if instanceIP != "" {
		if err := i.setIPAddressBinding(config.Name, instanceIP); err != nil {
			global.APP_LOG.Warn("设置IP地址绑定失败", zap.Error(err))
		}
	}
	if err := i.stopInstanceForConfig(config.Name); err != nil {
		return fmt.Errorf("停止实例以配置端口映射失败: %w", err)
	} else {
		// IPv6-only 的目标地址要在 configureIPv6Network 创建 eth1 后才
		// 可用。正常路径会在该阶段之后重新停止实例并配置 proxy；这里
		// 也必须保持相同顺序，否则重启失败时会永久漏掉 IPv6 映射。
		if networkConfig.NetworkType != "ipv6_only" {
			if err := i.configureInitialPortMappingsWithIP(ctx, config.Name, networkConfig, instanceIP); err != nil {
				return fmt.Errorf("配置端口映射失败: %w", err)
			}
		}

		// 重新启动实例
		if err := i.sshStartInstance(config.Name); err != nil {
			return fmt.Errorf("重新启动实例失败: %w", err)
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
