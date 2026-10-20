package utils

import "strings"

// NormalizeProviderPortMappingMethods also repairs legacy values at read and
// execution boundaries. PVE has no LXD proxy devices; NAT IPv4 requires DNAT.
// Preserve an explicitly selected IPv6 NAT mode and other providers' methods.
func NormalizeProviderPortMappingMethods(providerType, networkType, ipv4, ipv6 string) (string, string) {
	ipv4 = strings.ToLower(strings.TrimSpace(ipv4))
	ipv6 = strings.ToLower(strings.TrimSpace(ipv6))
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "docker", "podman", "containerd", "orbstack":
		return "native", "native"
	case "proxmox", "proxmoxve":
		switch strings.ToLower(strings.TrimSpace(networkType)) {
		case "nat_ipv4", "nat_ipv4_ipv6", "":
			ipv4 = "iptables"
		default:
			if ipv4 != "iptables" && ipv4 != "native" {
				ipv4 = "native"
			}
		}
		if ipv6 != "iptables" && ipv6 != "native" {
			ipv6 = "native"
		}
	default:
		if ipv4 == "" {
			ipv4 = "device_proxy"
		}
		if ipv6 == "" {
			ipv6 = "device_proxy"
		}
	}
	return ipv4, ipv6
}
