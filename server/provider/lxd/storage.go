package lxd

import (
	"fmt"
	"strings"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

func (l *LXDProvider) resolveStoragePoolForInstance() string {
	l.mu.RLock()
	configuredPool := strings.TrimSpace(l.config.StoragePool)
	client := l.sshClient
	l.mu.RUnlock()

	if client == nil {
		return configuredPool
	}

	if configuredPool != "" {
		checkCmd := fmt.Sprintf("lxc storage info %s >/dev/null 2>&1 && echo yes || echo no", shellSingleQuote(configuredPool))
		if output, err := client.Execute(checkCmd); err == nil && strings.TrimSpace(output) == "yes" {
			return configuredPool
		}
	}

	listCmd := "{ lxc storage list --format csv -c n 2>/dev/null || lxc storage list --format csv 2>/dev/null | cut -d, -f1; } | awk 'NF {print; exit}'"
	output, err := client.Execute(listCmd)
	if err == nil {
		detectedPool := strings.TrimSpace(output)
		if detectedPool != "" {
			l.mu.Lock()
			oldPool := l.config.StoragePool
			l.config.StoragePool = detectedPool
			l.mu.Unlock()
			if oldPool != detectedPool {
				global.APP_LOG.Warn("LXD实例创建前检测到存储池配置不可用，已使用远端真实存在的存储池",
					zap.String("provider", l.GetName()),
					zap.String("oldPool", oldPool),
					zap.String("newPool", detectedPool))
			}
			return detectedPool
		}
	}

	return configuredPool
}
