package ipv6tunnel

import (
	"fmt"
	"strings"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
)

// networkConfigPath has a lower lexical priority than distro-wide catch-all
// DHCP files such as 99-dhcp-others.network. That prevents networkd from
// removing the address and routes that the tunnel unit has just configured.
func networkConfigPath(id uint) string {
	return fmt.Sprintf("/etc/systemd/network/10-oneclickvirt-ipv6-tunnel-%d.network", id)
}

// routedBridgeNetworkConfigPath is deliberately per tunnel. Multiple routed
// tunnel units can safely carry the same bridge policy, and removing one
// leaves another matching policy in place when needed.
func routedBridgeNetworkConfigPath(id uint) string {
	return fmt.Sprintf("/etc/systemd/network/10-oneclickvirt-ipv6-tunnel-%d-routed-bridge.network", id)
}

func renderNetworkdConfig(tunnel providerModel.ProviderIPv6Tunnel) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, `[Match]
Name=%s

[Network]
DHCP=no
LinkLocalAddressing=no
IPv6Forwarding=true
ConfigureWithoutCarrier=yes

[Address]
Address=%s

[Route]
Destination=%s/128
Scope=link
Metric=%d
`, tunnel.Interface, tunnel.LocalIPv6, tunnel.RemoteIPv6, tunnel.RouteMetric)
	if tunnel.DefaultRoute {
		fmt.Fprintf(&builder, `
[Route]
Destination=::/0
Gateway=%s
GatewayOnLink=yes
Metric=%d
`, tunnel.RemoteIPv6, tunnel.RouteMetric)
	}
	return builder.String()
}

func renderRoutedBridgeNetworkdConfig() string {
	return fmt.Sprintf(`[Match]
Name=%s

[Network]
DHCP=no
LinkLocalAddressing=no
IPv6Forwarding=true
ConfigureWithoutCarrier=yes
`, utils.RoutedIPv6BridgeName)
}

const reloadNetworkdCommand = `
reload_networkd() {
  if command -v networkctl >/dev/null 2>&1 && systemctl is-active --quiet systemd-networkd; then
    networkctl reload
  fi
}
`
