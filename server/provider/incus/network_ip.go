package incus

import (
	"fmt"
	"net"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

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
				global.APP_LOG.Debug("虚拟机IPv4地址获取成功",
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
			global.APP_LOG.Debug("容器IPv4地址获取成功",
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
							global.APP_LOG.Debug("通过incus list找到有效IP地址",
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
	global.APP_LOG.Debug("从宿主机动态获取IP地址")
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
