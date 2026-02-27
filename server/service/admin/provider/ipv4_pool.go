package provider

import (
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// IPv4PoolService IPv4地址池管理服务
type IPv4PoolService struct{}

// NewIPv4PoolService 创建IPv4地址池服务
func NewIPv4PoolService() *IPv4PoolService {
	return &IPv4PoolService{}
}

// GetIPv4Pool 获取Provider的IPv4地址池（分页）
func (s *IPv4PoolService) GetIPv4Pool(providerID uint, page, pageSize int) ([]providerModel.ProviderIPv4Pool, int64, error) {
	var entries []providerModel.ProviderIPv4Pool
	var total int64

	query := global.APP_DB.Model(&providerModel.ProviderIPv4Pool{}).
		Where("provider_id = ? AND deleted_at IS NULL", providerID)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Order("id ASC").Offset(offset).Limit(pageSize).Find(&entries).Error; err != nil {
		return nil, 0, err
	}

	return entries, total, nil
}

// SetIPv4Pool 向Provider的IPv4地址池中追加新地址（不删除已有地址）
// text: 每行一个IP地址或CIDR（IPv4），支持注释行（#开头）
// 仅支持 IPv4 /20 以上（较小）网段，避免意外扩展超大地址块
func (s *IPv4PoolService) SetIPv4Pool(providerID uint, text string) (added []string, invalidLines []string, err error) {
	ips, invalids := parseIPsFromText(text)
	if len(invalids) > 0 {
		global.APP_LOG.Warn("IPv4地址池包含无效行",
			zap.Uint("providerID", providerID),
			zap.Strings("invalid", invalids))
		invalidLines = invalids
	}

	if len(ips) == 0 {
		return nil, invalidLines, fmt.Errorf("未解析到有效的IPv4地址")
	}

	// 查询已存在的地址（全量，用于去重）
	var existing []string
	if dbErr := global.APP_DB.Model(&providerModel.ProviderIPv4Pool{}).
		Where("provider_id = ? AND deleted_at IS NULL", providerID).
		Pluck("address", &existing).Error; dbErr != nil {
		return nil, invalidLines, dbErr
	}
	existingSet := make(map[string]struct{}, len(existing))
	for _, addr := range existing {
		existingSet[addr] = struct{}{}
	}

	// 批量插入新地址
	var newEntries []providerModel.ProviderIPv4Pool
	for _, ip := range ips {
		if _, ok := existingSet[ip]; ok {
			continue
		}
		newEntries = append(newEntries, providerModel.ProviderIPv4Pool{
			ProviderID:  providerID,
			Address:     ip,
			IsAllocated: false,
		})
	}

	if len(newEntries) > 0 {
		// 分批插入（每批200条）
		batchSize := 200
		for i := 0; i < len(newEntries); i += batchSize {
			end := i + batchSize
			if end > len(newEntries) {
				end = len(newEntries)
			}
			batch := newEntries[i:end]
			if dbErr := global.APP_DB.Create(&batch).Error; dbErr != nil {
				return added, invalidLines, fmt.Errorf("批量插入IPv4地址失败: %w", dbErr)
			}
			for _, entry := range batch {
				added = append(added, entry.Address)
			}
		}
	}

	return added, invalidLines, nil
}

// ClearUnallocated 软删除所有未分配的IP地址
func (s *IPv4PoolService) ClearUnallocated(providerID uint) (int64, error) {
	result := global.APP_DB.
		Where("provider_id = ? AND is_allocated = ? AND deleted_at IS NULL", providerID, false).
		Delete(&providerModel.ProviderIPv4Pool{})
	return result.RowsAffected, result.Error
}

// DeleteAddress 从地址池中删除一个未分配的IP（软删除）
func (s *IPv4PoolService) DeleteAddress(providerID, entryID uint) error {
	result := global.APP_DB.
		Where("id = ? AND provider_id = ? AND is_allocated = ? AND deleted_at IS NULL",
			entryID, providerID, false).
		Delete(&providerModel.ProviderIPv4Pool{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("地址不存在或已分配，无法删除")
	}
	return nil
}

// AllocateIPv4Address 从地址池中原子地分配一个未分配的IP给指定实例
// 使用数据库事务 + SELECT ... LIMIT 1 FOR UPDATE 确保并发安全
func (s *IPv4PoolService) AllocateIPv4Address(providerID, instanceID uint) (string, error) {
	var allocatedAddress string

	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// SELECT ... FOR UPDATE 锁定第一行未分配地址
		var entry struct {
			ID      uint
			Address string
		}
		rawSQL := `SELECT id, address FROM provider_ipv4_pools
		           WHERE provider_id = ? AND is_allocated = 0 AND deleted_at IS NULL
		           ORDER BY id ASC LIMIT 1 FOR UPDATE`
		if err := tx.Raw(rawSQL, providerID).Scan(&entry).Error; err != nil {
			return fmt.Errorf("查询可用IPv4地址失败: %w", err)
		}
		if entry.ID == 0 {
			return fmt.Errorf("地址池已耗尽，没有可用的IPv4地址")
		}

		// 更新为已分配
		if err := tx.Exec(
			`UPDATE provider_ipv4_pools SET is_allocated = 1, instance_id = ?, updated_at = NOW()
			 WHERE id = ? AND is_allocated = 0`,
			instanceID, entry.ID,
		).Error; err != nil {
			return fmt.Errorf("分配IPv4地址失败: %w", err)
		}

		allocatedAddress = entry.Address
		return nil
	})

	if err != nil {
		return "", err
	}
	return allocatedAddress, nil
}

// ReleaseIPv4 释放实例占用的IPv4地址回到地址池
func (s *IPv4PoolService) ReleaseIPv4(instanceID uint) error {
	result := global.APP_DB.Exec(
		`UPDATE provider_ipv4_pools SET is_allocated = 0, instance_id = NULL, updated_at = NOW()
		 WHERE instance_id = ? AND is_allocated = 1 AND deleted_at IS NULL`,
		instanceID,
	)
	return result.Error
}

// GetAllocatedAddress 获取实例已分配的IPv4地址（未找到时返回空字符串）
func (s *IPv4PoolService) GetAllocatedAddress(instanceID uint) (string, error) {
	var entry struct{ Address string }
	err := global.APP_DB.Raw(
		`SELECT address FROM provider_ipv4_pools
		 WHERE instance_id = ? AND is_allocated = 1 AND deleted_at IS NULL LIMIT 1`,
		instanceID,
	).Scan(&entry).Error
	if err != nil {
		return "", err
	}
	return entry.Address, nil
}

// GetPoolStats 获取地址池统计信息
func (s *IPv4PoolService) GetPoolStats(providerID uint) (total, allocated, available int64) {
	global.APP_DB.Model(&providerModel.ProviderIPv4Pool{}).
		Where("provider_id = ? AND deleted_at IS NULL", providerID).
		Count(&total)
	global.APP_DB.Model(&providerModel.ProviderIPv4Pool{}).
		Where("provider_id = ? AND is_allocated = ? AND deleted_at IS NULL", providerID, true).
		Count(&allocated)
	available = total - allocated
	return
}

// parseIPsFromText 从文本中解析IPv4地址（每行一个IP或CIDR）
func parseIPsFromText(text string) (ips []string, invalidLines []string) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	seenIPs := make(map[string]struct{})

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// CIDR 模式
		if strings.Contains(line, "/") {
			_, network, err := net.ParseCIDR(line)
			if err != nil {
				invalidLines = append(invalidLines, line)
				continue
			}

			// 只允许 IPv4，且网段不超过 /20（4096个地址以内）
			ones, bits := network.Mask.Size()
			if bits != 32 {
				invalidLines = append(invalidLines, fmt.Sprintf("%s (仅支持IPv4)", line))
				continue
			}
			if ones < 20 {
				invalidLines = append(invalidLines, fmt.Sprintf("%s (网段过大，仅支持/20及更小)", line))
				continue
			}

			for addr := cloneIP(network.IP); network.Contains(addr); incrementIP(addr) {
				// 对 /31 以外的网段跳过网络地址和广播地址
				if ones < 31 {
					if addr.Equal(network.IP) || isBroadcast(addr, network) {
						continue
					}
				}
				ipStr := addr.String()
				if _, ok := seenIPs[ipStr]; !ok {
					seenIPs[ipStr] = struct{}{}
					ips = append(ips, ipStr)
				}
			}
			continue
		}

		// 单个 IP 模式
		parsed := net.ParseIP(line)
		if parsed == nil {
			invalidLines = append(invalidLines, line)
			continue
		}
		if parsed.To4() == nil {
			invalidLines = append(invalidLines, fmt.Sprintf("%s (仅支持IPv4)", line))
			continue
		}

		ipStr := parsed.String()
		if _, ok := seenIPs[ipStr]; !ok {
			seenIPs[ipStr] = struct{}{}
			ips = append(ips, ipStr)
		}
	}

	sort.Strings(ips)
	return ips, invalidLines
}

func cloneIP(ip net.IP) net.IP {
	clone := make(net.IP, len(ip))
	copy(clone, ip)
	return clone
}

func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

func isBroadcast(ip net.IP, network *net.IPNet) bool {
	ip4 := ip.To4()
	netIP4 := network.IP.To4()
	if ip4 == nil || netIP4 == nil {
		return false
	}
	mask := network.Mask
	broadcast := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		broadcast[i] = netIP4[i] | ^mask[i]
	}
	return binary.BigEndian.Uint32(ip4) == binary.BigEndian.Uint32(broadcast)
}
