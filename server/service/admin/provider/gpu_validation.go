package provider

import (
	"fmt"
	"strings"
)

func normalizeProviderGPUConfig(providerType string, enabled bool, deviceIDs string) (bool, string, error) {
	if providerType != "lxd" && providerType != "incus" {
		return false, "", nil
	}
	deviceIDs = strings.TrimSpace(deviceIDs)
	if !enabled {
		return false, "", nil
	}
	if deviceIDs == "" {
		return true, "", nil
	}
	parts := strings.Split(deviceIDs, ",")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id == "" {
			return false, "", fmt.Errorf("GPU 设备 ID 不能为空")
		}
		for _, r := range id {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
				r == '_' || r == '-' || r == '.' || r == ':' {
				continue
			}
			return false, "", fmt.Errorf("GPU 设备 ID 包含非法字符")
		}
		normalized = append(normalized, id)
	}
	return true, strings.Join(normalized, ","), nil
}
