package incus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
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

// configureNATIPv6Network keeps NAT IPv4+IPv6 instances on the managed
// bridge.  A NAT IPv6 address is the node's public address plus an Incus
// proxy to the guest ULA; attaching a routed /128 here incorrectly relies on
// upstream NDP for an address that many providers do not route to the host.
func (i *IncusProvider) configureNATIPv6Network(ctx context.Context, containerName, requestedIPv6 string) (string, error) {
	if strings.TrimSpace(requestedIPv6) != "" {
		return "", fmt.Errorf("NAT IPv6实例不接受独立公网IPv6地址，请使用独立IPv6或纯IPv6网络类型")
	}
	selected, err := i.selectHostIPv6InterfaceNetwork(ctx, false)
	if err != nil {
		return "", fmt.Errorf("宿主机IPv6网络不可用: %w", err)
	}
	if err := i.configureIPv6Sysctls(selected.Interface); err != nil {
		return "", fmt.Errorf("配置NAT IPv6宿主机sysctl失败: %w", err)
	}
	// Incus' IPv6 bridge NAT/proxy path depends on bridge netfilter.  Load and
	// persist it before mutating the network so a minimal VPS kernel cannot
	// report a successful configuration while silently dropping forwarded IPv6.
	if err := i.ensureBridgeNetfilter(); err != nil {
		return "", fmt.Errorf("配置NAT IPv6 bridge netfilter失败: %w", err)
	}
	bridgeCmd := fmt.Sprintf(`set -eu
bridge="$(incus config show %s --expanded | awk '/^  eth0:/{seen=1; next} seen && /^    network:/{print $2; exit} seen && /^[^ ]/{seen=0}')"
[ -n "$bridge" ]
address="$(incus network get "$bridge" ipv6.address 2>/dev/null || true)"
if [ -z "$address" ] || [ "$address" = "none" ]; then incus network set "$bridge" ipv6.address auto; fi
if [ "$(incus network get "$bridge" ipv6.dhcp 2>/dev/null || true)" != "true" ]; then incus network set "$bridge" ipv6.dhcp true; fi
if [ "$(incus network get "$bridge" ipv6.nat 2>/dev/null || true)" != "true" ]; then incus network set "$bridge" ipv6.nat true; fi`, shellSingleQuote(containerName))
	if output, execErr := i.sshClient.Execute(bridgeCmd); execErr != nil {
		return "", fmt.Errorf("初始化Incus NAT IPv6网桥失败: output=%s: %w", summarizeIPv6ProbeOutput(output), execErr)
	}
	if err := i.sshStartInstance(containerName); err != nil {
		return "", fmt.Errorf("启动NAT IPv6实例失败: %w", err)
	}
	if err := i.waitForContainerNetworkReady(containerName); err != nil {
		return "", fmt.Errorf("NAT IPv6实例网桥地址未就绪: %w", err)
	}
	guestIPv6, err := i.getContainerIPv6(ctx, containerName)
	if err != nil {
		return "", fmt.Errorf("获取NAT IPv6实例ULA失败: %w", err)
	}
	// NAT proxy targets must be static. Bind while the guest is still running,
	// because only then can Incus state.network pair the DHCPv6 address with
	// the exact profile/local NIC. The later proxy phase intentionally stops
	// the guest and can only verify this already-persisted binding.
	if err := utils.SetLXCAddressBinding(i.sshClient, "incus", containerName, guestIPv6, true); err != nil {
		return "", fmt.Errorf("固定NAT IPv6实例ULA失败: %w", err)
	}
	saveCmd := fmt.Sprintf("printf '%%s\\n' %s > %s", shellSingleQuote(guestIPv6), shellSingleQuote(containerName+"_v6"))
	if output, execErr := i.sshClient.Execute(saveCmd); execErr != nil {
		return "", fmt.Errorf("保存NAT IPv6实例地址失败: output=%s: %w", summarizeIPv6ProbeOutput(output), execErr)
	}
	return guestIPv6, nil
}

func (i *IncusProvider) setupNetworkDeviceIPv6(ctx context.Context, config IPv6Config) (string, error) {
	global.APP_LOG.Debug("开始配置网络设备IPv6",
		zap.String("container", config.ContainerName))

	// 获取本机IPv6网络信息
	if _, err := i.checkIPv6(ctx); err != nil {
		return "", fmt.Errorf("检查IPv6失败: %w", err)
	}

	requestedIPv6 := strings.TrimSpace(config.ContainerIPv6)
	containerIPv6 := ""
	if requestedIPv6 != "" {
		var err error
		containerIPv6, err = utils.NormalizeIPv6Address(requestedIPv6)
		if err != nil {
			return "", fmt.Errorf("静态IPv6地址无效: %w", err)
		}
	}

	selectedNetwork, err := i.selectHostIPv6InterfaceNetwork(ctx, requestedIPv6 == "")
	if err != nil {
		return "", fmt.Errorf("无法选择本机IPv6网络接口和前缀: %w", err)
	}
	ipv6NetworkName := selectedNetwork.Interface
	network := selectedNetwork.Network
	if requestedIPv6 != "" {
		global.APP_LOG.Debug("检测到宿主机IPv6网络（静态地址模式）",
			zap.String("interface", ipv6NetworkName), zap.String("network", network.CIDR()))
	} else {
		global.APP_LOG.Debug("本地IPv6网络", zap.String("interface", ipv6NetworkName), zap.String("network", network.CIDR()))
	}

	if err := i.configureIPv6Sysctls(ipv6NetworkName); err != nil {
		return "", fmt.Errorf("配置IPv6 sysctl失败: %w", err)
	}
	// Incus' NAT proxy path for an IPv6 bridge depends on bridge netfilter.
	// Some minimal VPS kernels ship the module but do not load it by default;
	// without this guard the proxy device is accepted and nftables rules are
	// created, yet every packet is dropped before it reaches the guest.
	if err := i.ensureBridgeNetfilter(); err != nil {
		return "", fmt.Errorf("IPv6 bridge netfilter不可用: %w", err)
	}

	if requestedIPv6 == "" {
		// 只使用经过解析的网络地址，不把远端命令的多行诊断文本拼进前缀。
		randBitsCmd := "od -An -N2 -t x1 /dev/urandom | tr -d '[:space:]'"
		output, err := i.sshClient.Execute(randBitsCmd)
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

	// Stop through the state-aware helper.  A plain ignored `incus stop` can
	// leave a running instance untouched while the device mutation continues,
	// producing a half-applied network configuration.  The helper tolerates an
	// already stopped instance but propagates transport and runtime failures.
	if err := i.sshStopInstance(config.ContainerName); err != nil {
		return "", fmt.Errorf("停止容器进行IPv6配置失败: %w", err)
	}
	time.Sleep(3 * time.Second)

	instanceArg := shellSingleQuote(config.ContainerName)
	parentArg := shellSingleQuote(ipv6NetworkName)
	addressArg := shellSingleQuote(containerIPv6)
	deviceCmd := fmt.Sprintf(`set -eu
if incus config device get %s eth1 type >/dev/null 2>&1; then
  existing_type="$(incus config device get %s eth1 type)"
  existing_nictype="$(incus config device get %s eth1 nictype 2>/dev/null || true)"
  if [ "$existing_type" != "nic" ] || [ "$existing_nictype" != "routed" ]; then
    printf 'refusing to replace existing eth1: type=%%s nictype=%%s\n' "$existing_type" "$existing_nictype" >&2
    exit 1
  fi
  if ! { \
    incus config device set %s eth1 nictype routed && \
    incus config device set %s eth1 parent %s && \
	  incus config device set %s eth1 ipv6.address %s && \
	  incus config device set %s eth1 ipv6.gateway auto; \
  }; then
    # Profile-inherited devices cannot always be changed with set; create a
    # local override while retaining the type guard above.
    incus config device override %s eth1 nictype=routed parent=%s ipv6.address=%s ipv6.gateway=auto
  fi
else
	  incus config device add %s eth1 nic nictype=routed parent=%s ipv6.address=%s ipv6.gateway=auto
fi`, instanceArg, instanceArg, instanceArg, instanceArg, instanceArg, parentArg, instanceArg, addressArg, instanceArg, instanceArg, parentArg, addressArg, instanceArg, parentArg, addressArg)
	deviceOutput, err := i.sshClient.Execute(deviceCmd)
	if err != nil {
		return "", fmt.Errorf("添加IPv6网络设备失败: output=%s: %w", summarizeIPv6ProbeOutput(deviceOutput), err)
	}

	time.Sleep(3 * time.Second)

	// 配置防火墙
	i.configureFirewallForIPv6(ctx, ipv6NetworkName)

	// 启动容器
	startCmd := fmt.Sprintf("incus start %s", shellSingleQuote(config.ContainerName))
	startOutput, err := i.sshClient.Execute(startCmd)
	if err != nil {
		return "", fmt.Errorf("启动容器失败: output=%s: %w", summarizeIPv6ProbeOutput(startOutput), err)
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

	// This optional path refresh must not replace unrelated host cron jobs.
	if _, err := i.sshClient.Execute(utils.IPv6KeepaliveInstallCommand()); err != nil {
		global.APP_LOG.Warn("安装独立IPv6保活任务失败，保留现有计划任务", zap.Error(err))
	}

	return containerIPv6, nil
}

func (i *IncusProvider) ensureBridgeNetfilter() error {
	command := `set -eu
if [ ! -d /sys/module/br_netfilter ]; then
  if ! command -v modprobe >/dev/null 2>&1 || ! modprobe br_netfilter 2>/dev/null; then
    printf '%s\n' 'br_netfilter kernel module is unavailable' >&2
    exit 1
  fi
fi
test -d /sys/module/br_netfilter
mkdir -p /etc/modules-load.d
conf=/etc/modules-load.d/oneclickvirt.conf
tmp="${conf}.tmp.$$"
if [ -f "$conf" ]; then
  cp "$conf" "$tmp"
else
  : > "$tmp"
fi
if ! grep -qxF br_netfilter "$tmp" 2>/dev/null; then
  printf '%s\n' br_netfilter >> "$tmp"
fi
chmod 0644 "$tmp"
mv -f "$tmp" "$conf"`
	_, err := i.sshClient.Execute(command)
	return err
}

func (i *IncusProvider) setupRoutedNetworkDeviceIPv6(config IPv6Config) (string, error) {
	routed, present, err := provider.ResolveRoutedIPv6(provider.InstanceConfig{Metadata: map[string]string{
		"static_ipv6":                  config.ContainerIPv6,
		"static_ipv6_cidr":             config.RoutedCIDR,
		"static_ipv6_gateway":          config.RoutedGateway,
		"static_ipv6_bridge":           config.RoutedBridge,
		"static_ipv6_tunnel_interface": config.RoutedTunnelInterface,
	}})
	if err != nil {
		return "", fmt.Errorf("隧道路由IPv6配置无效: %w", err)
	}
	if !present {
		return "", fmt.Errorf("隧道路由IPv6缺少地址、前缀、网关或网桥")
	}
	checkCmd := routed.HostCheckCommand()
	if output, checkErr := i.sshClient.Execute(checkCmd); checkErr != nil {
		return "", fmt.Errorf("隧道路由IPv6网桥未就绪: output=%s: %w", summarizeIPv6ProbeOutput(output), checkErr)
	}
	// Incus validates routed NICs against the host-wide proxy-NDP switch.  A
	// tunnel bridge may be created outside the normal provider IPv6 path, so
	// repair the complete routed sysctl set after the platform/bridge check and
	// before the daemon sees eth1.
	if err := i.configureRoutedIPv6Sysctls(routed.Bridge, routed.TunnelInterface); err != nil {
		return "", fmt.Errorf("配置隧道路由IPv6 sysctl失败: %w", err)
	}
	name := shellSingleQuote(config.ContainerName)
	bridge := shellSingleQuote(routed.Bridge)
	addressArg := shellSingleQuote(routed.Address)
	if err := i.sshStopInstance(config.ContainerName); err != nil {
		return "", fmt.Errorf("停止隧道路由IPv6实例失败: %w", err)
	}
	deviceCmd := fmt.Sprintf(`set -eu
if incus config device get %s eth1 type >/dev/null 2>&1; then
  existing_type="$(incus config device get %s eth1 type)"
  existing_nictype="$(incus config device get %s eth1 nictype)"
  existing_parent="$(incus config device get %s eth1 parent)"
  existing_address="$(incus config device get %s eth1 ipv6.address)"
  if [ "$existing_type" != nic ] || [ "$existing_nictype" != routed ] || [ "$existing_parent" != %s ] || [ "$existing_address" != %s ]; then
    printf 'refusing to replace existing eth1: type=%%s nictype=%%s parent=%%s ipv6.address=%%s\n' "$existing_type" "$existing_nictype" "$existing_parent" "$existing_address" >&2
    exit 1
  fi
	  if ! incus config device set %s eth1 ipv6.gateway auto 2>/dev/null; then
	    incus config device override %s eth1 nictype=routed parent=%s ipv6.address=%s ipv6.gateway=auto
	  fi
else
	  incus config device add %s eth1 nic nictype=routed parent=%s ipv6.address=%s ipv6.gateway=auto
fi`, name, name, name, name, name, bridge, addressArg, name, name, bridge, addressArg, name, bridge, addressArg)
	if output, deviceErr := i.sshClient.Execute(deviceCmd); deviceErr != nil {
		return "", fmt.Errorf("添加隧道路由IPv6网络设备失败: output=%s: %w", summarizeIPv6ProbeOutput(output), deviceErr)
	}
	startOutput, startErr := i.sshClient.Execute(fmt.Sprintf("incus start %s", shellSingleQuote(config.ContainerName)))
	if startErr != nil {
		return "", fmt.Errorf("启动隧道路由IPv6实例失败: output=%s: %w", summarizeIPv6ProbeOutput(startOutput), startErr)
	}
	if config.InstanceType == "vm" {
		if waitErr := i.waitForVMNetworkReady(config.ContainerName); waitErr != nil {
			global.APP_LOG.Warn("等待Incus VM隧道IPv6网络就绪超时", zap.Error(waitErr))
		}
	} else if waitErr := i.waitForContainerNetworkReady(config.ContainerName); waitErr != nil {
		global.APP_LOG.Warn("等待Incus容器隧道IPv6网络就绪超时", zap.Error(waitErr))
	}
	return routed.Address, nil
}

// configureIPv6Sysctls writes one clean, dedicated sysctl file. Forwarding is
// global and Incus requires the global proxy_ndp switch before it can start a
// routed NIC. The selected uplink is configured explicitly as well; without
// the global switch Incus rejects the device even when the uplink itself has
// proxy_ndp enabled.
func (i *IncusProvider) configureIPv6Sysctls(interfaceName string) error {
	if strings.TrimSpace(interfaceName) == "" || utils.SanitizeShellArg(interfaceName) != interfaceName {
		return fmt.Errorf("无效的IPv6网络接口: %q", interfaceName)
	}
	command := fmt.Sprintf(`set -eu
conf=/etc/sysctl.d/99-oneclickvirt-ipv6.conf
mkdir -p /etc/sysctl.d
tmp="${conf}.tmp.$$"
{
  if [ -e "/proc/sys/net/ipv6/conf/%s/accept_ra" ]; then
    printf 'net.ipv6.conf.%%s.accept_ra=2\n' "%s"
  fi
	  printf 'net.ipv6.conf.all.forwarding=1\n'
	  printf 'net.ipv6.conf.default.forwarding=1\n'
  printf 'net.ipv6.conf.all.proxy_ndp=1\n'
  if [ -e "/proc/sys/net/ipv6/conf/%s/proxy_ndp" ]; then
    printf 'net.ipv6.conf.%%s.proxy_ndp=1\n' "%s"
  fi
} > "$tmp"
chmod 0644 "$tmp"
mv "$tmp" "$conf"
if [ -e "/proc/sys/net/ipv6/conf/%s/accept_ra" ]; then
  sysctl -w "net.ipv6.conf.%s.accept_ra=2" >/dev/null
fi
	sysctl -w net.ipv6.conf.all.forwarding=1 >/dev/null
	sysctl -w net.ipv6.conf.default.forwarding=1 >/dev/null
sysctl -w net.ipv6.conf.all.proxy_ndp=1 >/dev/null
if [ -e "/proc/sys/net/ipv6/conf/%s/proxy_ndp" ]; then
  sysctl -w "net.ipv6.conf.%s.proxy_ndp=1" >/dev/null
fi`, interfaceName, interfaceName, interfaceName, interfaceName, interfaceName, interfaceName, interfaceName, interfaceName)
	_, err := i.sshClient.Execute(command)
	return err
}

// configureRoutedIPv6Sysctls repairs both persistent and live settings used by
// a tunnel-owned bridge.  It is intentionally separate from the native uplink
// helper so one invocation cannot overwrite another interface's persisted
// forwarding setting.
func (i *IncusProvider) configureRoutedIPv6Sysctls(bridge, tunnel string) error {
	if strings.TrimSpace(bridge) == "" || utils.SanitizeShellArg(bridge) != bridge {
		return fmt.Errorf("无效的IPv6网桥: %q", bridge)
	}
	if strings.TrimSpace(tunnel) != "" && (utils.SanitizeShellArg(tunnel) != tunnel || tunnel == "lo") {
		return fmt.Errorf("无效的IPv6隧道接口: %q", tunnel)
	}
	bridgeQ, tunnelQ := shellSingleQuote(bridge), shellSingleQuote(tunnel)
	fileSuffix := bridge
	if tunnel != "" {
		fileSuffix += "-" + tunnel
	}
	confPath := shellSingleQuote("/etc/sysctl.d/99-oneclickvirt-ipv6-routed-" + fileSuffix + ".conf")
	lines := fmt.Sprintf("net.ipv6.conf.%s.forwarding=1\\nnet.ipv6.conf.%s.proxy_ndp=1\\n", bridge, bridge)
	runtime := fmt.Sprintf("if [ -e /proc/sys/net/ipv6/conf/%s/forwarding ]; then sysctl -w net.ipv6.conf.%s.forwarding=1 >/dev/null; fi\\nif [ -e /proc/sys/net/ipv6/conf/%s/proxy_ndp ]; then sysctl -w net.ipv6.conf.%s.proxy_ndp=1 >/dev/null; fi\\n", bridgeQ, bridgeQ, bridgeQ, bridgeQ)
	if tunnel != "" {
		lines += fmt.Sprintf("net.ipv6.conf.%s.forwarding=1\\nnet.ipv6.conf.%s.proxy_ndp=1\\n", tunnel, tunnel)
		runtime += fmt.Sprintf("if [ -e /proc/sys/net/ipv6/conf/%s/forwarding ]; then sysctl -w net.ipv6.conf.%s.forwarding=1 >/dev/null; fi\\nif [ -e /proc/sys/net/ipv6/conf/%s/proxy_ndp ]; then sysctl -w net.ipv6.conf.%s.proxy_ndp=1 >/dev/null; fi\\n", tunnelQ, tunnelQ, tunnelQ, tunnelQ)
	}
	command := fmt.Sprintf("set -eu\\nconf=%s\\nmkdir -p /etc/sysctl.d\\ntmp=\"${conf}.tmp.$$\"\\nprintf 'net.ipv6.conf.all.forwarding=1\\nnet.ipv6.conf.default.forwarding=1\\nnet.ipv6.conf.all.proxy_ndp=1\\n%s' > \"$tmp\"\\nchmod 0644 \"$tmp\"\\nmv -f \"$tmp\" \"$conf\"\\nsysctl -w net.ipv6.conf.all.forwarding=1 >/dev/null\\nsysctl -w net.ipv6.conf.default.forwarding=1 >/dev/null\\nsysctl -w net.ipv6.conf.all.proxy_ndp=1 >/dev/null\\n%s", confPath, lines, runtime)
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
	// A link-local address is required by routed NICs and router
	// advertisements. Retain it even when the default route uses a global
	// gateway; deleting it breaks the container's IPv6 route and also leaves a
	// destructive reboot helper behind.
	global.APP_LOG.Debug("保留IPv6链路本地网关地址", zap.String("interface", interfaceName))
}

// configureIPv6Network 主要的IPv6网络配置函数
func (i *IncusProvider) configureIPv6Network(ctx context.Context, containerName string, enableIPv6 bool, portMappingMethod, requestedIPv6 string, routed *provider.RoutedIPv6Config, instanceType string) error {
	if !enableIPv6 {
		global.APP_LOG.Debug("IPv6未启用，跳过IPv6配置", zap.String("container", containerName))
		return nil
	}

	global.APP_LOG.Debug("开始配置IPv6网络",
		zap.String("container", containerName),
		zap.String("portMappingMethod", portMappingMethod))
	if routed != nil {
		if portMappingMethod == "iptables" {
			global.APP_LOG.Warn("隧道独立IPv6不使用iptables NAT，改用routed网络设备", zap.String("container", containerName))
		}
		routedConfig := IPv6Config{
			ContainerName: containerName, ContainerIPv6: requestedIPv6,
			HostIPv6Prefix: routed.CIDR, IPv6Length: routed.Prefix,
			Interface: routed.Bridge, Gateway: routed.Gateway,
			UseNetworkDevice: true, RoutedCIDR: routed.CIDR,
			RoutedGateway: routed.Gateway, RoutedBridge: routed.Bridge,
			RoutedTunnelInterface: routed.TunnelInterface,
			InstanceType:          instanceType,
		}
		containerIPv6, err := i.setupRoutedNetworkDeviceIPv6(routedConfig)
		if err != nil {
			return err
		}
		saveCmd := fmt.Sprintf("printf '%%s\\n' %s > %s", shellSingleQuote(containerIPv6), shellSingleQuote(containerName+"_v6"))
		if _, err := i.sshClient.Execute(saveCmd); err != nil {
			return fmt.Errorf("保存实例IPv6地址失败: %w", err)
		}
		return nil
	}

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

	// Keep the selected prefix and interface together. An arbitrary lshw
	// interface can be IPv4-only or a host-only /128 while a PVE bridge owns
	// the actual delegated IPv6 pool.
	selectedNetwork, err := i.selectHostIPv6InterfaceNetwork(ctx, strings.TrimSpace(config.ContainerIPv6) == "")
	if err != nil {
		return "", fmt.Errorf("获取IPv6子网和接口失败: %w", err)
	}
	network := selectedNetwork.Network
	subnetPrefix := network.CIDR()
	ipv6Length := fmt.Sprintf("%d", network.PrefixLen)
	interfaceName := selectedNetwork.Interface

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
		if _, err := i.sshClient.Execute(updateCmd); err != nil {
			return fmt.Errorf("更新APT索引失败: %w", err)
		}
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
	checkScriptCmd := fmt.Sprintf("[ -s %s ]", shellSingleQuote(scriptPath))
	_, err := i.sshClient.Execute(checkScriptCmd)
	if err != nil {
		scriptUrl := cdnSuccessUrl + "https://raw.githubusercontent.com/oneclickvirt/incus/main/scripts/add-ipv6.sh"
		tmpPath := scriptPath + ".tmp.$$"
		downloadCmd := fmt.Sprintf("set -eu; tmp=%s; trap 'rm -f \"$tmp\"' EXIT; wget '%s' -O \"$tmp\"; chmod 0755 \"$tmp\"; mv -f \"$tmp\" %s", shellSingleQuote(tmpPath), scriptUrl, shellSingleQuote(scriptPath))
		if _, err := i.sshClient.Execute(downloadCmd); err != nil {
			return fmt.Errorf("下载并安装add-ipv6.sh失败: %w", err)
		}
	}

	// 下载add-ipv6.service服务文件 (Incus版本)
	servicePath := "/etc/systemd/system/add-ipv6.service"
	checkServiceCmd := fmt.Sprintf("[ -s %s ]", shellSingleQuote(servicePath))
	_, err = i.sshClient.Execute(checkServiceCmd)
	if err != nil {
		serviceUrl := cdnSuccessUrl + "https://raw.githubusercontent.com/oneclickvirt/incus/main/scripts/add-ipv6.service"
		tmpPath := servicePath + ".tmp.$$"
		downloadCmd := fmt.Sprintf("set -eu; tmp=%s; trap 'rm -f \"$tmp\"' EXIT; wget '%s' -O \"$tmp\"; chmod 0644 \"$tmp\"; mv -f \"$tmp\" %s", shellSingleQuote(tmpPath), serviceUrl, shellSingleQuote(servicePath))
		if _, err := i.sshClient.Execute(downloadCmd); err != nil {
			return fmt.Errorf("下载并安装add-ipv6.service失败: %w", err)
		}
		if _, err := i.sshClient.Execute("systemctl daemon-reload"); err != nil {
			return fmt.Errorf("重新加载IPv6 systemd服务失败: %w", err)
		}
		if _, err := i.sshClient.Execute("systemctl enable --now add-ipv6.service"); err != nil {
			return fmt.Errorf("启用IPv6持久化服务失败: %w", err)
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
		if _, err := i.sshClient.Execute("mkdir -p /etc/iptables"); err != nil {
			return fmt.Errorf("创建iptables规则目录失败: %w", err)
		}
		_, err := i.sshClient.Execute("ip6tables-save > /etc/iptables/rules.v6")
		if err != nil {
			return fmt.Errorf("保存ip6tables规则失败: %w", err)
		}

		// 检查netfilter-persistent是否可用
		_, err = i.sshClient.Execute("command -v netfilter-persistent")
		if err == nil {
			if _, err = i.sshClient.Execute("netfilter-persistent save"); err != nil {
				return fmt.Errorf("持久化IPv6规则失败: %w", err)
			}
			if _, err = i.sshClient.Execute("netfilter-persistent reload"); err != nil {
				return fmt.Errorf("重新加载IPv6规则失败: %w", err)
			}
			if _, err = i.sshClient.Execute("service netfilter-persistent restart"); err != nil {
				return fmt.Errorf("重启IPv6规则服务失败: %w", err)
			}
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
