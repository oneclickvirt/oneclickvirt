package lxd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// stopInstanceForConfig 停止实例进行配置
func (l *LXDProvider) stopInstanceForConfig(instanceName string) error {
	global.APP_LOG.Info("安全停止实例进行配置", zap.String("instanceName", instanceName))

	// 停止实例
	time.Sleep(6 * time.Second)
	_, err := l.sshClient.Execute(fmt.Sprintf("lxc stop %s --timeout=30", instanceName))
	if err != nil {
		return fmt.Errorf("停止实例失败: %w", err)
	}

	// 等待实例完全停止
	maxWait := 30
	waited := 0
	for waited < maxWait {
		cmd := fmt.Sprintf("lxc info %s | grep \"Status:\" | awk '{print $2}'", instanceName)
		output, err := l.sshClient.Execute(cmd)
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
func (l *LXDProvider) configureNetworkLimits(instanceName string, networkConfig NetworkConfig) error {
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

	// 获取实例的网络接口列表
	interfaceListCmd := fmt.Sprintf("lxc config device list %s", instanceName)
	output, err := l.sshClient.Execute(interfaceListCmd)

	var targetInterface string
	if err == nil {
		// 从输出中找到网络接口
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "eth0:" || strings.HasPrefix(line, "eth0 ") {
				targetInterface = "eth0"
				break
			} else if line == "enp5s0:" || strings.HasPrefix(line, "enp5s0 ") {
				targetInterface = "enp5s0"
				break
			}
		}
	}

	// 如果没有找到网络接口，默认尝试eth0
	if targetInterface == "" {
		targetInterface = "eth0"
		global.APP_LOG.Warn("未找到网络接口，默认使用eth0", zap.String("instanceName", instanceName))
	}

	// 配置网络限速
	egressCmd := fmt.Sprintf("lxc config device override %s %s limits.egress=%dMbit limits.ingress=%dMbit limits.max=%dMbit",
		instanceName, targetInterface, networkConfig.OutSpeed, networkConfig.InSpeed, speedLimit)

	_, err = l.sshClient.Execute(egressCmd)
	if err != nil {
		// 如果失败且不是eth0，再试一次eth0
		if targetInterface != "eth0" {
			global.APP_LOG.Info("配置主接口失败，尝试eth0",
				zap.String("interface", targetInterface),
				zap.Error(err))

			ethCmd := fmt.Sprintf("lxc config device override %s eth0 limits.egress=%dMbit limits.ingress=%dMbit limits.max=%dMbit",
				instanceName, networkConfig.OutSpeed, networkConfig.InSpeed, speedLimit)

			_, err = l.sshClient.Execute(ethCmd)
			if err != nil {
				return fmt.Errorf("配置网络限速失败: %w", err)
			}
			targetInterface = "eth0"
		} else {
			return fmt.Errorf("配置网络限速失败: %w", err)
		}
	}

	global.APP_LOG.Info("网络限速配置成功",
		zap.String("instanceName", instanceName),
		zap.String("interface", targetInterface),
		zap.Int("speedLimit", speedLimit))

	return nil
}

// setIPAddressBinding 设置IP地址绑定
func (l *LXDProvider) setIPAddressBinding(instanceName, instanceIP string) error {
	// 清理IP地址，移除接口名称和其他信息
	cleanIP := strings.TrimSpace(instanceIP)
	// 提取纯IP地址（移除接口名称等）
	if strings.Contains(cleanIP, "(") {
		cleanIP = strings.TrimSpace(strings.Split(cleanIP, "(")[0])
	}
	// 移除可能的端口号和其他后缀
	if strings.Contains(cleanIP, "/") {
		cleanIP = strings.Split(cleanIP, "/")[0]
	}

	global.APP_LOG.Info("设置IP地址绑定",
		zap.String("instanceName", instanceName),
		zap.String("originalIP", instanceIP),
		zap.String("cleanIP", cleanIP))

	// 获取实例的网络接口列表，智能选择接口
	interfaceListCmd := fmt.Sprintf("lxc config device list %s", instanceName)
	output, err := l.sshClient.Execute(interfaceListCmd)

	var targetInterface string
	if err == nil {
		// 从输出中找到网络接口
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "eth0:" || strings.HasPrefix(line, "eth0 ") {
				targetInterface = "eth0"
				break
			} else if line == "enp5s0:" || strings.HasPrefix(line, "enp5s0 ") {
				targetInterface = "enp5s0"
				break
			}
		}
	}

	// 如果没有找到网络接口，默认尝试eth0
	if targetInterface == "" {
		targetInterface = "eth0"
		global.APP_LOG.Warn("未找到网络接口，默认使用eth0", zap.String("instanceName", instanceName))
	}

	// 尝试设置IP地址绑定
	cmd := fmt.Sprintf("lxc config device set %s %s ipv4.address %s", instanceName, targetInterface, cleanIP)
	_, err = l.sshClient.Execute(cmd)
	if err != nil {
		global.APP_LOG.Debug("device set失败，尝试override方式",
			zap.String("interface", targetInterface),
			zap.Error(err))

		// 尝试override方式
		cmd = fmt.Sprintf("lxc config device override %s %s ipv4.address=%s", instanceName, targetInterface, cleanIP)
		_, err = l.sshClient.Execute(cmd)
		if err != nil {
			// 如果不是eth0，最后尝试eth0
			if targetInterface != "eth0" {
				global.APP_LOG.Debug("主接口override失败，尝试eth0",
					zap.String("interface", targetInterface),
					zap.Error(err))

				cmd = fmt.Sprintf("lxc config device override %s eth0 ipv4.address=%s", instanceName, cleanIP)
				_, err = l.sshClient.Execute(cmd)
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

// getBandwidthFromProvider 从Provider配置获取带宽设置，并结合用户等级限制
func (l *LXDProvider) getBandwidthFromProvider(userLevel int) (inSpeed, outSpeed int, err error) {
	// 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.Where("name = ?", l.config.Name).First(&providerInfo).Error; err != nil {
		// 如果获取Provider失败，使用默认值
		global.APP_LOG.Warn("无法获取Provider配置，使用默认带宽",
			zap.String("provider", l.config.Name),
			zap.Error(err))
		return 300, 300, nil // 默认300Mbps
	}

	// 基础带宽配置（来自Provider）
	providerInSpeed := providerInfo.DefaultInboundBandwidth
	providerOutSpeed := providerInfo.DefaultOutboundBandwidth

	// 获取用户等级对应的带宽限制
	userBandwidthLimit := l.getUserLevelBandwidth(userLevel)

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
		inSpeed = 100 // 默认100Mbps
	}
	if outSpeed <= 0 {
		outSpeed = 100 // 默认100Mbps
	}

	// 确保不超过Provider的最大限制
	if providerInfo.MaxInboundBandwidth > 0 && inSpeed > providerInfo.MaxInboundBandwidth {
		inSpeed = providerInfo.MaxInboundBandwidth
	}
	if providerInfo.MaxOutboundBandwidth > 0 && outSpeed > providerInfo.MaxOutboundBandwidth {
		outSpeed = providerInfo.MaxOutboundBandwidth
	}

	global.APP_LOG.Info("从Provider配置和用户等级获取带宽设置",
		zap.String("provider", l.config.Name),
		zap.Int("inSpeed", inSpeed),
		zap.Int("outSpeed", outSpeed),
		zap.Int("userLevel", userLevel),
		zap.Int("userBandwidthLimit", userBandwidthLimit),
		zap.Int("providerDefault", providerInSpeed))

	return inSpeed, outSpeed, nil
}

// getUserLevelBandwidth 根据用户等级获取带宽限制
func (l *LXDProvider) getUserLevelBandwidth(userLevel int) int {
	// 从全局配置中获取用户等级对应的带宽限制
	if levelLimits, exists := global.GetAppConfig().Quota.LevelLimits[userLevel]; exists {
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

// tryUseExistingNetworkConfig 尝试使用现有的网络配置继续
func (l *LXDProvider) tryUseExistingNetworkConfig(config provider.InstanceConfig, networkConfig NetworkConfig) error {
	global.APP_LOG.Info("尝试使用现有网络配置",
		zap.String("instanceName", config.Name))

	// 检查实例是否仍在运行
	statusCmd := fmt.Sprintf("lxc info %s | grep \"Status:\" | awk '{print $2}'", config.Name)
	output, err := l.sshClient.Execute(statusCmd)
	if err != nil {
		return fmt.Errorf("检查实例状态失败: %w", err)
	}

	status := utils.CleanCommandOutput(output)
	if status != "RUNNING" {
		global.APP_LOG.Warn("实例未运行，尝试启动",
			zap.String("instanceName", config.Name),
			zap.String("status", status))

		// 尝试启动实例
		startCmd := fmt.Sprintf("lxc start %s", config.Name)
		_, err := l.sshClient.Execute(startCmd)
		if err != nil {
			return fmt.Errorf("启动实例失败: %w", err)
		}

		// 等待实例网络就绪（根据实例类型选择合适的等待方法）
		global.APP_LOG.Info("等待实例网络就绪后再配置端口映射",
			zap.String("instanceName", config.Name))

		// 判断实例类型
		typeCmd := fmt.Sprintf("lxc info %s | grep \"Type:\" | awk '{print $2}'", config.Name)
		typeOutput, err := l.sshClient.Execute(typeCmd)
		instanceType := strings.TrimSpace(typeOutput)

		if err == nil && (instanceType == "virtual-machine" || instanceType == "vm") {
			// 虚拟机需要更长的等待时间
			if err := l.waitForVMNetworkReady(config.Name); err != nil {
				global.APP_LOG.Warn("等待虚拟机网络就绪超时，继续尝试配置",
					zap.String("instanceName", config.Name),
					zap.Error(err))
			}
		} else {
			// 容器使用较短的等待时间
			if err := l.waitForContainerNetworkReady(config.Name); err != nil {
				global.APP_LOG.Warn("等待容器网络就绪超时，继续尝试配置",
					zap.String("instanceName", config.Name),
					zap.Error(err))
			}
		}
	}

	// 尝试获取现有IP地址
	instanceIP, err := l.getInstanceIP(config.Name)
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
	hostIP, err := l.getHostIP()
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
	// 这是 LXD 的最佳实践，特别是在 Ubuntu 24 上
	global.APP_LOG.Info("停止实例以配置端口映射",
		zap.String("instanceName", config.Name))

	if err := l.stopInstanceForConfig(config.Name); err != nil {
		global.APP_LOG.Warn("停止实例失败，尝试直接配置",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	} else {
		// 尝试配置端口映射（容器停止状态）
		if err := l.configurePortMappingsWithIP(config.Name, networkConfig, instanceIP); err != nil {
			global.APP_LOG.Warn("配置端口映射失败，但继续",
				zap.String("instanceName", config.Name),
				zap.Error(err))
		}

		// 重新启动实例
		ctx := context.Background()
		if err := l.StartInstance(ctx, config.Name); err != nil {
			global.APP_LOG.Warn("启动实例失败",
				zap.String("instanceName", config.Name),
				zap.Error(err))
		}
	}

	// 尝试配置防火墙端口（如果失败只记录警告）
	if err := l.configureFirewallPorts(config.Name); err != nil {
		global.APP_LOG.Warn("配置防火墙端口失败，但继续",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	}

	return nil
}
