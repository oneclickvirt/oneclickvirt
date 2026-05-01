package incus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (i *IncusProvider) setupNetworkDeviceIPv6(ctx context.Context, config IPv6Config) (string, error) {
	global.APP_LOG.Debug("开始配置网络设备IPv6",
		zap.String("container", config.ContainerName))

	// 安装sipcalc
	if err := i.installSipcalc(ctx); err != nil {
		return "", fmt.Errorf("安装sipcalc失败: %w", err)
	}

	// 获取本机IPv6网络信息
	hostIPv6, err := i.checkIPv6(ctx)
	if err != nil {
		return "", fmt.Errorf("检查IPv6失败: %w", err)
	}

	// 确定IPv6网络接口
	var ipv6NetworkName string
	var ipNetworkGam string

	// 检查是否有he-ipv6接口
	heIPv6Check := "ip -f inet6 addr | grep -q 'he-ipv6' && echo 'found' || echo 'not_found'"
	output, err := i.sshClient.Execute(heIPv6Check)
	if err == nil && strings.TrimSpace(output) == "found" {
		ipv6NetworkName = "he-ipv6"
		cmd := fmt.Sprintf("ip -6 addr show %s | grep -E \"%s/24|%s/48|%s/64|%s/80|%s/96|%s/112\" | grep global | awk '{print $2}'",
			ipv6NetworkName, hostIPv6, hostIPv6, hostIPv6, hostIPv6, hostIPv6, hostIPv6)
		output, err := i.sshClient.Execute(cmd)
		if err == nil {
			ipNetworkGam = strings.TrimSpace(output)
		}
	} else {
		// 获取默认网络接口
		cmd := "ls /sys/class/net/ | grep -v \"$(ls /sys/devices/virtual/net/)\""
		output, err := i.sshClient.Execute(cmd)
		if err != nil {
			return "", fmt.Errorf("获取网络接口失败: %w", err)
		}
		// 清理输出，移除所有空白字符和回车符
		ipv6NetworkName = utils.CleanCommandOutput(output)

		cmd = fmt.Sprintf("ip -6 addr show %s | grep global | awk '{print $2}' | head -n 1", ipv6NetworkName)
		output, err = i.sshClient.Execute(cmd)
		if err == nil {
			ipNetworkGam = strings.TrimSpace(output)
		}
	}

	if ipNetworkGam == "" {
		return "", fmt.Errorf("无法获取本地IPv6网络配置")
	}

	global.APP_LOG.Debug("本地IPv6地址", zap.String("address", ipNetworkGam))

	// 配置系统参数
	sysctlConfigs := []string{
		fmt.Sprintf("net.ipv6.conf.%s.proxy_ndp=1", ipv6NetworkName),
		"net.ipv6.conf.all.forwarding=1",
		"net.ipv6.conf.all.proxy_ndp=1",
	}

	for _, sysctlConfig := range sysctlConfigs {
		i.updateSysctl(ctx, sysctlConfig)
	}

	// 重新加载sysctl配置（忽略不存在的参数错误）
	i.sshClient.Execute("sysctl -p 2>&1 | grep -v 'cannot stat' || true")

	// 使用sipcalc计算IPv6地址
	sipcalcCmd := fmt.Sprintf("sipcalc %s | grep \"Compressed address\" | awk '{print $4}' | awk -F: '{NF--; print}' OFS=:", ipNetworkGam)
	output, err = i.sshClient.Execute(sipcalcCmd)
	if err != nil {
		return "", fmt.Errorf("计算IPv6地址失败: %w", err)
	}

	ipv6Prefix := strings.TrimSpace(output) + ":"

	// 生成随机后缀
	randBitsCmd := "od -An -N2 -t x1 /dev/urandom | tr -d ' '"
	output, err = i.sshClient.Execute(randBitsCmd)
	if err != nil {
		return "", fmt.Errorf("生成随机数失败: %w", err)
	}

	randBits := strings.TrimSpace(output)
	containerIPv6 := ipv6Prefix + randBits

	global.APP_LOG.Debug("生成容器IPv6地址",
		zap.String("container", config.ContainerName),
		zap.String("ipv6", containerIPv6))

	// 停止容器
	stopCmd := fmt.Sprintf("incus stop %s", config.ContainerName)
	i.sshClient.Execute(stopCmd)
	time.Sleep(3 * time.Second)

	// IPv6网络设备
	deviceCmd := fmt.Sprintf("incus config device add %s eth1 nic nictype=routed parent=%s ipv6.address=%s",
		config.ContainerName, ipv6NetworkName, containerIPv6)
	_, err = i.sshClient.Execute(deviceCmd)
	if err != nil {
		return "", fmt.Errorf("添加IPv6网络设备失败: %w", err)
	}

	time.Sleep(3 * time.Second)

	// 配置防火墙
	i.configureFirewallForIPv6(ctx, ipv6NetworkName)

	// 启动容器
	startCmd := fmt.Sprintf("incus start %s", config.ContainerName)
	_, err = i.sshClient.Execute(startCmd)
	if err != nil {
		return "", fmt.Errorf("启动容器失败: %w", err)
	}

	// 等待容器网络就绪后再进行后续配置
	global.APP_LOG.Debug("等待容器网络就绪以配置IPv6",
		zap.String("containerName", config.ContainerName))
	if err := i.waitForContainerNetworkReady(config.ContainerName); err != nil {
		global.APP_LOG.Warn("等待容器网络就绪超时，继续尝试配置IPv6",
			zap.String("containerName", config.ContainerName),
			zap.Error(err))
	}

	// 处理IPv6网关配置
	if config.Gateway == "N" {
		i.handleIPv6Gateway(ctx, ipv6NetworkName)
	}

	// 设置IPv6连通性检查的cron任务
	cronCmd := "*/1 * * * * curl -m 6 -s ipv6.ip.sb && curl -m 6 -s ipv6.ip.sb"
	checkCronCmd := fmt.Sprintf("crontab -l | grep -q '%s'", cronCmd)
	_, err = i.sshClient.Execute(checkCronCmd)
	if err != nil {
		// cron任务不存在，添加它
		addCronCmd := fmt.Sprintf("echo '%s' | crontab -", cronCmd)
		i.sshClient.Execute(addCronCmd)
	}

	return containerIPv6, nil
}

// updateSysctl 更新sysctl配置
func (i *IncusProvider) updateSysctl(ctx context.Context, sysctlConfig string) error {
	parts := strings.Split(sysctlConfig, "=")
	if len(parts) != 2 {
		return fmt.Errorf("无效的sysctl配置: %s", sysctlConfig)
	}

	key := parts[0]
	value := parts[1]

	// 目标配置文件
	customConf := "/etc/sysctl.d/99-custom.conf"

	// 创建目录
	i.sshClient.Execute("mkdir -p /etc/sysctl.d")

	// 检查和更新配置文件
	checkCmd := fmt.Sprintf("grep -q \"^%s\" %s 2>/dev/null", sysctlConfig, customConf)
	_, err := i.sshClient.Execute(checkCmd)
	if err != nil {
		// 配置不存在，添加它
		addCmd := fmt.Sprintf("echo \"%s\" >> %s", sysctlConfig, customConf)
		i.sshClient.Execute(addCmd)
	}

	// 检查/etc/sysctl.conf并同步更新
	checkSysctlCmd := fmt.Sprintf("grep -q \"^%s\" /etc/sysctl.conf 2>/dev/null", sysctlConfig)
	_, err = i.sshClient.Execute(checkSysctlCmd)
	if err != nil {
		// 在/etc/sysctl.conf中不存在，添加
		addSysctlCmd := fmt.Sprintf("echo \"%s\" >> /etc/sysctl.conf", sysctlConfig)
		i.sshClient.Execute(addSysctlCmd)
	}

	// 立即应用配置
	applyCmd := fmt.Sprintf("sysctl -w \"%s=%s\"", key, value)
	_, err = i.sshClient.Execute(applyCmd)
	return err
}

// configureFirewallForIPv6 配置IPv6防火墙
func (i *IncusProvider) configureFirewallForIPv6(ctx context.Context, interfaceName string) {
	// 检查防火墙类型并配置
	if i.hasFirewalld() {
		trustedCmd := fmt.Sprintf("firewall-cmd --permanent --zone=trusted --add-interface=%s", interfaceName)
		i.sshClient.Execute(trustedCmd)
		i.sshClient.Execute("firewall-cmd --reload")
	} else if i.hasUfw() {
		allowInCmd := fmt.Sprintf("ufw allow in on %s", interfaceName)
		allowOutCmd := fmt.Sprintf("ufw allow out on %s", interfaceName)
		i.sshClient.Execute(allowInCmd)
		i.sshClient.Execute(allowOutCmd)
		i.sshClient.Execute("ufw reload")
	}
}

// handleIPv6Gateway 处理IPv6网关配置
func (i *IncusProvider) handleIPv6Gateway(ctx context.Context, interfaceName string) {
	// 获取并删除fe80地址
	delIPCmd := fmt.Sprintf("ip -6 addr show dev %s | awk '/inet6 fe80/ {print $2}'", interfaceName)
	output, err := i.sshClient.Execute(delIPCmd)
	if err == nil {
		delIP := strings.TrimSpace(output)
		if delIP != "" {
			// 删除地址
			deleteCmd := fmt.Sprintf("ip addr del %s dev %s", delIP, interfaceName)
			i.sshClient.Execute(deleteCmd)

			// 创建删除脚本
			scriptContent := fmt.Sprintf("#!/bin/bash\nip addr del %s dev %s", delIP, interfaceName)
			createScriptCmd := fmt.Sprintf("echo '%s' > /usr/local/bin/remove_route.sh", scriptContent)
			i.sshClient.Execute(createScriptCmd)
			i.sshClient.Execute("chmod 777 /usr/local/bin/remove_route.sh")

			// 到crontab
			checkCronCmd := "crontab -l | grep -q '/usr/local/bin/remove_route.sh'"
			_, err := i.sshClient.Execute(checkCronCmd)
			if err != nil {
				addCronCmd := "echo '@reboot /usr/local/bin/remove_route.sh' | crontab -"
				i.sshClient.Execute(addCronCmd)
			}
		}
	}
}

// configureIPv6Network 主要的IPv6网络配置函数
func (i *IncusProvider) configureIPv6Network(ctx context.Context, containerName string, enableIPv6 bool, portMappingMethod string) error {
	if !enableIPv6 {
		global.APP_LOG.Debug("IPv6未启用，跳过IPv6配置", zap.String("container", containerName))
		return nil
	}

	global.APP_LOG.Debug("开始配置IPv6网络",
		zap.String("container", containerName),
		zap.String("portMappingMethod", portMappingMethod))

	// 首先检查宿主机是否有公网IPv6地址
	hostIPv6, err := i.checkIPv6(ctx)
	if err != nil {
		global.APP_LOG.Warn("宿主机不支持IPv6，自动跳过IPv6配置",
			zap.String("container", containerName),
			zap.Error(err))
		return nil // 宿主机不支持IPv6时，静默跳过IPv6配置，不返回错误
	}

	global.APP_LOG.Debug("宿主机IPv6环境检查通过",
		zap.String("container", containerName),
		zap.String("hostIPv6", hostIPv6))

	// 获取IPv6网关信息
	gatewayInfo, err := i.getIPv6GatewayInfo(ctx)
	if err != nil {
		global.APP_LOG.Warn("获取IPv6网关信息失败", zap.Error(err))
		gatewayInfo = "N"
	}

	// 创建IPv6配置，根据端口映射方式选择IPv6配置方式
	config := IPv6Config{
		ContainerName:    containerName,
		Gateway:          gatewayInfo,
		UseNetworkDevice: portMappingMethod == "device_proxy", // device_proxy使用网络设备方式
		UseIptables:      portMappingMethod == "iptables",     // iptables使用iptables方式
	}

	var containerIPv6 string
	// 根据配置方式选择IPv6配置方法
	if config.UseNetworkDevice {
		containerIPv6, err = i.setupNetworkDeviceIPv6(ctx, config)
		if err != nil {
			return fmt.Errorf("使用device_proxy方式配置IPv6网络失败: %w", err)
		}
	} else if config.UseIptables {
		// 使用iptables方式配置IPv6映射
		containerIPv6, err = i.setupIptablesIPv6(ctx, config)
		if err != nil {
			return fmt.Errorf("使用iptables方式配置IPv6网络失败: %w", err)
		}
	} else {
		// 默认使用device_proxy方式
		config.UseNetworkDevice = true
		containerIPv6, err = i.setupNetworkDeviceIPv6(ctx, config)
		if err != nil {
			return fmt.Errorf("配置IPv6网络失败: %w", err)
		}
	}

	// 保存IPv6地址到文件
	saveCmd := fmt.Sprintf("echo \"%s\" >> %s_v6", containerIPv6, containerName)
	i.sshClient.Execute(saveCmd)

	global.APP_LOG.Info("IPv6网络配置完成",
		zap.String("container", containerName),
		zap.String("ipv6", containerIPv6),
		zap.String("method", portMappingMethod))

	return nil
}

// setupIptablesIPv6 使用iptables方式配置IPv6映射
func (i *IncusProvider) setupIptablesIPv6(ctx context.Context, config IPv6Config) (string, error) {
	global.APP_LOG.Debug("开始配置iptables IPv6映射",
		zap.String("container", config.ContainerName))

	// 检测操作系统类型
	osType, err := i.detectOS(ctx)
	if err != nil {
		return "", fmt.Errorf("检测操作系统失败: %w", err)
	}

	// 检查是否使用firewalld
	useFirewalld := false
	if osType == "centos" || osType == "almalinux" || osType == "rocky" {
		_, err := i.sshClient.Execute("command -v dnf")
		if err == nil {
			useFirewalld = true
		}
		_, err = i.sshClient.Execute("command -v yum")
		if err == nil {
			useFirewalld = true
		}
	}

	// 安装必要的包
	err = i.installNetfilterPackages(ctx, osType, useFirewalld)
	if err != nil {
		global.APP_LOG.Warn("安装网络过滤包失败", zap.Error(err))
	}

	// 获取容器的内网IPv6地址
	containerIPv6, err := i.getContainerIPv6(ctx, config.ContainerName)
	if err != nil {
		return "", fmt.Errorf("获取容器IPv6地址失败: %w", err)
	}

	// 获取宿主机IPv6子网前缀
	subnetPrefix, err := i.getHostIPv6Prefix(ctx)
	if err != nil {
		return "", fmt.Errorf("获取IPv6子网前缀失败: %w", err)
	}

	// 获取IPv6子网长度
	ipv6LengthCmd := "ip addr show | awk '/inet6.*scope global/ { print $2 }' | head -n 1"
	output, err := i.sshClient.Execute(ipv6LengthCmd)
	if err != nil {
		return "", fmt.Errorf("获取IPv6子网长度失败: %w", err)
	}

	ipv6AddressWithLength := utils.CleanCommandOutput(output)
	if !strings.Contains(ipv6AddressWithLength, "/") {
		return "", fmt.Errorf("查询不到IPv6的子网大小")
	}

	parts := strings.Split(ipv6AddressWithLength, "/")
	ipv6Length := parts[1]

	// 获取网络接口名称
	interfaceCmd := "lshw -C network | awk '/logical name:/{print $3}' | head -1"
	interfaceOutput, err := i.sshClient.Execute(interfaceCmd)
	if err != nil {
		interfaceCmd = "ip route | grep default | awk '{print $5}' | head -1"
		interfaceOutput, _ = i.sshClient.Execute(interfaceCmd)
	}
	interfaceName := utils.CleanCommandOutput(interfaceOutput)
	if interfaceName == "" {
		return "", fmt.Errorf("无法获取网络接口名称")
	}

	global.APP_LOG.Debug("网络配置信息",
		zap.String("interface", interfaceName),
		zap.String("subnetPrefix", subnetPrefix),
		zap.String("ipv6Length", ipv6Length),
		zap.String("containerIPv6", containerIPv6))

	// 查找可用的IPv6地址
	var mappedIPv6 string
	for idx := 3; idx <= 65535; idx++ {
		testIPv6 := fmt.Sprintf("%s%d", subnetPrefix, idx)

		// 跳过容器本身的地址
		if testIPv6 == containerIPv6 {
			continue
		}

		// 检查地址是否已被使用
		checkAddrCmd := fmt.Sprintf("ip -6 addr show dev %s | grep -qw %s", interfaceName, testIPv6)
		_, err := i.sshClient.Execute(checkAddrCmd)
		if err == nil {
			// 地址已被使用，继续下一个
			continue
		}

		// 检查地址是否可以ping通
		pingCmd := fmt.Sprintf("ping6 -c1 -w1 -q %s", testIPv6)
		_, err = i.sshClient.Execute(pingCmd)
		if err == nil {
			// 地址能ping通，说明已被占用
			global.APP_LOG.Debug("IPv6地址已被占用", zap.String("ipv6", testIPv6))
			continue
		}

		// 检查firewall或iptables规则
		var checkRuleCmd string
		if useFirewalld {
			checkRuleCmd = fmt.Sprintf("firewall-cmd --direct --query-rule ipv6 nat PREROUTING 0 -d %s -j DNAT --to-destination %s", testIPv6, containerIPv6)
		} else {
			checkRuleCmd = fmt.Sprintf("ip6tables -t nat -C PREROUTING -d %s -j DNAT --to-destination %s 2>/dev/null", testIPv6, containerIPv6)
		}
		_, err = i.sshClient.Execute(checkRuleCmd)
		if err == nil {
			// 规则已存在
			continue
		}

		// 找到可用地址
		mappedIPv6 = testIPv6
		global.APP_LOG.Debug("找到可用IPv6地址", zap.String("ipv6", mappedIPv6))
		break
	}

	if mappedIPv6 == "" {
		return "", fmt.Errorf("无可用IPv6地址，不进行自动映射")
	}

	// IPv6地址到接口
	addAddrCmd := fmt.Sprintf("ip addr add %s/%s dev %s", mappedIPv6, ipv6Length, interfaceName)
	_, err = i.sshClient.Execute(addAddrCmd)
	if err != nil {
		return "", fmt.Errorf("添加IPv6地址失败: %w", err)
	}

	// 防火墙/iptables规则
	if useFirewalld {
		// 启用firewalld
		i.sshClient.Execute("systemctl enable --now firewalld")
		time.Sleep(3 * time.Second)

		// firewalld直接规则
		natRuleCmd := fmt.Sprintf("firewall-cmd --permanent --direct --add-rule ipv6 nat PREROUTING 0 -d %s -j DNAT --to-destination %s", mappedIPv6, containerIPv6)
		_, err = i.sshClient.Execute(natRuleCmd)
		if err != nil {
			return "", fmt.Errorf("添加firewalld NAT规则失败: %w", err)
		}

		// 重新加载firewalld
		_, err = i.sshClient.Execute("firewall-cmd --reload")
		if err != nil {
			return "", fmt.Errorf("重新加载firewalld失败: %w", err)
		}
	} else {
		// ip6tables NAT规则
		natRuleCmd := fmt.Sprintf("ip6tables -t nat -A PREROUTING -d %s -j DNAT --to-destination %s", mappedIPv6, containerIPv6)
		_, err = i.sshClient.Execute(natRuleCmd)
		if err != nil {
			return "", fmt.Errorf("添加ip6tables NAT规则失败: %w", err)
		}
	}

	// 设置持久化服务和脚本
	err = i.setupPersistenceServiceIncus(ctx)
	if err != nil {
		global.APP_LOG.Warn("设置持久化服务失败", zap.Error(err))
	}

	// 保存规则
	err = i.saveNetfilterRules(ctx, useFirewalld)
	if err != nil {
		global.APP_LOG.Warn("保存防火墙规则失败", zap.Error(err))
	}

	// 测试连通性
	err = i.testIPv6Connectivity(ctx, mappedIPv6, config.ContainerName)
	if err != nil {
		return "", fmt.Errorf("IPv6连通性测试失败: %w", err)
	}

	return mappedIPv6, nil
}

// detectOS 检测操作系统类型
func (i *IncusProvider) detectOS(ctx context.Context) (string, error) {
	cmd := "cat /etc/os-release | grep ^ID= | cut -d= -f2 | tr -d '\"'"
	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("检测操作系统失败: %w", err)
	}

	osType := strings.TrimSpace(output)
	global.APP_LOG.Debug("检测到操作系统类型", zap.String("os", osType))

	// 标准化操作系统名称
	switch osType {
	case "ubuntu", "pop", "neon", "zorin":
		return "ubuntu", nil
	case "debian":
		return "debian", nil
	case "kali":
		return "debian", nil
	case "centos", "almalinux", "rocky":
		return osType, nil
	case "arch", "archarm", "endeavouros", "blendos", "garuda":
		return "arch", nil
	case "manjaro", "manjaro-arm":
		return "manjaro", nil
	default:
		return osType, nil
	}
}

// installNetfilterPackages 安装网络过滤相关包
func (i *IncusProvider) installNetfilterPackages(ctx context.Context, osType string, useFirewalld bool) error {
	switch osType {
	case "ubuntu", "debian":
		updateCmd := "apt update -y"
		i.sshClient.Execute(updateCmd)
		if !useFirewalld {
			installCmd := "apt install -y netfilter-persistent iptables-persistent"
			_, err := i.sshClient.Execute(installCmd)
			return err
		}
	case "centos", "almalinux", "rocky":
		if useFirewalld {
			installCmd := "yum install -y firewalld"
			_, err := i.sshClient.Execute(installCmd)
			return err
		} else {
			installCmd := "yum install -y iptables-services"
			_, err := i.sshClient.Execute(installCmd)
			return err
		}
	case "arch", "manjaro":
		if !useFirewalld {
			installCmd := "pacman -S --noconfirm --needed iptables"
			_, err := i.sshClient.Execute(installCmd)
			return err
		}
	}
	return nil
}

// setupPersistenceServiceIncus 设置持久化服务 (Incus版本)
func (i *IncusProvider) setupPersistenceServiceIncus(ctx context.Context) error {
	// 检查CDN可用性并下载脚本
	cdnUrls := []string{
		"https://cdn0.spiritlhl.top/",
		"http://cdn1.spiritlhl.net/",
		"http://cdn2.spiritlhl.net/",
		"http://cdn3.spiritlhl.net/",
		"http://cdn4.spiritlhl.net/",
	}

	var cdnSuccessUrl string
	for _, cdnUrl := range cdnUrls {
		testUrl := cdnUrl + "https://raw.githubusercontent.com/spiritLHLS/ecs/main/back/test"
		testCmd := fmt.Sprintf("curl -4 -sL -k '%s' --max-time 6 | grep -q 'success'", testUrl)
		_, err := i.sshClient.Execute(testCmd)
		if err == nil {
			cdnSuccessUrl = cdnUrl
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 下载add-ipv6.sh脚本 (Incus版本)
	scriptPath := "/usr/local/bin/add-ipv6.sh"
	checkScriptCmd := fmt.Sprintf("[ -f %s ]", scriptPath)
	_, err := i.sshClient.Execute(checkScriptCmd)
	if err != nil {
		scriptUrl := cdnSuccessUrl + "https://raw.githubusercontent.com/oneclickvirt/incus/main/scripts/add-ipv6.sh"
		downloadCmd := fmt.Sprintf("wget '%s' -O %s", scriptUrl, scriptPath)
		_, err := i.sshClient.Execute(downloadCmd)
		if err != nil {
			global.APP_LOG.Warn("下载add-ipv6.sh脚本失败", zap.Error(err))
		} else {
			i.sshClient.Execute(fmt.Sprintf("chmod +x %s", scriptPath))
		}
	}

	// 下载add-ipv6.service服务文件 (Incus版本)
	servicePath := "/etc/systemd/system/add-ipv6.service"
	checkServiceCmd := fmt.Sprintf("[ -f %s ]", servicePath)
	_, err = i.sshClient.Execute(checkServiceCmd)
	if err != nil {
		serviceUrl := cdnSuccessUrl + "https://raw.githubusercontent.com/oneclickvirt/incus/main/scripts/add-ipv6.service"
		downloadCmd := fmt.Sprintf("wget '%s' -O %s", serviceUrl, servicePath)
		_, err := i.sshClient.Execute(downloadCmd)
		if err != nil {
			global.APP_LOG.Warn("下载add-ipv6.service服务文件失败", zap.Error(err))
		} else {
			i.sshClient.Execute(fmt.Sprintf("chmod +x %s", servicePath))
			i.sshClient.Execute("systemctl daemon-reload")
			i.sshClient.Execute("systemctl enable --now add-ipv6.service")
		}
	}

	return nil
}

// saveNetfilterRules 保存网络过滤规则
func (i *IncusProvider) saveNetfilterRules(ctx context.Context, useFirewalld bool) error {
	if useFirewalld {
		// firewalld会自动持久化规则
		_, err := i.sshClient.Execute("systemctl restart firewalld")
		return err
	} else {
		// 保存iptables规则
		i.sshClient.Execute("mkdir -p /etc/iptables")
		_, err := i.sshClient.Execute("ip6tables-save > /etc/iptables/rules.v6")
		if err != nil {
			return fmt.Errorf("保存ip6tables规则失败: %w", err)
		}

		// 检查netfilter-persistent是否可用
		_, err = i.sshClient.Execute("command -v netfilter-persistent")
		if err == nil {
			i.sshClient.Execute("netfilter-persistent save")
			i.sshClient.Execute("netfilter-persistent reload")
			i.sshClient.Execute("service netfilter-persistent restart")
		}
	}

	return nil
}

// testIPv6Connectivity 测试IPv6连通性
func (i *IncusProvider) testIPv6Connectivity(ctx context.Context, ipv6Addr, containerName string) error {
	global.APP_LOG.Debug("测试IPv6连通性", zap.String("ipv6", ipv6Addr))

	testCmd := fmt.Sprintf("ping6 -c 3 %s", ipv6Addr)
	_, err := i.sshClient.Execute(testCmd)
	if err != nil {
		global.APP_LOG.Error("IPv6映射失败",
			zap.String("container", containerName),
			zap.String("ipv6", ipv6Addr))
		return fmt.Errorf("映射失败")
	}

	global.APP_LOG.Info("IPv6映射成功",
		zap.String("container", containerName),
		zap.String("ipv6", ipv6Addr))

	return nil
}
