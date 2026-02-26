package incus

import (
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

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
