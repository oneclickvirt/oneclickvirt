package incus

import (
	"fmt"
	"strings"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

func (i *IncusProvider) resolveStoragePoolForInstance() string {
	i.mu.RLock()
	configuredPool := strings.TrimSpace(i.config.StoragePool)
	client := i.sshClient
	i.mu.RUnlock()

	if client == nil {
		return configuredPool
	}

	if configuredPool != "" {
		checkCmd := fmt.Sprintf("incus storage info %s >/dev/null 2>&1 && echo yes || echo no", shellSingleQuote(configuredPool))
		if output, err := client.Execute(checkCmd); err == nil && strings.TrimSpace(output) == "yes" {
			return configuredPool
		}
	}

	listCmd := "{ incus storage list --format csv -c n 2>/dev/null || incus storage list --format csv 2>/dev/null | cut -d, -f1; } | awk 'NF {print; exit}'"
	output, err := client.Execute(listCmd)
	if err == nil {
		detectedPool := strings.TrimSpace(output)
		if detectedPool != "" {
			i.mu.Lock()
			oldPool := i.config.StoragePool
			i.config.StoragePool = detectedPool
			i.mu.Unlock()
			if oldPool != detectedPool {
				global.APP_LOG.Warn("Incus实例创建前检测到存储池配置不可用，已使用远端真实存在的存储池",
					zap.String("provider", i.GetName()),
					zap.String("oldPool", oldPool),
					zap.String("newPool", detectedPool))
			}
			return detectedPool
		}
	}

	return configuredPool
}
