package proxmox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/pmacct"
	"oneclickvirt/service/traffic"

	"go.uber.org/zap"
)

// configureInstanceNetwork 配置实例网络
func (p *ProxmoxProvider) configureInstanceNetwork(ctx context.Context, vmid int, config provider.InstanceConfig) error {
	// 根据实例类型配置网络
	if config.InstanceType == "container" {
		return p.configureContainerNetwork(ctx, vmid, config)
	} else {
		return p.configureVMNetwork(ctx, vmid, config)
	}
}

// configureContainerNetwork 配置容器网络
func (p *ProxmoxProvider) configureContainerNetwork(ctx context.Context, vmid int, config provider.InstanceConfig) error {
	// 解析网络配置
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	global.APP_LOG.Debug("配置容器网络",
		zap.Int("vmid", vmid),
		zap.String("networkType", networkConfig.NetworkType))

	// 对于独立IPv4模式，预先检查并确保该IPv4地址已绑定到宿主机网络接口
	if networkConfig.NetworkType == "dedicated_ipv4" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" {
		if config.Metadata != nil {
			if staticIPv4, ok := config.Metadata["static_ipv4"]; ok && staticIPv4 != "" {
				if err := p.ensureIPv4OnHostInterface(staticIPv4); err != nil {
					global.APP_LOG.Warn("独立IPv4宿主机接口绑定检查失败，继续执行",
						zap.Int("vmid", vmid),
						zap.String("ipv4", staticIPv4),
						zap.Error(err))
				}
			}
		}
	}

	// 检查是否包含IPv6
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" ||
		networkConfig.NetworkType == "dedicated_ipv4_ipv6" ||
		networkConfig.NetworkType == "ipv6_only"

	if hasIPv6 {
		// 配置IPv6网络（会根据NetworkType自动处理IPv4+IPv6或纯IPv6）
		if err := p.configureInstanceIPv6(ctx, vmid, config, "container"); err != nil {
			global.APP_LOG.Warn("配置容器IPv6失败，回退到IPv4-only", zap.Int("vmid", vmid), zap.Error(err))
			// IPv6配置失败，回退到IPv4-only配置
			hasIPv6 = false
		}
	}

	// 如果没有IPv6或IPv6配置失败，配置IPv4-only网络
	// 使用VMID到IP的映射函数
	if !hasIPv6 {
		userIP := VMIDToInternalIP(vmid)
		netCmd := fmt.Sprintf("pct set %d --net0 name=eth0,ip=%s/24,bridge=vmbr1,gw=%s", vmid, userIP, InternalGateway)
		_, err := p.sshClient.Execute(netCmd)
		if err != nil {
			return fmt.Errorf("配置容器IPv4网络失败: %w", err)
		}

		// 配置端口转发（只在IPv4模式下需要）
		if len(config.Ports) > 0 {
			p.configurePortForwarding(ctx, vmid, userIP, config.Ports)
		}
	}

	return nil
}

// configureVMNetwork 配置虚拟机网络
func (p *ProxmoxProvider) configureVMNetwork(ctx context.Context, vmid int, config provider.InstanceConfig) error {
	// 解析网络配置
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	global.APP_LOG.Debug("配置虚拟机网络",
		zap.Int("vmid", vmid),
		zap.String("networkType", networkConfig.NetworkType))

	// 对于独立IPv4模式，预先检查并确保该IPv4地址已绑定到宿主机网络接口
	if networkConfig.NetworkType == "dedicated_ipv4" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" {
		if config.Metadata != nil {
			if staticIPv4, ok := config.Metadata["static_ipv4"]; ok && staticIPv4 != "" {
				if err := p.ensureIPv4OnHostInterface(staticIPv4); err != nil {
					global.APP_LOG.Warn("独立IPv4宿主机接口绑定检查失败，继续执行",
						zap.Int("vmid", vmid),
						zap.String("ipv4", staticIPv4),
						zap.Error(err))
				}
			}
		}
	}

	// 检查是否包含IPv6
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" ||
		networkConfig.NetworkType == "dedicated_ipv4_ipv6" ||
		networkConfig.NetworkType == "ipv6_only"

	if hasIPv6 {
		// 配置IPv6网络（会根据NetworkType自动处理IPv4+IPv6或纯IPv6）
		if err := p.configureInstanceIPv6(ctx, vmid, config, "vm"); err != nil {
			global.APP_LOG.Warn("配置虚拟机IPv6失败，回退到IPv4-only", zap.Int("vmid", vmid), zap.Error(err))
			// IPv6配置失败，回退到IPv4-only配置
			hasIPv6 = false
		}
	}

	// 如果没有IPv6或IPv6配置失败，配置IPv4-only网络
	if !hasIPv6 {
		user_ip := fmt.Sprintf("172.16.1.%d", vmid)

		// 配置云初始化网络
		ipCmd := fmt.Sprintf("qm set %d --ipconfig0 ip=%s/24,gw=172.16.1.1", vmid, user_ip)
		_, err := p.sshClient.Execute(ipCmd)
		if err != nil {
			return fmt.Errorf("配置虚拟机IPv4网络失败: %w", err)
		}

		// 配置端口转发（只在IPv4模式下需要）
		if len(config.Ports) > 0 {
			p.configurePortForwarding(ctx, vmid, user_ip, config.Ports)
		}
	}

	return nil
}

// configurePortForwarding 配置端口转发
func (p *ProxmoxProvider) configurePortForwarding(ctx context.Context, vmid int, userIP string, ports []string) {
	for _, port := range ports {
		// 简单的端口字符串解析，假设格式为 "hostPort:containerPort"
		parts := strings.Split(port, ":")
		if len(parts) != 2 {
			continue
		}

		// iptables规则进行端口转发
		rule := fmt.Sprintf("iptables -t nat -A PREROUTING -i vmbr0 -p tcp --dport %s -j DNAT --to-destination %s:%s",
			parts[0], userIP, parts[1])

		_, err := p.sshClient.Execute(rule)
		if err != nil {
			global.APP_LOG.Warn("配置端口转发失败",
				zap.Int("vmid", vmid),
				zap.String("port", port),
				zap.Error(err))
		}
	}

	// 保存iptables规则
	_, err := p.sshClient.Execute("iptables-save > /etc/iptables/rules.v4")
	if err != nil {
		global.APP_LOG.Warn("保存iptables规则失败", zap.Error(err))
	}
}

// configureContainerSSH 配置容器SSH
func (p *ProxmoxProvider) configureContainerSSH(ctx context.Context, vmid int) {
	// 等待容器完全启动
	time.Sleep(3 * time.Second)

	global.APP_LOG.Debug("开始配置容器SSH", zap.Int("vmid", vmid))

	// 检测容器包管理器类型
	pkgManager := p.detectContainerPackageManager(vmid)
	global.APP_LOG.Debug("检测到容器包管理器", zap.Int("vmid", vmid), zap.String("packageManager", pkgManager))

	// 备份并配置DNS
	p.configureContainerDNS(vmid)

	// 根据包管理器类型配置SSH
	switch pkgManager {
	case "apk":
		p.configureAlpineSSH(vmid)
	case "opkg":
		p.configureOpenWrtSSH(vmid)
	case "pacman":
		p.configureArchSSH(vmid)
	case "apt-get", "apt":
		p.configureDebianBasedSSH(vmid)
	case "yum", "dnf":
		p.configureRHELBasedSSH(vmid)
	case "zypper":
		p.configureOpenSUSESSH(vmid)
	default:
		// 默认尝试Debian-based配置
		global.APP_LOG.Warn("未知的包管理器，尝试使用Debian-based配置", zap.Int("vmid", vmid), zap.String("packageManager", pkgManager))
		p.configureDebianBasedSSH(vmid)
	}

	global.APP_LOG.Debug("容器SSH配置完成", zap.Int("vmid", vmid), zap.String("packageManager", pkgManager))
}

// executeContainerCommands 执行容器命令的辅助函数
func (p *ProxmoxProvider) executeContainerCommands(vmid int, commands []string, osType string) {
	for _, cmd := range commands {
		fullCmd := fmt.Sprintf("pct exec %d -- %s", vmid, cmd)
		_, err := p.sshClient.Execute(fullCmd)
		if err != nil {
			global.APP_LOG.Warn("配置容器SSH命令失败",
				zap.Int("vmid", vmid),
				zap.String("osType", osType),
				zap.String("command", cmd),
				zap.Error(err))
		}
	}
}

// initializePmacctMonitoring 初始化pmacct流量监控
func (p *ProxmoxProvider) initializePmacctMonitoring(ctx context.Context, vmid int, instanceName string) error {
	// 首先检查实例状态，确保实例正在运行
	vmidStr := fmt.Sprintf("%d", vmid)

	// 查找实例类型
	_, instanceType, err := p.findVMIDByNameOrID(ctx, vmidStr)
	if err != nil {
		global.APP_LOG.Warn("查找实例类型失败，跳过pmacct初始化",
			zap.String("instance_name", instanceName),
			zap.Int("vmid", vmid),
			zap.Error(err))
		return err
	}

	// 检查实例状态
	var statusCmd string
	if instanceType == "container" {
		statusCmd = fmt.Sprintf("pct status %s", vmidStr)
	} else {
		statusCmd = fmt.Sprintf("qm status %s", vmidStr)
	}

	// 等待实例运行 - 最多等待30秒
	maxWaitTime := 30 * time.Second
	checkInterval := 6 * time.Second
	startTime := time.Now()
	isRunning := false

	for {
		if time.Since(startTime) > maxWaitTime {
			global.APP_LOG.Warn("等待实例运行超时，跳过pmacct初始化",
				zap.String("instance_name", instanceName),
				zap.Int("vmid", vmid))
			return fmt.Errorf("等待实例运行超时")
		}

		statusOutput, err := p.sshClient.Execute(statusCmd)
		if err == nil && strings.Contains(statusOutput, "status: running") {
			isRunning = true
			global.APP_LOG.Debug("实例已确认运行，准备初始化pmacct",
				zap.String("instance_name", instanceName),
				zap.Int("vmid", vmid),
				zap.Duration("wait_time", time.Since(startTime)))
			break
		}

		global.APP_LOG.Debug("等待实例启动以初始化pmacct",
			zap.Int("vmid", vmid),
			zap.Duration("elapsed", time.Since(startTime)))

		time.Sleep(checkInterval)
	}

	if !isRunning {
		global.APP_LOG.Warn("实例未运行，跳过pmacct初始化",
			zap.String("instance_name", instanceName),
			zap.Int("vmid", vmid))
		return fmt.Errorf("instance not running")
	}

	// 通过provider名称查找provider记录
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("查找provider记录失败，跳过pmacct初始化",
			zap.String("provider_name", p.config.Name),
			zap.Error(err))
		return err
	}

	// 查找实例ID用于pmacct初始化
	var instanceID uint
	var instance providerModel.Instance

	if err := global.APP_DB.Where("name = ? AND provider_id = ?", instanceName, providerRecord.ID).First(&instance).Error; err != nil {
		global.APP_LOG.Warn("查找实例记录失败，跳过pmacct初始化",
			zap.String("instance_name", instanceName),
			zap.Uint("provider_id", providerRecord.ID),
			zap.Error(err))
		return err
	}

	instanceID = instance.ID

	// 获取并更新实例的PrivateIP（确保pmacct配置使用正确的内网IP）
	ctx2, cancel2 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel2()
	if privateIP, err := p.GetInstanceIPv4(ctx2, instanceName); err == nil && privateIP != "" {
		// 更新数据库中的PrivateIP
		if err := global.APP_DB.Model(&instance).Update("private_ip", privateIP).Error; err == nil {
			global.APP_LOG.Debug("已更新Proxmox实例内网IP",
				zap.String("instanceName", instanceName),
				zap.String("privateIP", privateIP))
		}
	} else {
		global.APP_LOG.Warn("获取Proxmox实例内网IP失败，pmacct可能使用公网IP",
			zap.String("instanceName", instanceName),
			zap.Error(err))
	}

	// 获取并更新实例的IPv4网络接口（用于pmacct流量监控）
	// 使用pmacct Service的检测方法，保持一致性
	pmacctService := pmacct.NewService()
	if interfaceV4, err := pmacctService.DetectProxmoxNetworkInterface(p, instanceName, vmidStr); err == nil && interfaceV4 != "" {
		if err := global.APP_DB.Model(&instance).Update("pmacct_interface_v4", interfaceV4).Error; err == nil {
			global.APP_LOG.Debug("已更新Proxmox实例IPv4网络接口",
				zap.String("instanceName", instanceName),
				zap.String("interfaceV4", interfaceV4))
		}
	} else {
		global.APP_LOG.Debug("未获取到IPv4网络接口",
			zap.String("instanceName", instanceName),
			zap.Error(err))
	}

	// 获取并更新实例的IPv6网络接口（如果有IPv6的话）
	// 这里依赖于实例的public_ipv6字段已经在之前被设置
	ctx4, cancel4 := context.WithTimeout(ctx, 15*time.Second)
	defer cancel4()
	if interfaceV6, err := p.GetIPv6NetworkInterface(ctx4, instanceName); err == nil && interfaceV6 != "" {
		if err := global.APP_DB.Model(&instance).Update("pmacct_interface_v6", interfaceV6).Error; err == nil {
			global.APP_LOG.Debug("已更新Proxmox实例IPv6网络接口",
				zap.String("instanceName", instanceName),
				zap.String("interfaceV6", interfaceV6))
		}
	} else {
		global.APP_LOG.Debug("未获取到IPv6网络接口或实例无公网IPv6",
			zap.String("instanceName", instanceName))
	}

	// 通过provider名称查找provider记录以检查流量统计配置
	var providerRecordCheck providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecordCheck).Error; err != nil {
		global.APP_LOG.Warn("查找provider记录失败，跳过pmacct初始化",
			zap.String("provider_name", p.config.Name),
			zap.Error(err))
		return err
	}

	// 检查provider是否启用了流量统计
	if !providerRecordCheck.EnableTrafficControl {
		global.APP_LOG.Debug("Provider未启用流量统计，跳过Proxmox实例pmacct监控初始化",
			zap.String("providerName", p.config.Name),
			zap.String("instanceName", instanceName),
			zap.Int("vmid", vmid))
		return nil
	}

	// 初始化流量监控
	if pmacctErr := pmacctService.InitializePmacctForInstance(instanceID); pmacctErr != nil {
		global.APP_LOG.Warn("Proxmox实例创建后初始化 pmacct 监控失败",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instanceName),
			zap.Int("vmid", vmid),
			zap.Error(pmacctErr))
		return pmacctErr
	}

	global.APP_LOG.Info("Proxmox实例创建后 pmacct 监控初始化成功",
		zap.Uint("instanceId", instanceID),
		zap.String("instanceName", instanceName),
		zap.Int("vmid", vmid))

	// 触发流量数据同步
	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerInstanceTrafficSync(instanceID, "Proxmox实例创建后同步")

	return nil
}

// ensureIPv4OnHostInterface 确保独立 IPv4 地址已绑定到宿主机网络接口。
// 若尚未绑定，则自动将其以 /32 路由模式添加到宿主机主出口接口。
// 这是使用独立 IPv4（dedicated_ipv4 / dedicated_ipv4_ipv6）创建实例的前置条件检查。
func (p *ProxmoxProvider) ensureIPv4OnHostInterface(ipv4 string) error {
	if ipv4 == "" {
		return nil
	}

	// 清理 IP 地址格式（去除 CIDR 前缀、多余空格等）
	cleanIP := strings.TrimSpace(ipv4)
	if idx := strings.IndexByte(cleanIP, '/'); idx != -1 {
		cleanIP = cleanIP[:idx]
	}
	if cleanIP == "" {
		return nil
	}

	global.APP_LOG.Debug("检查独立IPv4是否已绑定到宿主机网络接口",
		zap.String("ip", cleanIP))

	// 检查该 IP 是否已绑定到宿主机的任意网络接口
	checkCmd := fmt.Sprintf("ip addr show | grep -w '%s'", cleanIP)
	output, err := p.sshClient.Execute(checkCmd)
	if err == nil && strings.Contains(output, cleanIP) {
		global.APP_LOG.Debug("独立IPv4已绑定到宿主机接口，无需添加",
			zap.String("ip", cleanIP))
		return nil
	}

	global.APP_LOG.Debug("独立IPv4未绑定到宿主机接口，正在自动添加",
		zap.String("ip", cleanIP))

	// 获取宿主机出口网络接口（具有默认路由的接口）
	getPrimaryIfaceCmd := `ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++){if($i=="dev"){print $(i+1);exit}}}'`
	ifaceOutput, ifaceErr := p.sshClient.Execute(getPrimaryIfaceCmd)
	primaryIface := strings.TrimSpace(ifaceOutput)
	if ifaceErr != nil || primaryIface == "" {
		// 回退方案：取第一个全局 IPv4 地址所在接口（排除 loopback 与链路本地地址）
		fallbackCmd := `ip -o -4 addr show up | awk '$4!~/^127\./ && $4!~/^169\.254\./ {print $2; exit}'`
		fallbackOutput, fallbackErr := p.sshClient.Execute(fallbackCmd)
		if fallbackErr != nil || strings.TrimSpace(fallbackOutput) == "" {
			return fmt.Errorf("无法确定宿主机主网络接口，请手动将 %s/32 绑定到对应接口", cleanIP)
		}
		primaryIface = strings.TrimSpace(fallbackOutput)
	}

	// 以 /32 方式将独立 IPv4 添加到宿主机接口（路由模式，适合绝大多数云服务器场景）
	addCmd := fmt.Sprintf("ip addr add %s/32 dev %s", cleanIP, primaryIface)
	if _, addErr := p.sshClient.Execute(addCmd); addErr != nil {
		// 并发场景下可能已被其他操作添加，再次确认
		output2, checkErr2 := p.sshClient.Execute(checkCmd)
		if checkErr2 == nil && strings.Contains(output2, cleanIP) {
			global.APP_LOG.Debug("独立IPv4已由并发操作绑定，跳过",
				zap.String("ip", cleanIP))
			return nil
		}
		return fmt.Errorf("自动绑定独立IPv4 %s 到宿主机接口 %s 失败: %w", cleanIP, primaryIface, addErr)
	}

	global.APP_LOG.Debug("成功将独立IPv4绑定到宿主机接口",
		zap.String("ip", cleanIP),
		zap.String("interface", primaryIface))
	return nil
}
