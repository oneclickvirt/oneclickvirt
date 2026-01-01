package lxd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// IPv6Config IPv6配置结构
type IPv6Config struct {
	ContainerName    string
	ContainerIPv6    string
	HostIPv6Prefix   string
	IPv6Length       int
	Interface        string
	Gateway          string
	UseIptables      bool
	UseNetworkDevice bool
}

// ConfigureIPv6 配置实例的IPv6网络
func (l *LXDProvider) ConfigureIPv6(instanceName string, enable bool) error {
	global.APP_LOG.Info("配置IPv6网络",
		zap.String("instance", instanceName),
		zap.Bool("enable", enable))

	if enable {
		return l.enableIPv6(instanceName)
	} else {
		return l.disableIPv6(instanceName)
	}
}

// enableIPv6 启用IPv6网络
func (l *LXDProvider) enableIPv6(instanceName string) error {
	// 1. 设置IPv6网络设备配置
	ipv6NetworkCmd := fmt.Sprintf("lxc config device override %s eth0 ipv6.address=auto", instanceName)
	_, err := l.sshClient.Execute(ipv6NetworkCmd)
	if err != nil {
		return fmt.Errorf("配置IPv6网络设备失败: %w", err)
	}

	// 2. 启用IPv6路由
	routeCmd := fmt.Sprintf("lxc config device override %s eth0 ipv6.routes=true", instanceName)
	_, err = l.sshClient.Execute(routeCmd)
	if err != nil {
		return fmt.Errorf("配置IPv6路由失败: %w", err)
	}

	// 3. 在容器内启用IPv6
	enableIPv6Cmd := fmt.Sprintf("lxc exec %s -- bash -c 'echo 0 > /proc/sys/net/ipv6/conf/all/disable_ipv6'", instanceName)
	_, err = l.sshClient.Execute(enableIPv6Cmd)
	if err != nil {
		global.APP_LOG.Warn("在容器内启用IPv6失败",
			zap.String("instance", instanceName),
			zap.Error(err))
		// 不阻断流程，可能容器还未完全启动
	}

	// 4. 重启网络接口
	restartNetworkCmd := fmt.Sprintf("lxc exec %s -- bash -c 'ip addr flush dev eth0 && dhclient -6 eth0'", instanceName)
	_, err = l.sshClient.Execute(restartNetworkCmd)
	if err != nil {
		global.APP_LOG.Warn("重启网络接口失败",
			zap.String("instance", instanceName),
			zap.Error(err))
		// 不阻断流程
	}

	global.APP_LOG.Info("IPv6网络配置成功",
		zap.String("instance", instanceName))

	return nil
}

// disableIPv6 禁用IPv6网络
func (l *LXDProvider) disableIPv6(instanceName string) error {
	// 1. 移除IPv6网络设备配置
	removeIPv6NetworkCmd := fmt.Sprintf("lxc config device unset %s eth0 ipv6.address", instanceName)
	_, err := l.sshClient.Execute(removeIPv6NetworkCmd)
	if err != nil {
		global.APP_LOG.Warn("移除IPv6网络配置失败",
			zap.String("instance", instanceName),
			zap.Error(err))
		// 不阻断流程
	}

	// 2. 禁用IPv6路由
	disableRouteCmd := fmt.Sprintf("lxc config device unset %s eth0 ipv6.routes", instanceName)
	_, err = l.sshClient.Execute(disableRouteCmd)
	if err != nil {
		global.APP_LOG.Warn("禁用IPv6路由失败",
			zap.String("instance", instanceName),
			zap.Error(err))
		// 不阻断流程
	}

	// 3. 在容器内禁用IPv6
	disableIPv6Cmd := fmt.Sprintf("lxc exec %s -- bash -c 'echo 1 > /proc/sys/net/ipv6/conf/all/disable_ipv6'", instanceName)
	_, err = l.sshClient.Execute(disableIPv6Cmd)
	if err != nil {
		global.APP_LOG.Warn("在容器内禁用IPv6失败",
			zap.String("instance", instanceName),
			zap.Error(err))
		// 不阻断流程
	}

	global.APP_LOG.Info("IPv6网络禁用成功",
		zap.String("instance", instanceName))

	return nil
}

// GetInstanceIPv6 获取实例的内网IPv6地址
func (l *LXDProvider) GetInstanceIPv6(instanceName string) (string, error) {
	// 获取实例的内网IPv6地址
	ipv6Cmd := fmt.Sprintf("lxc list %s --format json | jq -r '.[0].state.network.eth0.addresses[]? | select(.family==\"inet6\" and .scope==\"global\") | .address' 2>/dev/null", instanceName)
	ipv6Output, err := l.sshClient.Execute(ipv6Cmd)
	if err != nil {
		return "", fmt.Errorf("获取IPv6地址失败: %w", err)
	}

	ipv6 := utils.CleanCommandOutput(ipv6Output)
	if ipv6 == "" {
		return "", fmt.Errorf("实例未分配IPv6地址")
	}

	return ipv6, nil
}

// GetInstancePublicIPv6 获取实例的公网IPv6地址
func (l *LXDProvider) GetInstancePublicIPv6(instanceName string) (string, error) {
	// 尝试从保存的IPv6文件中读取公网IPv6地址
	publicIPv6Cmd := fmt.Sprintf("cat %s_v6 2>/dev/null | tail -1", instanceName)
	publicIPv6Output, err := l.sshClient.Execute(publicIPv6Cmd)
	if err == nil {
		publicIPv6 := utils.CleanCommandOutput(publicIPv6Output)
		if publicIPv6 != "" && !l.isPrivateIPv6(publicIPv6) {
			global.APP_LOG.Info("从文件获取到公网IPv6地址",
				zap.String("instanceName", instanceName),
				zap.String("publicIPv6", publicIPv6))
			return publicIPv6, nil
		}
	}

	// 如果文件中没有，尝试从eth1网络设备获取
	eth1Cmd := fmt.Sprintf("lxc list %s --format json | jq -r '.[0].state.network.eth1.addresses[]? | select(.family==\"inet6\" and .scope==\"global\") | .address' 2>/dev/null", instanceName)
	eth1Output, err := l.sshClient.Execute(eth1Cmd)
	if err == nil {
		eth1IPv6 := utils.CleanCommandOutput(eth1Output)
		if eth1IPv6 != "" && !l.isPrivateIPv6(eth1IPv6) {
			global.APP_LOG.Info("从eth1获取到公网IPv6地址",
				zap.String("instanceName", instanceName),
				zap.String("publicIPv6", eth1IPv6))
			return eth1IPv6, nil
		}
	}

	// 如果都没有获取到，返回空（表示没有公网IPv6）
	return "", fmt.Errorf("实例未分配公网IPv6地址")
}

// ConfigureIPv6Profile 为LXD profile配置IPv6网络
func (l *LXDProvider) ConfigureIPv6Profile(profileName string, enable bool) error {
	global.APP_LOG.Info("配置IPv6 Profile",
		zap.String("profile", profileName),
		zap.Bool("enable", enable))

	if enable {
		// 为profile启用IPv6
		profileCmd := fmt.Sprintf("lxc profile device set %s eth0 ipv6.address auto", profileName)
		_, err := l.sshClient.Execute(profileCmd)
		if err != nil {
			return fmt.Errorf("配置Profile IPv6失败: %w", err)
		}

		routeCmd := fmt.Sprintf("lxc profile device set %s eth0 ipv6.routes true", profileName)
		_, err = l.sshClient.Execute(routeCmd)
		if err != nil {
			return fmt.Errorf("配置Profile IPv6路由失败: %w", err)
		}
	} else {
		// 为profile禁用IPv6
		unsetCmd := fmt.Sprintf("lxc profile device unset %s eth0 ipv6.address", profileName)
		_, err := l.sshClient.Execute(unsetCmd)
		if err != nil {
			global.APP_LOG.Warn("移除Profile IPv6配置失败",
				zap.String("profile", profileName),
				zap.Error(err))
		}

		unsetRouteCmd := fmt.Sprintf("lxc profile device unset %s eth0 ipv6.routes", profileName)
		_, err = l.sshClient.Execute(unsetRouteCmd)
		if err != nil {
			global.APP_LOG.Warn("移除Profile IPv6路由失败",
				zap.String("profile", profileName),
				zap.Error(err))
		}
	}

	global.APP_LOG.Info("IPv6 Profile配置完成",
		zap.String("profile", profileName))

	return nil
}

// isPrivateIPv6 检查是否为私有IPv6地址
func (l *LXDProvider) isPrivateIPv6(address string) bool {
	if address == "" || !strings.Contains(address, ":") {
		return true
	}

	// 私有IPv6地址范围检查
	privateRanges := []string{
		"fe80:",    // 链路本地地址
		"fc00:",    // 唯一本地地址
		"fd00:",    // 唯一本地地址
		"2001:db8", // 文档用途
		"::1",      // 回环地址
		"::ffff:",  // IPv4映射地址
		"2002:",    // 6to4
		"2001:",    // Teredo (某些情况)
		"fd42:",    // Docker等使用的私有地址
	}

	for _, prefix := range privateRanges {
		if strings.HasPrefix(address, prefix) {
			return true
		}
	}
	return false
}

// checkIPv6 检查并获取IPv6地址
func (l *LXDProvider) checkIPv6(ctx context.Context) (string, error) {
	// 首先尝试从本地网络接口获取全局IPv6地址
	cmd := "ip -6 addr show | grep global | awk '{print length, $2}' | sort -nr | head -n 1 | awk '{print $2}' | cut -d '/' -f1"
	output, err := l.sshClient.Execute(cmd)
	if err == nil {
		ipv6 := strings.TrimSpace(output)
		if !l.isPrivateIPv6(ipv6) {
			global.APP_LOG.Info("从本地接口获取到IPv6地址", zap.String("ipv6", ipv6))
			return ipv6, nil
		}
	}

	// 如果本地没有全局IPv6地址，通过API获取
	apiEndpoints := []string{
		"ipv6.ip.sb",
		"https://ipget.net",
		"ipv6.ping0.cc",
		"https://api.my-ip.io/ip",
		"https://ipv6.icanhazip.com",
	}

	for _, endpoint := range apiEndpoints {
		cmd := fmt.Sprintf("curl -sLk6m8 '%s' | tr -d '[:space:]'", endpoint)
		output, err := l.sshClient.Execute(cmd)
		if err == nil {
			ipv6 := strings.TrimSpace(output)
			if ipv6 != "" && !strings.Contains(output, "error") && !l.isPrivateIPv6(ipv6) {
				global.APP_LOG.Info("通过API获取到IPv6地址",
					zap.String("endpoint", endpoint),
					zap.String("ipv6", ipv6))
				return ipv6, nil
			}
		}
		time.Sleep(1 * time.Second)
	}

	return "", fmt.Errorf("无法获取有效的IPv6地址")
}

// getContainerIPv6 获取容器内网IPv6地址
func (l *LXDProvider) getContainerIPv6(ctx context.Context, containerName string) (string, error) {
	cmd := fmt.Sprintf("lxc list %s --format=json | jq -r '.[0].state.network.eth0.addresses[] | select(.family==\"inet6\") | select(.scope==\"global\") | .address'", containerName)
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取容器IPv6地址失败: %w", err)
	}

	ipv6 := strings.TrimSpace(output)
	if ipv6 == "" || ipv6 == "null" {
		return "", fmt.Errorf("容器无内网IPv6地址")
	}

	global.APP_LOG.Info("获取到容器IPv6地址",
		zap.String("container", containerName),
		zap.String("ipv6", ipv6))
	return ipv6, nil
}

// getHostIPv6Prefix 获取宿主机IPv6子网前缀
func (l *LXDProvider) getHostIPv6Prefix(ctx context.Context) (string, error) {
	cmd := "ip -6 addr show | grep -E 'inet6.*global' | awk '{print $2}' | awk -F'/' '{print $1}' | head -n 1 | cut -d ':' -f1-5"
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取IPv6子网前缀失败: %w", err)
	}

	prefix := strings.TrimSpace(output)
	if prefix == "" {
		return "", fmt.Errorf("无IPv6子网")
	}

	prefix = prefix + ":"
	global.APP_LOG.Info("获取到IPv6子网前缀", zap.String("prefix", prefix))
	return prefix, nil
}

// getIPv6GatewayInfo 获取IPv6网关信息
func (l *LXDProvider) getIPv6GatewayInfo(ctx context.Context) (string, error) {
	cmd := "ip -6 route show | awk '/default via/{print $3}'"
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return "N", fmt.Errorf("获取IPv6网关信息失败: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	var gateway string

	if len(lines) == 1 {
		gateway = lines[0]
	} else if len(lines) >= 2 {
		// 优先选择非fe80的网关
		for _, line := range lines {
			if !strings.HasPrefix(line, "fe80") {
				gateway = line
				break
			}
		}
		if gateway == "" {
			gateway = lines[0]
		}
	}

	if strings.HasPrefix(gateway, "fe80") {
		return "Y", nil
	}
	return "N", nil
}

// installSipcalc 安装sipcalc工具
func (l *LXDProvider) installSipcalc(ctx context.Context) error {
	// 检查是否已安装
	_, err := l.sshClient.Execute("command -v sipcalc")
	if err == nil {
		return nil // 已安装
	}

	global.APP_LOG.Info("开始安装sipcalc工具")

	// 检测OS类型
	osCmd := "cat /etc/os-release | grep ^ID= | cut -d= -f2 | tr -d '\"'"
	osOutput, err := l.sshClient.Execute(osCmd)
	if err != nil {
		return fmt.Errorf("检测操作系统失败: %w", err)
	}

	osType := utils.CleanCommandOutput(osOutput)
	global.APP_LOG.Info("检测到操作系统类型", zap.String("os", osType))

	switch osType {
	case "centos", "almalinux", "rocky":
		return l.installSipcalcRHEL(ctx)
	case "ubuntu", "debian":
		return l.installSipcalcDebian(ctx)
	case "arch":
		_, err := l.sshClient.Execute("pacman -S --noconfirm --needed sipcalc")
		return err
	default:
		// 尝试通用方法
		_, err := l.sshClient.Execute("apt update -y && apt install -y sipcalc")
		if err != nil {
			_, err = l.sshClient.Execute("yum install -y sipcalc")
		}
		return err
	}
}

// installSipcalcRHEL 在RHEL系列系统上安装sipcalc
func (l *LXDProvider) installSipcalcRHEL(ctx context.Context) error {
	// 获取架构
	archCmd := "uname -m"
	archOutput, err := l.sshClient.Execute(archCmd)
	if err != nil {
		return fmt.Errorf("获取系统架构失败: %w", err)
	}

	arch := utils.CleanCommandOutput(archOutput)
	var relPath string

	switch arch {
	case "x86_64":
		relPath = "x86_64/Packages/s/sipcalc-1.1.6-17.el8.x86_64.rpm"
	case "aarch64":
		relPath = "aarch64/Packages/s/sipcalc-1.1.6-17.el8.aarch64.rpm"
	default:
		return fmt.Errorf("不支持的架构: %s", arch)
	}

	mirrors := []string{
		"https://dl.fedoraproject.org/pub/epel/8/Everything/" + relPath,
		"https://mirrors.aliyun.com/epel/8/Everything/" + relPath,
		"https://repo.huaweicloud.com/epel/8/Everything/" + relPath,
		"https://mirrors.tuna.tsinghua.edu.cn/epel/8/Everything/" + relPath,
	}

	filename := "sipcalc-1.1.6-17.el8." + arch + ".rpm"

	for _, mirror := range mirrors {
		global.APP_LOG.Info("尝试从镜像下载sipcalc", zap.String("mirror", mirror))
		downloadCmd := fmt.Sprintf("curl -fLO '%s'", mirror)
		_, err := l.sshClient.Execute(downloadCmd)
		if err == nil {
			break
		}
	}

	// 安装rpm包
	installCmd := fmt.Sprintf("rpm -ivh %s", filename)
	_, err = l.sshClient.Execute(installCmd)
	if err != nil {
		// 尝试使用dnf/yum安装
		_, err = l.sshClient.Execute("dnf install -y " + filename)
		if err != nil {
			_, err = l.sshClient.Execute("yum install -y " + filename)
		}
	}

	// 清理下载的文件
	l.sshClient.Execute("rm -f " + filename)

	return err
}

// installSipcalcDebian 在Debian系列系统上安装sipcalc
func (l *LXDProvider) installSipcalcDebian(ctx context.Context) error {
	updateCmd := "apt update -y"
	_, err := l.sshClient.Execute(updateCmd)
	if err != nil {
		global.APP_LOG.Warn("apt update失败", zap.Error(err))
	}

	installCmd := "apt install -y sipcalc"
	_, err = l.sshClient.Execute(installCmd)
	return err
}

// setupNetworkDeviceIPv6 配置网络设备方式的IPv6
func (l *LXDProvider) setupNetworkDeviceIPv6(ctx context.Context, config IPv6Config) (string, error) {
	global.APP_LOG.Info("开始配置网络设备IPv6",
		zap.String("container", config.ContainerName))

	// 安装sipcalc
	if err := l.installSipcalc(ctx); err != nil {
		return "", fmt.Errorf("安装sipcalc失败: %w", err)
	}

	// 获取本机IPv6网络信息
	hostIPv6, err := l.checkIPv6(ctx)
	if err != nil {
		return "", fmt.Errorf("检查IPv6失败: %w", err)
	}

	// 确定IPv6网络接口
	var ipv6NetworkName string
	var ipNetworkGam string

	// 检查是否有he-ipv6接口
	heIPv6Check := "ip -f inet6 addr | grep -q 'he-ipv6' && echo 'found' || echo 'not_found'"
	output, err := l.sshClient.Execute(heIPv6Check)
	if err == nil && strings.TrimSpace(output) == "found" {
		ipv6NetworkName = "he-ipv6"
		cmd := fmt.Sprintf("ip -6 addr show %s | grep -E \"%s/24|%s/48|%s/64|%s/80|%s/96|%s/112\" | grep global | awk '{print $2}'",
			ipv6NetworkName, hostIPv6, hostIPv6, hostIPv6, hostIPv6, hostIPv6, hostIPv6)
		output, err := l.sshClient.Execute(cmd)
		if err == nil {
			ipNetworkGam = strings.TrimSpace(output)
		}
	} else {
		// 获取默认网络接口
		cmd := "ls /sys/class/net/ | grep -v \"$(ls /sys/devices/virtual/net/)\""
		output, err := l.sshClient.Execute(cmd)
		if err != nil {
			return "", fmt.Errorf("获取网络接口失败: %w", err)
		}
		// 清理输出，移除所有空白字符和回车符
		ipv6NetworkName = utils.CleanCommandOutput(output)

		cmd = fmt.Sprintf("ip -6 addr show %s | grep global | awk '{print $2}' | head -n 1", ipv6NetworkName)
		output, err = l.sshClient.Execute(cmd)
		if err == nil {
			ipNetworkGam = strings.TrimSpace(output)
		}
	}

	if ipNetworkGam == "" {
		return "", fmt.Errorf("无法获取本地IPv6网络配置")
	}

	global.APP_LOG.Info("本地IPv6地址", zap.String("address", ipNetworkGam))

	// 配置系统参数
	sysctlConfigs := []string{
		fmt.Sprintf("net.ipv6.conf.%s.proxy_ndp=1", ipv6NetworkName),
		"net.ipv6.conf.all.forwarding=1",
		"net.ipv6.conf.all.proxy_ndp=1",
	}

	for _, sysctlConfig := range sysctlConfigs {
		l.updateSysctl(ctx, sysctlConfig)
	}

	// 重新加载sysctl配置（忽略不存在的参数错误）
	l.sshClient.Execute("sysctl -p 2>&1 | grep -v 'cannot stat' || true")

	// 使用sipcalc计算IPv6地址
	sipcalcCmd := fmt.Sprintf("sipcalc %s | grep \"Compressed address\" | awk '{print $4}' | awk -F: '{NF--; print}' OFS=:", ipNetworkGam)
	output, err = l.sshClient.Execute(sipcalcCmd)
	if err != nil {
		return "", fmt.Errorf("计算IPv6地址失败: %w", err)
	}

	ipv6Prefix := strings.TrimSpace(output) + ":"

	// 生成随机后缀
	randBitsCmd := "od -An -N2 -t x1 /dev/urandom | tr -d ' '"
	output, err = l.sshClient.Execute(randBitsCmd)
	if err != nil {
		return "", fmt.Errorf("生成随机数失败: %w", err)
	}

	randBits := strings.TrimSpace(output)
	containerIPv6 := ipv6Prefix + randBits

	global.APP_LOG.Info("生成容器IPv6地址",
		zap.String("container", config.ContainerName),
		zap.String("ipv6", containerIPv6))

	// 停止容器
	stopCmd := fmt.Sprintf("lxc stop %s", config.ContainerName)
	l.sshClient.Execute(stopCmd)
	time.Sleep(3 * time.Second)

	// IPv6网络设备
	deviceCmd := fmt.Sprintf("lxc config device add %s eth1 nic nictype=routed parent=%s ipv6.address=%s",
		config.ContainerName, ipv6NetworkName, containerIPv6)
	_, err = l.sshClient.Execute(deviceCmd)
	if err != nil {
		return "", fmt.Errorf("添加IPv6网络设备失败: %w", err)
	}

	time.Sleep(3 * time.Second)

	// 配置防火墙
	l.configureFirewallForIPv6(ctx, ipv6NetworkName)

	// 启动容器
	startCmd := fmt.Sprintf("lxc start %s", config.ContainerName)
	_, err = l.sshClient.Execute(startCmd)
	if err != nil {
		return "", fmt.Errorf("启动容器失败: %w", err)
	}

	// 等待容器网络就绪后再进行后续配置
	global.APP_LOG.Debug("等待容器网络就绪以配置IPv6",
		zap.String("containerName", config.ContainerName))
	if err := l.waitForContainerNetworkReady(config.ContainerName); err != nil {
		global.APP_LOG.Warn("等待容器网络就绪超时，继续尝试配置IPv6",
			zap.String("containerName", config.ContainerName),
			zap.Error(err))
	}

	// 处理IPv6网关配置
	if config.Gateway == "N" {
		l.handleIPv6Gateway(ctx, ipv6NetworkName)
	}

	// 设置IPv6连通性检查的cron任务
	cronCmd := "*/1 * * * * curl -m 6 -s ipv6.ip.sb && curl -m 6 -s ipv6.ip.sb"
	checkCronCmd := fmt.Sprintf("crontab -l | grep -q '%s'", cronCmd)
	_, err = l.sshClient.Execute(checkCronCmd)
	if err != nil {
		// cron任务不存在，添加它
		addCronCmd := fmt.Sprintf("echo '%s' | crontab -", cronCmd)
		l.sshClient.Execute(addCronCmd)
	}

	return containerIPv6, nil
}

// updateSysctl 更新sysctl配置
func (l *LXDProvider) updateSysctl(ctx context.Context, sysctlConfig string) error {
	parts := strings.Split(sysctlConfig, "=")
	if len(parts) != 2 {
		return fmt.Errorf("无效的sysctl配置: %s", sysctlConfig)
	}

	key := parts[0]
	value := parts[1]

	// 目标配置文件
	customConf := "/etc/sysctl.d/99-custom.conf"

	// 创建目录
	l.sshClient.Execute("mkdir -p /etc/sysctl.d")

	// 检查和更新配置文件
	checkCmd := fmt.Sprintf("grep -q \"^%s\" %s 2>/dev/null", sysctlConfig, customConf)
	_, err := l.sshClient.Execute(checkCmd)
	if err != nil {
		// 配置不存在，添加它
		addCmd := fmt.Sprintf("echo \"%s\" >> %s", sysctlConfig, customConf)
		l.sshClient.Execute(addCmd)
	}

	// 检查/etc/sysctl.conf并同步更新
	checkSysctlCmd := fmt.Sprintf("grep -q \"^%s\" /etc/sysctl.conf 2>/dev/null", sysctlConfig)
	_, err = l.sshClient.Execute(checkSysctlCmd)
	if err != nil {
		// 在/etc/sysctl.conf中不存在，添加
		addSysctlCmd := fmt.Sprintf("echo \"%s\" >> /etc/sysctl.conf", sysctlConfig)
		l.sshClient.Execute(addSysctlCmd)
	}

	// 立即应用配置
	applyCmd := fmt.Sprintf("sysctl -w \"%s=%s\"", key, value)
	_, err = l.sshClient.Execute(applyCmd)
	return err
}

// configureFirewallForIPv6 配置IPv6防火墙
func (l *LXDProvider) configureFirewallForIPv6(ctx context.Context, interfaceName string) {
	// 检查firewall-cmd是否可用
	_, err := l.sshClient.Execute("command -v firewall-cmd")
	if err == nil {
		trustedCmd := fmt.Sprintf("firewall-cmd --permanent --zone=trusted --add-interface=%s", interfaceName)
		l.sshClient.Execute(trustedCmd)
		l.sshClient.Execute("firewall-cmd --reload")
		return
	}

	// 检查ufw是否可用
	_, err = l.sshClient.Execute("command -v ufw")
	if err == nil {
		allowInCmd := fmt.Sprintf("ufw allow in on %s", interfaceName)
		allowOutCmd := fmt.Sprintf("ufw allow out on %s", interfaceName)
		l.sshClient.Execute(allowInCmd)
		l.sshClient.Execute(allowOutCmd)
		l.sshClient.Execute("ufw reload")
	}
}

// handleIPv6Gateway 处理IPv6网关配置
func (l *LXDProvider) handleIPv6Gateway(ctx context.Context, interfaceName string) {
	// 获取并删除fe80地址
	delIPCmd := fmt.Sprintf("ip -6 addr show dev %s | awk '/inet6 fe80/ {print $2}'", interfaceName)
	output, err := l.sshClient.Execute(delIPCmd)
	if err == nil {
		delIP := strings.TrimSpace(output)
		if delIP != "" {
			// 删除地址
			deleteCmd := fmt.Sprintf("ip addr del %s dev %s", delIP, interfaceName)
			l.sshClient.Execute(deleteCmd)

			// 创建删除脚本
			scriptContent := fmt.Sprintf("#!/bin/bash\nip addr del %s dev %s", delIP, interfaceName)
			createScriptCmd := fmt.Sprintf("echo '%s' > /usr/local/bin/remove_route.sh", scriptContent)
			l.sshClient.Execute(createScriptCmd)
			l.sshClient.Execute("chmod 777 /usr/local/bin/remove_route.sh")

			// 到crontab
			checkCronCmd := "crontab -l | grep -q '/usr/local/bin/remove_route.sh'"
			_, err := l.sshClient.Execute(checkCronCmd)
			if err != nil {
				addCronCmd := "echo '@reboot /usr/local/bin/remove_route.sh' | crontab -"
				l.sshClient.Execute(addCronCmd)
			}
		}
	}
}

// configureIPv6Network 主要的IPv6网络配置函数
func (l *LXDProvider) configureIPv6Network(ctx context.Context, containerName string, enableIPv6 bool, portMappingMethod string) error {
	if !enableIPv6 {
		global.APP_LOG.Info("IPv6未启用，跳过IPv6配置", zap.String("container", containerName))
		return nil
	}

	global.APP_LOG.Info("开始配置IPv6网络",
		zap.String("container", containerName),
		zap.String("portMappingMethod", portMappingMethod))

	// 首先检查宿主机是否有公网IPv6地址
	hostIPv6, err := l.checkIPv6(ctx)
	if err != nil {
		global.APP_LOG.Warn("宿主机不支持IPv6，自动跳过IPv6配置",
			zap.String("container", containerName),
			zap.Error(err))
		return nil // 宿主机不支持IPv6时，静默跳过IPv6配置，不返回错误
	}

	global.APP_LOG.Info("宿主机IPv6环境检查通过",
		zap.String("container", containerName),
		zap.String("hostIPv6", hostIPv6))

	// 获取IPv6网关信息
	gatewayInfo, err := l.getIPv6GatewayInfo(ctx)
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
		containerIPv6, err = l.setupNetworkDeviceIPv6(ctx, config)
		if err != nil {
			return fmt.Errorf("使用device_proxy方式配置IPv6网络失败: %w", err)
		}
	} else if config.UseIptables {
		// 使用iptables方式配置IPv6映射
		containerIPv6, err = l.setupIptablesIPv6(ctx, config)
		if err != nil {
			return fmt.Errorf("使用iptables方式配置IPv6网络失败: %w", err)
		}
	} else {
		// 默认使用device_proxy方式
		config.UseNetworkDevice = true
		containerIPv6, err = l.setupNetworkDeviceIPv6(ctx, config)
		if err != nil {
			return fmt.Errorf("配置IPv6网络失败: %w", err)
		}
	}

	// 保存IPv6地址到文件
	saveCmd := fmt.Sprintf("echo \"%s\" >> %s_v6", containerIPv6, containerName)
	l.sshClient.Execute(saveCmd)

	global.APP_LOG.Info("IPv6网络配置完成",
		zap.String("container", containerName),
		zap.String("ipv6", containerIPv6),
		zap.String("method", portMappingMethod))

	return nil
}

// setupIptablesIPv6 使用iptables方式配置IPv6映射
func (l *LXDProvider) setupIptablesIPv6(ctx context.Context, config IPv6Config) (string, error) {
	global.APP_LOG.Info("开始配置iptables IPv6映射",
		zap.String("container", config.ContainerName))

	// 安装必要的包
	l.sshClient.Execute("apt update -y 2>/dev/null || yum update -y 2>/dev/null || true")
	l.sshClient.Execute("apt install -y netfilter-persistent 2>/dev/null || yum install -y iptables-services 2>/dev/null || true")

	// 获取容器的内网IPv6地址
	containerIPv6, err := l.getContainerIPv6(ctx, config.ContainerName)
	if err != nil {
		return "", fmt.Errorf("获取容器IPv6地址失败: %w", err)
	}

	// 获取宿主机IPv6子网前缀
	subnetPrefix, err := l.getHostIPv6Prefix(ctx)
	if err != nil {
		return "", fmt.Errorf("获取IPv6子网前缀失败: %w", err)
	}

	// 获取IPv6子网长度
	ipv6LengthCmd := "ip addr show | awk '/inet6.*scope global/ { print $2 }' | head -n 1"
	output, err := l.sshClient.Execute(ipv6LengthCmd)
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
	interfaceOutput, err := l.sshClient.Execute(interfaceCmd)
	if err != nil {
		interfaceCmd = "ip route | grep default | awk '{print $5}' | head -1"
		interfaceOutput, _ = l.sshClient.Execute(interfaceCmd)
	}
	interfaceName := utils.CleanCommandOutput(interfaceOutput)
	if interfaceName == "" {
		return "", fmt.Errorf("无法获取网络接口名称")
	}

	global.APP_LOG.Info("网络配置信息",
		zap.String("interface", interfaceName),
		zap.String("subnetPrefix", subnetPrefix),
		zap.String("ipv6Length", ipv6Length),
		zap.String("containerIPv6", containerIPv6))

	// 查找可用的IPv6地址
	var mappedIPv6 string
	for i := 3; i <= 65535; i++ {
		testIPv6 := fmt.Sprintf("%s%d", subnetPrefix, i)

		// 跳过容器本身的地址
		if testIPv6 == containerIPv6 {
			continue
		}

		// 检查地址是否已被使用
		checkAddrCmd := fmt.Sprintf("ip -6 addr show dev %s | grep -q %s", interfaceName, testIPv6)
		_, err := l.sshClient.Execute(checkAddrCmd)
		if err == nil {
			// 地址已被使用，继续下一个
			continue
		}

		// 检查地址是否可以ping通
		pingCmd := fmt.Sprintf("ping6 -c1 -w1 -q %s", testIPv6)
		_, err = l.sshClient.Execute(pingCmd)
		if err == nil {
			// 地址能ping通，说明已被占用
			global.APP_LOG.Debug("IPv6地址已被占用", zap.String("ipv6", testIPv6))
			continue
		}

		// 检查是否已存在iptables规则
		checkRuleCmd := fmt.Sprintf("ip6tables -t nat -C PREROUTING -d %s -j DNAT --to-destination %s 2>/dev/null", testIPv6, containerIPv6)
		_, err = l.sshClient.Execute(checkRuleCmd)
		if err == nil {
			// 规则已存在
			continue
		}

		// 找到可用地址
		mappedIPv6 = testIPv6
		global.APP_LOG.Info("找到可用IPv6地址", zap.String("ipv6", mappedIPv6))
		break
	}

	if mappedIPv6 == "" {
		return "", fmt.Errorf("无可用IPv6地址，不进行自动映射")
	}

	// IPv6地址到接口
	addAddrCmd := fmt.Sprintf("ip addr add %s/%s dev %s", mappedIPv6, ipv6Length, interfaceName)
	_, err = l.sshClient.Execute(addAddrCmd)
	if err != nil {
		return "", fmt.Errorf("添加IPv6地址失败: %w", err)
	}

	// iptables NAT规则
	natRuleCmd := fmt.Sprintf("ip6tables -t nat -A PREROUTING -d %s -j DNAT --to-destination %s", mappedIPv6, containerIPv6)
	_, err = l.sshClient.Execute(natRuleCmd)
	if err != nil {
		return "", fmt.Errorf("添加ip6tables NAT规则失败: %w", err)
	}

	// 设置持久化服务和脚本
	err = l.setupPersistenceService(ctx)
	if err != nil {
		global.APP_LOG.Warn("设置持久化服务失败", zap.Error(err))
	}

	// 保存iptables规则
	err = l.saveIp6tablesRules(ctx)
	if err != nil {
		global.APP_LOG.Warn("保存ip6tables规则失败", zap.Error(err))
	}

	// 测试连通性
	err = l.testIPv6Connectivity(ctx, mappedIPv6, config.ContainerName)
	if err != nil {
		return "", fmt.Errorf("IPv6连通性测试失败: %w", err)
	}

	return mappedIPv6, nil
}

// setupPersistenceService 设置持久化服务
func (l *LXDProvider) setupPersistenceService(ctx context.Context) error {
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
		_, err := l.sshClient.Execute(testCmd)
		if err == nil {
			cdnSuccessUrl = cdnUrl
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 下载add-ipv6.sh脚本
	scriptPath := "/usr/local/bin/add-ipv6.sh"
	checkScriptCmd := fmt.Sprintf("[ -f %s ]", scriptPath)
	_, err := l.sshClient.Execute(checkScriptCmd)
	if err != nil {
		scriptUrl := cdnSuccessUrl + "https://raw.githubusercontent.com/oneclickvirt/lxd/main/scripts/add-ipv6.sh"
		downloadCmd := fmt.Sprintf("wget '%s' -O %s", scriptUrl, scriptPath)
		_, err := l.sshClient.Execute(downloadCmd)
		if err != nil {
			global.APP_LOG.Warn("下载add-ipv6.sh脚本失败", zap.Error(err))
		} else {
			l.sshClient.Execute(fmt.Sprintf("chmod +x %s", scriptPath))
		}
	}

	// 下载add-ipv6.service服务文件
	servicePath := "/etc/systemd/system/add-ipv6.service"
	checkServiceCmd := fmt.Sprintf("[ -f %s ]", servicePath)
	_, err = l.sshClient.Execute(checkServiceCmd)
	if err != nil {
		serviceUrl := cdnSuccessUrl + "https://raw.githubusercontent.com/oneclickvirt/lxd/main/scripts/add-ipv6.service"
		downloadCmd := fmt.Sprintf("wget '%s' -O %s", serviceUrl, servicePath)
		_, err := l.sshClient.Execute(downloadCmd)
		if err != nil {
			global.APP_LOG.Warn("下载add-ipv6.service服务文件失败", zap.Error(err))
		} else {
			l.sshClient.Execute(fmt.Sprintf("chmod +x %s", servicePath))
			l.sshClient.Execute("systemctl daemon-reload")
			l.sshClient.Execute("systemctl enable add-ipv6.service")
			l.sshClient.Execute("systemctl start add-ipv6.service")
		}
	}

	return nil
}

// saveIp6tablesRules 保存ip6tables规则
func (l *LXDProvider) saveIp6tablesRules(ctx context.Context) error {
	// 创建iptables目录
	l.sshClient.Execute("mkdir -p /etc/iptables")

	// 创建规则文件
	l.sshClient.Execute("touch /etc/iptables/rules.v6")

	// 保存当前规则
	_, err := l.sshClient.Execute("ip6tables-save > /etc/iptables/rules.v6")
	if err != nil {
		return fmt.Errorf("保存ip6tables规则失败: %w", err)
	}

	// 检查netfilter-persistent是否可用
	_, err = l.sshClient.Execute("command -v netfilter-persistent")
	if err == nil {
		l.sshClient.Execute("netfilter-persistent save")
		l.sshClient.Execute("netfilter-persistent reload")
		l.sshClient.Execute("service netfilter-persistent restart")
	}

	return nil
}

// testIPv6Connectivity 测试IPv6连通性
func (l *LXDProvider) testIPv6Connectivity(ctx context.Context, ipv6Addr, containerName string) error {
	global.APP_LOG.Info("测试IPv6连通性", zap.String("ipv6", ipv6Addr))

	testCmd := fmt.Sprintf("ping6 -c 3 %s", ipv6Addr)
	_, err := l.sshClient.Execute(testCmd)
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
