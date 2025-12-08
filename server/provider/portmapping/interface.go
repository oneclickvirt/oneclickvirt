package portmapping

import (
	"context"
	"fmt"
	"oneclickvirt/model/provider"
)

// PortMappingProvider 端口映射接口，定义不同Provider的端口映射方法
type PortMappingProvider interface {
	// GetProviderType 获取Provider类型
	GetProviderType() string

	// CreatePortMapping 创建端口映射
	CreatePortMapping(ctx context.Context, req *PortMappingRequest) (*PortMappingResult, error)

	// DeletePortMapping 删除端口映射
	DeletePortMapping(ctx context.Context, req *DeletePortMappingRequest) error

	// UpdatePortMapping 更新端口映射
	UpdatePortMapping(ctx context.Context, req *UpdatePortMappingRequest) (*PortMappingResult, error)

	// ListPortMappings 列出端口映射
	ListPortMappings(ctx context.Context, instanceID string) ([]*PortMappingResult, error)

	// ValidatePortRange 验证端口范围是否可用
	ValidatePortRange(ctx context.Context, startPort, endPort int) error

	// GetAvailablePortRange 获取可用端口范围
	GetAvailablePortRange(ctx context.Context) (startPort, endPort int, err error)

	// Cleanup 清理资源
	Cleanup(ctx context.Context) error

	// SupportsDynamicMapping 是否支持动态端口映射（运行时修改）
	SupportsDynamicMapping() bool
}

// PortMappingRequest 端口映射请求
type PortMappingRequest struct {
	InstanceID    string `json:"instanceId"`    // 实例ID
	ProviderID    uint   `json:"providerId"`    // Provider ID
	Protocol      string `json:"protocol"`      // 协议: tcp, udp
	HostPort      int    `json:"hostPort"`      // 主机端口（0表示自动分配）
	GuestPort     int    `json:"guestPort"`     // 客户端口
	Description   string `json:"description"`   // 描述
	IPv6Enabled   bool   `json:"ipv6Enabled"`   // 是否启用IPv6
	IPv6Address   string `json:"ipv6Address"`   // IPv6地址
	HostIP        string `json:"hostIP"`        // 主机IP（某些情况下需要指定）
	MappingMethod string `json:"mappingMethod"` // 映射方法: native, iptables, etc.
	IsSSH         *bool  `json:"isSSH"`         // 是否为SSH端口（可选，如果不提供则根据GuestPort==22判断）
}

// UpdatePortMappingRequest 更新端口映射请求
type UpdatePortMappingRequest struct {
	ID          uint   `json:"id"`          // 端口映射ID
	InstanceID  string `json:"instanceId"`  // 实例ID
	HostPort    int    `json:"hostPort"`    // 主机端口
	GuestPort   int    `json:"guestPort"`   // 客户端口
	Protocol    string `json:"protocol"`    // 协议
	Description string `json:"description"` // 描述
	Status      string `json:"status"`      // 状态
}

// DeletePortMappingRequest 删除端口映射请求
type DeletePortMappingRequest struct {
	ID          uint   `json:"id"`          // 端口映射ID
	InstanceID  string `json:"instanceId"`  // 实例ID
	ForceDelete bool   `json:"forceDelete"` // 强制删除（即使删除失败也从数据库删除）
}

// PortMappingResult 端口映射结果
type PortMappingResult struct {
	ID            uint   `json:"id"`            // 端口映射ID
	InstanceID    string `json:"instanceId"`    // 实例ID
	ProviderID    uint   `json:"providerId"`    // Provider ID
	Protocol      string `json:"protocol"`      // 协议
	HostPort      int    `json:"hostPort"`      // 主机端口
	GuestPort     int    `json:"guestPort"`     // 客户端口
	HostIP        string `json:"hostIP"`        // 主机IP
	PublicIP      string `json:"publicIP"`      // 公网IP
	IPv6Address   string `json:"ipv6Address"`   // IPv6地址
	Status        string `json:"status"`        // 状态: active, inactive
	Description   string `json:"description"`   // 描述
	MappingMethod string `json:"mappingMethod"` // 映射方法
	IsSSH         bool   `json:"isSSH"`         // 是否为SSH端口
	IsAutomatic   bool   `json:"isAutomatic"`   // 是否自动分配
	CreatedAt     string `json:"createdAt"`     // 创建时间
	UpdatedAt     string `json:"updatedAt"`     // 更新时间
}

// Registry 端口映射Provider注册表
type Registry struct {
	providers map[string]func(*ManagerConfig) PortMappingProvider
}

var globalRegistry = &Registry{
	providers: make(map[string]func(*ManagerConfig) PortMappingProvider),
}

// RegisterProvider 注册端口映射Provider
func RegisterProvider(providerType string, factory func(*ManagerConfig) PortMappingProvider) {
	globalRegistry.providers[providerType] = factory
}

// GetProvider 获取端口映射Provider
func GetProvider(providerType string) (PortMappingProvider, error) {
	factory, exists := globalRegistry.providers[providerType]
	if !exists {
		return nil, fmt.Errorf("port mapping provider %s not found", providerType)
	}
	return factory(nil), nil
}

// GetProviderWithConfig 获取端口映射Provider（带配置）
func GetProviderWithConfig(providerType string, config *ManagerConfig) (PortMappingProvider, error) {
	factory, exists := globalRegistry.providers[providerType]
	if !exists {
		return nil, fmt.Errorf("port mapping provider %s not found", providerType)
	}
	return factory(config), nil
}

// ListProviders 列出所有注册的端口映射Provider
func ListProviders() []string {
	var types []string
	for providerType := range globalRegistry.providers {
		types = append(types, providerType)
	}
	return types
}

// GetRegisteredProviders 获取所有注册的Provider工厂函数
func GetRegisteredProviders() map[string]func(*ManagerConfig) PortMappingProvider {
	providers := make(map[string]func(*ManagerConfig) PortMappingProvider)
	for providerType, factory := range globalRegistry.providers {
		providers[providerType] = factory
	}
	return providers
}

// ManagerConfig 端口映射管理器配置
type ManagerConfig struct {
	DefaultMappingMethod string            `json:"defaultMappingMethod"` // 默认映射方法
	PortRangeStart       int               `json:"portRangeStart"`       // 端口范围起始
	PortRangeEnd         int               `json:"portRangeEnd"`         // 端口范围结束
	IPv6Enabled          bool              `json:"ipv6Enabled"`          // 是否启用IPv6
	ExtraMethods         map[string]string `json:"extraMethods"`         // 额外映射方法配置
}

// ProviderWithDBModel Provider与数据库模型的映射接口
type ProviderWithDBModel interface {
	PortMappingProvider

	// ToDBModel 转换为数据库模型
	ToDBModel(result *PortMappingResult) *provider.Port

	// FromDBModel 从数据库模型转换
	FromDBModel(port *provider.Port) *PortMappingResult
}
