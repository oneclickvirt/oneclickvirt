package resources

import (
	"fmt"
	"sort"
)

const requiredFixedSSHPort = 22

// NormalizeFixedPorts returns a deterministic fixed guest-port list.
// SSH/22 is always present because the instance SSH entry is managed by this
// default-port allocation path.
func NormalizeFixedPorts(raw []int) ([]int, error) {
	seen := map[int]struct{}{requiredFixedSSHPort: {}}
	for _, port := range raw {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("固定端口 %d 无效，必须在 1-65535 范围内", port)
		}
		seen[port] = struct{}{}
	}

	ports := make([]int, 0, len(seen))
	for port := range seen {
		if port != requiredFixedSSHPort {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return append([]int{requiredFixedSSHPort}, ports...), nil
}

func NormalizeProviderFixedPorts(raw []int, defaultPortCount int) ([]int, error) {
	if defaultPortCount <= 0 {
		defaultPortCount = 10
	}
	ports, err := NormalizeFixedPorts(raw)
	if err != nil {
		return nil, err
	}
	if len(ports) > defaultPortCount {
		return nil, fmt.Errorf("固定端口数量 %d 不能超过默认端口数 %d", len(ports), defaultPortCount)
	}
	return ports, nil
}
