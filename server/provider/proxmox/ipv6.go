package proxmox

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// IPv6Info IPv6配置信息
type IPv6Info struct {
	HostIPv6Address      string // 主机IPv6地址
	IPv6AddressPrefix    string // 规范化后的网络地址（兼容旧日志/调用方）
	IPv6PrefixLen        string // IPv6前缀长度
	IPv6Gateway          string // IPv6网关
	HasAppendedAddresses bool   // 是否存在额外的IPv6地址
	Network              utils.IPv6Network
}

type proxmoxIPv6Mode struct {
	Info          *IPv6Info
	BridgeName    string
	UseNATMapping bool
}

func cleanIPv6Value(raw string) string {
	network, err := utils.ParseFirstIPv6NetworkOutput(raw, 128)
	if err != nil {
		return ""
	}
	return network.Address.String()
}

func hasProxmoxIPv6(networkType string) bool {
	return networkType == "nat_ipv4_ipv6" ||
		networkType == "dedicated_ipv4_ipv6" ||
		networkType == "ipv6_only"
}

func hasDirectProxmoxIPv6Info(info *IPv6Info) bool {
	if info == nil {
		return false
	}
	return strings.TrimSpace(info.HostIPv6Address) != "" && info.Network.Address != nil &&
		info.Network.PrefixLen >= 0 && info.Network.PrefixLen <= 128
}

func requestedProxmoxIPv6(config provider.InstanceConfig) string {
	if config.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(config.Metadata["static_ipv6"])
}

func (p *ProxmoxProvider) addressForVMID(info *IPv6Info, vmid int) (string, error) {
	if info == nil || info.Network.Address == nil {
		return "", fmt.Errorf("IPv6网络信息不可用")
	}
	if vmid < 0 {
		return "", fmt.Errorf("无效的VMID")
	}
	return utils.IPv6AddressWithSuffix(info.Network, uint64(vmid))
}

func (p *ProxmoxProvider) resolveProxmoxIPv6Mode(ctx context.Context) (*proxmoxIPv6Mode, error) {
	info, err := p.getIPv6Info(ctx)
	if err != nil {
		return nil, err
	}

	if info.HasAppendedAddresses {
		bridgeName := p.getBridgeName("nat")
		if strings.TrimSpace(bridgeName) == "" {
			return nil, fmt.Errorf("IPv6 NAT 模式需要 NAT 网桥")
		}
		return &proxmoxIPv6Mode{
			Info:          info,
			BridgeName:    bridgeName,
			UseNATMapping: true,
		}, nil
	}

	bridgeName := p.getBridgeName("dedicated_v6")
	if strings.TrimSpace(bridgeName) == "" {
		return nil, fmt.Errorf("独立 IPv6 模式需要独立 IPv6 网桥")
	}
	if !hasDirectProxmoxIPv6Info(info) {
		return nil, fmt.Errorf("独立 IPv6 模式缺少宿主机 IPv6 地址或地址前缀")
	}
	return &proxmoxIPv6Mode{
		Info:          info,
		BridgeName:    bridgeName,
		UseNATMapping: false,
	}, nil
}

func (p *ProxmoxProvider) resolveProxmoxIPv6ModeForCreate(ctx context.Context) (*proxmoxIPv6Mode, error) {
	if err := p.checkIPv6Environment(ctx); err != nil {
		return nil, err
	}
	return p.resolveProxmoxIPv6Mode(ctx)
}

// configureInstanceIPv6 配置实例IPv6网络
func (p *ProxmoxProvider) configureInstanceIPv6(ctx context.Context, vmid int, config provider.InstanceConfig, instanceType string) error {
	// 解析网络配置
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	global.APP_LOG.Debug("开始配置实例IPv6网络",
		zap.Int("vmid", vmid),
		zap.String("instance", config.Name),
		zap.String("type", instanceType),
		zap.String("networkType", networkConfig.NetworkType))

	// 检查是否需要配置IPv6
	hasIPv6 := hasProxmoxIPv6(networkConfig.NetworkType)

	if !hasIPv6 {
		if requestedProxmoxIPv6(config) != "" {
			return fmt.Errorf("已分配静态IPv6，但实例网络类型 %s 未启用IPv6", networkConfig.NetworkType)
		}
		global.APP_LOG.Debug("网络类型不包含IPv6，跳过IPv6配置",
			zap.Int("vmid", vmid),
			zap.String("networkType", networkConfig.NetworkType))
		return nil
	}

	// 检查IPv6环境和配置
	if err := p.checkIPv6Environment(ctx); err != nil {
		// IPv6环境检查失败，如果是ipv6_only模式则返回错误，否则记录警告
		if networkConfig.NetworkType == "ipv6_only" || requestedProxmoxIPv6(config) != "" {
			return fmt.Errorf("IPv6环境检查失败（ipv6_only模式要求IPv6环境）: %w", err)
		}
		global.APP_LOG.Warn("IPv6环境检查失败，跳过IPv6配置", zap.Error(err))
		return nil
	}

	// 获取IPv6基础信息，并按 oneclickvirt/pve 脚本约定选择模式：
	// 有 pve_appended_content.txt → vmbr1/NAT IPv6；否则 → vmbr2/独立 IPv6。
	ipv6Mode, err := p.resolveProxmoxIPv6Mode(ctx)
	if err != nil {
		if networkConfig.NetworkType == "ipv6_only" || requestedProxmoxIPv6(config) != "" {
			return fmt.Errorf("获取IPv6信息失败（ipv6_only模式要求IPv6信息）: %w", err)
		}
		global.APP_LOG.Warn("获取IPv6信息失败，跳过IPv6配置", zap.Error(err))
		return nil
	}

	// 根据网络类型配置IPv6
	switch networkConfig.NetworkType {
	case "nat_ipv4_ipv6":
		// NAT模式的IPv4+IPv6
		return p.configureIPv6Network(ctx, vmid, config, instanceType, ipv6Mode, false)
	case "dedicated_ipv4_ipv6":
		// 独立的IPv4+IPv6
		return p.configureIPv6Network(ctx, vmid, config, instanceType, ipv6Mode, false)
	case "ipv6_only":
		// 纯IPv6模式
		return p.configureIPv6Network(ctx, vmid, config, instanceType, ipv6Mode, true)
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
		global.APP_LOG.Debug("检测到额外的IPv6地址用于NAT映射")
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

	global.APP_LOG.Debug("宿主机IPv6地址检查通过",
		zap.String("provider", p.config.Name),
		zap.String("ipv6Info", strings.TrimSpace(output)))

	// 检查IPv6地址文件是否存在
	checkIPv6Cmd := "[ -f /usr/local/bin/pve_check_ipv6 ]"
	_, err = p.sshClient.Execute(checkIPv6Cmd)
	if err != nil {
		return fmt.Errorf("没有IPv6地址用于开设带独立IPv6地址的服务")
	}

	// 检查配置的IPv6网桥是否存在
	v6Bridge := p.getBridgeName("dedicated_v6")
	if v6Bridge == "" {
		return fmt.Errorf("没有配置单独IPv6网桥，无法开设带独立IPv6地址的服务")
	}
	checkVmbrCmd := fmt.Sprintf("ip link show '%s' >/dev/null 2>&1 || grep -Rqs '^iface[[:space:]]\\+%s\\b\\|^auto[[:space:]]\\+%s\\b' /etc/network/interfaces /etc/network/interfaces.d 2>/dev/null", v6Bridge, v6Bridge, v6Bridge)
	_, err = p.sshClient.Execute(checkVmbrCmd)
	if err != nil {
		return fmt.Errorf("没有%s网桥用于开设带独立IPv6地址的服务", v6Bridge)
	}

	// 检查ndpresponder服务状态
	checkServiceCmd := "systemctl is-active ndpresponder.service"
	output, err = p.sshClient.Execute(checkServiceCmd)
	if err != nil || strings.TrimSpace(output) != "active" {
		return fmt.Errorf("ndpresponder服务状态异常，无法开设带独立IPv6地址的服务")
	}

	global.APP_LOG.Debug("ndpresponder服务运行正常，可以开设带独立IPv6地址的服务")
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
			info.HostIPv6Address = cleanIPv6Value(output)
			if network, parseErr := utils.ParseFirstIPv6NetworkOutput(output, 64); parseErr == nil {
				info.Network = network
				info.HostIPv6Address = network.Address.String()
				info.IPv6AddressPrefix = network.NetworkAddress().String()
				info.IPv6PrefixLen = strconv.Itoa(network.PrefixLen)
			}
		}
	}

	// 获取IPv6前缀长度
	if _, err := p.sshClient.Execute("[ -f /usr/local/bin/pve_ipv6_prefixlen ]"); err == nil {
		output, err := p.sshClient.Execute("cat /usr/local/bin/pve_ipv6_prefixlen")
		if err == nil {
			parsed, parseErr := utils.ParseFirstIPv6PrefixLengthOutput(output)
			if parseErr == nil {
				info.IPv6PrefixLen = strconv.Itoa(parsed)
				if info.HostIPv6Address != "" {
					if network, networkErr := utils.ParseIPv6Network(info.HostIPv6Address+"/"+info.IPv6PrefixLen, 64); networkErr == nil {
						info.Network = network
						info.IPv6AddressPrefix = network.NetworkAddress().String()
					}
				}
			}
		}
	}

	// 获取IPv6网关
	if _, err := p.sshClient.Execute("[ -f /usr/local/bin/pve_ipv6_gateway ]"); err == nil {
		output, err := p.sshClient.Execute("cat /usr/local/bin/pve_ipv6_gateway")
		if err == nil {
			info.IPv6Gateway = cleanIPv6Value(output)
		}
	}

	return info, nil
}

// configureIPv6Network 配置IPv6网络（合并NAT和直接映射逻辑）
func (p *ProxmoxProvider) configureIPv6Network(ctx context.Context, vmid int, config provider.InstanceConfig, instanceType string, ipv6Mode *proxmoxIPv6Mode, ipv6Only bool) error {
	if ipv6Mode == nil || ipv6Mode.Info == nil {
		return fmt.Errorf("IPv6模式未解析")
	}
	bridgeName := ipv6Mode.BridgeName
	useNATMapping := ipv6Mode.UseNATMapping
	ipv6Info := ipv6Mode.Info
	if strings.TrimSpace(bridgeName) == "" {
		return fmt.Errorf("IPv6网桥未配置")
	}
	if !useNATMapping && !hasDirectProxmoxIPv6Info(ipv6Info) {
		return fmt.Errorf("缺少宿主机IPv6地址或地址前缀，无法配置独立IPv6")
	}

	global.APP_LOG.Debug("配置IPv6网络",
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

func (p *ProxmoxProvider) executeIPv6NetworkCommand(primaryCommand, fallbackCommand, description string) error {
	if _, err := p.sshClient.Execute(primaryCommand); err == nil {
		return nil
	} else if fallbackCommand == "" || fallbackCommand == primaryCommand {
		return fmt.Errorf("%s: %w", description, err)
	} else {
		global.APP_LOG.Warn(description+"（带rate）失败，尝试不带rate的配置", zap.Error(err))
		if _, fallbackErr := p.sshClient.Execute(fallbackCommand); fallbackErr != nil {
			return fmt.Errorf("%s（带rate失败: %v，无rate回退也失败）: %w", description, err, fallbackErr)
		}
		return nil
	}
}

// configureVMIPv6 配置虚拟机IPv6
func (p *ProxmoxProvider) configureVMIPv6(ctx context.Context, vmid int, config provider.InstanceConfig, bridgeName string, useNATMapping bool, ipv6Info *IPv6Info, ipv6Only bool) error {
	// 获取网络配置以应用带宽限制
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)
	var err error

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
			fallbackCmd := ""
			if networkConfig.OutSpeed > 0 {
				fallbackCmd = fmt.Sprintf("qm set %d --net0 %s", vmid, net0ConfigBase)
			}
			if err := p.executeIPv6NetworkCommand(net0Cmd, fallbackCmd, "配置虚拟机IPv6-only net0接口失败"); err != nil {
				return err
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig0 ip6='%s/64',gw6='2001:db8:1::1'", vmid, vmInternalIPv6)
			if err := p.executeIPv6NetworkCommand(ipv6Cmd, "", "配置虚拟机IPv6 cloud-init失败"); err != nil {
				return err
			}
		} else {
			// IPv4+IPv6: net1为IPv6
			// net1 不需要 rate 限制，因为 rate 已在 net0 上配置（Proxmox 的 rate 是整体VM/CT级别的限制）
			netCmd := fmt.Sprintf("qm set %d --net1 virtio,bridge=%s,firewall=0", vmid, bridgeName)
			if err := p.executeIPv6NetworkCommand(netCmd, "", "添加虚拟机IPv6 net1接口失败"); err != nil {
				return err
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig1 ip6='%s/64',gw6='2001:db8:1::1'", vmid, vmInternalIPv6)
			if err := p.executeIPv6NetworkCommand(ipv6Cmd, "", "配置虚拟机IPv6 cloud-init失败"); err != nil {
				return err
			}
		}

		// 使用控制面预分配的地址时，不再从硬编码的 vmbr1 文件轮转。
		hostExternalIPv6 := requestedProxmoxIPv6(config)
		if hostExternalIPv6 != "" {
			hostExternalIPv6, err = utils.NormalizeIPv6Address(hostExternalIPv6)
			if err != nil {
				return fmt.Errorf("静态IPv6地址无效: %w", err)
			}
		} else {
			hostExternalIPv6, err = p.getAvailableVmbr1IPv6(ctx)
			if err != nil {
				return fmt.Errorf("没有可用的IPv6地址用于NAT映射: %w", err)
			}
		}

		return p.setupNATMapping(ctx, vmInternalIPv6, hostExternalIPv6)

	} else {
		// 直接分配模式
		vmExternalIPv6 := requestedProxmoxIPv6(config)
		if vmExternalIPv6 != "" {
			vmExternalIPv6, err = utils.NormalizeIPv6Address(vmExternalIPv6)
			if err != nil {
				return fmt.Errorf("静态IPv6地址无效: %w", err)
			}
		} else {
			vmExternalIPv6, err = p.addressForVMID(ipv6Info, vmid)
			if err != nil {
				return fmt.Errorf("根据IPv6前缀生成实例地址失败: %w", err)
			}
		}

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
			fallbackCmd := ""
			if networkConfig.OutSpeed > 0 {
				fallbackCmd = fmt.Sprintf("qm set %d --net0 %s", vmid, net0ConfigBase)
			}
			if err := p.executeIPv6NetworkCommand(net0Cmd, fallbackCmd, "配置虚拟机IPv6-only net0接口失败"); err != nil {
				return err
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig0 ip6='%s/128',gw6='%s'", vmid, vmExternalIPv6, ipv6Info.HostIPv6Address)
			if err := p.executeIPv6NetworkCommand(ipv6Cmd, "", "配置虚拟机IPv6 cloud-init失败"); err != nil {
				return err
			}
		} else {
			// IPv4+IPv6: net1为IPv6
			// net1 不需要 rate 限制，因为 rate 已在 net0 上配置（Proxmox 的 rate 是整体VM/CT级别的限制）
			netCmd := fmt.Sprintf("qm set %d --net1 virtio,bridge=%s,firewall=0", vmid, bridgeName)
			if err := p.executeIPv6NetworkCommand(netCmd, "", "添加虚拟机IPv6 net1接口失败"); err != nil {
				return err
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig1 ip6='%s/128',gw6='%s'", vmid, vmExternalIPv6, ipv6Info.HostIPv6Address)
			if err := p.executeIPv6NetworkCommand(ipv6Cmd, "", "配置虚拟机IPv6 cloud-init失败"); err != nil {
				return err
			}
		}
	}

	return nil
}

// configureContainerIPv6 配置容器IPv6
func (p *ProxmoxProvider) configureContainerIPv6(ctx context.Context, vmid int, config provider.InstanceConfig, bridgeName string, useNATMapping bool, ipv6Info *IPv6Info, ipv6Only bool) error {
	// 获取网络配置以应用带宽限制
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)
	var err error

	if useNATMapping {
		// NAT映射模式
		vmInternalIPv6 := fmt.Sprintf("2001:db8:1::%d", vmid)

		if ipv6Only {
			// IPv6-only: net0为IPv6
			net0ConfigBase := fmt.Sprintf("name=eth0,ip6='%s/64',bridge=%s,gw6='2001:db8:1::1'", vmInternalIPv6, bridgeName)
			net0ConfigStr := net0ConfigBase
			if networkConfig.OutSpeed > 0 {
				// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
				rateMBps := networkConfig.OutSpeed / 8
				if rateMBps < 1 {
					rateMBps = 1 // 最小1MB/s
				}
				net0ConfigStr = fmt.Sprintf("%s,rate=%d", net0ConfigStr, rateMBps)
			}
			net0Cmd := fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigStr)
			fallbackCmd := ""
			if networkConfig.OutSpeed > 0 {
				fallbackCmd = fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigBase)
			}
			if err := p.executeIPv6NetworkCommand(net0Cmd, fallbackCmd, "配置容器IPv6-only接口失败"); err != nil {
				return err
			}
		} else {
			// IPv4+IPv6: net0为IPv4，net1为IPv6
			userIP := p.vmidToInternalIP(vmid)
			net0ConfigBase := fmt.Sprintf("name=eth0,ip=%s/24,bridge=%s,gw=%s", userIP, p.getBridgeName("nat"), p.getInternalGateway())
			net0ConfigStr := net0ConfigBase
			if networkConfig.OutSpeed > 0 {
				// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
				rateMBps := networkConfig.OutSpeed / 8
				if rateMBps < 1 {
					rateMBps = 1 // 最小1MB/s
				}
				net0ConfigStr = fmt.Sprintf("%s,rate=%d", net0ConfigStr, rateMBps)
			}
			net0Cmd := fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigStr)
			fallbackCmd := ""
			if networkConfig.OutSpeed > 0 {
				fallbackCmd = fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigBase)
			}
			if err := p.executeIPv6NetworkCommand(net0Cmd, fallbackCmd, "配置容器IPv4 net0接口失败"); err != nil {
				return err
			}

			// net1 不需要 rate 限制，因为 rate 已在 net0 上配置
			net1Cmd := fmt.Sprintf("pct set %d --net1 name=eth1,ip6='%s/64',bridge=%s,gw6='2001:db8:1::1'", vmid, vmInternalIPv6, bridgeName)
			if err := p.executeIPv6NetworkCommand(net1Cmd, "", "配置容器IPv6 net1接口失败"); err != nil {
				return err
			}
		}

		// 配置DNS
		var dnsCmd string
		if ipv6Only {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		} else {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '8.8.8.8 8.8.4.4 2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		}
		if err := p.executeIPv6NetworkCommand(dnsCmd, "", "配置容器IPv6 DNS失败"); err != nil {
			return err
		}

		hostExternalIPv6 := requestedProxmoxIPv6(config)
		if hostExternalIPv6 != "" {
			hostExternalIPv6, err = utils.NormalizeIPv6Address(hostExternalIPv6)
			if err != nil {
				return fmt.Errorf("静态IPv6地址无效: %w", err)
			}
		} else {
			hostExternalIPv6, err = p.getAvailableVmbr1IPv6(ctx)
			if err != nil {
				return fmt.Errorf("没有可用的IPv6地址用于NAT映射: %w", err)
			}
		}

		return p.setupNATMapping(ctx, vmInternalIPv6, hostExternalIPv6)

	} else {
		// 直接分配模式
		vmExternalIPv6 := requestedProxmoxIPv6(config)
		if vmExternalIPv6 != "" {
			vmExternalIPv6, err = utils.NormalizeIPv6Address(vmExternalIPv6)
			if err != nil {
				return fmt.Errorf("静态IPv6地址无效: %w", err)
			}
		} else {
			vmExternalIPv6, err = p.addressForVMID(ipv6Info, vmid)
			if err != nil {
				return fmt.Errorf("根据IPv6前缀生成实例地址失败: %w", err)
			}
		}

		if ipv6Only {
			// IPv6-only: net0为IPv6
			net0ConfigBase := fmt.Sprintf("name=eth0,ip6='%s/128',bridge=%s,gw6='%s'", vmExternalIPv6, bridgeName, ipv6Info.HostIPv6Address)
			net0ConfigStr := net0ConfigBase
			if networkConfig.OutSpeed > 0 {
				// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
				rateMBps := networkConfig.OutSpeed / 8
				if rateMBps < 1 {
					rateMBps = 1 // 最小1MB/s
				}
				net0ConfigStr = fmt.Sprintf("%s,rate=%d", net0ConfigStr, rateMBps)
			}
			net0Cmd := fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigStr)
			fallbackCmd := ""
			if networkConfig.OutSpeed > 0 {
				fallbackCmd = fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigBase)
			}
			if err := p.executeIPv6NetworkCommand(net0Cmd, fallbackCmd, "配置容器IPv6-only接口失败"); err != nil {
				return err
			}
		} else {
			// IPv4+IPv6: net0为IPv4，net1为IPv6
			// 使用VMID到IP的映射函数
			userIP := p.vmidToInternalIP(vmid)
			net0ConfigBase := fmt.Sprintf("name=eth0,ip=%s/24,bridge=%s,gw=%s", userIP, p.getBridgeName("nat"), p.getInternalGateway())
			net0ConfigStr := net0ConfigBase
			if networkConfig.OutSpeed > 0 {
				// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
				rateMBps := networkConfig.OutSpeed / 8
				if rateMBps < 1 {
					rateMBps = 1 // 最小1MB/s
				}
				net0ConfigStr = fmt.Sprintf("%s,rate=%d", net0ConfigStr, rateMBps)
			}
			net0Cmd := fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigStr)
			fallbackCmd := ""
			if networkConfig.OutSpeed > 0 {
				fallbackCmd = fmt.Sprintf("pct set %d --net0 %s", vmid, net0ConfigBase)
			}
			if err := p.executeIPv6NetworkCommand(net0Cmd, fallbackCmd, "配置容器IPv4 net0接口失败"); err != nil {
				return err
			}

			// net1 不需要 rate 限制，因为 rate 已在 net0 上配置
			net1Cmd := fmt.Sprintf("pct set %d --net1 name=eth1,ip6='%s/128',bridge=%s,gw6='%s'", vmid, vmExternalIPv6, bridgeName, ipv6Info.HostIPv6Address)
			if err := p.executeIPv6NetworkCommand(net1Cmd, "", "配置容器IPv6 net1接口失败"); err != nil {
				return err
			}
		}

		// 配置DNS
		var dnsCmd string
		if ipv6Only {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		} else {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '8.8.8.8 8.8.4.4 2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		}
		if err := p.executeIPv6NetworkCommand(dnsCmd, "", "配置容器IPv6 DNS失败"); err != nil {
			return err
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

	// This is a persisted allocation file, not a noisy probe. Validate every
	// non-empty line so a corrupted pool cannot be silently treated as valid.
	availableNetworks, parseErr := utils.ParseIPv6NetworkLines(output, 128)
	if parseErr != nil {
		return "", fmt.Errorf("IPv6地址文件包含无效内容: %w", parseErr)
	}

	// 读取已使用的IPv6地址
	usedOutput, usedErr := p.sshClient.Execute(fmt.Sprintf("cat '%s' 2>/dev/null || true", usedIPsFile))
	if usedErr != nil {
		return "", fmt.Errorf("读取已使用IPv6地址失败: %w", usedErr)
	}
	usedIPs := make(map[string]bool)
	if strings.TrimSpace(usedOutput) != "" {
		usedNetworks, usedParseErr := utils.ParseIPv6NetworkLines(usedOutput, 128)
		if usedParseErr != nil {
			return "", fmt.Errorf("已使用IPv6地址文件包含无效内容: %w", usedParseErr)
		}
		for _, network := range usedNetworks {
			usedIPs[network.Address.String()] = true
		}
	}

	// 查找第一个可用的IPv6地址
	for _, network := range availableNetworks {
		ip := network.Address.String()
		if !usedIPs[ip] {
			// 标记为已使用
			_, err := p.sshClient.Execute(fmt.Sprintf("printf '%%s\\n' %s >> %s", utils.ShellSingleQuote(ip), utils.ShellSingleQuote(usedIPsFile)))
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
	vmInternalIPv6, err := utils.NormalizeIPv6Address(vmInternalIPv6)
	if err != nil {
		return fmt.Errorf("内部IPv6地址无效: %w", err)
	}
	hostExternalIPv6, err = utils.NormalizeIPv6Address(hostExternalIPv6)
	if err != nil {
		return fmt.Errorf("外部IPv6地址无效: %w", err)
	}

	// 确保规则文件存在
	_, err = p.sshClient.Execute(fmt.Sprintf("touch %s", utils.ShellSingleQuote(rulesFile)))
	if err != nil {
		return fmt.Errorf("创建IPv6 NAT规则文件失败: %w", err)
	}

	quotedInternal := utils.ShellSingleQuote(vmInternalIPv6)
	quotedExternal := utils.ShellSingleQuote(hostExternalIPv6)
	dnatSpec := fmt.Sprintf("PREROUTING -d %s -j DNAT --to-destination %s", quotedExternal, quotedInternal)
	snatSpec := fmt.Sprintf("POSTROUTING -s %s -j SNAT --to-source %s", quotedInternal, quotedExternal)
	dnatRule := "ip6tables -t nat -A " + dnatSpec
	snatRule := "ip6tables -t nat -A " + snatSpec

	_, err = p.sshClient.Execute("ip6tables -t nat -C " + dnatSpec + " 2>/dev/null || " + dnatRule)
	if err != nil {
		return fmt.Errorf("添加IPv6 DNAT规则失败: %w", err)
	}

	_, err = p.sshClient.Execute("ip6tables -t nat -C " + snatSpec + " 2>/dev/null || " + snatRule)
	if err != nil {
		return fmt.Errorf("添加IPv6 SNAT规则失败: %w", err)
	}

	quotedRulesFile := utils.ShellSingleQuote(rulesFile)
	for _, rule := range []string{dnatRule, snatRule} {
		quotedRule := utils.ShellSingleQuote(rule)
		persistCommand := fmt.Sprintf("grep -Fqx -- %s %s || printf '%%s\\n' %s >> %s", quotedRule, quotedRulesFile, quotedRule, quotedRulesFile)
		if _, persistErr := p.sshClient.Execute(persistCommand); persistErr != nil {
			return fmt.Errorf("保存IPv6 NAT规则到文件失败: %w", persistErr)
		}
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
	publicIPv6Cmd := fmt.Sprintf("cat %s 2>/dev/null", utils.ShellSingleQuote(instanceName+"_v6"))
	publicIPv6Output, err := p.sshClient.Execute(publicIPv6Cmd)
	if err == nil {
		publicIPv6, parseErr := utils.ParseFirstIPv6AddressOutput(publicIPv6Output)
		if parseErr == nil && !p.isPrivateIPv6(publicIPv6) {
			global.APP_LOG.Debug("从文件获取到公网IPv6地址",
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
		if err == nil {
			if ipv6, parseErr := utils.ParseFirstIPv6AddressOutput(output); parseErr == nil {
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
		if err == nil {
			if ipv6, parseErr := utils.ParseFirstIPv6AddressOutput(output); parseErr == nil {
				return ipv6, nil
			}
		}

		// 如果没有静态IPv6配置，尝试通过guest agent获取IPv6
		cmd = fmt.Sprintf("qm guest cmd %s network-get-interfaces 2>/dev/null | grep -o '\"ip-address\":[[:space:]]*\"[^\"]*:' | sed 's/.*\"\\([^\"]*\\)\".*/\\1/' | head -1 || true", vmid)
		output, err = p.sshClient.Execute(cmd)
		if err == nil {
			if ipv6, parseErr := utils.ParseFirstIPv6AddressOutput(output); parseErr == nil {
				return ipv6, nil
			}
		}

		// 最后尝试从虚拟机内部获取IPv6地址
		cmd = fmt.Sprintf("qm guest exec %s -- ip -6 addr show | grep 'inet6.*global' | awk '{print $2}' | cut -d'/' -f1 | head -1 2>/dev/null || true", vmid)
	}

	output, err := p.sshClient.Execute(cmd)
	if err != nil {
		return "", err
	}

	ipv6, parseErr := utils.ParseFirstIPv6AddressOutput(output)
	if parseErr != nil {
		return "", fmt.Errorf("no valid IPv6 address found for %s %s: %w", instanceType, vmid, parseErr)
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
		if err == nil && vmidInt > 0 {
			if publicIPv6, addressErr := p.addressForVMID(ipv6Info, vmidInt); addressErr == nil {
				return publicIPv6, nil
			}
		}
	}

	return "", fmt.Errorf("无法获取实例公网IPv6地址")
}

// getNATMappedIPv6 获取NAT映射的外部IPv6地址
func (p *ProxmoxProvider) getNATMappedIPv6(ctx context.Context, vmid string) (string, error) {
	// 从IPv6 NAT规则文件中查找映射
	cmd := fmt.Sprintf("grep -E 'DNAT.*2001:db8:1::%s' /usr/local/bin/ipv6_nat_rules.sh 2>/dev/null | grep -oP '\\-d\\s+\\K[^\\s]+' | head -1 || true", vmid)
	output, err := p.sshClient.Execute(cmd)
	if err == nil {
		if ipv6, parseErr := utils.ParseFirstIPv6AddressOutput(output); parseErr == nil {
			return ipv6, nil
		}
	}

	// 如果没有找到，从ip6tables规则中查找
	cmd = fmt.Sprintf("ip6tables -t nat -L PREROUTING -n | grep 'DNAT.*2001:db8:1::%s' | awk '{print $4}' | head -1 || true", vmid)
	output, err = p.sshClient.Execute(cmd)
	if err == nil {
		if ipv6, parseErr := utils.ParseFirstIPv6AddressOutput(output); parseErr == nil {
			return ipv6, nil
		}
	}

	return "", fmt.Errorf("未找到IPv6 NAT映射")
}
