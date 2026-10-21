package qemu

import (
	"fmt"
	"strings"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

// qemuIPv6Plan is resolved once before the first disk/network mutation. A
// routed allocation is intentionally represented separately from libvirt's
// default NAT network: the latter remains the IPv4 management path while the
// tunnel bridge carries the controller-assigned IPv6 address.
type qemuIPv6Plan struct {
	NetworkType string
	Routed      *provider.RoutedIPv6Config
	IPv6Only    bool
}

func qemuNetworkType(config provider.InstanceConfig) string {
	if config.Metadata != nil {
		if value := strings.TrimSpace(config.Metadata["network_type"]); value != "" {
			return strings.ToLower(value)
		}
	}
	return "nat_ipv4"
}

func resolveQEMUIPv6Plan(config provider.InstanceConfig) (qemuIPv6Plan, error) {
	networkType := qemuNetworkType(config)
	plan := qemuIPv6Plan{NetworkType: networkType, IPv6Only: networkType == "ipv6_only"}
	routed, present, err := provider.ResolveRoutedIPv6(config)
	if err != nil {
		return plan, err
	}
	requested := ""
	if config.Metadata != nil {
		requested = strings.TrimSpace(config.Metadata["static_ipv6"])
	}
	if present {
		if !utils.NetworkTypeHasIPv6(networkType) {
			return plan, fmt.Errorf("已分配隧道路由IPv6，但网络类型 %q 未启用IPv6", networkType)
		}
		plan.Routed = &routed
		return plan, nil
	}
	if requested != "" {
		return plan, fmt.Errorf("QEMU/libvirt 只能为带路由前缀元数据的IPv6地址配置静态网络；当前地址缺少 static_ipv6_cidr")
	}
	if utils.NetworkTypeHasIPv6(networkType) {
		return plan, fmt.Errorf("QEMU/libvirt 的IPv6网络必须绑定已启用的IPv6隧道路由地址池")
	}
	return plan, nil
}

func (p *QEMUProvider) preflightQEMUIPv6(config provider.InstanceConfig) (qemuIPv6Plan, error) {
	plan, err := resolveQEMUIPv6Plan(config)
	if err != nil {
		return plan, err
	}
	if plan.Routed == nil {
		return plan, nil
	}
	if _, commandErr := p.sshClient.Execute("command -v base64 >/dev/null 2>&1"); commandErr != nil {
		return plan, fmt.Errorf("配置QEMU隧道IPv6需要 base64 命令: %w", commandErr)
	}
	output, err := p.sshClient.Execute(plan.Routed.HostCheckCommand())
	if err != nil {
		return plan, fmt.Errorf("QEMU隧道路由IPv6网桥未就绪: %s: %w", utils.TruncateString(strings.TrimSpace(output), 1000), err)
	}
	return plan, nil
}

// qemuVirtInstallNetworkArgs returns complete --network arguments. Keeping
// each argument quoted as one token prevents bridge names and MAC addresses
// from being interpreted as shell syntax in the remote command.
func qemuVirtInstallNetworkArgs(plan qemuIPv6Plan, primaryMAC, routedMAC string) string {
	args := make([]string, 0, 2)
	if !plan.IPv6Only {
		args = append(args, "--network "+shellSingleQuote(fmt.Sprintf("network=default,mac=%s,model=virtio", primaryMAC)))
	}
	if plan.Routed != nil {
		args = append(args, "--network "+shellSingleQuote(fmt.Sprintf("bridge=%s,mac=%s,model=virtio", plan.Routed.Bridge, routedMAC)))
	}
	return strings.Join(args, " ")
}

func qemuCloudInitNetworkData(plan qemuIPv6Plan, primaryMAC, routedMAC string) string {
	var builder strings.Builder
	builder.WriteString("version: 2\nethernets:\n")
	if !plan.IPv6Only {
		fmt.Fprintf(&builder, "  oneclickvirt0:\n    match:\n      macaddress: %s\n    set-name: oneclickvirt0\n    dhcp4: true\n    dhcp6: false\n    accept-ra: false\n", yamlDoubleQuote(primaryMAC))
	}
	if plan.Routed == nil {
		return builder.String()
	}
	fmt.Fprintf(&builder, "  oneclickvirt6:\n    match:\n      macaddress: %s\n    set-name: oneclickvirt6\n    dhcp4: false\n    dhcp6: false\n    accept-ra: false\n    addresses:\n      - %s\n    routes:\n      - to: \"::/0\"\n        via: %s\n        on-link: true\n", yamlDoubleQuote(routedMAC), yamlDoubleQuote(plan.Routed.AddressCIDR()), yamlDoubleQuote(plan.Routed.Gateway))
	if plan.IPv6Only {
		// Without IPv4 DHCP there is no DNS server to inherit. Use IPv6
		// resolvers so a static guest can resolve names on its only interface.
		builder.WriteString("    nameservers:\n      addresses:\n        - 2606:4700:4700::1111\n        - 2001:4860:4860::8888\n")
	}
	return builder.String()
}
