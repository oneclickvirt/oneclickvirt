package portmapping

import (
	"context"
	"fmt"
)

// Manager 端口映射管理器
type Manager struct {
	config    *ManagerConfig
	providers map[string]func(*ManagerConfig) PortMappingProvider
}

// NewManager 创建端口映射管理器
func NewManager(config *ManagerConfig) *Manager {
	return &Manager{
		config:    config,
		providers: make(map[string]func(*ManagerConfig) PortMappingProvider),
	}
}

// RegisterProvider 注册Provider到管理器
func (m *Manager) RegisterProvider(providerType string, factory func(*ManagerConfig) PortMappingProvider) {
	m.providers[providerType] = factory
}

// GetProvider 从管理器获取Provider
func (m *Manager) GetProvider(providerType string) (PortMappingProvider, error) {
	factory, exists := m.providers[providerType]
	if !exists {
		// 尝试从全局注册表获取
		return GetProviderWithConfig(providerType, m.config)
	}
	return factory(m.config), nil
}

// CreatePortMapping 创建端口映射（统一入口）
func (m *Manager) CreatePortMapping(ctx context.Context, providerType string, req *PortMappingRequest) (*PortMappingResult, error) {
	provider, err := m.GetProvider(providerType)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}

	// 如果没有指定映射方法，使用默认方法
	if req.MappingMethod == "" && m.config != nil {
		req.MappingMethod = m.config.DefaultMappingMethod
	}

	return provider.CreatePortMapping(ctx, req)
}

// DeletePortMapping 删除端口映射（统一入口）
func (m *Manager) DeletePortMapping(ctx context.Context, providerType string, req *DeletePortMappingRequest) error {
	provider, err := m.GetProvider(providerType)
	if err != nil {
		return fmt.Errorf("failed to get provider: %v", err)
	}

	return provider.DeletePortMapping(ctx, req)
}

// UpdatePortMapping 更新端口映射（统一入口）
func (m *Manager) UpdatePortMapping(ctx context.Context, providerType string, req *UpdatePortMappingRequest) (*PortMappingResult, error) {
	provider, err := m.GetProvider(providerType)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}

	// 检查Provider是否支持动态映射
	if !provider.SupportsDynamicMapping() {
		return nil, fmt.Errorf("provider %s does not support dynamic port mapping updates", providerType)
	}

	return provider.UpdatePortMapping(ctx, req)
}

// ListPortMappings 列出端口映射（统一入口）
func (m *Manager) ListPortMappings(ctx context.Context, providerType string, instanceID string) ([]*PortMappingResult, error) {
	provider, err := m.GetProvider(providerType)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}

	return provider.ListPortMappings(ctx, instanceID)
}

// GetSupportedProviders 获取支持的Provider类型列表
func (m *Manager) GetSupportedProviders() []string {
	var providers []string
	for providerType := range m.providers {
		providers = append(providers, providerType)
	}

	// 也包括全局注册的Provider
	for providerType := range globalRegistry.providers {
		found := false
		for _, p := range providers {
			if p == providerType {
				found = true
				break
			}
		}
		if !found {
			providers = append(providers, providerType)
		}
	}

	return providers
}

// AutoSelectProvider 自动选择最适合的Provider
func (m *Manager) AutoSelectProvider(instanceType string) string {
	// 根据实例类型自动选择最适合的端口映射Provider
	switch instanceType {
	case "docker":
		return "docker"
	case "lxd":
		return "lxd"
	case "incus":
		return "incus"
	case "pve", "proxmox":
		return "pve"
	default:
		// 默认使用iptables
		return "iptables"
	}
}

// GetProviderCapabilities 获取Provider能力信息
func (m *Manager) GetProviderCapabilities(providerType string) map[string]interface{} {
	provider, err := m.GetProvider(providerType)
	if err != nil {
		return map[string]interface{}{
			"available":        false,
			"supports_dynamic": false,
			"error":            err.Error(),
		}
	}

	capabilities := map[string]interface{}{
		"available":        true,
		"supports_dynamic": provider.SupportsDynamicMapping(),
		"type":             provider.GetProviderType(),
	}

	// 特定Provider的能力信息
	switch providerType {
	case "docker":
		capabilities["description"] = "Docker原生端口映射，端口在容器创建时固定"
		capabilities["methods"] = []string{"port-binding"}
		capabilities["limitations"] = []string{"不支持运行时端口修改", "需要重新创建容器"}
	case "lxd":
		capabilities["description"] = "LXD原生端口映射，支持动态调整"
		capabilities["methods"] = []string{"proxy-device"}
		capabilities["limitations"] = []string{}
	case "incus":
		capabilities["description"] = "Incus原生端口映射，支持动态调整"
		capabilities["methods"] = []string{"proxy-device"}
		capabilities["limitations"] = []string{}
	case "pve":
		capabilities["description"] = "Proxmox VE使用iptables端口转发"
		capabilities["methods"] = []string{"iptables-nat"}
		capabilities["limitations"] = []string{"需要root权限"}
	case "iptables":
		capabilities["description"] = "通用iptables NAT端口映射"
		capabilities["methods"] = []string{"nat", "dnat", "snat"}
		capabilities["limitations"] = []string{"需要root权限"}
	}

	return capabilities
}

// GetStats 获取端口映射统计信息
func (m *Manager) GetStats() map[string]interface{} {
	stats := map[string]interface{}{
		"total_providers": len(m.providers),
		"supported_types": m.GetSupportedProviders(),
		"config":          m.config,
	}

	// 统计每种Provider的使用情况和能力
	providerStats := make(map[string]interface{})
	for _, providerType := range m.GetSupportedProviders() {
		capabilities := m.GetProviderCapabilities(providerType)
		providerStats[providerType] = map[string]interface{}{
			"capabilities": capabilities,
			"usage_count":  0, // TODO: 从数据库统计, 暂时设为0
		}
	}
	stats["provider_details"] = providerStats

	return stats
}
