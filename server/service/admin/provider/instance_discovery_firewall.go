package provider

import (
	"context"
	"net"
	"strings"

	providerCore "oneclickvirt/provider"
	"oneclickvirt/provider/firewall"
)

// enrichDiscoveredFirewallMappings is a provider-agnostic compatibility pass
// for node-side DNAT. Native runtime/proxy mappings remain the primary source;
// nftables and iptables rules are merged afterwards and normalized by the
// common discovery pipeline.
func enrichDiscoveredFirewallMappings(ctx context.Context, providerInstance providerCore.Provider, instances []providerCore.DiscoveredInstance) []providerCore.DiscoveredInstance {
	switch strings.ToLower(strings.TrimSpace(providerInstance.GetType())) {
	case "lxd", "incus":
		// These providers can use either native proxy devices or node-side DNAT.
	default:
		// Proxmox/QEMU/KubeVirt already enrich inside their provider discovery,
		// while container runtimes expose authoritative bindings themselves.
		return instances
	}
	hasIPv4 := false
	for _, instance := range instances {
		if normalizeDiscoveryFirewallIP(instance.PrivateIP) != "" {
			hasIPv4 = true
			break
		}
	}
	if !hasIPv4 || ctx.Err() != nil {
		return instances
	}

	nftOutput, nftErr := providerInstance.ExecuteSSHCommand(ctx, "nft -a list ruleset 2>/dev/null || true")
	iptablesOutput, iptablesErr := providerInstance.ExecuteSSHCommand(ctx, "iptables-save -t nat 2>/dev/null || iptables -t nat -S 2>/dev/null || iptables -t nat -L PREROUTING -n 2>/dev/null || true")
	if nftErr != nil && iptablesErr != nil {
		return instances
	}
	rulesByIP := firewall.ParseDNATRules(nftOutput, iptablesOutput)
	for index := range instances {
		ip := normalizeDiscoveryFirewallIP(instances[index].PrivateIP)
		for _, rule := range rulesByIP[ip] {
			instances[index].PortMappings = append(instances[index].PortMappings, providerCore.DiscoveredPortMapping{
				HostPort: rule.HostPort, GuestPort: rule.GuestPort, Protocol: rule.Protocol, IsSSH: rule.IsSSH, MappingMethod: "iptables",
			})
			if rule.IsSSH {
				instances[index].SSHPort = rule.HostPort
			} else {
				instances[index].ExtraPorts = append(instances[index].ExtraPorts, rule.HostPort)
			}
		}
	}
	return instances
}

func normalizeDiscoveryFirewallIP(value string) string {
	value = strings.TrimSpace(value)
	if ip, _, err := net.ParseCIDR(value); err == nil && ip.To4() != nil {
		return ip.String()
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil && ip.To4() != nil {
		return ip.String()
	}
	return ""
}
