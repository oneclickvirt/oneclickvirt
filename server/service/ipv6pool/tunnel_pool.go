package ipv6pool

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SourceTunnel identifies prefixes installed by the managed IPv6 tunnel
// service. They are kept separate from manual and node-file sources so a file
// sync can never remove an active routed prefix.
const SourceTunnel = "tunnel"

// IPv6AllocationMetadata is the small amount of host routing information a
// provider needs in addition to the allocated address. Native pools return an
// empty CIDR/Gateway and continue using their existing discovery path.
type IPv6AllocationMetadata struct {
	Address         string
	CIDR            string
	Gateway         string
	Bridge          string
	TunnelID        uint
	TunnelInterface string
}

// RoutedPrefixDetails canonicalizes a routed prefix and derives the bridge
// gateway used by the host and guests. The network and gateway addresses are
// deliberately not allocatable; allocations begin at network+2.
func RoutedPrefixDetails(raw string) (cidr, gateway, first string, prefix int, err error) {
	ip, network, parseErr := net.ParseCIDR(strings.TrimSpace(raw))
	if parseErr != nil || ip == nil || network == nil || ip.To4() != nil {
		return "", "", "", 0, fmt.Errorf("无效的IPv6路由前缀")
	}
	ones, bits := network.Mask.Size()
	if bits != 128 || ones < 1 || ones > 127 {
		return "", "", "", 0, fmt.Errorf("隧道路由前缀必须是IPv6 /1至/127，当前为/%d", ones)
	}
	base := network.IP.To16()
	// A /127 is a valid point-to-point routed prefix (RFC 6164): both
	// addresses are usable, so the network base is the host bridge gateway and
	// the other address is the first allocatable guest address.  Larger
	// prefixes retain the traditional network + gateway + guest reservation.
	if ones == 127 {
		client, ok := incrementIPv6(base)
		if !ok || !network.Contains(client) {
			return "", "", "", 0, fmt.Errorf("/127隧道路由前缀没有可用客户端地址")
		}
		return network.String(), base.String(), client.String(), ones, nil
	}
	server, ok := incrementIPv6(base)
	if !ok || !network.Contains(server) {
		return "", "", "", 0, fmt.Errorf("隧道路由前缀没有可用网关地址")
	}
	client, ok := incrementIPv6(server)
	if !ok || !network.Contains(client) {
		return "", "", "", 0, fmt.Errorf("隧道路由前缀至少需要三个地址")
	}
	return network.String(), server.String(), client.String(), ones, nil
}

// SyncTunnelPool reconciles one tunnel prefix after the remote host operation
// has succeeded. All database work is a short provider-locked transaction;
// remote SSH/API calls must remain outside this method.
func (s *Service) SyncTunnelPool(providerID, tunnelID uint, routedCIDR string, enabled bool) error {
	if providerID == 0 || tunnelID == 0 {
		return fmt.Errorf("隧道地址池参数无效")
	}
	db := s.db
	if db == nil {
		db = global.APP_DB
	}
	if db == nil {
		return fmt.Errorf("数据库连接不可用")
	}
	canonicalCIDR, gateway, first, prefix := "", "", "", 0
	if enabled && strings.TrimSpace(routedCIDR) != "" {
		var parseErr error
		canonicalCIDR, gateway, first, prefix, parseErr = RoutedPrefixDetails(routedCIDR)
		if parseErr != nil {
			return parseErr
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var provider providerModel.Provider
		if err := tx.Select("id").Where("id = ?", providerID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&provider).Error; err != nil {
			return fmt.Errorf("锁定Provider隧道地址池失败: %w", err)
		}

		var current []providerModel.ProviderIPv6Pool
		if err := tx.Unscoped().Where("provider_id = ? AND tunnel_id = ?", providerID, tunnelID).
			Order("id ASC").Find(&current).Error; err != nil {
			return fmt.Errorf("读取隧道地址池失败: %w", err)
		}
		var parent *providerModel.ProviderIPv6Pool
		for index := range current {
			if current[index].ParentID == nil && current[index].IsRange {
				candidate := current[index]
				parent = &candidate
				break
			}
		}

		if canonicalCIDR == "" {
			return retireTunnelPool(tx, current)
		}
		if parent != nil && parent.Address != canonicalCIDR {
			var allocated int64
			if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
				Where("(id = ? OR parent_id = ?) AND is_allocated = ? AND is_reserved = ? AND deleted_at IS NULL", parent.ID, parent.ID, true, false).
				Count(&allocated).Error; err != nil {
				return fmt.Errorf("检查旧隧道IPv6绑定失败: %w", err)
			}
			if allocated > 0 {
				return fmt.Errorf("隧道路由前缀仍有 %d 个实例地址绑定，不能直接更换前缀；请先释放或迁移实例", allocated)
			}
			if err := retireTunnelPool(tx, current); err != nil {
				return err
			}
			parent = nil
		}

		if parent == nil {
			var conflict providerModel.ProviderIPv6Pool
			result := tx.Unscoped().Where("provider_id = ? AND address = ?", providerID, canonicalCIDR).
				Order("id DESC").First(&conflict)
			if result.Error == nil && (conflict.TunnelID == nil || *conflict.TunnelID != tunnelID) {
				return fmt.Errorf("隧道路由前缀 %s 已存在于其他IPv6地址池，请先清理重复配置", canonicalCIDR)
			}
			if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return fmt.Errorf("检查隧道路由前缀冲突失败: %w", result.Error)
			}

			newParent := providerModel.ProviderIPv6Pool{
				ProviderID: providerID, Address: canonicalCIDR, PrefixLength: prefix,
				IsRange: true, RangeNext: first, Source: SourceTunnel,
				TunnelID: ptrUint(tunnelID), PendingRetire: false,
			}
			if result.Error == nil && conflict.DeletedAt != nil {
				newParent.ID = conflict.ID
				newParent.CreatedAt = conflict.CreatedAt
				if err := tx.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).Where("id = ?", conflict.ID).
					Updates(map[string]interface{}{
						"deleted_at": nil, "provider_id": providerID, "address": canonicalCIDR,
						"prefix_length": prefix, "is_range": true, "range_next": first,
						"source": SourceTunnel, "tunnel_id": tunnelID, "pending_retire": false,
					}).Error; err != nil {
					return fmt.Errorf("恢复隧道IPv6前缀失败: %w", err)
				}
			} else if err := tx.Create(&newParent).Error; err != nil {
				return fmt.Errorf("写入隧道IPv6前缀失败: %w", err)
			} else {
				parent = &newParent
			}
			if parent == nil {
				parent = &newParent
			}
		}

		// Restore the parent and any still-bound children after a temporary
		// disable. Do not reset a non-empty cursor: that would recycle addresses.
		if err := tx.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).Where("id = ?", parent.ID).
			Updates(map[string]interface{}{"deleted_at": nil, "source": SourceTunnel, "tunnel_id": tunnelID, "pending_retire": false}).Error; err != nil {
			return fmt.Errorf("恢复隧道IPv6前缀状态失败: %w", err)
		}
		if strings.TrimSpace(parent.RangeNext) == "" {
			if err := tx.Model(&providerModel.ProviderIPv6Pool{}).Where("id = ?", parent.ID).Update("range_next", first).Error; err != nil {
				return fmt.Errorf("恢复隧道IPv6前缀游标失败: %w", err)
			}
		}
		if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
			Where("parent_id = ?", parent.ID).
			Updates(map[string]interface{}{"tunnel_id": tunnelID, "pending_retire": false}).Error; err != nil {
			return fmt.Errorf("恢复隧道IPv6子项状态失败: %w", err)
		}

		var gatewayRow providerModel.ProviderIPv6Pool
		gatewayResult := tx.Unscoped().Where("provider_id = ? AND parent_id = ? AND address = ?", providerID, parent.ID, gateway).
			First(&gatewayRow)
		if gatewayResult.Error != nil && !errors.Is(gatewayResult.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("读取隧道IPv6网关保留项失败: %w", gatewayResult.Error)
		}
		if errors.Is(gatewayResult.Error, gorm.ErrRecordNotFound) {
			gatewayRow = providerModel.ProviderIPv6Pool{
				ProviderID: providerID, Address: gateway, PrefixLength: 128,
				ParentID: &parent.ID, TunnelID: ptrUint(tunnelID), Source: SourceTunnel,
				IsReserved: true,
			}
			if err := tx.Create(&gatewayRow).Error; err != nil {
				return fmt.Errorf("保留隧道IPv6网关失败: %w", err)
			}
		} else if err := tx.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).Where("id = ?", gatewayRow.ID).
			Updates(map[string]interface{}{"deleted_at": nil, "source": SourceTunnel, "tunnel_id": tunnelID, "is_reserved": true, "pending_retire": false, "is_allocated": false, "instance_id": nil}).Error; err != nil {
			return fmt.Errorf("恢复隧道IPv6网关保留项失败: %w", err)
		}
		return nil
	})
}

func retireTunnelPool(tx *gorm.DB, rows []providerModel.ProviderIPv6Pool) error {
	if len(rows) == 0 {
		return nil
	}
	parentIDs := make([]uint, 0)
	for _, row := range rows {
		if row.ParentID == nil && row.IsRange {
			parentIDs = append(parentIDs, row.ID)
		}
	}
	if len(parentIDs) == 0 {
		return tx.Unscoped().Where("id IN ? AND is_allocated = ?", idsOfRows(rows), false).
			Delete(&providerModel.ProviderIPv6Pool{}).Error
	}
	var allocated int64
	if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
		Where("parent_id IN ? AND is_allocated = ? AND deleted_at IS NULL AND is_reserved = ?", parentIDs, true, false).
		Count(&allocated).Error; err != nil {
		return fmt.Errorf("检查隧道IPv6已分配子项失败: %w", err)
	}
	if allocated == 0 {
		return tx.Unscoped().Where("id IN ? OR parent_id IN ?", parentIDs, parentIDs).
			Delete(&providerModel.ProviderIPv6Pool{}).Error
	}
	if err := tx.Model(&providerModel.ProviderIPv6Pool{}).Where("id IN ?", parentIDs).
		Update("pending_retire", true).Error; err != nil {
		return fmt.Errorf("标记隧道IPv6前缀退休失败: %w", err)
	}
	if err := tx.Where("parent_id IN ? AND is_allocated = ?", parentIDs, false).
		Delete(&providerModel.ProviderIPv6Pool{}).Error; err != nil {
		return fmt.Errorf("清理隧道IPv6未分配子项失败: %w", err)
	}
	return tx.Model(&providerModel.ProviderIPv6Pool{}).
		Where("parent_id IN ? AND is_allocated = ?", parentIDs, true).
		Update("pending_retire", true).Error
}

func idsOfRows(rows []providerModel.ProviderIPv6Pool) []uint {
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func ptrUint(value uint) *uint { return &value }

// GetAllocationMetadata performs one bounded lookup for a newly allocated
// instance. It is intentionally separate from allocation so the allocator's
// short lock/transaction remains unchanged.
func (s *Service) GetAllocationMetadata(providerID, instanceID uint) (IPv6AllocationMetadata, error) {
	db := s.database()
	if db == nil {
		return IPv6AllocationMetadata{}, fmt.Errorf("数据库连接不可用")
	}
	var row struct {
		Address         string `gorm:"column:address"`
		TunnelID        *uint  `gorm:"column:tunnel_id"`
		ParentID        *uint  `gorm:"column:parent_id"`
		ParentCIDR      string `gorm:"column:parent_cidr"`
		ParentTunnel    *uint  `gorm:"column:parent_tunnel"`
		TunnelInterface string `gorm:"column:tunnel_interface"`
	}
	query := db.Table("provider_ipv6_pools AS child").
		Select("child.address, child.tunnel_id, child.parent_id, parent.address AS parent_cidr, parent.tunnel_id AS parent_tunnel, tunnel.interface AS tunnel_interface").
		Joins("LEFT JOIN provider_ipv6_pools AS parent ON parent.id = child.parent_id").
		Joins("LEFT JOIN provider_ipv6_tunnels AS tunnel ON tunnel.id = COALESCE(parent.tunnel_id, child.tunnel_id)").
		Where("child.provider_id = ? AND child.instance_id = ? AND child.is_allocated = ? AND child.is_reserved = ? AND child.deleted_at IS NULL", providerID, instanceID, true, false).
		Order("child.id ASC").Limit(1)
	if err := query.Scan(&row).Error; err != nil {
		return IPv6AllocationMetadata{}, fmt.Errorf("读取IPv6地址路由元数据失败: %w", err)
	}
	if strings.TrimSpace(row.Address) == "" {
		return IPv6AllocationMetadata{}, nil
	}
	metadata := IPv6AllocationMetadata{Address: row.Address}
	if row.ParentTunnel != nil {
		metadata.TunnelID = *row.ParentTunnel
	} else if row.TunnelID != nil {
		metadata.TunnelID = *row.TunnelID
	}
	metadata.CIDR = strings.TrimSpace(row.ParentCIDR)
	if metadata.CIDR == "" || metadata.TunnelID == 0 {
		return metadata, nil
	}
	_, gateway, _, _, err := RoutedPrefixDetails(metadata.CIDR)
	if err != nil {
		return IPv6AllocationMetadata{}, fmt.Errorf("解析IPv6隧道网关失败: %w", err)
	}
	metadata.Gateway = gateway
	metadata.Bridge = utils.RoutedIPv6BridgeName
	metadata.TunnelInterface = strings.TrimSpace(row.TunnelInterface)
	if metadata.TunnelInterface == "" {
		return IPv6AllocationMetadata{}, fmt.Errorf("读取IPv6隧道接口失败")
	}
	return metadata, nil
}
