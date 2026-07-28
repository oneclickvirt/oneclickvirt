package agent

import (
	"encoding/json"
	"net"
	"sort"
	"strings"

	providerModel "oneclickvirt/model/provider"
)

// TrafficBinding is the canonical traffic identity sent to the Rust Agent.
// Addresses may contain both families when a runtime uses one NIC for IPv4 and IPv6.
type TrafficBinding struct {
	Interface string   `json:"interface"`
	Addresses []string `json:"addresses,omitempty"`
	Families  []string `json:"families,omitempty"`
}

const (
	trafficFamilyIPv4 = "ipv4"
	trafficFamilyIPv6 = "ipv6"
)

func normalizeTrafficAddress(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if ip, _, err := net.ParseCIDR(raw); err == nil {
		return ip.String()
	}
	if ip := net.ParseIP(raw); ip != nil {
		return ip.String()
	}
	return ""
}

func uniqueTrafficAddresses(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		address := normalizeTrafficAddress(value)
		if address == "" {
			continue
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		result = append(result, address)
	}
	return result
}

func normalizeTrafficFamilies(values ...string) []string {
	seen := make(map[string]struct{}, 2)
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case trafficFamilyIPv4:
			seen[trafficFamilyIPv4] = struct{}{}
		case trafficFamilyIPv6:
			seen[trafficFamilyIPv6] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	if _, ok := seen[trafficFamilyIPv4]; ok {
		result = append(result, trafficFamilyIPv4)
	}
	if _, ok := seen[trafficFamilyIPv6]; ok {
		result = append(result, trafficFamilyIPv6)
	}
	return result
}

func splitInstanceTrafficAddresses(instance *providerModel.Instance) (v4 []string, v6 []string) {
	v4 = uniqueTrafficAddresses(instance.PrivateIP)
	if len(v4) == 0 {
		v4 = uniqueTrafficAddresses(instance.PublicIP)
	}
	v6 = uniqueTrafficAddresses(instance.IPv6Address, instance.PublicIPv6)
	return v4, v6
}

func normalizeTrafficBindings(bindings []TrafficBinding) []TrafficBinding {
	type bindingParts struct {
		addresses []string
		families  []string
	}
	merged := make(map[string]bindingParts, len(bindings))
	order := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		iface := strings.TrimSpace(strings.Split(binding.Interface, "@")[0])
		if iface == "" {
			continue
		}
		if _, ok := merged[iface]; !ok {
			order = append(order, iface)
		}
		parts := merged[iface]
		parts.addresses = append(parts.addresses, binding.Addresses...)
		parts.families = append(parts.families, binding.Families...)
		for _, address := range binding.Addresses {
			if ip := net.ParseIP(normalizeTrafficAddress(address)); ip != nil {
				if ip.To4() != nil {
					parts.families = append(parts.families, trafficFamilyIPv4)
				} else {
					parts.families = append(parts.families, trafficFamilyIPv6)
				}
			}
		}
		merged[iface] = parts
	}

	result := make([]TrafficBinding, 0, len(order))
	for _, iface := range order {
		parts := merged[iface]
		addresses := uniqueTrafficAddresses(parts.addresses...)
		sort.Strings(addresses)
		families := normalizeTrafficFamilies(parts.families...)
		if len(families) == 0 {
			families = []string{trafficFamilyIPv4, trafficFamilyIPv6}
		}
		result = append(result, TrafficBinding{Interface: iface, Addresses: addresses, Families: families})
	}
	return result
}

func buildTrafficBindings(instance *providerModel.Instance, interfaces *InstanceInterfaces) []TrafficBinding {
	if interfaces == nil {
		return nil
	}
	v4, v6 := splitInstanceTrafficAddresses(instance)
	bindings := make([]TrafficBinding, 0, 2)

	v4Interface := strings.TrimSpace(interfaces.V4)
	v6Interface := strings.TrimSpace(interfaces.V6)
	expectsIPv4 := instance.NetworkType != "ipv6_only"
	expectsIPv6 := isIPv6Capable(instance.NetworkType)
	if v4Interface != "" && (len(v4) > 0 || expectsIPv4) {
		bindings = append(bindings, TrafficBinding{
			Interface: v4Interface,
			Addresses: v4,
			Families:  []string{trafficFamilyIPv4},
		})
	}
	if v6Interface != "" && (len(v6) > 0 || expectsIPv6) {
		bindings = append(bindings, TrafficBinding{
			Interface: v6Interface,
			Addresses: v6,
			Families:  []string{trafficFamilyIPv6},
		})
	}
	return normalizeTrafficBindings(bindings)
}

func bindingsInterfaces(bindings []TrafficBinding) []string {
	result := make([]string, 0, len(bindings))
	for _, binding := range normalizeTrafficBindings(bindings) {
		result = append(result, binding.Interface)
	}
	return result
}

func bindingsLegacyInnerIP(bindings []TrafficBinding) string {
	for _, binding := range normalizeTrafficBindings(bindings) {
		for _, address := range binding.Addresses {
			if ip := net.ParseIP(address); ip != nil && ip.To4() != nil {
				return address
			}
		}
	}
	return ""
}

func marshalTrafficBindings(bindings []TrafficBinding) string {
	encoded, err := json.Marshal(normalizeTrafficBindings(bindings))
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func unmarshalTrafficBindings(raw string) []TrafficBinding {
	var bindings []TrafficBinding
	if err := json.Unmarshal([]byte(raw), &bindings); err != nil {
		return nil
	}
	return normalizeTrafficBindings(bindings)
}

func trafficBindingsEqual(left, right []TrafficBinding) bool {
	return marshalTrafficBindings(left) == marshalTrafficBindings(right)
}
