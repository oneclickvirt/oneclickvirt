package provider

import "strings"

// ExpandPortMappingFamilies resolves the persisted family contract for node
// operations. A NAT dual-stack row with IPv6Enabled=true represents both
// families, while an explicit IPv6Address identifies a single static family.
// Returned copies are for node operations only and must not be persisted.
func ExpandPortMappingFamilies(port Port, networkType, ipv4Method, ipv6Method string) []Port {
	if port.MappingType == "controller" {
		if port.IsAutomatic && port.PortType == "range_mapped" && strings.TrimSpace(port.IPv6Address) == "" {
			port.IPv6Enabled = false // The tunnel's InternalHost/guest IPv4 is authoritative.
		}
		return []Port{port}
	}
	// A NAT dual-stack mapping row represents the same public port in both
	// families. This applies to manual rows as well as the historical automatic
	// range row: the API contract uses ipv6Enabled=true (or an omitted value,
	// which is defaulted to true) to request dual-stack behavior. An explicit
	// IPv6Address remains a single-family mapping because it identifies a
	// particular routed/static guest address rather than the NAT guest target.
	dualStackDefault := port.IPv6Enabled && strings.TrimSpace(port.IPv6Address) == "" && networkType == "nat_ipv4_ipv6"
	if dualStackDefault {
		ipv4, ipv6 := port, port
		ipv4.IPv6Enabled = false
		if strings.TrimSpace(ipv4.MappingMethod) == "" {
			ipv4.MappingMethod = ipv4Method
		}
		ipv6.MappingMethod = ipv6Method
		return []Port{ipv4, ipv6}
	}
	if strings.TrimSpace(port.MappingMethod) == "" {
		if port.IPv6Enabled || strings.TrimSpace(port.IPv6Address) != "" || networkType == "ipv6_only" {
			port.MappingMethod = ipv6Method
		} else {
			port.MappingMethod = ipv4Method
		}
	}
	if networkType == "ipv6_only" {
		port.IPv6Enabled = true
	}
	return []Port{port}
}
