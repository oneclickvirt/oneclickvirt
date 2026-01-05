package incus

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

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

// restartInstanceForNetwork 重启实例以获取网络配置
func (i *IncusProvider) restartInstanceForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启实例获取网络配置", zap.String("instanceName", instanceName))

	// 检查实例类型以决定重启策略
	instanceType, err := i.getInstanceType(instanceName)
	if err != nil {
		global.APP_LOG.Warn("无法检测实例类型，使用默认重启策略",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		instanceType = "container" // 默认按容器处理
	}

	// 根据实例类型选择不同的重启策略
	if instanceType == "virtual-machine" {
		return i.restartVMForNetwork(instanceName)
	} else {
		return i.restartContainerForNetwork(instanceName)
	}
}

// restartVMForNetwork 重启虚拟机以获取网络配置
func (i *IncusProvider) restartVMForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启虚拟机获取网络配置", zap.String("instanceName", instanceName))

	// 尝试优雅重启，给虚拟机足够的超时时间
	restartCmd := fmt.Sprintf("incus restart %s --timeout=120", instanceName)
	_, err := i.sshClient.Execute(restartCmd)

	if err != nil {
		global.APP_LOG.Warn("优雅重启虚拟机失败，尝试强制重启",
			zap.String("instanceName", instanceName),
			zap.Error(err))

		// 如果优雅重启失败，尝试强制停止后重启
		return i.forceRestartVM(instanceName)
	}

	// 等待虚拟机完全启动并获取IP
	return i.waitForVMNetworkReady(instanceName)
}

// restartContainerForNetwork 重启容器以获取网络配置
func (i *IncusProvider) restartContainerForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启容器获取网络配置", zap.String("instanceName", instanceName))

	// 容器重启
	restartCmd := fmt.Sprintf("incus restart %s --timeout=60", instanceName)
	_, err := i.sshClient.Execute(restartCmd)

	if err != nil {
		global.APP_LOG.Warn("容器重启失败，尝试强制重启",
			zap.String("instanceName", instanceName),
			zap.Error(err))

		// 强制重启容器
		return i.forceRestartContainer(instanceName)
	}

	// 等待容器启动并获取IP
	return i.waitForContainerNetworkReady(instanceName)
}

// forceRestartVM 强制重启虚拟机
func (i *IncusProvider) forceRestartVM(instanceName string) error {
	global.APP_LOG.Info("强制重启虚拟机", zap.String("instanceName", instanceName))

	// 强制停止虚拟机
	stopCmd := fmt.Sprintf("incus stop %s --force --timeout=60", instanceName)
	_, err := i.sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Error("强制停止虚拟机失败",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		return fmt.Errorf("强制停止虚拟机失败: %w", err)
	}

	// 等待完全停止
	time.Sleep(10 * time.Second)

	// 启动虚拟机
	startCmd := fmt.Sprintf("incus start %s", instanceName)
	_, err = i.sshClient.Execute(startCmd)
	if err != nil {
		return fmt.Errorf("启动虚拟机失败: %w", err)
	}

	// 等待虚拟机网络就绪
	return i.waitForVMNetworkReady(instanceName)
}

// forceRestartContainer 强制重启容器
func (i *IncusProvider) forceRestartContainer(instanceName string) error {
	global.APP_LOG.Info("强制重启容器", zap.String("instanceName", instanceName))

	// 强制停止容器
	stopCmd := fmt.Sprintf("incus stop %s --force --timeout=30", instanceName)
	_, err := i.sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Error("强制停止容器失败",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		return fmt.Errorf("强制停止容器失败: %w", err)
	}

	// 短暂等待
	time.Sleep(3 * time.Second)

	// 启动容器
	startCmd := fmt.Sprintf("incus start %s", instanceName)
	_, err = i.sshClient.Execute(startCmd)
	if err != nil {
		return fmt.Errorf("启动容器失败: %w", err)
	}

	// 等待容器网络就绪
	return i.waitForContainerNetworkReady(instanceName)
}

// waitForVMNetworkReady 等待虚拟机网络就绪
func (i *IncusProvider) waitForVMNetworkReady(instanceName string) error {
	global.APP_LOG.Info("等待虚拟机网络就绪", zap.String("instanceName", instanceName))

	maxRetries := 8 // 重试次数
	delay := 15     // 虚拟机需要更长的启动时间

	for attempt := 1; attempt <= maxRetries; attempt++ {
		global.APP_LOG.Info("等待虚拟机启动并获取IP地址",
			zap.String("instanceName", instanceName),
			zap.Int("attempt", attempt),
			zap.Int("maxRetries", maxRetries),
			zap.Int("delay", delay))

		time.Sleep(time.Duration(delay) * time.Second)

		// 检查虚拟机状态
		statusCmd := fmt.Sprintf("incus info %s | grep \"Status:\" | awk '{print $2}'", instanceName)
		output, err := i.sshClient.Execute(statusCmd)
		if err != nil {
			global.APP_LOG.Warn("检查虚拟机状态失败",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", attempt),
				zap.Error(err))
			continue
		}

		status := strings.TrimSpace(output)
		if status != "RUNNING" {
			global.APP_LOG.Info("虚拟机尚未运行",
				zap.String("instanceName", instanceName),
				zap.String("status", status),
				zap.Int("attempt", attempt))
			continue
		}

		// 检查是否已获取到IP地址
		if _, err := i.getInstanceIP(instanceName); err == nil {
			global.APP_LOG.Info("虚拟机网络已就绪",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", attempt))
			return nil
		}

		// 逐渐增加等待时间
		if attempt < maxRetries {
			delay = min(delay+5, 25)
		}
	}

	return fmt.Errorf("虚拟机网络就绪超时，已等待 %d 次", maxRetries)
}

// waitForContainerNetworkReady 等待容器网络就绪
func (i *IncusProvider) waitForContainerNetworkReady(instanceName string) error {
	global.APP_LOG.Info("等待容器网络就绪", zap.String("instanceName", instanceName))

	maxRetries := 10 // 容器启动较快
	delay := 5       // 容器启动时间较短

	for attempt := 1; attempt <= maxRetries; attempt++ {
		global.APP_LOG.Info("等待容器启动并获取IP地址",
			zap.String("instanceName", instanceName),
			zap.Int("attempt", attempt),
			zap.Int("maxRetries", maxRetries),
			zap.Int("delay", delay))

		time.Sleep(time.Duration(delay) * time.Second)

		// 检查容器状态
		statusCmd := fmt.Sprintf("incus info %s | grep \"Status:\" | awk '{print $2}'", instanceName)
		output, err := i.sshClient.Execute(statusCmd)
		if err != nil {
			global.APP_LOG.Warn("检查容器状态失败",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", attempt),
				zap.Error(err))
			continue
		}

		status := strings.TrimSpace(output)
		if status != "RUNNING" {
			global.APP_LOG.Info("容器尚未运行",
				zap.String("instanceName", instanceName),
				zap.String("status", status),
				zap.Int("attempt", attempt))
			continue
		}

		// 检查是否已获取到IP地址
		if _, err := i.getInstanceIP(instanceName); err == nil {
			global.APP_LOG.Info("容器网络已就绪",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", attempt))
			return nil
		}

		// 逐渐增加等待时间
		if attempt < maxRetries {
			delay = min(delay+2, 15) // 最大等待15秒
		}
	}

	return fmt.Errorf("容器网络就绪超时，已等待 %d 次", maxRetries)
}

// getInstanceType 获取实例类型
func (i *IncusProvider) getInstanceType(instanceName string) (string, error) {
	cmd := fmt.Sprintf("incus info %s | grep \"Type:\" | awk '{print $2}'", instanceName)
	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取实例类型失败: %w", err)
	}

	instanceType := utils.CleanCommandOutput(output)
	global.APP_LOG.Debug("检测到实例类型",
		zap.String("instanceName", instanceName),
		zap.String("type", instanceType))

	return instanceType, nil
}

// stopInstanceForConfig 停止实例进行配置
func (i *IncusProvider) stopInstanceForConfig(instanceName string) error {
	global.APP_LOG.Info("停止实例进行配置", zap.String("instanceName", instanceName))

	// 等待一段时间确保实例已经获取到IP
	time.Sleep(6 * time.Second)
	_, err := i.sshClient.Execute(fmt.Sprintf("incus stop %s --timeout=30", instanceName))
	if err != nil {
		return fmt.Errorf("停止实例失败: %w", err)
	}

	// 等待实例完全停止
	maxWait := 30
	waited := 0
	for waited < maxWait {
		cmd := fmt.Sprintf("incus info %s | grep \"Status:\" | awk '{print $2}'", instanceName)
		output, err := i.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) == "STOPPED" {
			global.APP_LOG.Info("实例已安全停止", zap.String("instanceName", instanceName))
			return nil
		}

		time.Sleep(2 * time.Second)
		waited += 2
		global.APP_LOG.Info("等待实例停止",
			zap.String("instanceName", instanceName),
			zap.Int("waited", waited),
			zap.Int("maxWait", maxWait))
	}
	time.Sleep(6 * time.Second)
	global.APP_LOG.Warn("实例停止超时，但继续配置流程", zap.String("instanceName", instanceName))
	return nil
}

// configureNetworkLimits 配置网络限速
func (i *IncusProvider) configureNetworkLimits(instanceName string, networkConfig NetworkConfig) error {
	global.APP_LOG.Info("配置网络限速",
		zap.String("instanceName", instanceName),
		zap.Int("inSpeed", networkConfig.InSpeed),
		zap.Int("outSpeed", networkConfig.OutSpeed))

	var speedLimit int
	if networkConfig.InSpeed == networkConfig.OutSpeed {
		speedLimit = networkConfig.InSpeed
	} else {
		if networkConfig.InSpeed > networkConfig.OutSpeed {
			speedLimit = networkConfig.InSpeed
		} else {
			speedLimit = networkConfig.OutSpeed
		}
	}

	// 找到主网络接口
	cmd := fmt.Sprintf("incus config show %s | grep -A5 \"devices:\" | grep \"type: nic\" -B3 | grep \"^  \" | head -n1 | sed 's/://g'", instanceName)
	output, err := i.sshClient.Execute(cmd)
	var targetInterface string
	if err == nil && utils.CleanCommandOutput(output) != "" {
		targetInterface = utils.CleanCommandOutput(output)
	} else {
		targetInterface = "eth0" // 默认接口
	}

	// 设置网络限速
	inSpeedMbit := fmt.Sprintf("%dMbit", networkConfig.InSpeed)
	outSpeedMbit := fmt.Sprintf("%dMbit", networkConfig.OutSpeed)
	maxSpeedMbit := fmt.Sprintf("%dMbit", speedLimit)

	cmd = fmt.Sprintf("incus config device override %s %s limits.egress=%s limits.ingress=%s limits.max=%s",
		instanceName, targetInterface, outSpeedMbit, inSpeedMbit, maxSpeedMbit)
	_, err = i.sshClient.Execute(cmd)
	if err != nil {
		global.APP_LOG.Warn("网络限速配置失败",
			zap.String("instanceName", instanceName),
			zap.String("interface", targetInterface),
			zap.Error(err))
		return err
	}

	global.APP_LOG.Info("网络限速配置成功",
		zap.String("instanceName", instanceName),
		zap.String("interface", targetInterface),
		zap.String("inSpeed", inSpeedMbit),
		zap.String("outSpeed", outSpeedMbit))

	return nil
}

// getBandwidthFromProvider 从Provider配置获取带宽设置，并结合用户等级限制
func (i *IncusProvider) getBandwidthFromProvider(userLevel int) (inSpeed, outSpeed int, err error) {
	// 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.Where("name = ?", i.config.Name).First(&providerInfo).Error; err != nil {
		// 如果获取Provider失败，使用默认值
		global.APP_LOG.Warn("无法获取Provider配置，使用默认带宽",
			zap.String("provider", i.config.Name),
			zap.Error(err))
		return 300, 300, nil // 默认300Mbps
	}

	// 基础带宽配置（来自Provider）
	providerInSpeed := providerInfo.DefaultInboundBandwidth
	providerOutSpeed := providerInfo.DefaultOutboundBandwidth

	// 获取用户等级对应的带宽限制
	userBandwidthLimit := i.getUserLevelBandwidth(userLevel)

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
		zap.String("provider", i.config.Name),
		zap.Int("inSpeed", inSpeed),
		zap.Int("outSpeed", outSpeed),
		zap.Int("userLevel", userLevel),
		zap.Int("userBandwidthLimit", userBandwidthLimit),
		zap.Int("providerDefault", providerInSpeed))

	return inSpeed, outSpeed, nil
}

// getUserLevelBandwidth 根据用户等级获取带宽限制
func (i *IncusProvider) getUserLevelBandwidth(userLevel int) int {
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

// getInstanceIP 获取实例IP地址
func (i *IncusProvider) getInstanceIP(instanceName string) (string, error) {
	// 检查实例类型以决定获取IP的策略
	instanceType, err := i.getInstanceType(instanceName)
	if err != nil {
		global.APP_LOG.Warn("无法检测实例类型，使用通用IP获取方式",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		return i.getInstanceIPGeneric(instanceName)
	}

	// 根据实例类型选择不同的IP获取方式
	if instanceType == "virtual-machine" {
		return i.getVMInstanceIP(instanceName)
	} else {
		return i.getContainerInstanceIP(instanceName)
	}
}

// getVMInstanceIP 获取虚拟机实例IP地址
func (i *IncusProvider) getVMInstanceIP(instanceName string) (string, error) {
	global.APP_LOG.Debug("获取虚拟机IP地址", zap.String("instanceName", instanceName))

	maxRetries := 5
	delay := 10

	for attempt := 1; attempt <= maxRetries; attempt++ {
		global.APP_LOG.Debug("尝试获取虚拟机IP地址",
			zap.String("instanceName", instanceName),
			zap.Int("attempt", attempt),
			zap.Int("maxRetries", maxRetries),
			zap.Int("delay", delay))

		time.Sleep(time.Duration(delay) * time.Second)

		// 虚拟机通常使用 enp5s0 接口，如果没有则尝试 eth0
		interfaces := []string{"enp5s0", "eth0"}

		for _, iface := range interfaces {
			cmd := fmt.Sprintf("incus list %s --format json | jq -r '.[0].state.network.%s.addresses[]? | select(.family==\"inet\") | .address' 2>/dev/null", instanceName, iface)
			output, err := i.sshClient.Execute(cmd)

			if err == nil && strings.TrimSpace(output) != "" {
				vmIP := strings.TrimSpace(output)
				global.APP_LOG.Info("虚拟机IPv4地址获取成功",
					zap.String("instanceName", instanceName),
					zap.String("interface", iface),
					zap.String("ip", vmIP),
					zap.Int("attempt", attempt))
				return vmIP, nil
			}
		}

		// 逐渐增加等待时间
		delay += 5
	}

	// 如果专用方法失败，回退到通用方法
	global.APP_LOG.Warn("虚拟机专用IP获取方法失败，回退到通用方法",
		zap.String("instanceName", instanceName))
	return i.getInstanceIPGeneric(instanceName)
}

// getContainerInstanceIP 获取容器实例IP地址
func (i *IncusProvider) getContainerInstanceIP(instanceName string) (string, error) {
	global.APP_LOG.Debug("获取容器IP地址", zap.String("instanceName", instanceName))

	maxRetries := 3
	delay := 5

	for attempt := 1; attempt <= maxRetries; attempt++ {
		global.APP_LOG.Debug("尝试获取容器IP地址",
			zap.String("instanceName", instanceName),
			zap.Int("attempt", attempt),
			zap.Int("maxRetries", maxRetries),
			zap.Int("delay", delay))

		time.Sleep(time.Duration(delay) * time.Second)

		// 容器通常使用 eth0 接口
		cmd := fmt.Sprintf("incus list %s --format json | jq -r '.[0].state.network.eth0.addresses[]? | select(.family==\"inet\") | .address' 2>/dev/null", instanceName)
		output, err := i.sshClient.Execute(cmd)

		if err == nil && strings.TrimSpace(output) != "" {
			containerIP := strings.TrimSpace(output)
			global.APP_LOG.Info("容器IPv4地址获取成功",
				zap.String("instanceName", instanceName),
				zap.String("ip", containerIP),
				zap.Int("attempt", attempt))
			return containerIP, nil
		}

		// 指数退避
		delay *= 2
	}

	// 如果专用方法失败，回退到通用方法
	global.APP_LOG.Warn("容器专用IP获取方法失败，回退到通用方法",
		zap.String("instanceName", instanceName))
	return i.getInstanceIPGeneric(instanceName)
}

// getInstanceIPGeneric 通用IP获取方法（作为后备方案）
func (i *IncusProvider) getInstanceIPGeneric(instanceName string) (string, error) {
	global.APP_LOG.Debug("使用通用方法获取IP地址", zap.String("instanceName", instanceName))

	// 多种方式尝试获取IP地址
	maxRetries := 5
	for attempt := 1; attempt <= maxRetries; attempt++ {
		global.APP_LOG.Debug("尝试获取实例IP地址（通用方法）",
			zap.String("instanceName", instanceName),
			zap.Int("attempt", attempt),
			zap.Int("maxRetries", maxRetries))

		// 使用 incus list 简单格式获取IP
		cmd := fmt.Sprintf("incus list %s -c 4 --format csv", instanceName)
		output, err := i.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) != "" {
			global.APP_LOG.Debug("incus list原始输出",
				zap.String("instanceName", instanceName),
				zap.String("output", output))

			// 解析输出，查找IPv4地址
			lines := strings.Split(strings.TrimSpace(output), "\n")
			for _, line := range lines {
				addresses := strings.Split(strings.TrimSpace(line), ",")
				for _, addr := range addresses {
					addr = strings.TrimSpace(addr)
					global.APP_LOG.Debug("检查地址", zap.String("addr", addr))

					// 检查是否是IPv4地址格式
					if strings.Contains(addr, ".") && !strings.Contains(addr, ":") {
						// 移除可能的网络前缀 (如 /24)
						if strings.Contains(addr, "/") {
							addr = strings.Split(addr, "/")[0]
						}
						// 移除可能的接口名称信息 (如 "(eth0)")
						if strings.Contains(addr, "(") {
							addr = strings.TrimSpace(strings.Split(addr, "(")[0])
						}
						// 移除可能的空格和接口名称
						if strings.Contains(addr, " ") {
							addr = strings.TrimSpace(strings.Split(addr, " ")[0])
						}

						// 验证是否是有效的IPv4地址
						parts := strings.Split(addr, ".")
						if len(parts) == 4 {
							global.APP_LOG.Info("通过incus list找到有效IP地址",
								zap.String("instanceName", instanceName),
								zap.String("ip", addr),
								zap.Int("attempt", attempt))
							return addr, nil
						}
					}
				}
			}
		}

		// 如果还没有获取到IP，等待一段时间后重试
		if attempt < maxRetries {
			waitTime := time.Duration(attempt*3) * time.Second
			global.APP_LOG.Warn("未能获取到IP地址，等待后重试",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", attempt),
				zap.Duration("waitTime", waitTime))
			time.Sleep(waitTime)
		}
	}

	return "", fmt.Errorf("经过%d次尝试仍无法获取实例IP地址", maxRetries)
}

// getHostIP 获取主机IP地址
func (i *IncusProvider) getHostIP() (string, error) {
	// 1. 优先使用配置的 PortIP（端口映射专用IP）
	if i.config.PortIP != "" {
		global.APP_LOG.Debug("使用配置的PortIP作为端口映射地址",
			zap.String("portIP", i.config.PortIP))
		return i.config.PortIP, nil
	}

	// 2. 如果 PortIP 为空，尝试从 Host 提取或解析 IP
	if i.config.Host != "" {
		// 检查 Host 是否已经是 IP 地址
		if net.ParseIP(i.config.Host) != nil {
			global.APP_LOG.Debug("SSH连接地址是IP，直接用于端口映射",
				zap.String("host", i.config.Host))
			return i.config.Host, nil
		}

		// Host 是域名，尝试解析为 IP
		global.APP_LOG.Debug("SSH连接地址是域名，尝试解析",
			zap.String("domain", i.config.Host))
		ips, err := net.LookupIP(i.config.Host)
		if err == nil && len(ips) > 0 {
			for _, ip := range ips {
				if ipv4 := ip.To4(); ipv4 != nil {
					global.APP_LOG.Debug("域名解析成功，使用解析的IP",
						zap.String("domain", i.config.Host),
						zap.String("resolvedIP", ipv4.String()))
					return ipv4.String(), nil
				}
			}
		} else if err != nil {
			global.APP_LOG.Warn("域名解析失败，回退到宿主机IP获取",
				zap.String("domain", i.config.Host),
				zap.Error(err))
		}
	}

	// 3. 最后才从宿主机动态获取 IP地址
	global.APP_LOG.Info("从宿主机动态获取IP地址")
	cmd := "ip addr show | awk '/inet .*global/ && !/inet6/ {print $2}' | sed -n '1p' | cut -d/ -f1"
	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取主机IP失败: %w", err)
	}

	hostIP := strings.TrimSpace(output)
	if hostIP == "" {
		return "", fmt.Errorf("主机IP为空")
	}

	global.APP_LOG.Info("从宿主机获取到IP地址",
		zap.String("hostIP", hostIP))
	return hostIP, nil
}

// waitForInstanceReady 等待实例就绪
func (i *IncusProvider) waitForInstanceReady(instanceName string) error {
	maxWait := 60 // 等待60秒
	waited := 0

	for waited < maxWait {
		cmd := fmt.Sprintf("incus info %s | grep \"Status:\" | awk '{print $2}'", instanceName)
		output, err := i.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) == "RUNNING" {
			// 额外等待网络配置就绪
			time.Sleep(5 * time.Second)
			return nil
		}

		time.Sleep(3 * time.Second)
		waited += 3
		global.APP_LOG.Debug("等待实例就绪",
			zap.String("instanceName", instanceName),
			zap.Int("waited", waited))
	}

	return fmt.Errorf("等待实例就绪超时")
}

// setIPAddressBinding 设置IP地址绑定
func (i *IncusProvider) setIPAddressBinding(instanceName, instanceIP string) error {
	global.APP_LOG.Info("设置IP地址绑定",
		zap.String("instanceName", instanceName),
		zap.String("instanceIP", instanceIP))

	// 清理IP地址格式
	cleanIP := strings.TrimSpace(instanceIP)
	if strings.Contains(cleanIP, "/") {
		cleanIP = strings.Split(cleanIP, "/")[0]
	}

	// 获取网络接口名称
	cmd := fmt.Sprintf("incus config show %s | grep -A5 \"devices:\" | grep \"type: nic\" -B3 | grep \"^  \" | head -n1 | sed 's/://g'", instanceName)
	output, err := i.sshClient.Execute(cmd)
	var targetInterface string
	if err == nil && utils.CleanCommandOutput(output) != "" {
		targetInterface = utils.CleanCommandOutput(output)
	}

	// 如果没有找到网络接口，默认尝试eth0
	if targetInterface == "" {
		targetInterface = "eth0"
		global.APP_LOG.Warn("未找到网络接口，默认使用eth0", zap.String("instanceName", instanceName))
	}

	// 尝试设置IP地址绑定
	cmd = fmt.Sprintf("incus config device set %s %s ipv4.address %s", instanceName, targetInterface, cleanIP)
	_, err = i.sshClient.Execute(cmd)
	if err != nil {
		global.APP_LOG.Debug("device set失败，尝试override方式",
			zap.String("interface", targetInterface),
			zap.Error(err))

		// 尝试override方式
		cmd = fmt.Sprintf("incus config device override %s %s ipv4.address=%s", instanceName, targetInterface, cleanIP)
		_, err = i.sshClient.Execute(cmd)
		if err != nil {
			// 如果不是eth0，最后尝试eth0
			if targetInterface != "eth0" {
				global.APP_LOG.Debug("主接口override失败，尝试eth0",
					zap.String("interface", targetInterface),
					zap.Error(err))

				cmd = fmt.Sprintf("incus config device override %s eth0 ipv4.address=%s", instanceName, cleanIP)
				_, err = i.sshClient.Execute(cmd)
				if err != nil {
					global.APP_LOG.Warn("IP地址绑定失败，继续执行",
						zap.String("finalCommand", cmd),
						zap.Error(err))
					return nil // 不阻止流程继续
				}
				targetInterface = "eth0"
			} else {
				global.APP_LOG.Warn("IP地址绑定失败，继续执行",
					zap.String("finalCommand", cmd),
					zap.Error(err))
				return nil // 不阻止流程继续
			}
		}
	}

	global.APP_LOG.Info("IP地址绑定成功",
		zap.String("instanceName", instanceName),
		zap.String("interface", targetInterface),
		zap.String("cleanIP", cleanIP))

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
