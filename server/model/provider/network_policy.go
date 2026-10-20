package provider

import (
	"fmt"
	"strings"
)

func IsInstanceNetworkType(value string) bool {
	switch value {
	case "nat_ipv4", "nat_ipv4_ipv6", "dedicated_ipv4", "dedicated_ipv4_ipv6", "ipv6_only", "no_port_mapping":
		return true
	}
	return false
}

// NormalizeAllowedNetworkTypes validates the administrator's additional choices.
// An empty list preserves the existing default-only policy on upgraded nodes.
func NormalizeAllowedNetworkTypes(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if !IsInstanceNetworkType(value) {
			return nil, fmt.Errorf("无效的可选网络类型: %q", value)
		}
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result, nil
}

// UserNetworkTypes always includes the node default; extra modes need an
// explicit administrator grant and never change existing instance snapshots.
func (p *Provider) UserNetworkTypes() []string {
	defaultType := strings.ToLower(strings.TrimSpace(p.NetworkType))
	if defaultType == "" {
		defaultType = "nat_ipv4"
	}
	result := []string{}
	seen := make(map[string]bool)
	for _, value := range append([]string{defaultType}, p.AllowedNetworkTypes...) {
		if IsInstanceNetworkType(value) && !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result
}

func (p *Provider) ResolveUserNetworkType(requested string) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		requested = strings.ToLower(strings.TrimSpace(p.NetworkType))
		if requested == "" {
			requested = "nat_ipv4"
		}
	}
	for _, value := range p.UserNetworkTypes() {
		if requested == value {
			return requested, nil
		}
	}
	return "", fmt.Errorf("该节点未开放所选网络类型，请重新选择")
}
