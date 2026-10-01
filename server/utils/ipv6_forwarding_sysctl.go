package utils

import (
	"encoding/json"
	"fmt"
	"strings"
)

// IPv6ForwardingInterfaces keeps router advertisements enabled on both the
// allocation interface and every IPv6 default-route interface. Linux ignores
// RA on an interface whose accept_ra remains 1 after forwarding is enabled.
func IPv6ForwardingInterfaces(routeOutput, allocationInterface string) ([]string, error) {
	allocationInterface, err := ParseNetworkInterfaceOutput(allocationInterface)
	if err != nil {
		return nil, fmt.Errorf("无效的IPv6分配接口: %w", err)
	}
	var routes []struct {
		Destination string `json:"dst"`
		Device      string `json:"dev"`
		NextHops    []struct {
			Device string `json:"dev"`
		} `json:"nexthops"`
		Multipath []struct {
			Device string `json:"dev"`
		} `json:"multipath"`
	}
	routeOutput = StripTerminalCSI(strings.TrimSpace(routeOutput))
	if err := json.Unmarshal([]byte(routeOutput), &routes); err != nil || routes == nil {
		return nil, fmt.Errorf("无法解析宿主机IPv6默认路由JSON")
	}
	result := []string{allocationInterface}
	seen := map[string]bool{allocationInterface: true}
	add := func(raw string) error {
		device, err := ParseNetworkInterfaceOutput(raw)
		if err != nil {
			return fmt.Errorf("IPv6默认路由接口无效: %w", err)
		}
		if !seen[device] {
			result = append(result, device)
			seen[device] = true
		}
		return nil
	}
	for _, route := range routes {
		// `ip -j route show default` normally includes dst=default, but
		// older iproute2 releases may omit the field for the default route.
		if route.Destination != "" && route.Destination != "default" {
			continue
		}
		if route.Device == "" && len(route.NextHops) == 0 && len(route.Multipath) == 0 {
			return nil, fmt.Errorf("IPv6默认路由缺少接口，无法保护宿主机RA")
		}
		if route.Device != "" {
			if err := add(route.Device); err != nil {
				return nil, err
			}
		}
		for _, hop := range route.NextHops {
			if err := add(hop.Device); err != nil {
				return nil, err
			}
		}
		for _, hop := range route.Multipath {
			if err := add(hop.Device); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

// IPv6ForwardingSysctlCommand applies accept_ra=2 before enabling forwarding
// and persists the same order in one installer-owned sysctl file.
func IPv6ForwardingSysctlCommand(interfaces []string) (string, error) {
	if len(interfaces) == 0 {
		return "", fmt.Errorf("IPv6转发缺少接口")
	}
	validated := make([]string, 0, len(interfaces))
	seen := make(map[string]bool)
	for _, raw := range interfaces {
		device, err := ParseNetworkInterfaceOutput(raw)
		if err != nil {
			return "", fmt.Errorf("无效的IPv6网络接口: %w", err)
		}
		if !seen[device] {
			validated = append(validated, ShellSingleQuote(device))
			seen[device] = true
		}
	}
	return fmt.Sprintf(`set -eu
conf=/etc/sysctl.d/99-oneclickvirt-ipv6.conf
mkdir -p /etc/sysctl.d
tmp="${conf}.tmp.$$"
{
  for iface in %s; do
    if [ -e "/proc/sys/net/ipv6/conf/$iface/accept_ra" ]; then
      printf 'net.ipv6.conf.%%s.accept_ra=2\n' "$iface"
    fi
  done
  printf 'net.ipv6.conf.all.forwarding=1\n'
  printf 'net.ipv6.conf.default.forwarding=1\n'
  printf 'net.ipv6.conf.all.proxy_ndp=1\n'
  for iface in %s; do
    if [ -e "/proc/sys/net/ipv6/conf/$iface/proxy_ndp" ]; then
      printf 'net.ipv6.conf.%%s.proxy_ndp=1\n' "$iface"
    fi
  done
} > "$tmp"
chmod 0644 "$tmp"
mv -f "$tmp" "$conf"
for iface in %s; do
  if [ -e "/proc/sys/net/ipv6/conf/$iface/accept_ra" ]; then
    sysctl -w "net.ipv6.conf.$iface.accept_ra=2" >/dev/null
  fi
done
sysctl -w net.ipv6.conf.all.forwarding=1 >/dev/null
sysctl -w net.ipv6.conf.default.forwarding=1 >/dev/null
sysctl -w net.ipv6.conf.all.proxy_ndp=1 >/dev/null
for iface in %s; do
  if [ -e "/proc/sys/net/ipv6/conf/$iface/proxy_ndp" ]; then
    sysctl -w "net.ipv6.conf.$iface.proxy_ndp=1" >/dev/null
  fi
done`, strings.Join(validated, " "), strings.Join(validated, " "), strings.Join(validated, " "), strings.Join(validated, " ")), nil
}
