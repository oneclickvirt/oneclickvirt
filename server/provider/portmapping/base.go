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
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
// 整个查找+更新流程在同一短事务中串行执行，避免并发重复分配同一端口。
// 调用方在收到端口后仍须立即执行 DB.Create(portModel)；Port 表上的
// uniqueIndex:idx_provider_host_port 作为最终兜底保障。
func (bp *BaseProvider) AllocatePort(ctx context.Context, providerID uint, preferredPort int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if global.APP_DB == nil {
		return 0, fmt.Errorf("database is unavailable")
	}
	var allocatedPort int

	err := global.APP_DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 锁定 Provider 行，序列化同一节点的端口分配
		var providerInfo provider.Provider
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
			return fmt.Errorf("provider not found: %v", err)
		}

		startPort := providerInfo.PortRangeStart
		endPort := providerInfo.PortRangeEnd
		if startPort == 0 {
			startPort = 10000
		}
		if endPort == 0 {
			endPort = 65535
		}
		if err := bp.ValidatePortRange(ctx, startPort, endPort); err != nil {
			return err
		}

		// 如果指定了首选端口，先检查是否可用
		if preferredPort > 0 {
			if preferredPort >= startPort && preferredPort <= endPort {
				if bp.isPortAvailableInTx(tx, providerID, preferredPort) {
					allocatedPort = preferredPort
					return nil
				}
			}
			return fmt.Errorf("preferred port %d is not available", preferredPort)
		}

		// 从下一个可用端口开始分配
		nextPort := providerInfo.NextAvailablePort
		if nextPort < startPort {
			nextPort = startPort
		}

		// 循环查找可用端口
		for port := nextPort; port <= endPort; port++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if bp.isPortAvailableInTx(tx, providerID, port) {
				if err := bp.updateNextAvailablePortInTx(tx, providerID, port+1); err != nil {
					return err
				}
				allocatedPort = port
				return nil
			}
		}

		// 如果从nextPort到endPort没有找到，从startPort到nextPort再找一遍
		for port := startPort; port < nextPort; port++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if bp.isPortAvailableInTx(tx, providerID, port) {
				if err := bp.updateNextAvailablePortInTx(tx, providerID, port+1); err != nil {
					return err
				}
				allocatedPort = port
				return nil
			}
		}

		return fmt.Errorf("no available ports in range %d-%d", startPort, endPort)
	})

	if err != nil {
		return 0, err
	}
	return allocatedPort, nil
}

// isPortAvailableInTx 在已有事务中检查端口是否可用（使用 LOCK IN SHARE MODE 防止幻读）
func (bp *BaseProvider) isPortAvailableInTx(tx *gorm.DB, providerID uint, port int) bool {
	var count int64
	result := tx.Model(&provider.Port{}).
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where("provider_id = ? AND host_port <= ?", providerID, port).
		Where("CASE WHEN host_port_end > 0 THEN host_port_end WHEN port_count > 1 THEN host_port + port_count - 1 ELSE host_port END >= ?", port).
		Count(&count)
	return result.Error == nil && count == 0
}

// updateNextAvailablePortInTx 在已有事务中更新下一个可用端口
func (bp *BaseProvider) updateNextAvailablePortInTx(tx *gorm.DB, providerID uint, nextPort int) error {
	return tx.Model(&provider.Provider{}).
		Where("id = ?", providerID).
		Update("next_available_port", nextPort).Error
}

// ToDBModel 转换为数据库模型
func (bp *BaseProvider) ToDBModel(result *PortMappingResult) *provider.Port {
	now := time.Now()
	portCount := result.PortCount
	if portCount <= 0 {
		portCount = 1
	}

	port := &provider.Port{
		ID:            result.ID,
		InstanceID:    parseUint(result.InstanceID),
		ProviderID:    result.ProviderID,
		HostPort:      result.HostPort,
		HostPortEnd:   result.HostPortEnd,
		GuestPort:     result.GuestPort,
		GuestPortEnd:  result.GuestPortEnd,
		PortCount:     portCount,
		Protocol:      result.Protocol,
		Status:        result.Status,
		Description:   result.Description,
		IsSSH:         result.IsSSH,
		IsAutomatic:   result.IsAutomatic,
		IPv6Enabled:   result.IPv6Enabled || result.IPv6Address != "",
		IPv6Address:   result.IPv6Address,
		MappingMethod: result.MappingMethod,
		MappingType:   result.MappingType,
		InternalHost:  result.InternalHost,
		CreatedAt:     now,
		UpdatedAt:     now,
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
	if port == nil {
		return nil
	}
	portCount := port.PortCount
	if portCount <= 0 {
		portCount = 1
	}
	mappingMethod := port.MappingMethod
	if mappingMethod == "" {
		mappingMethod = "native"
	}
	ipv6Address := port.IPv6Address
	return &PortMappingResult{
		ID:            port.ID,
		InstanceID:    fmt.Sprintf("%d", port.InstanceID),
		ProviderID:    port.ProviderID,
		Protocol:      port.Protocol,
		HostPort:      port.HostPort,
		GuestPort:     port.GuestPort,
		HostPortEnd:   port.HostPortEnd,
		GuestPortEnd:  port.GuestPortEnd,
		PortCount:     portCount,
		HostIP:        "", // Port模型中没有HostIP字段，需要从Provider获取
		PublicIP:      "", // Port模型中没有PublicIP字段，需要从Provider获取
		Status:        port.Status,
		Description:   port.Description,
		IPv6Address:   ipv6Address,
		IPv6Enabled:   port.IPv6Enabled,
		MappingMethod: mappingMethod,
		MappingType:   port.MappingType,
		InternalHost:  port.InternalHost,
		IsSSH:         port.IsSSH,
		IsAutomatic:   port.IsAutomatic,
		CreatedAt:     port.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     port.UpdatedAt.Format(time.RFC3339),
	}
}

// LoadOwnedPort loads a mapping only when it belongs to the requested
// instance and provider. The legacy provider adapters are also callable from
// reset/compatibility paths, so an ID-only lookup is not sufficient here.
func (bp *BaseProvider) LoadOwnedPort(id uint, instanceID string, providerID uint) (*provider.Port, error) {
	if global.APP_DB == nil {
		return nil, fmt.Errorf("database is unavailable")
	}
	instanceNumeric, err := strconv.ParseUint(instanceID, 10, 32)
	if err != nil || instanceNumeric == 0 {
		return nil, fmt.Errorf("invalid instance ID: %s", instanceID)
	}
	var port provider.Port
	query := global.APP_DB.Where("id = ? AND instance_id = ?", id, uint(instanceNumeric))
	if providerID > 0 {
		query = query.Where("provider_id = ?", providerID)
	}
	if err := query.First(&port).Error; err != nil {
		return nil, fmt.Errorf("port mapping not found for instance %s: %w", instanceID, err)
	}
	return &port, nil
}

// ValidateMappingRange validates both single-port and contiguous range
// requests. Zero end/count values are accepted as the single-port form.
func ValidateMappingRange(hostPort, hostPortEnd, guestPort, guestPortEnd, portCount int) error {
	if hostPort < 1 || hostPort > 65535 || guestPort < 1 || guestPort > 65535 {
		return fmt.Errorf("invalid port range")
	}
	if portCount <= 0 {
		portCount = 1
	}
	if portCount > 1500 || hostPort+portCount-1 > 65535 || guestPort+portCount-1 > 65535 {
		return fmt.Errorf("port range exceeds allowed limits")
	}
	if hostPortEnd > 0 && hostPortEnd != hostPort+portCount-1 {
		return fmt.Errorf("host port range does not match port count")
	}
	if guestPortEnd > 0 && guestPortEnd != guestPort+portCount-1 {
		return fmt.Errorf("guest port range does not match port count")
	}
	return nil
}

// Cleanup 清理资源
func (bp *BaseProvider) Cleanup(ctx context.Context) error {
	global.APP_LOG.Debug("Cleanup called for base provider", zap.String("type", bp.providerType))
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
