package incus

import (
	"context"
	"fmt"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// IPv6Config IPv6配置结构
type IPv6Config struct {
	ContainerName         string
	ContainerIPv6         string
	HostIPv6Prefix        string
	IPv6Length            int
	Interface             string
	Gateway               string
	UseIptables           bool
	UseNetworkDevice      bool
	RoutedCIDR            string
	RoutedGateway         string
	RoutedBridge          string
	RoutedTunnelInterface string
	InstanceType          string
}

// isPrivateIPv6 检查是否为私有IPv6地址
func (i *IncusProvider) isPrivateIPv6(address string) bool {
	return !utils.IsPublicIPv6(address)
}

// selectHostIPv6InterfaceNetwork keeps the selected address pool paired with
// its owning interface. This matters on PVE-style hosts where vmbr0 owns an
// IPv6 /128 default route while vmbr2 carries the delegated allocation prefix.
func (i *IncusProvider) selectHostIPv6InterfaceNetwork(ctx context.Context, requireAssignable bool) (utils.IPv6InterfaceNetwork, error) {
	preferredInterface := ""
	defaultRouteCmd := `ip -6 route show default 2>/dev/null | awk '{for (i=1; i<=NF; i++) if ($i=="dev" && i<NF) {print $(i+1); exit}}'`
	if output, err := i.sshClient.Execute(defaultRouteCmd); err == nil {
		preferredInterface, _ = utils.ParseFirstNetworkInterfaceOutput(output)
	}

	addressCmd := "ip -o -6 addr show scope global 2>/dev/null"
	output, err := i.sshClient.Execute(addressCmd)
	if err != nil {
		return utils.IPv6InterfaceNetwork{}, fmt.Errorf("获取本机IPv6接口地址失败: %w", err)
	}
	selected, err := utils.SelectPublicIPv6InterfaceNetwork(output, preferredInterface, requireAssignable)
	if err != nil {
		return utils.IPv6InterfaceNetwork{}, fmt.Errorf("%w: output=%s", err, utils.SanitizeUserInput(strings.TrimSpace(output)))
	}
	return selected, nil
}

// checkIPv6 检查并获取IPv6地址
func (i *IncusProvider) checkIPv6(ctx context.Context) (string, error) {
	// A routed container prefix must be present on this host. An egress API can
	// report an address owned by an upstream NAT or tunnel and is not valid input
	// for local IPv6 allocation.
	cmd := "ip -o -6 addr show scope global 2>/dev/null | awk '$0 !~ / tentative/ {print $4}'"
	output, err := i.sshClient.Execute(cmd)
	if err == nil {
		for _, ipv6 := range utils.ExtractIPv6Addresses(output) {
			if !i.isPrivateIPv6(ipv6) {
				global.APP_LOG.Debug("从本地接口获取到IPv6地址", zap.String("ipv6", ipv6))
				return ipv6, nil
			}
		}
	}
	return "", fmt.Errorf("未检测到本机绑定的有效公网IPv6地址")
}

// getContainerIPv6 获取容器内网IPv6地址
func (i *IncusProvider) getContainerIPv6(ctx context.Context, containerName string) (string, error) {
	cmd := fmt.Sprintf("incus list %s --format=json | jq -r '.[0].state.network.eth0.addresses[] | select(.family==\"inet6\") | select(.scope==\"global\") | .address'", shellSingleQuote(containerName))
	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取容器IPv6地址失败: %w", err)
	}

	ipv6, parseErr := utils.ParseFirstIPv6AddressOutput(output)
	if parseErr != nil {
		return "", fmt.Errorf("容器IPv6输出无效: %w", parseErr)
	}

	global.APP_LOG.Debug("获取到容器IPv6地址",
		zap.String("container", containerName),
		zap.String("ipv6", ipv6))
	return ipv6, nil
}

// GetInstanceIPv6 获取实例的内网IPv6地址 (公开方法)
func (i *IncusProvider) GetInstanceIPv6(ctx context.Context, instanceName string) (string, error) {
	return i.getContainerIPv6(ctx, instanceName)
}

// GetInstanceIPv4 获取实例的内网IPv4地址 (公开方法)
func (i *IncusProvider) GetInstanceIPv4(ctx context.Context, instanceName string) (string, error) {
	// 复用已有的getInstanceIP方法来获取内网IPv4地址
	return i.getInstanceIP(instanceName)
}

// GetInstancePublicIPv6 获取实例的公网IPv6地址
func (i *IncusProvider) GetInstancePublicIPv6(ctx context.Context, instanceName string) (string, error) {
	// 尝试从保存的IPv6文件中读取公网IPv6地址
	publicIPv6Cmd := fmt.Sprintf("cat %s 2>/dev/null | tail -1", shellSingleQuote(instanceName+"_v6"))
	publicIPv6Output, err := i.sshClient.Execute(publicIPv6Cmd)
	if err == nil {
		publicIPv6, parseErr := utils.ParseFirstIPv6AddressOutput(publicIPv6Output)
		if parseErr == nil && !i.isPrivateIPv6(publicIPv6) {
			global.APP_LOG.Debug("从文件获取到公网IPv6地址",
				zap.String("instanceName", instanceName),
				zap.String("publicIPv6", publicIPv6))
			return publicIPv6, nil
		}
	}

	// 如果文件中没有，尝试从eth1网络设备获取
	eth1Cmd := fmt.Sprintf("incus list %s --format json | jq -r '.[0].state.network.eth1.addresses[]? | select(.family==\"inet6\" and .scope==\"global\") | .address' 2>/dev/null", shellSingleQuote(instanceName))
	eth1Output, err := i.sshClient.Execute(eth1Cmd)
	if err == nil {
		eth1IPv6, parseErr := utils.ParseFirstIPv6AddressOutput(eth1Output)
		if parseErr == nil && !i.isPrivateIPv6(eth1IPv6) {
			global.APP_LOG.Debug("从eth1获取到公网IPv6地址",
				zap.String("instanceName", instanceName),
				zap.String("publicIPv6", eth1IPv6))
			return eth1IPv6, nil
		}
	}

	// 如果都没有获取到，返回空（表示没有公网IPv6）
	return "", fmt.Errorf("实例未分配公网IPv6地址")
}

// GetVethInterfaceName 获取容器对应的宿主机veth接口名称（IPv4）
// 通过 incus config show 获取 volatile.eth0.host_name
func (i *IncusProvider) GetVethInterfaceName(ctx context.Context, instanceName string) (string, error) {
	cmd := fmt.Sprintf("incus config show %s | grep 'volatile.eth0.host_name:' | awk '{print $2}'", shellSingleQuote(instanceName))
	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取veth接口名称失败: %w", err)
	}

	vethName, parseErr := utils.ParseFirstNetworkInterfaceOutput(output)
	if parseErr != nil {
		return "", fmt.Errorf("veth接口名称输出无效: %w", parseErr)
	}

	global.APP_LOG.Debug("获取到veth接口名称",
		zap.String("instanceName", instanceName),
		zap.String("vethInterface", vethName))

	return vethName, nil
}

// GetVethInterfaceNameV6 获取容器对应的宿主机veth接口名称（IPv6）
// 通过 incus config show 获取 volatile.eth1.host_name（如果存在）
func (i *IncusProvider) GetVethInterfaceNameV6(ctx context.Context, instanceName string) (string, error) {
	cmd := fmt.Sprintf("incus config show %s | grep 'volatile.eth1.host_name:' | awk '{print $2}'", shellSingleQuote(instanceName))
	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("获取veth接口名称(IPv6)失败: %w", err)
	}

	if strings.TrimSpace(output) == "" {
		// 如果没有eth1，可能使用eth0，返回eth0的veth接口
		return i.GetVethInterfaceName(ctx, instanceName)
	}
	vethName, parseErr := utils.ParseFirstNetworkInterfaceOutput(output)
	if parseErr != nil {
		return "", fmt.Errorf("IPv6 veth接口名称输出无效: %w", parseErr)
	}

	global.APP_LOG.Debug("获取到veth接口名称(IPv6)",
		zap.String("instanceName", instanceName),
		zap.String("vethInterface", vethName))

	return vethName, nil
}

// getHostIPv6Prefix 获取宿主机IPv6子网前缀
func (i *IncusProvider) getHostIPv6Prefix(ctx context.Context) (string, error) {
	selected, err := i.selectHostIPv6InterfaceNetwork(ctx, false)
	if err != nil {
		return "", fmt.Errorf("无IPv6子网: %w", err)
	}

	prefix := selected.Network.CIDR()
	global.APP_LOG.Debug("获取到IPv6子网前缀", zap.String("prefix", prefix))
	return prefix, nil
}

// getIPv6GatewayInfo 获取IPv6网关信息
func (i *IncusProvider) getIPv6GatewayInfo(ctx context.Context) (string, error) {
	cmd := "ip -6 route show | awk '/default via/{print $3}'"
	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		return "N", fmt.Errorf("获取IPv6网关信息失败: %w", err)
	}

	gateways := utils.ExtractIPv6Addresses(output)
	if len(gateways) == 0 {
		return "N", nil
	}
	for _, gateway := range gateways {
		if !strings.HasPrefix(gateway, "fe80:") {
			return "N", nil
		}
	}
	if strings.HasPrefix(gateways[0], "fe80:") {
		return "Y", nil
	}
	return "N", nil
}

// installSipcalc 安装sipcalc工具
func (i *IncusProvider) installSipcalc(ctx context.Context) error {
	// 检查是否已安装
	_, err := i.sshClient.Execute("command -v sipcalc")
	if err == nil {
		return nil // 已安装
	}

	global.APP_LOG.Debug("开始安装sipcalc工具")

	// 检测OS类型
	osCmd := "cat /etc/os-release | grep ^ID= | cut -d= -f2 | tr -d '\"'"
	osOutput, err := i.sshClient.Execute(osCmd)
	if err != nil {
		return fmt.Errorf("检测操作系统失败: %w", err)
	}

	osType := utils.CleanCommandOutput(osOutput)
	global.APP_LOG.Debug("检测到操作系统类型", zap.String("os", osType))

	switch osType {
	case "centos", "almalinux", "rocky":
		return i.installSipcalcRHEL(ctx)
	case "ubuntu", "debian":
		return i.installSipcalcDebian(ctx)
	case "arch":
		_, err := i.sshClient.Execute("pacman -S --noconfirm --needed sipcalc")
		return err
	default:
		// 尝试通用方法
		_, err := i.sshClient.Execute("apt update -y && apt install -y sipcalc")
		if err != nil {
			_, err = i.sshClient.Execute("yum install -y sipcalc")
		}
		return err
	}
}

// installSipcalcRHEL 在RHEL系列系统上安装sipcalc
func (i *IncusProvider) installSipcalcRHEL(ctx context.Context) error {
	// 获取架构
	archCmd := "uname -m"
	archOutput, err := i.sshClient.Execute(archCmd)
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
		_, err := i.sshClient.Execute(downloadCmd)
		if err == nil {
			break
		}
	}

	// 安装rpm包
	installCmd := fmt.Sprintf("rpm -ivh %s", filename)
	_, err = i.sshClient.Execute(installCmd)
	if err != nil {
		// 尝试使用dnf/yum安装
		_, err = i.sshClient.Execute("dnf install -y " + filename)
		if err != nil {
			_, err = i.sshClient.Execute("yum install -y " + filename)
		}
	}

	// 清理下载的文件
	i.sshClient.Execute("rm -f " + filename)

	return err
}

// installSipcalcDebian 在Debian系列系统上安装sipcalc
func (i *IncusProvider) installSipcalcDebian(ctx context.Context) error {
	updateCmd := "apt update -y"
	_, err := i.sshClient.Execute(updateCmd)
	if err != nil {
		global.APP_LOG.Warn("apt update失败", zap.Error(err))
	}

	installCmd := "apt install -y sipcalc"
	_, err = i.sshClient.Execute(installCmd)
	return err
}

// setupNetworkDeviceIPv6 配置网络设备方式的IPv6
