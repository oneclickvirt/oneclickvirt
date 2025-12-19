package proxmox

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/provider"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// IPv6Info IPv6配置信息
type IPv6Info struct {
	HostIPv6Address      string // 主机IPv6地址
	IPv6AddressPrefix    string // IPv6地址前缀
	IPv6PrefixLen        string // IPv6前缀长度
	IPv6Gateway          string // IPv6网关
	HasAppendedAddresses bool   // 是否存在额外的IPv6地址
}

// configureInstanceIPv6 配置实例IPv6网络
func (p *ProxmoxProvider) configureInstanceIPv6(ctx context.Context, vmid int, config provider.InstanceConfig, instanceType string) error {
	// 解析网络配置
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	global.APP_LOG.Info("开始配置实例IPv6网络",
		zap.Int("vmid", vmid),
		zap.String("instance", config.Name),
		zap.String("type", instanceType),
		zap.String("networkType", networkConfig.NetworkType))

	// 检查是否需要配置IPv6
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" ||
		networkConfig.NetworkType == "dedicated_ipv4_ipv6" ||
		networkConfig.NetworkType == "ipv6_only"

	if !hasIPv6 {
		global.APP_LOG.Info("网络类型不包含IPv6，跳过IPv6配置",
			zap.Int("vmid", vmid),
			zap.String("networkType", networkConfig.NetworkType))
		return nil
	}

	// 检查IPv6环境和配置
	if err := p.checkIPv6Environment(ctx); err != nil {
		// IPv6环境检查失败，如果是ipv6_only模式则返回错误，否则记录警告
		if networkConfig.NetworkType == "ipv6_only" {
			return fmt.Errorf("IPv6环境检查失败（ipv6_only模式要求IPv6环境）: %w", err)
		}
		global.APP_LOG.Warn("IPv6环境检查失败，跳过IPv6配置", zap.Error(err))
		return nil
	}

	// 获取IPv6基础信息
	ipv6Info, err := p.getIPv6Info(ctx)
	if err != nil {
		if networkConfig.NetworkType == "ipv6_only" {
			return fmt.Errorf("获取IPv6信息失败（ipv6_only模式要求IPv6信息）: %w", err)
		}
		global.APP_LOG.Warn("获取IPv6信息失败，跳过IPv6配置", zap.Error(err))
		return nil
	}

	// 根据网络类型配置IPv6
	switch networkConfig.NetworkType {
	case "nat_ipv4_ipv6":
		// NAT模式的IPv4+IPv6
		return p.configureIPv6Network(ctx, vmid, config, instanceType, ipv6Info, false)
	case "dedicated_ipv4_ipv6":
		// 独立的IPv4+IPv6
		return p.configureIPv6Network(ctx, vmid, config, instanceType, ipv6Info, false)
	case "ipv6_only":
		// 纯IPv6模式
		return p.configureIPv6Network(ctx, vmid, config, instanceType, ipv6Info, true)
	}

	return nil
}

// checkIPv6Environment 检查IPv6环境
func (p *ProxmoxProvider) checkIPv6Environment(ctx context.Context) error {
	appendedFile := "/usr/local/bin/pve_appended_content.txt"

	// 检查是否有appended_content文件
	checkCmd := fmt.Sprintf("[ -s '%s' ]", appendedFile)
	_, err := p.sshClient.Execute(checkCmd)

	if err != nil {
		// 如果没有appended_content文件，检查基础IPv6环境
		if err := p.checkBasicIPv6Environment(ctx); err != nil {
			return err
		}
	} else {
		global.APP_LOG.Info("检测到额外的IPv6地址用于NAT映射")
	}

	return nil
}

// checkBasicIPv6Environment 检查基础IPv6环境
func (p *ProxmoxProvider) checkBasicIPv6Environment(ctx context.Context) error {
	// 首先检查宿主机是否有公网IPv6地址
	checkHostIPv6Cmd := "ip -6 addr show | grep 'inet6.*global' | head -n 1"
	output, err := p.sshClient.Execute(checkHostIPv6Cmd)
	if err != nil || strings.TrimSpace(output) == "" {
		global.APP_LOG.Warn("宿主机没有公网IPv6地址",
			zap.String("provider", p.config.Name),
			zap.Error(err))
		return fmt.Errorf("宿主机没有公网IPv6地址，无法开设带IPv6的服务")
	}

	global.APP_LOG.Info("宿主机IPv6地址检查通过",
		zap.String("provider", p.config.Name),
		zap.String("ipv6Info", strings.TrimSpace(output)))

	// 检查IPv6地址文件是否存在
	checkIPv6Cmd := "[ -f /usr/local/bin/pve_check_ipv6 ]"
	_, err = p.sshClient.Execute(checkIPv6Cmd)
	if err != nil {
		return fmt.Errorf("没有IPv6地址用于开设带独立IPv6地址的服务")
	}

	// 检查vmbr2网桥是否存在
	checkVmbrCmd := "grep -q 'vmbr2' /etc/network/interfaces"
	_, err = p.sshClient.Execute(checkVmbrCmd)
	if err != nil {
		return fmt.Errorf("没有vmbr2网桥用于开设带独立IPv6地址的服务")
	}

	// 检查ndpresponder服务状态
	checkServiceCmd := "systemctl is-active ndpresponder.service"
	output, err = p.sshClient.Execute(checkServiceCmd)
	if err != nil || strings.TrimSpace(output) != "active" {
		return fmt.Errorf("ndpresponder服务状态异常，无法开设带独立IPv6地址的服务")
	}

	global.APP_LOG.Info("ndpresponder服务运行正常，可以开设带独立IPv6地址的服务")
	return nil
}

// getIPv6Info 获取IPv6配置信息
func (p *ProxmoxProvider) getIPv6Info(ctx context.Context) (*IPv6Info, error) {
	info := &IPv6Info{}

	// 检查是否存在额外的IPv6地址
	appendedFile := "/usr/local/bin/pve_appended_content.txt"
	checkCmd := fmt.Sprintf("[ -s '%s' ]", appendedFile)
	_, err := p.sshClient.Execute(checkCmd)
	info.HasAppendedAddresses = (err == nil)

	// 获取主机IPv6地址
	if _, err := p.sshClient.Execute("[ -f /usr/local/bin/pve_check_ipv6 ]"); err == nil {
		output, err := p.sshClient.Execute("cat /usr/local/bin/pve_check_ipv6")
		if err == nil {
			info.HostIPv6Address = strings.TrimSpace(output)
			// 生成IPv6地址前缀
			if info.HostIPv6Address != "" {
				parts := strings.Split(info.HostIPv6Address, ":")
				if len(parts) > 1 {
					info.IPv6AddressPrefix = strings.Join(parts[:len(parts)-1], ":") + ":"
				}
			}
		}
	}

	// 获取IPv6前缀长度
	if _, err := p.sshClient.Execute("[ -f /usr/local/bin/pve_ipv6_prefixlen ]"); err == nil {
		output, err := p.sshClient.Execute("cat /usr/local/bin/pve_ipv6_prefixlen")
		if err == nil {
			info.IPv6PrefixLen = strings.TrimSpace(output)
		}
	}

	// 获取IPv6网关
	if _, err := p.sshClient.Execute("[ -f /usr/local/bin/pve_ipv6_gateway ]"); err == nil {
		output, err := p.sshClient.Execute("cat /usr/local/bin/pve_ipv6_gateway")
		if err == nil {
			info.IPv6Gateway = strings.TrimSpace(output)
		}
	}

	return info, nil
}

// configureIPv6Network 配置IPv6网络（合并NAT和直接映射逻辑）
func (p *ProxmoxProvider) configureIPv6Network(ctx context.Context, vmid int, config provider.InstanceConfig, instanceType string, ipv6Info *IPv6Info, ipv6Only bool) error {
	// 选择网桥和配置模式
	var bridgeName string
	var useNATMapping bool

	if ipv6Info.HasAppendedAddresses {
		// 有额外IPv6地址，使用NAT映射模式
		bridgeName = "vmbr1"
		useNATMapping = true
	} else {
		// 使用直接分配模式
		bridgeName = "vmbr2"
		useNATMapping = false
	}

	global.APP_LOG.Info("配置IPv6网络",
		zap.Int("vmid", vmid),
		zap.String("instanceType", instanceType),
		zap.String("bridge", bridgeName),
		zap.Bool("useNAT", useNATMapping),
		zap.Bool("ipv6Only", ipv6Only))

	if instanceType == "vm" {
		return p.configureVMIPv6(ctx, vmid, config, bridgeName, useNATMapping, ipv6Info, ipv6Only)
	} else {
		return p.configureContainerIPv6(ctx, vmid, config, bridgeName, useNATMapping, ipv6Info, ipv6Only)
	}
}

// configureVMIPv6 配置虚拟机IPv6
func (p *ProxmoxProvider) configureVMIPv6(ctx context.Context, vmid int, config provider.InstanceConfig, bridgeName string, useNATMapping bool, ipv6Info *IPv6Info, ipv6Only bool) error {
	// 获取网络配置以应用带宽限制
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	if useNATMapping {
		// NAT映射模式
		vmInternalIPv6 := fmt.Sprintf("2001:db8:1::%d", vmid)

		if ipv6Only {
			// IPv6-only: net0为IPv6
			net0ConfigBase := fmt.Sprintf("virtio,bridge=%s,firewall=0", bridgeName)
			net0Config := net0ConfigBase

			if networkConfig.OutSpeed > 0 {
				// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
				rateMBps := networkConfig.OutSpeed / 8
				if rateMBps < 1 {
					rateMBps = 1 // 最小1MB/s
				}
				net0Config = fmt.Sprintf("%s,rate=%d", net0ConfigBase, rateMBps)
			}

			net0Cmd := fmt.Sprintf("qm set %d --net0 %s", vmid, net0Config)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil && networkConfig.OutSpeed > 0 {
				// 带rate失败，尝试不带rate
				global.APP_LOG.Warn("配置虚拟机IPv6-only net0接口（带rate）失败，尝试不带rate",
					zap.Int("vmid", vmid),
					zap.Error(err))

				net0Cmd = fmt.Sprintf("qm set %d --net0 %s", vmid, net0ConfigBase)
				_, err = p.sshClient.Execute(net0Cmd)
				if err != nil {
					global.APP_LOG.Warn("配置虚拟机IPv6-only net0接口失败", zap.Int("vmid", vmid), zap.Error(err))
				}
			} else if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6-only net0接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig0 ip6='%s/64',gw6='2001:db8:1::1'", vmid, vmInternalIPv6)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// IPv4+IPv6: net1为IPv6
			// net1 不需要 rate 限制，因为 rate 已在 net0 上配置（Proxmox 的 rate 是整体VM/CT级别的限制）
			netCmd := fmt.Sprintf("qm set %d --net1 virtio,bridge=%s,firewall=0", vmid, bridgeName)
			_, err := p.sshClient.Execute(netCmd)
			if err != nil {
				global.APP_LOG.Warn("添加虚拟机net1接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig1 ip6='%s/64',gw6='2001:db8:1::1'", vmid, vmInternalIPv6)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}

		// 获取可用的外部IPv6地址并设置NAT映射
		hostExternalIPv6, err := p.getAvailableVmbr1IPv6(ctx)
		if err != nil {
			return fmt.Errorf("没有可用的IPv6地址用于NAT映射: %w", err)
		}

		return p.setupNATMapping(ctx, vmInternalIPv6, hostExternalIPv6)

	} else {
		// 直接分配模式
		vmExternalIPv6 := fmt.Sprintf("%s%d", ipv6Info.IPv6AddressPrefix, vmid)

		if ipv6Only {
			// IPv6-only: net0为IPv6
			net0ConfigBase := fmt.Sprintf("virtio,bridge=%s,firewall=0", bridgeName)
			net0Config := net0ConfigBase

			if networkConfig.OutSpeed > 0 {
				// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
				rateMBps := networkConfig.OutSpeed / 8
				if rateMBps < 1 {
					rateMBps = 1 // 最小1MB/s
				}
				net0Config = fmt.Sprintf("%s,rate=%d", net0ConfigBase, rateMBps)
			}

			net0Cmd := fmt.Sprintf("qm set %d --net0 %s", vmid, net0Config)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil && networkConfig.OutSpeed > 0 {
				// 带rate失败，尝试不带rate
				global.APP_LOG.Warn("配置虚拟机IPv6-only net0接口（带rate）失败，尝试不带rate",
					zap.Int("vmid", vmid),
					zap.Error(err))

				net0Cmd = fmt.Sprintf("qm set %d --net0 %s", vmid, net0ConfigBase)
				_, err = p.sshClient.Execute(net0Cmd)
				if err != nil {
					global.APP_LOG.Warn("配置虚拟机IPv6-only net0接口失败", zap.Int("vmid", vmid), zap.Error(err))
				}
			} else if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6-only net0接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig0 ip6='%s/128',gw6='%s'", vmid, vmExternalIPv6, ipv6Info.HostIPv6Address)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// IPv4+IPv6: net1为IPv6
			// net1 不需要 rate 限制，因为 rate 已在 net0 上配置（Proxmox 的 rate 是整体VM/CT级别的限制）
			netCmd := fmt.Sprintf("qm set %d --net1 virtio,bridge=%s,firewall=0", vmid, bridgeName)
			_, err := p.sshClient.Execute(netCmd)
			if err != nil {
				global.APP_LOG.Warn("添加虚拟机net1接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig1 ip6='%s/128',gw6='%s'", vmid, vmExternalIPv6, ipv6Info.HostIPv6Address)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}
	}

	return nil
}

// configureContainerIPv6 配置容器IPv6
func (p *ProxmoxProvider) configureContainerIPv6(ctx context.Context, vmid int, config provider.InstanceConfig, bridgeName string, useNATMapping bool, ipv6Info *IPv6Info, ipv6Only bool) error {
	// 获取网络配置以应用带宽限制
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	if useNATMapping {
		// NAT映射模式
		vmInternalIPv6 := fmt.Sprintf("2001:db8:1::%d", vmid)

		if ipv6Only {
			// IPv6-only: net0为IPv6
			net0ConfigStr := fmt.Sprintf("name=eth0,ip6='%s/64',bridge=%s,gw6='2001:db8:1::1'", vmInternalIPv6, bridgeName)
			if networkConfig.OutSpeed > 0 {
				// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
				rateMBps := networkConfig.OutSpeed / 8
				if rateMBps < 1 {
					rateMBps = 1 // 最小1MB/s
				}
				net0ConfigStr = fmt.Sprintf("%s,rate=%d", net0ConfigStr, rateMBps)
			}
			net0Cmd := fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigStr)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6-only接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// IPv4+IPv6: net0为IPv4，net1为IPv6
			userIP := VMIDToInternalIP(vmid)
			net0ConfigStr := fmt.Sprintf("name=eth0,ip=%s/24,bridge=vmbr1,gw=%s", userIP, InternalGateway)
			if networkConfig.OutSpeed > 0 {
				// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
				rateMBps := networkConfig.OutSpeed / 8
				if rateMBps < 1 {
					rateMBps = 1 // 最小1MB/s
				}
				net0ConfigStr = fmt.Sprintf("%s,rate=%d", net0ConfigStr, rateMBps)
			}
			net0Cmd := fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigStr)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv4接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			// net1 不需要 rate 限制，因为 rate 已在 net0 上配置
			net1Cmd := fmt.Sprintf("pct set %d --net1 name=eth1,ip6='%s/64',bridge=%s,gw6='2001:db8:1::1'", vmid, vmInternalIPv6, bridgeName)
			_, err = p.sshClient.Execute(net1Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}

		// 配置DNS
		var dnsCmd string
		if ipv6Only {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		} else {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '8.8.8.8 8.8.4.4 2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		}
		_, err := p.sshClient.Execute(dnsCmd)
		if err != nil {
			global.APP_LOG.Warn("配置容器DNS失败", zap.Int("vmid", vmid), zap.Error(err))
		}

		// 获取可用的外部IPv6地址并设置NAT映射
		hostExternalIPv6, err := p.getAvailableVmbr1IPv6(ctx)
		if err != nil {
			return fmt.Errorf("没有可用的IPv6地址用于NAT映射: %w", err)
		}

		return p.setupNATMapping(ctx, vmInternalIPv6, hostExternalIPv6)

	} else {
		// 直接分配模式
		vmExternalIPv6 := fmt.Sprintf("%s%d", ipv6Info.IPv6AddressPrefix, vmid)

		if ipv6Only {
			// IPv6-only: net0为IPv6
			net0ConfigStr := fmt.Sprintf("name=eth0,ip6='%s/128',bridge=%s,gw6='%s'", vmExternalIPv6, bridgeName, ipv6Info.HostIPv6Address)
			if networkConfig.OutSpeed > 0 {
				// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
				rateMBps := networkConfig.OutSpeed / 8
				if rateMBps < 1 {
					rateMBps = 1 // 最小1MB/s
				}
				net0ConfigStr = fmt.Sprintf("%s,rate=%d", net0ConfigStr, rateMBps)
			}
			net0Cmd := fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigStr)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6-only接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// IPv4+IPv6: net0为IPv4，net1为IPv6
			// 使用VMID到IP的映射函数
			userIP := VMIDToInternalIP(vmid)
			net0ConfigStr := fmt.Sprintf("name=eth0,ip=%s/24,bridge=vmbr1,gw=%s", userIP, InternalGateway)
			if networkConfig.OutSpeed > 0 {
				// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
				rateMBps := networkConfig.OutSpeed / 8
				if rateMBps < 1 {
					rateMBps = 1 // 最小1MB/s
				}
				net0ConfigStr = fmt.Sprintf("%s,rate=%d", net0ConfigStr, rateMBps)
			}
			net0Cmd := fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigStr)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv4接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			// net1 不需要 rate 限制，因为 rate 已在 net0 上配置
			net1Cmd := fmt.Sprintf("pct set %d --net1 name=eth1,ip6='%s/128',bridge=%s,gw6='%s'", vmid, vmExternalIPv6, bridgeName, ipv6Info.HostIPv6Address)
			_, err = p.sshClient.Execute(net1Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}

		// 配置DNS
		var dnsCmd string
		if ipv6Only {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		} else {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '8.8.8.8 8.8.4.4 2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		}
		_, err := p.sshClient.Execute(dnsCmd)
		if err != nil {
			global.APP_LOG.Warn("配置容器DNS失败", zap.Int("vmid", vmid), zap.Error(err))
		}
	}

	return nil
}

// getAvailableVmbr1IPv6 获取可用的vmbr1 IPv6地址
func (p *ProxmoxProvider) getAvailableVmbr1IPv6(ctx context.Context) (string, error) {
	appendedFile := "/usr/local/bin/pve_appended_content.txt"
	usedIPsFile := "/usr/local/bin/pve_used_vmbr1_ips.txt"

	// 读取可用的IPv6地址
	output, err := p.sshClient.Execute(fmt.Sprintf("cat '%s' 2>/dev/null || true", appendedFile))
	if err != nil || strings.TrimSpace(output) == "" {
		return "", fmt.Errorf("没有可用的IPv6地址")
	}

	availableIPs := strings.Split(strings.TrimSpace(output), "\n")

	// 读取已使用的IPv6地址
	usedOutput, _ := p.sshClient.Execute(fmt.Sprintf("cat '%s' 2>/dev/null || true", usedIPsFile))
	usedIPs := make(map[string]bool)
	if usedOutput != "" {
		for _, ip := range strings.Split(strings.TrimSpace(usedOutput), "\n") {
			usedIPs[strings.TrimSpace(ip)] = true
		}
	}

	// 查找第一个可用的IPv6地址
	for _, ip := range availableIPs {
		ip = strings.TrimSpace(ip)
		if ip != "" && !usedIPs[ip] {
			// 标记为已使用
			_, err := p.sshClient.Execute(fmt.Sprintf("echo '%s' >> '%s'", ip, usedIPsFile))
			if err != nil {
				global.APP_LOG.Warn("标记IPv6地址为已使用失败", zap.String("ip", ip), zap.Error(err))
			}
			return ip, nil
		}
	}

	return "", fmt.Errorf("没有可用的IPv6地址")
}

// setupNATMapping 设置IPv6 NAT映射
func (p *ProxmoxProvider) setupNATMapping(ctx context.Context, vmInternalIPv6, hostExternalIPv6 string) error {
	rulesFile := "/usr/local/bin/ipv6_nat_rules.sh"

	// 确保规则文件存在
	_, err := p.sshClient.Execute(fmt.Sprintf("touch '%s'", rulesFile))
	if err != nil {
		return fmt.Errorf("创建IPv6 NAT规则文件失败: %w", err)
	}

	// ip6tables规则
	dnatRule := fmt.Sprintf("ip6tables -t nat -A PREROUTING -d '%s' -j DNAT --to-destination '%s'", hostExternalIPv6, vmInternalIPv6)
	snatRule := fmt.Sprintf("ip6tables -t nat -A POSTROUTING -s '%s' -j SNAT --to-source '%s'", vmInternalIPv6, hostExternalIPv6)

	// 执行规则
	_, err = p.sshClient.Execute(dnatRule)
	if err != nil {
		global.APP_LOG.Warn("添加IPv6 DNAT规则失败", zap.Error(err))
	}

	_, err = p.sshClient.Execute(snatRule)
	if err != nil {
		global.APP_LOG.Warn("添加IPv6 SNAT规则失败", zap.Error(err))
	}

	// 将规则写入文件以便持久化
	rulesContent := fmt.Sprintf("%s\n%s\n", dnatRule, snatRule)
	_, err = p.sshClient.Execute(fmt.Sprintf("echo '%s' >> '%s'", rulesContent, rulesFile))
	if err != nil {
		global.APP_LOG.Warn("保存IPv6 NAT规则到文件失败", zap.Error(err))
	}

	// 重启相关服务
	_, _ = p.sshClient.Execute("systemctl daemon-reload")
	_, _ = p.sshClient.Execute("systemctl restart ipv6nat.service 2>/dev/null || true")

	global.APP_LOG.Info("IPv6 NAT映射规则配置完成",
		zap.String("internal", vmInternalIPv6),
		zap.String("external", hostExternalIPv6))

	return nil
}

// GetInstanceIPv6 获取实例的内网IPv6地址 (公开方法)
func (p *ProxmoxProvider) GetInstanceIPv6(ctx context.Context, instanceName string) (string, error) {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, instanceName)
	if err != nil {
		return "", fmt.Errorf("failed to find instance %s: %w", instanceName, err)
	}

	return p.getInstanceIPv6ByVMID(ctx, vmid, instanceType)
}

// GetInstancePublicIPv6 获取实例的公网IPv6地址
func (p *ProxmoxProvider) GetInstancePublicIPv6(ctx context.Context, instanceName string) (string, error) {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, instanceName)
	if err != nil {
		return "", fmt.Errorf("failed to find instance %s: %w", instanceName, err)
	}

	// 尝试从保存的IPv6文件中读取公网IPv6地址
	publicIPv6Cmd := fmt.Sprintf("cat %s_v6 2>/dev/null | tail -1", instanceName)
	publicIPv6Output, err := p.sshClient.Execute(publicIPv6Cmd)
	if err == nil {
		publicIPv6 := strings.TrimSpace(publicIPv6Output)
		if publicIPv6 != "" && !p.isPrivateIPv6(publicIPv6) {
			global.APP_LOG.Info("从文件获取到公网IPv6地址",
				zap.String("instanceName", instanceName),
				zap.String("publicIPv6", publicIPv6))
			return publicIPv6, nil
		}
	}

	// 如果文件中没有，尝试获取实例配置中的IPv6地址
	return p.getInstancePublicIPv6ByVMID(ctx, vmid, instanceType)
}

// getInstanceIPv6ByVMID 根据VMID获取实例内网IPv6地址
func (p *ProxmoxProvider) getInstanceIPv6ByVMID(ctx context.Context, vmid string, instanceType string) (string, error) {
	var cmd string

	if instanceType == "container" {
		// 对于容器，尝试从配置中获取IPv6地址
		// 支持 net0, net1 等多个网络接口的IPv6配置
		cmd = fmt.Sprintf("pct config %s | grep -E 'net[0-9]+:.*ip6=' | sed -n 's/.*ip6=\\([^/,[:space:]]*\\).*/\\1/p' | head -1", vmid)
		output, err := p.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) != "" {
			ipv6 := strings.TrimSpace(output)
			if ipv6 != "auto" && ipv6 != "dhcp" {
				return ipv6, nil
			}
		}

		// 如果没有静态IPv6，尝试从容器内部获取
		cmd = fmt.Sprintf("pct exec %s -- ip -6 addr show | grep 'inet6.*global' | awk '{print $2}' | cut -d'/' -f1 | head -1 || true", vmid)
	} else {
		// 对于虚拟机，尝试从配置中获取IPv6地址
		// 支持 ipconfig0, ipconfig1 等多个网络接口的IPv6配置
		cmd = fmt.Sprintf("qm config %s | grep -E 'ipconfig[0-9]+:.*ip6=' | sed -n 's/.*ip6=\\([^/,[:space:]]*\\).*/\\1/p' | head -1", vmid)
		output, err := p.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) != "" {
			ipv6 := strings.TrimSpace(output)
			if ipv6 != "auto" && ipv6 != "dhcp" {
				return ipv6, nil
			}
		}

		// 如果没有静态IPv6配置，尝试通过guest agent获取IPv6
		cmd = fmt.Sprintf("qm guest cmd %s network-get-interfaces 2>/dev/null | grep -o '\"ip-address\":[[:space:]]*\"[^\"]*:' | sed 's/.*\"\\([^\"]*\\)\".*/\\1/' | head -1 || true", vmid)
		output, err = p.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) != "" {
			return strings.TrimSpace(output), nil
		}

		// 最后尝试从虚拟机内部获取IPv6地址
		cmd = fmt.Sprintf("qm guest exec %s -- ip -6 addr show | grep 'inet6.*global' | awk '{print $2}' | cut -d'/' -f1 | head -1 2>/dev/null || true", vmid)
	}

	output, err := p.sshClient.Execute(cmd)
	if err != nil {
		return "", err
	}

	ipv6 := strings.TrimSpace(output)
	if ipv6 == "" {
		return "", fmt.Errorf("no IPv6 address found for %s %s", instanceType, vmid)
	}

	return ipv6, nil
}

// getInstancePublicIPv6ByVMID 根据VMID获取实例公网IPv6地址
func (p *ProxmoxProvider) getInstancePublicIPv6ByVMID(ctx context.Context, vmid string, instanceType string) (string, error) {
	// 首先尝试直接从配置中获取IPv6地址（通常这就是公网IPv6地址）
	ipv6Address, err := p.getInstanceIPv6ByVMID(ctx, vmid, instanceType)
	if err == nil && ipv6Address != "" && !p.isPrivateIPv6(ipv6Address) {
		// 如果获取到的IPv6地址不是私有地址，则认为它是公网地址
		return ipv6Address, nil
	}

	// 获取IPv6信息进行进一步判断
	ipv6Info, err := p.getIPv6Info(ctx)
	if err != nil {
		return "", fmt.Errorf("获取IPv6信息失败: %w", err)
	}

	if ipv6Info.HasAppendedAddresses {
		// NAT映射模式，从映射文件中查找外部IPv6地址
		return p.getNATMappedIPv6(ctx, vmid)
	} else {
		// 直接分配模式，优先返回从配置中获取的IPv6地址
		if ipv6Address != "" {
			return ipv6Address, nil
		}

		// 如果配置中没有，尝试计算外部IPv6地址
		vmidInt, err := strconv.Atoi(vmid)
		if err == nil && vmidInt > 0 && ipv6Info.IPv6AddressPrefix != "" {
			publicIPv6 := fmt.Sprintf("%s%d", ipv6Info.IPv6AddressPrefix, vmidInt)
			return publicIPv6, nil
		}
	}

	return "", fmt.Errorf("无法获取实例公网IPv6地址")
}

// getNATMappedIPv6 获取NAT映射的外部IPv6地址
func (p *ProxmoxProvider) getNATMappedIPv6(ctx context.Context, vmid string) (string, error) {
	// 从IPv6 NAT规则文件中查找映射
	cmd := fmt.Sprintf("grep -E 'DNAT.*2001:db8:1::%s' /usr/local/bin/ipv6_nat_rules.sh 2>/dev/null | grep -oP '\\-d\\s+\\K[^\\s]+' | head -1 || true", vmid)
	output, err := p.sshClient.Execute(cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		return strings.TrimSpace(output), nil
	}

	// 如果没有找到，从ip6tables规则中查找
	cmd = fmt.Sprintf("ip6tables -t nat -L PREROUTING -n | grep 'DNAT.*2001:db8:1::%s' | awk '{print $4}' | head -1 || true", vmid)
	output, err = p.sshClient.Execute(cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		return strings.TrimSpace(output), nil
	}

	return "", fmt.Errorf("未找到IPv6 NAT映射")
}
