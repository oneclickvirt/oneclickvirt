package agent

import (
	"context"
	"fmt"
	"strings"

	"oneclickvirt/global"
)

func init() {
	RegisterAgentReconnectHook(func(providerID uint) {
		if global.APP_DB == nil {
			return
		}
		// A reconnect, a burst of recovered starts, and network-address repair can
		// happen together after a node reboot. Route all of them through one
		// debounced provider-wide replay instead of issuing one Agent request per
		// event or per instance.
		ScheduleProviderEgressRefresh(global.APP_DB, providerID, true)
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
