package utils

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

type hostIPv6AddressJSON struct {
	AddrInfo []struct {
		Family string `json:"family"`
		Local  string `json:"local"`
	} `json:"addr_info"`
}

type hostIPv6RouteJSON struct {
	Dst      string `json:"dst"`
	Gateway  string `json:"gateway"`
	NextHops []struct {
		Gateway string `json:"gateway"`
	} `json:"nexthops"`
	Multipath []struct {
		Gateway string `json:"gateway"`
	} `json:"multipath"`
}

// ParseHostIPv6Reservations returns addresses that must never be handed to a
// guest: every local IPv6 address, every route gateway, and exact host routes.
// iproute2 JSON avoids dependence on the machine's display language.
func ParseHostIPv6Reservations(addressOutput, routeOutput string) ([]string, error) {
	var addresses []hostIPv6AddressJSON
	var routes []hostIPv6RouteJSON
	addressOutput = terminalCSISequence.ReplaceAllString(strings.TrimSpace(addressOutput), "")
	routeOutput = terminalCSISequence.ReplaceAllString(strings.TrimSpace(routeOutput), "")
	if err := json.Unmarshal([]byte(addressOutput), &addresses); err != nil || addresses == nil {
		return nil, fmt.Errorf("无法解析宿主机IPv6地址JSON")
	}
	if err := json.Unmarshal([]byte(routeOutput), &routes); err != nil || routes == nil {
		return nil, fmt.Errorf("无法解析宿主机IPv6路由JSON")
	}
	reserved := make(map[netip.Addr]struct{})
	add := func(raw string) error {
		address, err := netip.ParseAddr(raw)
		if err != nil || !address.Is6() {
			return fmt.Errorf("宿主机IPv6地址或网关无效")
		}
		reserved[address.WithZone("")] = struct{}{}
		return nil
	}
	for _, iface := range addresses {
		for _, address := range iface.AddrInfo {
			if address.Family == "inet6" {
				if err := add(address.Local); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, route := range routes {
		gateways := []string{route.Gateway}
		for _, hop := range route.NextHops {
			gateways = append(gateways, hop.Gateway)
		}
		for _, hop := range route.Multipath {
			gateways = append(gateways, hop.Gateway)
		}
		for _, gateway := range gateways {
			if gateway == "" {
				continue
			}
			if err := add(gateway); err != nil {
				return nil, err
			}
		}
		if route.Dst != "" && route.Dst != "default" {
			if !strings.Contains(route.Dst, "/") {
				// `ip -j route show table all` can expose route-kind labels
				// such as "local" or "multicast" in dst. They are not
				// addresses and must not make an otherwise valid host probe
				// fail. Real exact IPv6 destinations remain reserved.
				if address, err := netip.ParseAddr(route.Dst); err == nil && address.Is6() {
					reserved[address.WithZone("")] = struct{}{}
				}
				continue
			}
			prefix, err := netip.ParsePrefix(route.Dst)
			if err != nil || !prefix.Addr().Is6() {
				// Ignore non-address route-kind labels while keeping the
				// parser fail-closed for malformed gateway values above.
				continue
			}
			if prefix.Bits() == 128 {
				reserved[prefix.Addr().WithZone("")] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(reserved))
	for address := range reserved {
		result = append(result, address.String())
	}
	return result, nil
}

func ReadHostIPv6Reservations(executor ShellExecutor) ([]string, error) {
	if executor == nil {
		return nil, fmt.Errorf("缺少宿主机IPv6执行器")
	}
	addresses, err := executor.Execute("LC_ALL=C NO_COLOR=1 ip -j -6 addr show")
	if err != nil {
		return nil, fmt.Errorf("读取宿主机IPv6地址失败: %w", err)
	}
	routes, err := executor.Execute("LC_ALL=C NO_COLOR=1 ip -j -6 route show table all")
	if err != nil {
		return nil, fmt.Errorf("读取宿主机IPv6路由失败: %w", err)
	}
	return ParseHostIPv6Reservations(addresses, routes)
}

func HostIPv6AddressReserved(address string, reserved []string) bool {
	parsed, err := netip.ParseAddr(address)
	if err != nil || !parsed.Is6() {
		return true
	}
	for _, value := range reserved {
		candidate, err := netip.ParseAddr(value)
		if err != nil || !candidate.Is6() {
			return true
		}
		if parsed.WithZone("") == candidate.WithZone("") {
			return true
		}
	}
	return false
}
