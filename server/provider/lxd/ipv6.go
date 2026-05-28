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
	global.APP_LOG.Debug("配置IPv6网络",
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
	ipv6NetworkCmd := fmt.Sprintf("lxc config device override %s eth0 ipv6.address=auto", shellSingleQuote(instanceName))
	_, err := l.sshClient.Execute(ipv6NetworkCmd)
	if err != nil {
		return fmt.Errorf("配置IPv6网络设备失败: %w", err)
	}

	// 2. 启用IPv6路由
	routeCmd := fmt.Sprintf("lxc config device override %s eth0 ipv6.routes=true", shellSingleQuote(instanceName))
	_, err = l.sshClient.Execute(routeCmd)
	if err != nil {
		return fmt.Errorf("配置IPv6路由失败: %w", err)
	}

	// 3. 在容器内启用IPv6
	enableIPv6Cmd := fmt.Sprintf("lxc exec %s -- bash -c 'echo 0 > /proc/sys/net/ipv6/conf/all/disable_ipv6'", shellSingleQuote(instanceName))
	_, err = l.sshClient.Execute(enableIPv6Cmd)
	if err != nil {
		global.APP_LOG.Warn("在容器内启用IPv6失败",
			zap.String("instance", instanceName),
			zap.Error(err))
		// 不阻断流程，可能容器还未完全启动
	}

	// 4. 重启网络接口
	restartNetworkCmd := fmt.Sprintf("lxc exec %s -- bash -c 'ip addr flush dev eth0 && dhclient -6 eth0'", shellSingleQuote(instanceName))
	_, err = l.sshClient.Execute(restartNetworkCmd)
	if err != nil {
		global.APP_LOG.Warn("重启网络接口失败",
			zap.String("instance", instanceName),
			zap.Error(err))
		// 不阻断流程
	}

	global.APP_LOG.Debug("IPv6网络配置成功",
		zap.String("instance", instanceName))

	return nil
}

// disableIPv6 禁用IPv6网络
func (l *LXDProvider) disableIPv6(instanceName string) error {
	// 1. 移除IPv6网络设备配置
	removeIPv6NetworkCmd := fmt.Sprintf("lxc config device unset %s eth0 ipv6.address", shellSingleQuote(instanceName))
	_, err := l.sshClient.Execute(removeIPv6NetworkCmd)
	if err != nil {
		global.APP_LOG.Warn("移除IPv6网络配置失败",
			zap.String("instance", instanceName),
			zap.Error(err))
		// 不阻断流程
	}

	// 2. 禁用IPv6路由
	disableRouteCmd := fmt.Sprintf("lxc config device unset %s eth0 ipv6.routes", shellSingleQuote(instanceName))
	_, err = l.sshClient.Execute(disableRouteCmd)
	if err != nil {
		global.APP_LOG.Warn("禁用IPv6路由失败",
			zap.String("instance", instanceName),
			zap.Error(err))
		// 不阻断流程
	}

	// 3. 在容器内禁用IPv6
	disableIPv6Cmd := fmt.Sprintf("lxc exec %s -- bash -c 'echo 1 > /proc/sys/net/ipv6/conf/all/disable_ipv6'", shellSingleQuote(instanceName))
	_, err = l.sshClient.Execute(disableIPv6Cmd)
	if err != nil {
		global.APP_LOG.Warn("在容器内禁用IPv6失败",
			zap.String("instance", instanceName),
			zap.Error(err))
		// 不阻断流程
	}

	global.APP_LOG.Debug("IPv6网络禁用成功",
		zap.String("instance", instanceName))

	return nil
}

// GetInstanceIPv6 获取实例的内网IPv6地址
func (l *LXDProvider) GetInstanceIPv6(instanceName string) (string, error) {
	// 获取实例的内网IPv6地址
	ipv6Cmd := fmt.Sprintf("lxc list %s --format json | jq -r '.[0].state.network.eth0.addresses[]? | select(.family==\"inet6\" and .scope==\"global\") | .address' 2>/dev/null", shellSingleQuote(instanceName))
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
	publicIPv6Cmd := fmt.Sprintf("cat %s 2>/dev/null | tail -1", shellSingleQuote(instanceName+"_v6"))
	publicIPv6Output, err := l.sshClient.Execute(publicIPv6Cmd)
	if err == nil {
		publicIPv6 := utils.CleanCommandOutput(publicIPv6Output)
		if publicIPv6 != "" && !l.isPrivateIPv6(publicIPv6) {
			global.APP_LOG.Debug("从文件获取到公网IPv6地址",
				zap.String("instanceName", instanceName),
				zap.String("publicIPv6", publicIPv6))
			return publicIPv6, nil
		}
	}

	// 如果文件中没有，尝试从eth1网络设备获取
	eth1Cmd := fmt.Sprintf("lxc list %s --format json | jq -r '.[0].state.network.eth1.addresses[]? | select(.family==\"inet6\" and .scope==\"global\") | .address' 2>/dev/null", shellSingleQuote(instanceName))
	eth1Output, err := l.sshClient.Execute(eth1Cmd)
	if err == nil {
		eth1IPv6 := utils.CleanCommandOutput(eth1Output)
		if eth1IPv6 != "" && !l.isPrivateIPv6(eth1IPv6) {
			global.APP_LOG.Debug("从eth1获取到公网IPv6地址",
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
	global.APP_LOG.Debug("配置IPv6 Profile",
		zap.String("profile", profileName),
		zap.Bool("enable", enable))

	if enable {
		// 为profile启用IPv6
		profileCmd := fmt.Sprintf("lxc profile device set %s eth0 ipv6.address auto", shellSingleQuote(profileName))
		_, err := l.sshClient.Execute(profileCmd)
		if err != nil {
			return fmt.Errorf("配置Profile IPv6失败: %w", err)
		}

		routeCmd := fmt.Sprintf("lxc profile device set %s eth0 ipv6.routes true", shellSingleQuote(profileName))
		_, err = l.sshClient.Execute(routeCmd)
		if err != nil {
			return fmt.Errorf("配置Profile IPv6路由失败: %w", err)
		}
	} else {
		// 为profile禁用IPv6
		unsetCmd := fmt.Sprintf("lxc profile device unset %s eth0 ipv6.address", shellSingleQuote(profileName))
		_, err := l.sshClient.Execute(unsetCmd)
		if err != nil {
			global.APP_LOG.Warn("移除Profile IPv6配置失败",
				zap.String("profile", profileName),
				zap.Error(err))
		}

		unsetRouteCmd := fmt.Sprintf("lxc profile device unset %s eth0 ipv6.routes", shellSingleQuote(profileName))
		_, err = l.sshClient.Execute(unsetRouteCmd)
		if err != nil {
			global.APP_LOG.Warn("移除Profile IPv6路由失败",
				zap.String("profile", profileName),
				zap.Error(err))
		}
	}

	global.APP_LOG.Debug("IPv6 Profile配置完成",
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
		"fd42:",    // Docker等使用的私有地址
	}

	for _, prefix := range privateRanges {
		if strings.HasPrefix(address, prefix) {
			return true
		}
	}

	// Teredo 前缀是 2001:0000::/32，不能把所有 2001:* 都视为私有地址。
	if strings.HasPrefix(address, "2001:0000:") || strings.HasPrefix(address, "2001:0:") {
		return true
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
			global.APP_LOG.Debug("从本地接口获取到IPv6地址", zap.String("ipv6", ipv6))
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
				global.APP_LOG.Debug("通过API获取到IPv6地址",
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
	cmd := fmt.Sprintf("lxc list %s --format=json | jq -r '.[0].state.network.eth0.addresses[] | select(.family==\"inet6\") | select(.scope==\"global\") | .address'", shellSingleQuote(containerName))
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取容器IPv6地址失败: %w", err)
	}

	ipv6 := strings.TrimSpace(output)
	if ipv6 == "" || ipv6 == "null" {
		return "", fmt.Errorf("容器无内网IPv6地址")
	}

	global.APP_LOG.Debug("获取到容器IPv6地址",
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
	global.APP_LOG.Debug("获取到IPv6子网前缀", zap.String("prefix", prefix))
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

	global.APP_LOG.Debug("开始安装sipcalc工具")

	// 检测OS类型
	osCmd := "cat /etc/os-release | grep ^ID= | cut -d= -f2 | tr -d '\"'"
	osOutput, err := l.sshClient.Execute(osCmd)
	if err != nil {
		return fmt.Errorf("检测操作系统失败: %w", err)
	}

	osType := utils.CleanCommandOutput(osOutput)
	global.APP_LOG.Debug("检测到操作系统类型", zap.String("os", osType))

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
		global.APP_LOG.Debug("尝试从镜像下载sipcalc", zap.String("mirror", mirror))
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
