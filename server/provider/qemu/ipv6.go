package qemu

import (
	"encoding/base64"
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
	if plan.Routed == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("version: 2\nethernets:\n")
	if !plan.IPv6Only {
		fmt.Fprintf(&builder, "  oneclickvirt0:\n    match:\n      macaddress: %s\n    set-name: oneclickvirt0\n    dhcp4: true\n    dhcp6: false\n", yamlDoubleQuote(primaryMAC))
	}
	fmt.Fprintf(&builder, "  oneclickvirt6:\n    match:\n      macaddress: %s\n    set-name: oneclickvirt6\n    dhcp4: false\n    dhcp6: false\n    accept-ra: false\n    addresses:\n      - %s\n    routes:\n      - to: \"::/0\"\n        via: %s\n        on-link: true\n", yamlDoubleQuote(routedMAC), yamlDoubleQuote(plan.Routed.AddressCIDR()), yamlDoubleQuote(plan.Routed.Gateway))
	return builder.String()
}

// (libvirt-lxc does not consume cloud-init network-data.) Install a small,
// idempotent guest-side script plus both common persistence formats, then run
// it once through lxc-enter-namespace after the domain starts.
func (p *QEMUProvider) configureLXCIPv6Rootfs(rootfs string, routed provider.RoutedIPv6Config, routedMAC string) error {
	addressCIDR := routed.AddressCIDR()
	if addressCIDR == "" {
		return fmt.Errorf("隧道路由IPv6地址前缀无效")
	}
	bridgeScript := fmt.Sprintf(`#!/bin/sh
set -eu
IFACE=$(ip -o link 2>/dev/null | awk -v mac='%s' 'tolower($0) ~ mac {split($2, a, "@"); gsub(":", "", a[1]); print a[1]; exit}')
if [ -z "$IFACE" ]; then
  IFACE=$(ip -o link 2>/dev/null | awk -F': ' '$2 !~ /^lo([:@]|$)/ {split($2, a, "@"); print a[1]; exit}')
fi
[ -n "$IFACE" ] || exit 1
ip link set dev "$IFACE" up
ip -6 addr replace '%s' dev "$IFACE"
ip -6 route replace default via '%s' dev "$IFACE" onlink 2>/dev/null || ip -6 route replace default via '%s' dev "$IFACE"
`, strings.ToLower(strings.TrimSpace(routedMAC)), addressCIDR, routed.Gateway, routed.Gateway)

	networkd := fmt.Sprintf(`[Match]
MACAddress=%s

[Network]
Address=%s
Gateway=%s
IPv6AcceptRA=no
`, strings.ToLower(strings.TrimSpace(routedMAC)), addressCIDR, routed.Gateway)
	ifupdown := fmt.Sprintf(`auto eth1
iface eth1 inet6 static
    address %s
    netmask %d
    gateway %s
`, routed.Address, routed.Prefix, routed.Gateway)
	service := `[Unit]
Description=OneClickVirt routed IPv6
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/oneclickvirt-routed-ipv6
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`
	files := map[string]string{
		"/usr/local/sbin/oneclickvirt-routed-ipv6":                 bridgeScript,
		"/etc/systemd/network/99-oneclickvirt-routed-ipv6.network": networkd,
		"/etc/network/interfaces.d/99-oneclickvirt-routed-ipv6":    ifupdown,
		"/etc/systemd/system/oneclickvirt-routed-ipv6.service":     service,
	}
	var command strings.Builder
	command.WriteString(fmt.Sprintf("set -eu; mkdir -p %s/etc/systemd/network %s/etc/network/interfaces.d %s/etc/systemd/system %s/usr/local/sbin; ",
		shellSingleQuote(rootfs), shellSingleQuote(rootfs), shellSingleQuote(rootfs), shellSingleQuote(rootfs)))
	for path, content := range files {
		encoded := base64.StdEncoding.EncodeToString([]byte(content))
		mode := "0644"
		if strings.HasSuffix(path, "/sbin/oneclickvirt-routed-ipv6") {
			mode = "0755"
		}
		command.WriteString(fmt.Sprintf("printf '%%s' %s | base64 -d > %s; chmod %s %s; ", shellSingleQuote(encoded), shellSingleQuote(rootfs+path), mode, shellSingleQuote(rootfs+path)))
	}
	if output, err := p.sshClient.Execute(command.String()); err != nil {
		return fmt.Errorf("写入libvirt-lxc隧道IPv6配置失败: %s: %w", utils.TruncateString(strings.TrimSpace(output), 1000), err)
	}
	return nil
}
