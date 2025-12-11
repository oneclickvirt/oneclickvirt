package lxd

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

	"go.uber.org/zap"
)

// NetworkConfig LXD网络配置结构
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

// configureInstanceNetwork 配置实例网络
func (l *LXDProvider) configureInstanceNetwork(ctx context.Context, config provider.InstanceConfig, networkConfig NetworkConfig) error {
	// 检查是否启用IPv6
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" || networkConfig.NetworkType == "ipv6_only"

	global.APP_LOG.Debug("LXD网络配置IPv6检测",
		zap.String("instanceName", config.Name),
		zap.String("networkType", networkConfig.NetworkType),
		zap.Bool("hasIPv6", hasIPv6))

	// 重启实例以获取IP地址（增强容错）
	if err := l.restartInstanceForNetwork(config.Name); err != nil {
		global.APP_LOG.Warn("重启实例获取网络配置失败，尝试直接获取现有网络配置",
			zap.String("instanceName", config.Name),
			zap.Error(err))

		// 如果重启失败，尝试直接使用现有网络配置继续
		if err := l.tryUseExistingNetworkConfig(config, networkConfig); err != nil {
			return fmt.Errorf("重启实例获取网络配置失败且无法使用现有配置: %w", err)
		}
		global.APP_LOG.Info("使用现有网络配置继续",
			zap.String("instanceName", config.Name))
		return nil
	}

	// 获取实例IP地址
	instanceIP, err := l.getInstanceIP(config.Name)
	if err != nil {
		return fmt.Errorf("获取实例IP地址失败: %w", err)
	}

	// 获取主机IP地址
	hostIP, err := l.getHostIP()
	if err != nil {
		return fmt.Errorf("获取主机IP地址失败: %w", err)
	}

	global.APP_LOG.Info("开始配置实例网络",
		zap.String("instanceName", config.Name),
		zap.String("instanceIP", instanceIP),
		zap.String("hostIP", hostIP))

	// 停止实例进行网络配置
	if err := l.stopInstanceForConfig(config.Name); err != nil {
		return fmt.Errorf("停止实例进行配置失败: %w", err)
	}

	// 配置网络限速
	if err := l.configureNetworkLimits(config.Name, networkConfig); err != nil {
		global.APP_LOG.Warn("配置网络限速失败", zap.Error(err))
	}

	// 设置IP地址绑定
	if err := l.setIPAddressBinding(config.Name, instanceIP); err != nil {
		global.APP_LOG.Warn("设置IP地址绑定失败", zap.Error(err))
	}

	// 启动实例 - 先启动实例，确保有静态IP地址后再配置端口映射
	if err := l.StartInstance(ctx, config.Name); err != nil {
		return fmt.Errorf("启动实例失败: %w", err)
	}

	// 等待实例完全启动并获取IP地址
	if err := l.waitForInstanceReady(ctx, config.Name); err != nil {
		global.APP_LOG.Warn("等待实例就绪超时，但继续配置", zap.Error(err))
	}

	// 配置端口映射 - 在实例启动后配置，确保有静态IP地址
	if err := l.configurePortMappingsWithIP(config.Name, networkConfig, instanceIP); err != nil {
		global.APP_LOG.Warn("配置端口映射失败", zap.Error(err))
	}

	// 配置防火墙端口
	if err := l.configureFirewallPorts(config.Name); err != nil {
		global.APP_LOG.Warn("配置防火墙端口失败", zap.Error(err))
	}

	// 配置IPv6网络（如果启用）
	global.APP_LOG.Debug("检查是否需要配置IPv6网络",
		zap.String("instanceName", config.Name),
		zap.Bool("enableIPv6", hasIPv6),
		zap.String("ipv6PortMappingMethod", networkConfig.IPv6PortMappingMethod))

	if hasIPv6 {
		global.APP_LOG.Info("开始配置IPv6网络",
			zap.String("instanceName", config.Name),
			zap.String("ipv6PortMappingMethod", networkConfig.IPv6PortMappingMethod))

		if err := l.configureIPv6Network(ctx, config.Name, hasIPv6, networkConfig.IPv6PortMappingMethod); err != nil {
			global.APP_LOG.Warn("配置IPv6网络失败", zap.Error(err))
		}
	} else {
		global.APP_LOG.Info("IPv6未启用，跳过IPv6网络配置",
			zap.String("instanceName", config.Name))
	}

	global.APP_LOG.Info("实例网络配置完成",
		zap.String("instanceName", config.Name),
		zap.String("instanceIP", instanceIP))

	return nil
}

// restartInstanceForNetwork 重启实例以获取网络配置
func (l *LXDProvider) restartInstanceForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启实例获取网络配置", zap.String("instanceName", instanceName))

	// 检查实例类型以决定重启策略
	instanceType, err := l.getInstanceType(instanceName)
	if err != nil {
		global.APP_LOG.Warn("无法检测实例类型，使用默认重启策略",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		instanceType = "container" // 默认按容器处理
	}

	// 根据实例类型选择不同的重启策略
	if instanceType == "virtual-machine" {
		return l.restartVMForNetwork(instanceName)
	} else {
		return l.restartContainerForNetwork(instanceName)
	}
}

// restartVMForNetwork 重启虚拟机以获取网络配置
func (l *LXDProvider) restartVMForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启虚拟机获取网络配置", zap.String("instanceName", instanceName))

	// 尝试优雅重启，给虚拟机足够的超时时间
	restartCmd := fmt.Sprintf("lxc restart %s --timeout=120", instanceName)
	_, err := l.sshClient.Execute(restartCmd)

	if err != nil {
		global.APP_LOG.Warn("优雅重启虚拟机失败，尝试强制重启",
			zap.String("instanceName", instanceName),
			zap.Error(err))

		// 如果优雅重启失败，尝试强制停止后重启
		return l.forceRestartVM(instanceName)
	}

	// 等待虚拟机完全启动并获取IP
	return l.waitForVMNetworkReady(instanceName)
}

// restartContainerForNetwork 重启容器以获取网络配置
func (l *LXDProvider) restartContainerForNetwork(instanceName string) error {
	global.APP_LOG.Info("重启容器获取网络配置", zap.String("instanceName", instanceName))
	restartCmd := fmt.Sprintf("lxc restart %s --timeout=60", instanceName)
	_, err := l.sshClient.Execute(restartCmd)

	if err != nil {
		global.APP_LOG.Warn("容器重启失败，尝试强制重启",
			zap.String("instanceName", instanceName),
			zap.Error(err))

		// 强制重启容器
		return l.forceRestartContainer(instanceName)
	}

	// 等待容器启动并获取IP
	return l.waitForContainerNetworkReady(instanceName)
}

// forceRestartVM 强制重启虚拟机
func (l *LXDProvider) forceRestartVM(instanceName string) error {
	global.APP_LOG.Info("强制重启虚拟机", zap.String("instanceName", instanceName))

	// 强制停止虚拟机
	stopCmd := fmt.Sprintf("lxc stop %s --force --timeout=60", instanceName)
	_, err := l.sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Error("强制停止虚拟机失败",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		return fmt.Errorf("强制停止虚拟机失败: %w", err)
	}

	// 等待完全停止
	time.Sleep(10 * time.Second)

	// 启动虚拟机
	startCmd := fmt.Sprintf("lxc start %s", instanceName)
	_, err = l.sshClient.Execute(startCmd)
	if err != nil {
		return fmt.Errorf("启动虚拟机失败: %w", err)
	}

	// 等待虚拟机网络就绪
	return l.waitForVMNetworkReady(instanceName)
}

// forceRestartContainer 强制重启容器
func (l *LXDProvider) forceRestartContainer(instanceName string) error {
	global.APP_LOG.Info("强制重启容器", zap.String("instanceName", instanceName))

	// 强制停止容器
	stopCmd := fmt.Sprintf("lxc stop %s --force --timeout=30", instanceName)
	_, err := l.sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Error("强制停止容器失败",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		return fmt.Errorf("强制停止容器失败: %w", err)
	}

	// 短暂等待
	time.Sleep(3 * time.Second)

	// 启动容器
	startCmd := fmt.Sprintf("lxc start %s", instanceName)
	_, err = l.sshClient.Execute(startCmd)
	if err != nil {
		return fmt.Errorf("启动容器失败: %w", err)
	}

	// 等待容器网络就绪
	return l.waitForContainerNetworkReady(instanceName)
}

// waitForVMNetworkReady 等待虚拟机网络就绪
func (l *LXDProvider) waitForVMNetworkReady(instanceName string) error {
	global.APP_LOG.Info("等待虚拟机网络就绪", zap.String("instanceName", instanceName))

	maxRetries := 8 // 增加重试次数
	delay := 15     // 虚拟机需要更长的启动时间

	for attempt := 1; attempt <= maxRetries; attempt++ {
		global.APP_LOG.Info("等待虚拟机启动并获取IP地址",
			zap.String("instanceName", instanceName),
			zap.Int("attempt", attempt),
			zap.Int("maxRetries", maxRetries),
			zap.Int("delay", delay))

		time.Sleep(time.Duration(delay) * time.Second)

		// 检查虚拟机状态
		statusCmd := fmt.Sprintf("lxc info %s | grep \"Status:\" | awk '{print $2}'", instanceName)
		output, err := l.sshClient.Execute(statusCmd)
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
		if _, err := l.getInstanceIP(instanceName); err == nil {
			global.APP_LOG.Info("虚拟机网络已就绪",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", attempt))
			return nil
		}

		// 逐渐增加等待时间
		if attempt < maxRetries {
			delay = l.min(delay+5, 25)
		}
	}

	return fmt.Errorf("虚拟机网络就绪超时，已等待 %d 次", maxRetries)
}

// waitForContainerNetworkReady 等待容器网络就绪
func (l *LXDProvider) waitForContainerNetworkReady(instanceName string) error {
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
		statusCmd := fmt.Sprintf("lxc info %s | grep \"Status:\" | awk '{print $2}'", instanceName)
		output, err := l.sshClient.Execute(statusCmd)
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
		if _, err := l.getInstanceIP(instanceName); err == nil {
			global.APP_LOG.Info("容器网络已就绪",
				zap.String("instanceName", instanceName),
				zap.Int("attempt", attempt))
			return nil
		}

		// 逐渐增加等待时间
		if attempt < maxRetries {
			delay = l.min(delay+2, 15) // 最大等待15秒
		}
	}

	return fmt.Errorf("容器网络就绪超时，已等待 %d 次", maxRetries)
}

// getInstanceType 获取实例类型
func (l *LXDProvider) getInstanceType(instanceName string) (string, error) {
	cmd := fmt.Sprintf("lxc info %s | grep \"Type:\" | awk '{print $2}'", instanceName)
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取实例类型失败: %w", err)
	}

	instanceType := strings.TrimSpace(output)
	global.APP_LOG.Debug("检测到实例类型",
		zap.String("instanceName", instanceName),
		zap.String("type", instanceType))

	return instanceType, nil
}

// getInstanceIP 获取实例IP地址
func (l *LXDProvider) getInstanceIP(instanceName string) (string, error) {
	// 检查实例类型以决定获取IP的策略
	instanceType, err := l.getInstanceType(instanceName)
	if err != nil {
		global.APP_LOG.Warn("无法检测实例类型，使用通用IP获取方式",
			zap.String("instanceName", instanceName),
			zap.Error(err))
		return l.getInstanceIPGeneric(instanceName)
	}

	// 根据实例类型选择不同的IP获取方式
	if instanceType == "virtual-machine" {
		return l.getVMInstanceIP(instanceName)
	} else {
		return l.getContainerInstanceIP(instanceName)
	}
}

// getVMInstanceIP 获取虚拟机实例IP地址
func (l *LXDProvider) getVMInstanceIP(instanceName string) (string, error) {
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
			cmd := fmt.Sprintf("lxc list %s --format json | jq -r '.[0].state.network.%s.addresses[]? | select(.family==\"inet\") | .address' 2>/dev/null", instanceName, iface)
			output, err := l.sshClient.Execute(cmd)

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
	return l.getInstanceIPGeneric(instanceName)
}

// getContainerInstanceIP 获取容器实例IP地址
func (l *LXDProvider) getContainerInstanceIP(instanceName string) (string, error) {
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
		cmd := fmt.Sprintf("lxc list %s --format json | jq -r '.[0].state.network.eth0.addresses[]? | select(.family==\"inet\") | .address' 2>/dev/null", instanceName)
		output, err := l.sshClient.Execute(cmd)

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
	return l.getInstanceIPGeneric(instanceName)
}

// getInstanceIPGeneric 通用IP获取方法（作为后备方案）
func (l *LXDProvider) getInstanceIPGeneric(instanceName string) (string, error) {
	global.APP_LOG.Debug("使用通用方法获取IP地址", zap.String("instanceName", instanceName))

	// 首先尝试使用 lxc list 简单格式获取IP
	cmd := fmt.Sprintf("lxc list %s -c 4 --format csv", instanceName)
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取实例信息失败: %w", err)
	}

	global.APP_LOG.Debug("lxc list原始输出",
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
					global.APP_LOG.Debug("找到有效IP地址",
						zap.String("instanceName", instanceName),
						zap.String("ip", addr))
					return addr, nil
				}
			}
		}
	}

	return "", fmt.Errorf("未找到实例IP地址")
}

// getHostIP 获取主机IP地址
func (l *LXDProvider) getHostIP() (string, error) {
	// 1. 优先使用配置的 PortIP（端口映射专用IP）
	if l.config.PortIP != "" {
		global.APP_LOG.Debug("使用配置的PortIP作为端口映射地址",
			zap.String("portIP", l.config.PortIP))
		return l.config.PortIP, nil
	}

	// 2. 如果 PortIP 为空，尝试从 Host 提取或解析 IP
	if l.config.Host != "" {
		// 检查 Host 是否已经是 IP 地址
		if net.ParseIP(l.config.Host) != nil {
			global.APP_LOG.Debug("SSH连接地址是IP，直接用于端口映射",
				zap.String("host", l.config.Host))
			return l.config.Host, nil
		}

		// Host 是域名，尝试解析为 IP
		global.APP_LOG.Debug("SSH连接地址是域名，尝试解析",
			zap.String("domain", l.config.Host))
		ips, err := net.LookupIP(l.config.Host)
		if err == nil && len(ips) > 0 {
			for _, ip := range ips {
				if ipv4 := ip.To4(); ipv4 != nil {
					global.APP_LOG.Debug("域名解析成功，使用解析的IP",
						zap.String("domain", l.config.Host),
						zap.String("resolvedIP", ipv4.String()))
					return ipv4.String(), nil
				}
			}
		} else if err != nil {
			global.APP_LOG.Warn("域名解析失败，回退到宿主机IP获取",
				zap.String("domain", l.config.Host),
				zap.Error(err))
		}
	}

	// 3. 最后才从宿主机动态获取 IP地址
	global.APP_LOG.Info("从宿主机动态获取IP地址")
	cmd := "ip addr show | awk '/inet .*global/ && !/inet6/ {print $2}' | sed -n '1p' | cut -d/ -f1"
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取主机IP失败: %w", err)
	}

	hostIP := strings.TrimSpace(output)
	if hostIP == "" {
		return "", fmt.Errorf("主机IP地址为空")
	}

	global.APP_LOG.Info("从宿主机获取到IP地址",
		zap.String("hostIP", hostIP))
	return hostIP, nil
}

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

// parseNetworkConfigFromInstanceConfig 从实例配置中解析网络配置
func (l *LXDProvider) parseNetworkConfigFromInstanceConfig(config provider.InstanceConfig) NetworkConfig {
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
	defaultInSpeed, defaultOutSpeed, err := l.getBandwidthFromProvider(userLevel)
	if err != nil {
		global.APP_LOG.Warn("获取Provider带宽配置失败，使用硬编码默认值", zap.Error(err))
		defaultInSpeed = 300 // 降级到硬编码默认值
		defaultOutSpeed = 300
	}

	// 获取Provider配置信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.Where("name = ?", l.config.Name).First(&providerInfo).Error; err != nil {
		global.APP_LOG.Warn("无法获取Provider配置，使用默认值",
			zap.String("provider", l.config.Name),
			zap.Error(err))
	}

	// 设置默认的IPv4和IPv6端口映射方法（如果Provider配置为空则使用默认值）
	ipv4Method := providerInfo.IPv4PortMappingMethod
	if ipv4Method == "" {
		ipv4Method = "device_proxy" // LXD默认使用device_proxy
	}

	ipv6Method := providerInfo.IPv6PortMappingMethod
	if ipv6Method == "" {
		ipv6Method = "device_proxy" // LXD默认使用device_proxy
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

	// 定义网络类型相关变量
	hasIPv6 := networkType == "nat_ipv4_ipv6" || networkType == "dedicated_ipv4_ipv6" || networkType == "ipv6_only"

	global.APP_LOG.Debug("初始化网络配置（从Provider读取网络配置）",
		zap.String("instanceName", config.Name),
		zap.String("networkType", networkType),
		zap.Bool("providerEnableIPv6", hasIPv6),
		zap.String("providerIPv6PortMappingMethod", networkConfig.IPv6PortMappingMethod),
		zap.String("providerIPv4PortMappingMethod", networkConfig.IPv4PortMappingMethod))

	global.APP_LOG.Info("从Provider配置读取网络设置",
		zap.String("provider", l.config.Name),
		zap.Bool("enableIPv6", hasIPv6),
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
			global.APP_LOG.Debug("从Metadata中发现enable_ipv6配置，但IPv6配置以Provider为准",
				zap.String("instanceName", config.Name),
				zap.String("metadata_enable_ipv6", enableIPv6),
				zap.Bool("provider_enable_ipv6", hasIPv6))

			global.APP_LOG.Info("IPv6配置以Provider为准，忽略实例Metadata配置",
				zap.String("instanceName", config.Name),
				zap.String("metadata_value", enableIPv6),
				zap.Bool("final_enable_ipv6", hasIPv6))
		} else {
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
	} else {
		global.APP_LOG.Debug("实例配置中没有Metadata",
			zap.String("instanceName", config.Name))
	}

	// 输出最终的网络配置结果
	global.APP_LOG.Info("LXD网络配置解析完成",
		zap.String("instanceName", config.Name),
		zap.Int("sshPort", networkConfig.SSHPort),
		zap.Int("inSpeed", networkConfig.InSpeed),
		zap.Int("outSpeed", networkConfig.OutSpeed),
		zap.Bool("enableIPv6", hasIPv6),
		zap.String("ipv4PortMappingMethod", networkConfig.IPv4PortMappingMethod),
		zap.String("ipv6PortMappingMethod", networkConfig.IPv6PortMappingMethod))

	return networkConfig
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

// GetInstanceIPv4 获取实例的内网IPv4地址
func (l *LXDProvider) GetInstanceIPv4(ctx context.Context, instanceName string) (string, error) {
	// 复用已有的getInstanceIP方法来获取内网IPv4地址
	return l.getInstanceIP(instanceName)
}

// GetVethInterfaceName 获取容器对应的宿主机veth接口名称（IPv4）
// 通过 lxc config show 获取 volatile.eth0.host_name
func (l *LXDProvider) GetVethInterfaceName(instanceName string) (string, error) {
	cmd := fmt.Sprintf("lxc config show %s | grep 'volatile.eth0.host_name:' | awk '{print $2}'", instanceName)
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取veth接口名称失败: %w", err)
	}

	vethName := strings.TrimSpace(output)
	if vethName == "" {
		return "", fmt.Errorf("未找到veth接口名称")
	}

	global.APP_LOG.Debug("获取到veth接口名称",
		zap.String("instanceName", instanceName),
		zap.String("vethInterface", vethName))

	return vethName, nil
}

// GetVethInterfaceNameV6 获取容器对应的宿主机veth接口名称（IPv6）
// 通过 lxc config show 获取 volatile.eth1.host_name（如果存在）
func (l *LXDProvider) GetVethInterfaceNameV6(instanceName string) (string, error) {
	cmd := fmt.Sprintf("lxc config show %s | grep 'volatile.eth1.host_name:' | awk '{print $2}'", instanceName)
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取veth接口名称(IPv6)失败: %w", err)
	}

	vethName := strings.TrimSpace(output)
	if vethName == "" {
		// 如果没有eth1，可能使用eth0，返回eth0的veth接口
		return l.GetVethInterfaceName(instanceName)
	}

	global.APP_LOG.Debug("获取到veth接口名称(IPv6)",
		zap.String("instanceName", instanceName),
		zap.String("vethInterface", vethName))

	return vethName, nil
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

	status := strings.TrimSpace(output)
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

	// 尝试配置端口映射（如果失败只记录警告，不中断流程）
	if err := l.configurePortMappingsWithIP(config.Name, networkConfig, instanceIP); err != nil {
		global.APP_LOG.Warn("配置端口映射失败，但继续",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	}

	// 尝试配置防火墙端口（如果失败只记录警告）
	if err := l.configureFirewallPorts(config.Name); err != nil {
		global.APP_LOG.Warn("配置防火墙端口失败，但继续",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	}

	return nil
}
