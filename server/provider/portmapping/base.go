package portmapping

import (
	"context"
	"fmt"
	"net"
	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// BaseProvider 基础端口映射Provider实现
type BaseProvider struct {
	providerType string
	config       *ManagerConfig
}

// NewBaseProvider 创建基础Provider
func NewBaseProvider(providerType string, config *ManagerConfig) *BaseProvider {
	return &BaseProvider{
		providerType: providerType,
		config:       config,
	}
}

// GetProviderType 获取Provider类型
func (bp *BaseProvider) GetProviderType() string {
	return bp.providerType
}

// SupportsDynamicMapping 默认不支持动态端口映射（子类可以覆盖）
func (bp *BaseProvider) SupportsDynamicMapping() bool {
	return false
}

// ValidatePortRange 验证端口范围
func (bp *BaseProvider) ValidatePortRange(ctx context.Context, startPort, endPort int) error {
	if startPort < 1 || startPort > 65535 {
		return fmt.Errorf("invalid start port: %d", startPort)
	}
	if endPort < 1 || endPort > 65535 {
		return fmt.Errorf("invalid end port: %d", endPort)
	}
	if startPort > endPort {
		return fmt.Errorf("start port %d must be less than or equal to end port %d", startPort, endPort)
	}
	return nil
}

// GetAvailablePortRange 获取可用端口范围
func (bp *BaseProvider) GetAvailablePortRange(ctx context.Context) (startPort, endPort int, err error) {
	if bp.config != nil {
		return bp.config.PortRangeStart, bp.config.PortRangeEnd, nil
	}
	return 10000, 65535, nil // 默认端口范围
}

// AllocatePort 分配端口
func (bp *BaseProvider) AllocatePort(ctx context.Context, providerID uint, preferredPort int) (int, error) {
	// 获取Provider信息
	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return 0, fmt.Errorf("provider not found: %v", err)
	}

	startPort := providerInfo.PortRangeStart
	endPort := providerInfo.PortRangeEnd
	if startPort == 0 {
		startPort = 10000
	}
	if endPort == 0 {
		endPort = 65535
	}

	// 如果指定了首选端口，先检查是否可用
	if preferredPort > 0 {
		if preferredPort >= startPort && preferredPort <= endPort {
			if bp.isPortAvailable(providerID, preferredPort) {
				return preferredPort, nil
			}
		}
		return 0, fmt.Errorf("preferred port %d is not available", preferredPort)
	}

	// 从下一个可用端口开始分配
	nextPort := providerInfo.NextAvailablePort
	if nextPort < startPort {
		nextPort = startPort
	}

	// 循环查找可用端口
	for port := nextPort; port <= endPort; port++ {
		if bp.isPortAvailable(providerID, port) {
			// 更新下一个可用端口
			bp.updateNextAvailablePort(providerID, port+1)
			return port, nil
		}
	}

	// 如果从nextPort到endPort没有找到，从startPort到nextPort再找一遍
	for port := startPort; port < nextPort; port++ {
		if bp.isPortAvailable(providerID, port) {
			bp.updateNextAvailablePort(providerID, port+1)
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available ports in range %d-%d", startPort, endPort)
}

// isPortAvailable 检查端口是否可用
func (bp *BaseProvider) isPortAvailable(providerID uint, port int) bool {
	var count int64
	global.APP_DB.Model(&provider.Port{}).
		Where("provider_id = ? AND host_port = ? AND status = 'active'", providerID, port).
		Count(&count)
	return count == 0
}

// updateNextAvailablePort 更新下一个可用端口
func (bp *BaseProvider) updateNextAvailablePort(providerID uint, nextPort int) {
	global.APP_DB.Model(&provider.Provider{}).
		Where("id = ?", providerID).
		Update("next_available_port", nextPort)
}

// ToDBModel 转换为数据库模型
func (bp *BaseProvider) ToDBModel(result *PortMappingResult) *provider.Port {
	now := time.Now()

	port := &provider.Port{
		ID:          result.ID,
		InstanceID:  parseUint(result.InstanceID),
		ProviderID:  result.ProviderID,
		HostPort:    result.HostPort,
		GuestPort:   result.GuestPort,
		Protocol:    result.Protocol,
		Status:      result.Status,
		Description: result.Description,
		IsSSH:       result.IsSSH,
		IsAutomatic: result.IsAutomatic,
		IPv6Enabled: result.IPv6Address != "",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// 如果有创建时间字符串，尝试解析
	if result.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, result.CreatedAt); err == nil {
			port.CreatedAt = t
		}
	}
	if result.UpdatedAt != "" {
		if t, err := time.Parse(time.RFC3339, result.UpdatedAt); err == nil {
			port.UpdatedAt = t
		}
	}

	return port
}

// FromDBModel 从数据库模型转换
func (bp *BaseProvider) FromDBModel(port *provider.Port) *PortMappingResult {
	return &PortMappingResult{
		ID:            port.ID,
		InstanceID:    fmt.Sprintf("%d", port.InstanceID),
		ProviderID:    port.ProviderID,
		Protocol:      port.Protocol,
		HostPort:      port.HostPort,
		GuestPort:     port.GuestPort,
		HostIP:        "", // Port模型中没有HostIP字段，需要从Provider获取
		PublicIP:      "", // Port模型中没有PublicIP字段，需要从Provider获取
		Status:        port.Status,
		Description:   port.Description,
		MappingMethod: "native", // 默认为原生方法
		IsSSH:         port.IsSSH,
		IsAutomatic:   port.IsAutomatic,
		CreatedAt:     port.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     port.UpdatedAt.Format(time.RFC3339),
	}
}

// Cleanup 清理资源
func (bp *BaseProvider) Cleanup(ctx context.Context) error {
	global.APP_LOG.Info("Cleanup called for base provider", zap.String("type", bp.providerType))
	return nil
}

// parseUint 安全地解析字符串为uint
func parseUint(s string) uint {
	if i, err := strconv.ParseUint(s, 10, 32); err == nil {
		return uint(i)
	}
	return 0
}

// ValidateIP 验证IP地址
func ValidateIP(ip string) error {
	if ip == "" {
		return nil // 空IP地址是允许的
	}
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address: %s", ip)
	}
	return nil
}

// ValidateProtocol 验证协议
func ValidateProtocol(protocol string) error {
	switch protocol {
	case "tcp", "udp", "both", "TCP", "UDP", "BOTH":
		return nil
	default:
		return fmt.Errorf("unsupported protocol: %s", protocol)
	}
}

// ValidatePort 验证端口号
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port: %d", port)
	}
	return nil
}
