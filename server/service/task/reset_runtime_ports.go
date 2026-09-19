package task

import (
	"fmt"
	"strings"

	providerModel "oneclickvirt/model/provider"
)

// Container runtimes bind NAT IPv4 ports during creation. Independent IPv6
// connectivity is configured separately, not duplicated as host NAT bindings.
func resetRuntimePortBindings(ports []providerModel.Port) ([]string, error) {
	var bindings []string
	for _, port := range ports {
		if port.MappingType == "controller" {
			continue
		}
		endpoints, err := expandPortEndpoints(port)
		if err != nil {
			return nil, fmt.Errorf("重建运行时端口 %d 无效: %w", port.HostPort, err)
		}
		protocol := strings.ToLower(strings.TrimSpace(port.Protocol))
		protocols := []string{protocol}
		if protocol == "" || protocol == "both" {
			protocols = []string{"tcp", "udp"}
		}
		for _, protocol := range protocols {
			if protocol != "tcp" && protocol != "udp" {
				return nil, fmt.Errorf("重建端口协议无效: %q", protocol)
			}
			for _, endpoint := range endpoints {
				bindings = append(bindings, fmt.Sprintf("0.0.0.0:%d:%d/%s", endpoint.host, endpoint.guest, protocol))
			}
		}
	}
	return bindings, nil
}
