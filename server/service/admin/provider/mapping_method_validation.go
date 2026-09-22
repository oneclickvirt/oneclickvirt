package provider

import (
	"fmt"
	"strings"
)

func normalizeAgentNetworkType(requested string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "no_port_mapping"
	}
	return requested
}

func normalizeRequestedPortMappingMethod(value, field string) (string, error) {
	method := strings.ToLower(strings.TrimSpace(value))
	if method == "" {
		return "", nil
	}
	switch method {
	case "device_proxy", "iptables", "native":
		return method, nil
	default:
		return "", fmt.Errorf("%s端口映射方式 %q 无效，仅支持 device_proxy、iptables 或 native", field, value)
	}
}
