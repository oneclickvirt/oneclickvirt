package utils

import (
	"encoding/json"
	"fmt"
	"strings"
)

type hostIPv6ProbeInterface struct {
	Name     string `json:"ifname"`
	AddrInfo []struct {
		Family    string   `json:"family"`
		Local     string   `json:"local"`
		PrefixLen *int     `json:"prefixlen"`
		Scope     string   `json:"scope"`
		Flags     []string `json:"flags"`
		Tentative bool     `json:"tentative"`
		DadFailed bool     `json:"dadfailed"`
	} `json:"addr_info"`
}

type hostIPv6ProbeRoute struct {
	Destination string `json:"dst"`
	Device      string `json:"dev"`
	NextHops    []struct {
		Device string `json:"dev"`
	} `json:"nexthops"`
	Multipath []struct {
		Device string `json:"dev"`
	} `json:"multipath"`
}

// SelectPublicIPv6InterfaceNetworkJSON reads iproute2's machine-readable
// output. The JSON field names remain stable when the host's display language
// changes, and the selected CIDR always stays paired with its own interface.
func SelectPublicIPv6InterfaceNetworkJSON(addressOutput, routeOutput string, requireAssignable bool) (IPv6InterfaceNetwork, error) {
	var interfaces []hostIPv6ProbeInterface
	var routes []hostIPv6ProbeRoute
	addressOutput = terminalCSISequence.ReplaceAllString(strings.TrimSpace(addressOutput), "")
	routeOutput = terminalCSISequence.ReplaceAllString(strings.TrimSpace(routeOutput), "")
	if err := json.Unmarshal([]byte(addressOutput), &interfaces); err != nil || interfaces == nil {
		return IPv6InterfaceNetwork{}, fmt.Errorf("无法解析宿主机IPv6接口JSON")
	}
	if err := json.Unmarshal([]byte(routeOutput), &routes); err != nil || routes == nil {
		return IPv6InterfaceNetwork{}, fmt.Errorf("无法解析宿主机IPv6默认路由JSON")
	}
	preferredInterface := ""
	for _, route := range routes {
		// Treat an omitted dst field as the default route. The command already
		// filters to default routes, and this keeps parsing compatible with
		// older iproute2 JSON output.
		if route.Destination != "" && route.Destination != "default" {
			continue
		}
		setPreferred := func(raw string) bool {
			name, err := ParseNetworkInterfaceOutput(raw)
			if err != nil {
				return false
			}
			preferredInterface = name
			return true
		}
		if route.Device != "" && setPreferred(route.Device) {
			break
		}
		for _, hop := range route.NextHops {
			if setPreferred(hop.Device) {
				break
			}
		}
		if preferredInterface != "" {
			break
		}
		for _, hop := range route.Multipath {
			if setPreferred(hop.Device) {
				break
			}
		}
		if preferredInterface != "" {
			break
		}
	}
	var best IPv6InterfaceNetwork
	found := false
	for _, iface := range interfaces {
		name, err := ParseNetworkInterfaceOutput(iface.Name)
		if err != nil {
			continue
		}
		for _, info := range iface.AddrInfo {
			if info.Family != "inet6" || (info.Scope != "" && info.Scope != "global") || info.PrefixLen == nil || info.Tentative || info.DadFailed {
				continue
			}
			unready := false
			for _, flag := range info.Flags {
				if flag == "tentative" || flag == "dadfailed" {
					unready = true
					break
				}
			}
			if unready || !IsPublicIPv6(info.Local) {
				continue
			}
			network, err := ParseIPv6Network(fmt.Sprintf("%s/%d", info.Local, *info.PrefixLen), 64)
			if err != nil || (requireAssignable && network.PrefixLen == 128) {
				continue
			}
			candidate := IPv6InterfaceNetwork{Interface: name, Network: network}
			if !found || betterIPv6InterfaceNetworkCandidate(candidate, best, preferredInterface) {
				best = candidate
				found = true
			}
		}
	}
	if !found {
		if requireAssignable {
			return IPv6InterfaceNetwork{}, fmt.Errorf("未找到可分配的本机公网IPv6前缀")
		}
		return IPv6InterfaceNetwork{}, fmt.Errorf("未找到本机绑定的公网IPv6前缀")
	}
	return best, nil
}

// ParseFirstGlobalIPv6AddressJSON reads a guest's iproute2 address JSON.
// Global scope includes both public and ULA guest addresses. The caller can
// separately require a public address when its network mode needs one.
func ParseFirstGlobalIPv6AddressJSON(output string) (string, error) {
	var interfaces []hostIPv6ProbeInterface
	output = terminalCSISequence.ReplaceAllString(strings.TrimSpace(output), "")
	if err := json.Unmarshal([]byte(output), &interfaces); err != nil || interfaces == nil {
		return "", fmt.Errorf("无法解析实例IPv6接口JSON")
	}
	for _, iface := range interfaces {
		for _, info := range iface.AddrInfo {
			if info.Family != "inet6" || info.Scope != "global" || info.Tentative || info.DadFailed {
				continue
			}
			unready := false
			for _, flag := range info.Flags {
				if flag == "tentative" || flag == "dadfailed" {
					unready = true
					break
				}
			}
			if unready {
				continue
			}
			if address, err := NormalizeIPv6Address(info.Local); err == nil {
				return address, nil
			}
		}
	}
	return "", fmt.Errorf("实例尚无可用的全局作用域IPv6地址")
}
