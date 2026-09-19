package portmapping

import (
	"context"
	"fmt"
	"sync"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
)

func canonicalProviderType(providerType string) string {
	switch providerType {
	case "pve", "proxmox", "proxmoxve", "qemu", "kubevirt", "vmware", "virtualbox", "multipass", "vagrant":
		return "iptables"
	default:
		return providerType
	}
}

// Manager 端口映射管理器
type Manager struct {
	config    *ManagerConfig
	providers map[string]func(*ManagerConfig) PortMappingProvider
	mu        sync.RWMutex
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[providerType] = factory
}

// GetProvider 从管理器获取Provider
func (m *Manager) GetProvider(providerType string) (PortMappingProvider, error) {
	m.mu.RLock()
	factory, exists := m.providers[providerType]
	config := m.config
	canonicalType := canonicalProviderType(providerType)
	var mappedFactory func(*ManagerConfig) PortMappingProvider
	var mapped bool
	if !exists && canonicalType != providerType {
		mappedFactory, mapped = m.providers[canonicalType]
	}
	m.mu.RUnlock()
	if !exists {
		if canonicalType != providerType {
			if mapped {
				return mappedFactory(config), nil
			}
			return GetProviderWithConfig(canonicalType, config)
		}
		// 尝试从全局注册表获取
		return GetProviderWithConfig(providerType, config)
	}
	return factory(config), nil
}

// CreatePortMapping 创建端口映射（统一入口）
func (m *Manager) CreatePortMapping(ctx context.Context, providerType string, req *PortMappingRequest) (*PortMappingResult, error) {
	if req == nil {
		return nil, fmt.Errorf("port mapping request is nil")
	}
	provider, err := m.GetProvider(providerType)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}

	// 如果没有指定映射方法，使用默认方法
	if req.MappingMethod == "" {
		m.mu.RLock()
		config := m.config
		m.mu.RUnlock()
		if config != nil {
			req.MappingMethod = config.DefaultMappingMethod
		}
	}

	return provider.CreatePortMapping(ctx, req)
}

// DeletePortMapping 删除端口映射（统一入口）
func (m *Manager) DeletePortMapping(ctx context.Context, providerType string, req *DeletePortMappingRequest) error {
	if req == nil {
		return fmt.Errorf("port mapping delete request is nil")
	}
	provider, err := m.GetProvider(providerType)
	if err != nil {
		return fmt.Errorf("failed to get provider: %v", err)
	}

	return provider.DeletePortMapping(ctx, req)
}

// UpdatePortMapping 更新端口映射（统一入口）
func (m *Manager) UpdatePortMapping(ctx context.Context, providerType string, req *UpdatePortMappingRequest) (*PortMappingResult, error) {
	if req == nil {
		return nil, fmt.Errorf("port mapping update request is nil")
	}
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
	m.mu.RLock()
	var providers []string
	for providerType := range m.providers {
		providers = append(providers, providerType)
	}
	m.mu.RUnlock()

	// 也包括全局注册的Provider
	for _, providerType := range ListProviders() {
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
	case "docker", "orbstack":
		return "docker"
	case "lxd":
		return "lxd"
	case "incus":
		return "incus"
	case "pve", "proxmox", "proxmoxve":
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
	if provider == nil {
		return map[string]interface{}{
			"available":        false,
			"supports_dynamic": false,
			"error":            fmt.Sprintf("provider %s returned nil", providerType),
		}
	}

	capabilities := map[string]interface{}{
		"available":        true,
		"supports_dynamic": provider.SupportsDynamicMapping(),
		"type":             provider.GetProviderType(),
	}

	// 特定Provider的能力信息
	switch providerType {
	case "docker", "orbstack":
		capabilities["description"] = "容器运行时原生端口映射，端口在容器创建时固定"
		capabilities["methods"] = []string{"port-binding"}
		capabilities["limitations"] = []string{"不支持运行时端口修改", "需要重新创建容器"}
	case "qemu", "kubevirt", "vmware", "virtualbox", "multipass", "vagrant":
		capabilities["description"] = "本地虚拟机使用iptables端口转发"
		capabilities["methods"] = []string{"iptables-nat"}
		capabilities["limitations"] = []string{"需要root权限", "需要虚拟机内网地址可达"}
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
	m.mu.RLock()
	localProviderCount := len(m.providers)
	config := m.config
	m.mu.RUnlock()
	stats := map[string]interface{}{
		"total_providers": localProviderCount,
		"supported_types": m.GetSupportedProviders(),
		"config":          config,
	}

	// 统计每种Provider的使用情况和能力
	providerStats := make(map[string]interface{})
	usageCounts := m.getProviderUsageCounts()
	for _, providerType := range m.GetSupportedProviders() {
		capabilities := m.GetProviderCapabilities(providerType)
		providerStats[providerType] = map[string]interface{}{
			"capabilities": capabilities,
			"usage_count":  usageCounts[providerType],
		}
	}
	stats["provider_details"] = providerStats

	return stats
}

func (m *Manager) getProviderUsageCounts() map[string]int64 {
	counts := make(map[string]int64)
	if global.APP_DB == nil {
		return counts
	}

	type usageRow struct {
		Type  string
		Count int64
	}

	var rows []usageRow
	if err := global.APP_DB.Model(&providerModel.Port{}).
		Select("providers.type AS type, COUNT(ports.id) AS count").
		Joins("JOIN providers ON providers.id = ports.provider_id").
		Group("providers.type").
		Scan(&rows).Error; err != nil {
		return counts
	}

	for _, row := range rows {
		counts[row.Type] = row.Count
	}
	return counts
}
