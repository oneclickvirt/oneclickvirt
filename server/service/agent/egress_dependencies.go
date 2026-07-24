package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

func init() {
	RegisterAgentReconnectHook(func(providerID uint) {
		if global.APP_DB == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		restored, err := NewInstanceEgressService(global.APP_DB).RestoreProviderEgress(ctx, providerID, true)
		if err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("Agent重连后恢复独立出口失败",
					zap.Uint("provider_id", providerID), zap.Error(err))
			}
			return
		}
		if restored > 0 && global.APP_LOG != nil {
			global.APP_LOG.Info("Agent重连后独立出口恢复完成",
				zap.Uint("provider_id", providerID), zap.Int("restored", restored))
		}
	})
}

func (s *InstanceEgressService) EnsureDependencies(ctx context.Context, instanceID uint, packageSet string) (*InstanceEgressDependencyResult, error) {
	packageSet = strings.TrimSpace(packageSet)
	if packageSet == "" {
		packageSet = "wireguard"
	}
	if packageSet != "native" && packageSet != "wireguard" {
		return nil, fmt.Errorf("依赖集合仅支持native或wireguard")
	}
	_, node, config, err := s.loadContext(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	client, err := egressClient(node, config)
	if err != nil {
		return nil, err
	}
	result, err := client.EnsureEgressDependencies(packageSet)
	if err != nil {
		return nil, err
	}
	return &InstanceEgressDependencyResult{Result: result}, nil
}
