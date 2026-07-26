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

func summarizeIPv6ProbeOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "<empty>"
	}
	return utils.SanitizeUserInput(output)
}

func (l *LXDProvider) setupNetworkDeviceIPv6(ctx context.Context, config IPv6Config) (string, error) {
	global.APP_LOG.Debug("开始配置网络设备IPv6",
		zap.String("container", config.ContainerName))

	// 获取本机IPv6网络信息
	if _, err := l.checkIPv6(ctx); err != nil {
		return "", fmt.Errorf("检查IPv6失败: %w", err)
	}

	// 确定IPv6网络接口
	var ipv6NetworkName string
	var ipNetworkGam string
	var networkProbeCommand string
	var networkProbeOutput string
	var networkProbeErr error

	// 检查是否有he-ipv6接口
	heIPv6Check := "ip -f inet6 addr | grep -q 'he-ipv6' && echo 'found' || echo 'not_found'"
	output, err := l.sshClient.Execute(heIPv6Check)
	heIPv6Status, heIPv6ParseErr := utils.ParseFirstCommandLineMatching(output, func(value string) bool {
		return value == "found" || value == "not_found"
	})
	if err == nil && heIPv6ParseErr == nil && heIPv6Status == "found" {
		ipv6NetworkName = "he-ipv6"
		cmd := fmt.Sprintf("ip -6 addr show dev %s scope global | awk '$1==\"inet6\" {print $2; exit}'",
			shellSingleQuote(ipv6NetworkName))
		networkProbeCommand = cmd
		networkProbeOutput, networkProbeErr = l.sshClient.Execute(cmd)
		if networkProbeErr == nil {
			ipNetworkGam = networkProbeOutput
		}
	} else {
		// Prefer the interface carrying the IPv6 default route. The first
		// physical interface is often IPv4-only while a tunnel (for example
		// sit0/he-ipv6) carries the configured IPv6 pool.
		cmd := `iface="$(ip -6 route show default 2>/dev/null | awk '{for (i=1; i<=NF; i++) if ($i=="dev" && i<NF) {print $(i+1); exit}}')"
if [ -z "$iface" ]; then
  iface="$(ip -o -6 addr show scope global 2>/dev/null | awk 'NR==1 {print $2}')"
fi
if [ -z "$iface" ]; then
  iface="$(ls /sys/class/net/ | grep -v "$(ls /sys/devices/virtual/net/)" | head -n 1)"
fi
printf '%s\n' "$iface"`
		output, err := l.sshClient.Execute(cmd)
		if err != nil {
			return "", fmt.Errorf("获取网络接口失败: output=%s: %w", summarizeIPv6ProbeOutput(output), err)
		}
		ipv6NetworkName, err = utils.ParseFirstNetworkInterfaceOutput(output)
		if err != nil {
			return "", fmt.Errorf("解析网络接口名称失败: output=%s: %w", summarizeIPv6ProbeOutput(output), err)
		}

		cmd = fmt.Sprintf("ip -6 addr show dev %s scope global | awk '$1==\"inet6\" {print $2; exit}'", shellSingleQuote(ipv6NetworkName))
		networkProbeCommand = cmd
		networkProbeOutput, networkProbeErr = l.sshClient.Execute(cmd)
		if networkProbeErr == nil {
			ipNetworkGam = networkProbeOutput
		}
	}

	requestedIPv6 := strings.TrimSpace(config.ContainerIPv6)
	containerIPv6 := ""
	if requestedIPv6 != "" {
		containerIPv6, err = utils.NormalizeIPv6Address(requestedIPv6)
		if err != nil {
			return "", fmt.Errorf("静态IPv6地址无效: %w", err)
		}
	}
	var network utils.IPv6Network
	if requestedIPv6 == "" {
		if networkProbeErr != nil {
			return "", fmt.Errorf("获取本地IPv6网络配置命令失败: interface=%s command=%s output=%s: %w",
				ipv6NetworkName, networkProbeCommand, summarizeIPv6ProbeOutput(networkProbeOutput), networkProbeErr)
		}
		network, err = utils.ParseFirstIPv6NetworkOutput(ipNetworkGam, 64)
		if err != nil {
			return "", fmt.Errorf("无法获取本地IPv6网络配置: interface=%s command=%s output=%s: %w",
				ipv6NetworkName, networkProbeCommand, summarizeIPv6ProbeOutput(networkProbeOutput), err)
		}
		global.APP_LOG.Debug("本地IPv6网络", zap.String("network", network.CIDR()))
	} else if networkProbeErr != nil || strings.TrimSpace(ipNetworkGam) == "" {
		global.APP_LOG.Debug("已提供静态IPv6地址，跳过宿主机接口地址解析失败",
			zap.String("interface", ipv6NetworkName),
			zap.String("command", networkProbeCommand),
			zap.String("output", summarizeIPv6ProbeOutput(networkProbeOutput)),
			zap.Error(networkProbeErr))
	} else if discovered, parseErr := utils.ParseFirstIPv6NetworkOutput(ipNetworkGam, 64); parseErr == nil {
		global.APP_LOG.Debug("检测到宿主机IPv6网络（静态地址模式）",
			zap.String("network", discovered.CIDR()))
	} else {
		global.APP_LOG.Debug("静态IPv6地址模式跳过宿主机IPv6网络解析",
			zap.String("interface", ipv6NetworkName),
			zap.String("command", networkProbeCommand),
			zap.String("output", summarizeIPv6ProbeOutput(networkProbeOutput)),
			zap.Error(parseErr))
	}

	if err := l.configureIPv6Sysctls(ipv6NetworkName); err != nil {
		return "", fmt.Errorf("配置IPv6 sysctl失败: %w", err)
	}

	if requestedIPv6 == "" {
		// 只使用经过解析的网络地址，不把远端命令的多行诊断文本拼进前缀。
		randBitsCmd := "od -An -N2 -t x1 /dev/urandom | tr -d '[:space:]'"
		output, err = l.sshClient.Execute(randBitsCmd)
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
	stopCmd := fmt.Sprintf("lxc stop %s", shellSingleQuote(config.ContainerName))
	l.sshClient.Execute(stopCmd)
	time.Sleep(3 * time.Second)

	// Add once, then update in place on task retries. This avoids turning an
	// already-created eth1 into a permanent retry failure.
	instanceArg := shellSingleQuote(config.ContainerName)
	parentArg := shellSingleQuote(ipv6NetworkName)
	addressArg := shellSingleQuote(containerIPv6)
	deviceCmd := fmt.Sprintf(`set -eu
if lxc config device get %s eth1 type >/dev/null 2>&1; then
  lxc config device set %s eth1 nictype routed
  lxc config device set %s eth1 parent %s
  lxc config device set %s eth1 ipv6.address %s
else
  lxc config device add %s eth1 nic nictype=routed parent=%s ipv6.address=%s
fi`, instanceArg, instanceArg, instanceArg, parentArg, instanceArg, addressArg, instanceArg, parentArg, addressArg)
	deviceOutput, err := l.sshClient.Execute(deviceCmd)
	if err != nil {
		return "", fmt.Errorf("添加IPv6网络设备失败: output=%s: %w", summarizeIPv6ProbeOutput(deviceOutput), err)
	}

	time.Sleep(3 * time.Second)

	// 配置防火墙
	l.configureFirewallForIPv6(ctx, ipv6NetworkName)

	// 启动容器
	startCmd := fmt.Sprintf("lxc start %s", shellSingleQuote(config.ContainerName))
	startOutput, err := l.sshClient.Execute(startCmd)
	if err != nil {
		return "", fmt.Errorf("启动容器失败: output=%s: %w", summarizeIPv6ProbeOutput(startOutput), err)
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

// configureIPv6Sysctls writes one clean, dedicated sysctl file. The
// interface-specific key is persisted only when its procfs knob exists, so a
// missing/renamed interface can never poison /etc/sysctl.conf.
func (l *LXDProvider) configureIPv6Sysctls(interfaceName string) error {
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
	_, err := l.sshClient.Execute(command)
	return err
}

// configureFirewallForIPv6 配置IPv6防火墙
func (l *LXDProvider) configureFirewallForIPv6(ctx context.Context, interfaceName string) {
	// 检查firewall-cmd是否可用
	_, err := l.sshClient.Execute("command -v firewall-cmd")
	if err == nil {
		trustedCmd := fmt.Sprintf("firewall-cmd --permanent --zone=trusted --add-interface=%s", shellSingleQuote(interfaceName))
		l.sshClient.Execute(trustedCmd)
		l.sshClient.Execute("firewall-cmd --reload")
		return
	}

	// 检查ufw是否可用
	_, err = l.sshClient.Execute("command -v ufw")
	if err == nil {
		allowInCmd := fmt.Sprintf("ufw allow in on %s", shellSingleQuote(interfaceName))
		allowOutCmd := fmt.Sprintf("ufw allow out on %s", shellSingleQuote(interfaceName))
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
		delIP, parseErr := utils.ParseFirstIPv6AddressOutput(output)
		if parseErr != nil {
			if strings.TrimSpace(output) != "" {
				global.APP_LOG.Warn("解析待删除的链路本地IPv6地址失败", zap.Error(parseErr))
			}
		} else {
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
func (l *LXDProvider) configureIPv6Network(ctx context.Context, containerName string, enableIPv6 bool, portMappingMethod, requestedIPv6 string) error {
	if !enableIPv6 {
		global.APP_LOG.Debug("IPv6未启用，跳过IPv6配置", zap.String("container", containerName))
		return nil
	}

	global.APP_LOG.Debug("开始配置IPv6网络",
		zap.String("container", containerName),
		zap.String("portMappingMethod", portMappingMethod))

	// 首先检查宿主机是否有公网IPv6地址
	hostIPv6, err := l.checkIPv6(ctx)
	if err != nil {
		return fmt.Errorf("宿主机IPv6环境不可用: %w", err)
	}

	global.APP_LOG.Debug("宿主机IPv6环境检查通过",
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
		ContainerIPv6:    requestedIPv6,
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

	// 保存单一的规范地址，避免重试时产生多行污染。
	saveCmd := fmt.Sprintf("printf '%%s\\n' %s > %s", shellSingleQuote(containerIPv6), shellSingleQuote(containerName+"_v6"))
	if _, err := l.sshClient.Execute(saveCmd); err != nil {
		return fmt.Errorf("保存实例IPv6地址失败: %w", err)
	}

	global.APP_LOG.Debug("IPv6网络配置完成",
		zap.String("container", containerName),
		zap.String("ipv6", containerIPv6),
		zap.String("method", portMappingMethod))

	return nil
}

// setupIptablesIPv6 使用iptables方式配置IPv6映射
func (l *LXDProvider) setupIptablesIPv6(ctx context.Context, config IPv6Config) (string, error) {
	global.APP_LOG.Debug("开始配置iptables IPv6映射",
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
	network, err := utils.ParseIPv6Network(subnetPrefix, 64)
	if err != nil {
		return "", fmt.Errorf("解析IPv6子网失败: %w", err)
	}
	ipv6Length := fmt.Sprintf("%d", network.PrefixLen)

	// 获取网络接口名称
	interfaceCmd := "lshw -C network | awk '/logical name:/{print $3}' | head -1"
	interfaceOutput, err := l.sshClient.Execute(interfaceCmd)
	if err != nil {
		interfaceCmd = "ip route | grep default | awk '{print $5}' | head -1"
		interfaceOutput, _ = l.sshClient.Execute(interfaceCmd)
	}
	interfaceName, parseErr := utils.ParseFirstNetworkInterfaceOutput(interfaceOutput)
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
		// One remote snapshot replaces the previous per-candidate addr/ping/rule
		// probes. Selection is entirely local and bounded after this call.
		snapshotCmd := fmt.Sprintf("{ ip -6 addr show dev %s; ip -6 neigh show dev %s; ip6tables -t nat -S PREROUTING; } 2>/dev/null || true", shellSingleQuote(interfaceName), shellSingleQuote(interfaceName))
		snapshot, snapshotErr := l.sshClient.Execute(snapshotCmd)
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
	_, err = l.sshClient.Execute(addAddrCmd)
	if err != nil {
		return "", fmt.Errorf("添加IPv6地址失败: %w", err)
	}

	// iptables NAT规则
	natRuleCmd := fmt.Sprintf("ip6tables -t nat -C PREROUTING -d %s -j DNAT --to-destination %s 2>/dev/null || ip6tables -t nat -A PREROUTING -d %s -j DNAT --to-destination %s", shellSingleQuote(mappedIPv6), shellSingleQuote(containerIPv6), shellSingleQuote(mappedIPv6), shellSingleQuote(containerIPv6))
	_, err = l.sshClient.Execute(natRuleCmd)
	if err != nil {
		return "", fmt.Errorf("添加ip6tables NAT规则失败: %w", err)
	}

	// 设置持久化服务和脚本
	err = l.setupPersistenceService(ctx)
	if err != nil {
		return "", fmt.Errorf("设置IPv6规则持久化服务失败: %w", err)
	}

	// 保存iptables规则
	err = l.saveIp6tablesRules(ctx)
	if err != nil {
		return "", fmt.Errorf("保存ip6tables规则失败: %w", err)
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
	global.APP_LOG.Debug("测试IPv6连通性", zap.String("ipv6", ipv6Addr))

	testCmd := fmt.Sprintf("ping6 -c 3 %s", ipv6Addr)
	_, err := l.sshClient.Execute(testCmd)
	if err != nil {
		global.APP_LOG.Error("IPv6映射失败",
			zap.String("container", containerName),
			zap.String("ipv6", ipv6Addr))
		return fmt.Errorf("映射失败")
	}

	global.APP_LOG.Debug("IPv6映射成功",
		zap.String("container", containerName),
		zap.String("ipv6", ipv6Addr))

	return nil
}
