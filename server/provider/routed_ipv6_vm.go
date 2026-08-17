package provider

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"path"
	"strings"

	"oneclickvirt/utils"
)

// RoutedIPv6VMPlan is the common guest-facing form of a controller-managed
// routed IPv6 allocation. VM backends use a separate NIC for this plan so an
// existing NAT IPv4 NIC remains untouched for dual-stack instances.
type RoutedIPv6VMPlan struct {
	NetworkType string
	IPv6Only    bool
	Routed      *RoutedIPv6Config
	MAC         string
}

// ResolveRoutedIPv6VMPlan keeps VM-only backends fail-closed. Unlike LXD or a
// Docker runtime, these backends cannot safely infer a guest IPv6 address from
// a host route, so every IPv6 network request must carry the tunnel metadata
// selected by the controller.
func ResolveRoutedIPv6VMPlan(config InstanceConfig, backend string) (RoutedIPv6VMPlan, error) {
	networkType := "nat_ipv4"
	if config.Metadata != nil {
		if value := strings.TrimSpace(config.Metadata["network_type"]); value != "" {
			networkType = strings.ToLower(value)
		}
	}
	plan := RoutedIPv6VMPlan{
		NetworkType: networkType,
		IPv6Only:    networkType == "ipv6_only",
	}

	requested := ""
	if config.Metadata != nil {
		requested = strings.TrimSpace(config.Metadata["static_ipv6"])
	}
	routed, present, err := ResolveRoutedIPv6(config)
	if err != nil {
		return plan, err
	}
	if !utils.NetworkTypeHasIPv6(networkType) {
		if present || requested != "" {
			return plan, fmt.Errorf("已分配静态IPv6，但网络类型 %q 未启用IPv6", networkType)
		}
		return plan, nil
	}
	if !present {
		if requested == "" {
			return plan, fmt.Errorf("%s 的IPv6网络需要控制器分配的IPv6隧道路由前缀，当前未配置可用IPv6地址池", strings.TrimSpace(backend))
		}
		return plan, fmt.Errorf("%s 的静态IPv6必须携带 static_ipv6_cidr、static_ipv6_gateway 和 static_ipv6_bridge 隧道路由元数据", strings.TrimSpace(backend))
	}

	plan.Routed = &routed
	plan.MAC = RoutedIPv6VMGuestMAC(backend, routed.Address)
	// Multipass and Vagrant keep a provider-owned IPv4 management NIC for
	// launch/provisioning. Reporting ipv6_only there would be dishonest, so
	// reject it before the backend creates anything.
	if plan.IPv6Only {
		switch strings.ToLower(strings.TrimSpace(backend)) {
		case "multipass", "vagrant":
			return plan, fmt.Errorf("%s 当前必须保留管理IPv4网卡，不支持严格的 ipv6_only；请选择 NAT IPv4 + 独立 IPv6", strings.TrimSpace(backend))
		}
	}
	return plan, nil
}

// RoutedIPv6VMGuestMAC returns a stable, locally scoped MAC. VMware's static
// MAC validation only accepts the vendor prefix range, while the remaining
// hypervisors accept a normal locally-administered unicast address.
func RoutedIPv6VMGuestMAC(backend, address string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(backend)) + ":" + strings.TrimSpace(address)))
	if strings.EqualFold(strings.TrimSpace(backend), "vmware") {
		return fmt.Sprintf("00:50:56:%02x:%02x:%02x", sum[0]&0x3f, sum[1], sum[2])
	}
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", sum[0], sum[1], sum[2], sum[3], sum[4])
}

// RoutedIPv6VMSeedFileName avoids deriving a persistent seed path directly
// from a user-controlled instance name while keeping lifecycle cleanup
// deterministic for a backend and guest pair.
func RoutedIPv6VMSeedFileName(backend, instanceName string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(backend)) + ":" + strings.TrimSpace(instanceName)))
	return fmt.Sprintf("seed-%x.iso", sum[:8])
}

// HostCheckCommand verifies the bridge owned by the IPv6 tunnel in one remote
// operation before a VM backend clones or starts a guest.
func (p RoutedIPv6VMPlan) HostCheckCommand() string {
	if p.Routed == nil {
		return ""
	}
	return p.Routed.HostCheckCommand()
}

// NoCloudNetworkData configures only the routed NIC. Leaving the primary NIC
// out of this document preserves a backend's existing IPv4/NAT configuration.
func (p RoutedIPv6VMPlan) NoCloudNetworkData() (string, error) {
	if p.Routed == nil || p.MAC == "" {
		return "", fmt.Errorf("IPv6隧道路由虚拟机网络计划为空")
	}
	return fmt.Sprintf(`version: 2
ethernets:
  oneclickvirt6:
    match:
      macaddress: %s
    set-name: oneclickvirt6
    dhcp4: false
    dhcp6: false
    accept-ra: false
    addresses:
      - %s
    routes:
      - to: "::/0"
        via: %s
        on-link: true
`, routedIPv6YAMLString(strings.ToLower(p.MAC)), routedIPv6YAMLString(p.Routed.AddressCIDR()), routedIPv6YAMLString(p.Routed.Gateway)), nil
}

// NoCloudUserData is deliberately minimal. It does not replace image-provided
// user data; it only supplies a hostname and, when available, the controller
// generated initial password used by the existing VM creation flows.
func (p RoutedIPv6VMPlan) NoCloudUserData(instanceName, password string) string {
	password = strings.ReplaceAll(strings.ReplaceAll(password, "\r", ""), "\n", "")
	content := fmt.Sprintf("#cloud-config\nhostname: %s\nmanage_etc_hosts: true\nssh_pwauth: true\ndisable_root: false\n", routedIPv6YAMLString(instanceName))
	if password != "" {
		content += fmt.Sprintf("chpasswd:\n  expire: false\n  list:\n    - %s\n", routedIPv6YAMLString("root:"+password))
	}
	return content
}

// NoCloudISOCommand renders one bounded remote command that creates a NoCloud
// seed ISO. It prefers cloud-localds but has standard ISO-tool fallbacks so
// VirtualBox and VMware do not need provider-specific host packages.
func (p RoutedIPv6VMPlan) NoCloudISOCommand(isoPath, instanceName, password string) (string, error) {
	networkData, err := p.NoCloudNetworkData()
	if err != nil {
		return "", err
	}
	isoPath = strings.TrimSpace(isoPath)
	if isoPath == "" || !strings.HasPrefix(isoPath, "/") {
		return "", fmt.Errorf("cloud-init ISO路径无效")
	}
	userData := p.NoCloudUserData(instanceName, password)
	metaData := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", routedIPv6YAMLString("oneclickvirt-"+instanceName), routedIPv6YAMLString(instanceName))

	return fmt.Sprintf(`set -eu
command -v base64 >/dev/null 2>&1 || { echo 'base64 is unavailable for cloud-init seed creation' >&2; exit 1; }
iso=%s
iso_dir=%s
mkdir -p "$iso_dir"
work="$(mktemp -d /tmp/oneclickvirt-ipv6-seed.XXXXXX)"
cleanup() { rm -rf "$work"; }
trap cleanup EXIT
printf '%%s' %s | base64 -d > "$work/user-data"
printf '%%s' %s | base64 -d > "$work/meta-data"
printf '%%s' %s | base64 -d > "$work/network-config"
rm -f "$iso"
if command -v cloud-localds >/dev/null 2>&1; then
  cloud-localds --network-config="$work/network-config" "$iso" "$work/user-data" "$work/meta-data"
elif command -v genisoimage >/dev/null 2>&1; then
  genisoimage -quiet -output "$iso" -volid cidata -joliet -rock "$work/user-data" "$work/meta-data" "$work/network-config"
elif command -v mkisofs >/dev/null 2>&1; then
  mkisofs -quiet -output "$iso" -volid cidata -joliet -rock "$work/user-data" "$work/meta-data" "$work/network-config"
else
  echo 'cloud-localds, genisoimage, or mkisofs is required for routed IPv6 VM cloud-init' >&2
  exit 1
fi
test -s "$iso"
`,
		utils.ShellSingleQuote(isoPath),
		utils.ShellSingleQuote(path.Dir(isoPath)),
		utils.ShellSingleQuote(base64.StdEncoding.EncodeToString([]byte(userData))),
		utils.ShellSingleQuote(base64.StdEncoding.EncodeToString([]byte(metaData))),
		utils.ShellSingleQuote(base64.StdEncoding.EncodeToString([]byte(networkData)))), nil
}

func routedIPv6YAMLString(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "\n", "")
	value = strings.ReplaceAll(value, "\r", "")
	return "\"" + value + "\""
}
