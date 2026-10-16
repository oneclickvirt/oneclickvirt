package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

type proxmoxDedicatedIPv4 struct {
	Address netip.Addr
	Prefix  int
	Gateway netip.Addr
	Bridge  string
}

// Read the live bridge subnet and its own default route together. A /27 must
// not become /24, and a route on a separate management NIC is not a gateway
// for a public guest. No host alias is created: the guest owns this address.
func (p *ProxmoxProvider) dedicatedIPv4Config(ctx context.Context, config provider.InstanceConfig, networkType string) (*proxmoxDedicatedIPv4, error) {
	if networkType != "dedicated_ipv4" && networkType != "dedicated_ipv4_ipv6" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bridge := strings.TrimSpace(p.getBridgeName("dedicated_v4"))
	if !linuxInterfaceName.MatchString(bridge) {
		return nil, fmt.Errorf("独立IPv4网桥名称无效")
	}
	requested := strings.TrimSpace(config.Metadata["static_ipv4"])
	address, err := netip.ParseAddr(requested)
	if err != nil || !address.Is4() || !address.IsGlobalUnicast() {
		return nil, fmt.Errorf("独立IPv4需要有效的地址池分配地址")
	}
	command := `set -eu
addresses=$(LC_ALL=C NO_COLOR=1 ip -j -4 addr show)
routes=$(LC_ALL=C NO_COLOR=1 ip -j -4 route show default)
printf '{"addresses":%s,"routes":%s}\n' "$addresses" "$routes"
`
	output, err := p.sshClient.Execute(command)
	if err != nil {
		return nil, fmt.Errorf("读取独立IPv4网桥和路由失败: %w", err)
	}
	return parseProxmoxDedicatedIPv4(output, address, bridge)
}

func parseProxmoxDedicatedIPv4(output string, address netip.Addr, bridge string) (*proxmoxDedicatedIPv4, error) {
	var snapshot struct {
		Addresses []struct {
			Name string `json:"ifname"`
			Info []struct {
				Family string `json:"family"`
				Local  string `json:"local"`
				Prefix int    `json:"prefixlen"`
				Scope  string `json:"scope"`
			} `json:"addr_info"`
		} `json:"addresses"`
		Routes []struct {
			Destination string `json:"dst"`
			Device      string `json:"dev"`
			Gateway     string `json:"gateway"`
			Type        string `json:"type"`
		} `json:"routes"`
	}
	if err := json.Unmarshal([]byte(utils.StripTerminalCSI(output)), &snapshot); err != nil {
		return nil, fmt.Errorf("独立IPv4网络探测响应无效: %w", err)
	}
	var networks []netip.Prefix
	for _, row := range snapshot.Addresses {
		for _, info := range row.Info {
			if info.Family != "inet" || info.Scope != "global" {
				continue
			}
			host, err := netip.ParseAddr(info.Local)
			if err != nil || !host.Is4() {
				return nil, fmt.Errorf("独立IPv4宿主地址无效")
			}
			if host == address {
				return nil, fmt.Errorf("独立IPv4地址已由宿主机使用，请从地址池排除宿主地址")
			}
			if row.Name == bridge && info.Prefix >= 1 && info.Prefix <= 31 {
				networks = append(networks, netip.PrefixFrom(host, info.Prefix).Masked())
			}
		}
	}
	sort.SliceStable(networks, func(i, j int) bool { return networks[i].Bits() > networks[j].Bits() })
	for _, route := range snapshot.Routes {
		gateway, err := netip.ParseAddr(route.Gateway)
		if err != nil || !gateway.Is4() || route.Device != bridge || route.Destination != "default" || (route.Type != "" && route.Type != "unicast") {
			continue
		}
		if gateway == address {
			return nil, fmt.Errorf("独立IPv4地址与上游网关冲突，请从地址池排除网关")
		}
		for _, network := range networks {
			if !network.Contains(address) || !network.Contains(gateway) {
				continue
			}
			if network.Bits() < 31 {
				last := network.Addr().As4()
				for bit := network.Bits(); bit < 32; bit++ {
					last[bit/8] |= 1 << (7 - uint(bit%8))
				}
				if address == network.Addr() || address == netip.AddrFrom4(last) {
					return nil, fmt.Errorf("独立IPv4不能使用网络地址或广播地址")
				}
			}
			return &proxmoxDedicatedIPv4{Address: address, Prefix: network.Bits(), Gateway: gateway, Bridge: bridge}, nil
		}
	}
	return nil, fmt.Errorf("独立IPv4地址不属于 %s 上可桥接的静态子网，或该网桥缺少同网段默认网关", bridge)
}

// Preserve the PVE-generated MAC, rate, VLAN and firewall options while moving
// only this guest NIC to the public bridge. IPv6 remains on its existing NIC.
func (p *ProxmoxProvider) configureDedicatedIPv4(ctx context.Context, vmid int, instanceType string, network *proxmoxDedicatedIPv4) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tool := "pct"
	if instanceType != "container" {
		tool = "qm"
	}
	output, err := p.sshClient.Execute(fmt.Sprintf("%s config %d", tool, vmid))
	if err != nil {
		return fmt.Errorf("读取独立IPv4客体网卡失败: %w", err)
	}
	var original string
	for _, line := range strings.Split(utils.StripTerminalCSI(output), "\n") {
		if strings.HasPrefix(line, "net0: ") {
			if original != "" {
				return fmt.Errorf("客体net0配置重复")
			}
			original = strings.TrimSpace(strings.TrimPrefix(line, "net0: "))
		}
	}
	if original == "" || strings.ContainsAny(original, "\r\n\x00") {
		return fmt.Errorf("独立IPv4客体net0配置缺失或无效")
	}
	values := map[string]string{"bridge": network.Bridge}
	cidr := network.Address.String() + "/" + strconv.Itoa(network.Prefix)
	if tool == "pct" {
		values["ip"], values["gw"] = cidr, network.Gateway.String()
	}
	parts := strings.Split(original, ",")
	seen := make(map[string]bool, len(parts))
	for i, part := range parts {
		key, _, found := strings.Cut(part, "=")
		if !found || key == "" || seen[key] {
			return fmt.Errorf("独立IPv4客体网卡存在无效或重复选项")
		}
		seen[key] = true
		if replacement, ok := values[key]; ok {
			parts[i] = key + "=" + replacement
			delete(values, key)
		}
	}
	for _, key := range []string{"bridge", "ip", "gw"} {
		if value, ok := values[key]; ok {
			parts = append(parts, key+"="+value)
		}
	}
	command := fmt.Sprintf("%s set %d --net0 %s", tool, vmid, utils.ShellSingleQuote(strings.Join(parts, ",")))
	if tool == "qm" {
		command += " --ipconfig0 " + utils.ShellSingleQuote("ip="+cidr+",gw="+network.Gateway.String())
	}
	if _, err := p.sshClient.Execute(command); err != nil {
		return fmt.Errorf("配置独立IPv4客体地址和网桥失败: %w", err)
	}
	return nil
}
