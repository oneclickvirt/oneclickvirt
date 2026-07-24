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

	// 获取本机IPv6网络信息
	if _, err := i.checkIPv6(ctx); err != nil {
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
		cmd := fmt.Sprintf("ip -6 addr show dev %s scope global | awk '$1==\"inet6\" {print $2; exit}'",
			shellSingleQuote(ipv6NetworkName))
		output, err := i.sshClient.Execute(cmd)
		if err == nil {
			ipNetworkGam = output
		}
	} else {
		// 获取默认网络接口
		cmd := "ls /sys/class/net/ | grep -v \"$(ls /sys/devices/virtual/net/)\" | head -n 1"
		output, err := i.sshClient.Execute(cmd)
		if err != nil {
			return "", fmt.Errorf("获取网络接口失败: %w", err)
		}
		ipv6NetworkName, err = utils.ParseNetworkInterfaceOutput(output)
		if err != nil {
			return "", fmt.Errorf("解析网络接口名称失败: %w", err)
		}

		cmd = fmt.Sprintf("ip -6 addr show dev %s scope global | awk '$1==\"inet6\" {print $2; exit}'", shellSingleQuote(ipv6NetworkName))
		output, err = i.sshClient.Execute(cmd)
		if err == nil {
			ipNetworkGam = output
		}
	}

	network, err := utils.ParseSingleIPv6NetworkOutput(ipNetworkGam, 64)
	if err != nil {
		return "", fmt.Errorf("无法获取本地IPv6网络配置: %w", err)
	}

	global.APP_LOG.Debug("本地IPv6网络", zap.String("network", network.CIDR()))

	if err := i.configureIPv6Sysctls(ipv6NetworkName); err != nil {
		return "", fmt.Errorf("配置IPv6 sysctl失败: %w", err)
	}

	containerIPv6 := ""
	if strings.TrimSpace(config.ContainerIPv6) != "" {
		containerIPv6, err = utils.NormalizeIPv6Address(config.ContainerIPv6)
		if err != nil {
			return "", fmt.Errorf("静态IPv6地址无效: %w", err)
		}
	} else {
		// 只使用经过解析的网络地址，不把远端命令的多行诊断文本拼进前缀。
		randBitsCmd := "od -An -N2 -t x1 /dev/urandom | tr -d '[:space:]'"
		output, err = i.sshClient.Execute(randBitsCmd)
		if err != nil {
			return "", fmt.Errorf("生成随机数失败: %w", err)
		}
		randBits, parseErr := utils.ParseHexUint64(output)
		if parseErr != nil {
			return "", fmt.Errorf("解析随机数失败: %w", parseErr)
		}
		containerIPv6, err = utils.IPv6AddressWithSuffix(network, randBits)
		if err != nil {
			return "", fmt.Errorf("生成容器IPv6地址失败: %w", err)
		}
	}

	global.APP_LOG.Debug("生成容器IPv6地址",
		zap.String("container", config.ContainerName),
		zap.String("ipv6", containerIPv6))

	// 停止容器
	stopCmd := fmt.Sprintf("incus stop %s", shellSingleQuote(config.ContainerName))
	i.sshClient.Execute(stopCmd)
	time.Sleep(3 * time.Second)

	instanceArg := shellSingleQuote(config.ContainerName)
	parentArg := shellSingleQuote(ipv6NetworkName)
	addressArg := shellSingleQuote(containerIPv6)
	deviceCmd := fmt.Sprintf(`set -eu
if incus config device get %s eth1 type >/dev/null 2>&1; then
  incus config device set %s eth1 nictype routed
  incus config device set %s eth1 parent %s
  incus config device set %s eth1 ipv6.address %s
else
  incus config device add %s eth1 nic nictype=routed parent=%s ipv6.address=%s
fi`, instanceArg, instanceArg, instanceArg, parentArg, instanceArg, addressArg, instanceArg, parentArg, addressArg)
	_, err = i.sshClient.Execute(deviceCmd)
	if err != nil {
		return "", fmt.Errorf("添加IPv6网络设备失败: %w", err)
	}

	time.Sleep(3 * time.Second)

	// 配置防火墙
	i.configureFirewallForIPv6(ctx, ipv6NetworkName)

	// 启动容器
	startCmd := fmt.Sprintf("incus start %s", shellSingleQuote(config.ContainerName))
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

// configureIPv6Sysctls writes one clean, dedicated sysctl file. The
// interface-specific key is persisted only when its procfs knob exists.
func (i *IncusProvider) configureIPv6Sysctls(interfaceName string) error {
	if strings.TrimSpace(interfaceName) == "" || utils.SanitizeShellArg(interfaceName) != interfaceName {
		return fmt.Errorf("无效的IPv6网络接口: %q", interfaceName)
	}
	quotedInterface := shellSingleQuote(interfaceName)
	command := fmt.Sprintf(`set -eu
conf=/etc/sysctl.d/99-oneclickvirt-ipv6.conf
mkdir -p /etc/sysctl.d
tmp="${conf}.tmp.$$"
{
  printf 'net.ipv6.conf.all.forwarding=1\n'
  printf 'net.ipv6.conf.all.proxy_ndp=1\n'
  if [ -e /proc/sys/net/ipv6/conf/%s/proxy_ndp ]; then
    printf 'net.ipv6.conf.%%s.proxy_ndp=1\n' %s
  fi
} > "$tmp"
chmod 0644 "$tmp"
mv "$tmp" "$conf"
sysctl -w net.ipv6.conf.all.forwarding=1 >/dev/null
sysctl -w net.ipv6.conf.all.proxy_ndp=1 >/dev/null
if [ -e /proc/sys/net/ipv6/conf/%s/proxy_ndp ]; then
  sysctl -w "net.ipv6.conf.%s.proxy_ndp=1" >/dev/null
fi`, quotedInterface, quotedInterface, quotedInterface, interfaceName)
	_, err := i.sshClient.Execute(command)
	return err
}

// configureFirewallForIPv6 配置IPv6防火墙
func (i *IncusProvider) configureFirewallForIPv6(ctx context.Context, interfaceName string) {
	// 检查防火墙类型并配置
	if i.hasFirewalld() {
		trustedCmd := fmt.Sprintf("firewall-cmd --permanent --zone=trusted --add-interface=%s", shellSingleQuote(interfaceName))
		i.sshClient.Execute(trustedCmd)
		i.sshClient.Execute("firewall-cmd --reload")
	} else if i.hasUfw() {
		allowInCmd := fmt.Sprintf("ufw allow in on %s", shellSingleQuote(interfaceName))
		allowOutCmd := fmt.Sprintf("ufw allow out on %s", shellSingleQuote(interfaceName))
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
		delIP, parseErr := utils.ParseSingleIPv6AddressOutput(output)
		if parseErr != nil {
			if strings.TrimSpace(output) != "" {
				global.APP_LOG.Warn("解析待删除的链路本地IPv6地址失败", zap.Error(parseErr))
			}
		} else {
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
func (i *IncusProvider) configureIPv6Network(ctx context.Context, containerName string, enableIPv6 bool, portMappingMethod, requestedIPv6 string) error {
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
		return fmt.Errorf("宿主机IPv6环境不可用: %w", err)
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
		ContainerIPv6:    requestedIPv6,
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

	// 保存单一的规范地址，避免重试时产生多行污染。
	saveCmd := fmt.Sprintf("printf '%%s\\n' %s > %s", shellSingleQuote(containerIPv6), shellSingleQuote(containerName+"_v6"))
	if _, err := i.sshClient.Execute(saveCmd); err != nil {
		return fmt.Errorf("保存实例IPv6地址失败: %w", err)
	}

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
	network, err := utils.ParseIPv6Network(subnetPrefix, 64)
	if err != nil {
		return "", fmt.Errorf("解析IPv6子网失败: %w", err)
	}
	ipv6Length := fmt.Sprintf("%d", network.PrefixLen)

	// 获取网络接口名称
	interfaceCmd := "lshw -C network | awk '/logical name:/{print $3}' | head -1"
	interfaceOutput, err := i.sshClient.Execute(interfaceCmd)
	if err != nil {
		interfaceCmd = "ip route | grep default | awk '{print $5}' | head -1"
		interfaceOutput, _ = i.sshClient.Execute(interfaceCmd)
	}
	interfaceName, parseErr := utils.ParseNetworkInterfaceOutput(interfaceOutput)
	if parseErr != nil {
		return "", fmt.Errorf("无法获取网络接口名称: %w", parseErr)
	}

	global.APP_LOG.Debug("网络配置信息",
		zap.String("interface", interfaceName),
		zap.String("subnetPrefix", subnetPrefix),
		zap.String("ipv6Length", ipv6Length),
		zap.String("containerIPv6", containerIPv6))

	var mappedIPv6 string
	if strings.TrimSpace(config.ContainerIPv6) != "" {
		mappedIPv6, err = utils.NormalizeIPv6Address(config.ContainerIPv6)
		if err != nil {
			return "", fmt.Errorf("静态IPv6地址无效: %w", err)
		}
		ipv6Length = "128"
	} else {
		snapshotCmd := fmt.Sprintf("{ ip -6 addr show dev %s; ip -6 neigh show dev %s; ip6tables -t nat -S PREROUTING; } 2>/dev/null || true", shellSingleQuote(interfaceName), shellSingleQuote(interfaceName))
		snapshot, snapshotErr := i.sshClient.Execute(snapshotCmd)
		if snapshotErr != nil {
			return "", fmt.Errorf("读取IPv6占用快照失败: %w", snapshotErr)
		}
		occupied := utils.ExtractIPv6Addresses(snapshot)
		occupied = append(occupied, containerIPv6)
		mappedIPv6, err = utils.FirstAvailableIPv6(network, occupied, 3, 65533)
		if err != nil {
			return "", fmt.Errorf("无可用IPv6地址，不进行自动映射: %w", err)
		}
	}

	if mappedIPv6 == "" {
		return "", fmt.Errorf("无可用IPv6地址，不进行自动映射")
	}

	// IPv6地址到接口
	addAddrCmd := fmt.Sprintf("ip -6 addr replace %s/%s dev %s", shellSingleQuote(mappedIPv6), ipv6Length, shellSingleQuote(interfaceName))
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
		natRuleCmd := fmt.Sprintf("firewall-cmd --direct --query-rule ipv6 nat PREROUTING 0 -d %s -j DNAT --to-destination %s >/dev/null 2>&1 || firewall-cmd --permanent --direct --add-rule ipv6 nat PREROUTING 0 -d %s -j DNAT --to-destination %s", shellSingleQuote(mappedIPv6), shellSingleQuote(containerIPv6), shellSingleQuote(mappedIPv6), shellSingleQuote(containerIPv6))
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
		natRuleCmd := fmt.Sprintf("ip6tables -t nat -C PREROUTING -d %s -j DNAT --to-destination %s 2>/dev/null || ip6tables -t nat -A PREROUTING -d %s -j DNAT --to-destination %s", shellSingleQuote(mappedIPv6), shellSingleQuote(containerIPv6), shellSingleQuote(mappedIPv6), shellSingleQuote(containerIPv6))
		_, err = i.sshClient.Execute(natRuleCmd)
		if err != nil {
			return "", fmt.Errorf("添加ip6tables NAT规则失败: %w", err)
		}
	}

	// 设置持久化服务和脚本
	err = i.setupPersistenceServiceIncus(ctx)
	if err != nil {
		return "", fmt.Errorf("设置IPv6规则持久化服务失败: %w", err)
	}

	// 保存规则
	err = i.saveNetfilterRules(ctx, useFirewalld)
	if err != nil {
		return "", fmt.Errorf("保存IPv6防火墙规则失败: %w", err)
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
