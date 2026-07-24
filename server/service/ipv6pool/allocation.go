package ipv6pool

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) ClearUnallocated(providerID uint) (int64, error) {
	var deleted int64
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var lockedProvider providerModel.Provider
		if err := tx.Select("id").Where("id = ?", providerID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProvider).Error; err != nil {
			return fmt.Errorf("锁定Provider IPv6地址池清理失败: %w", err)
		}
		result := tx.Where("provider_id = ? AND is_allocated = ? AND deleted_at IS NULL AND is_range = ?", providerID, false, false).
			Delete(&providerModel.ProviderIPv6Pool{})
		if result.Error != nil {
			return result.Error
		}
		deleted += result.RowsAffected
		result = tx.Where("provider_id = ? AND is_range = ? AND deleted_at IS NULL AND id NOT IN (?)", providerID, true,
			tx.Model(&providerModel.ProviderIPv6Pool{}).Select("parent_id").
				Where("provider_id = ? AND parent_id IS NOT NULL AND is_allocated = ? AND deleted_at IS NULL", providerID, true)).
			Delete(&providerModel.ProviderIPv6Pool{})
		if result.Error != nil {
			return result.Error
		}
		deleted += result.RowsAffected
		return nil
	})
	return deleted, err
}

func (s *Service) DeleteAddress(providerID, entryID uint) error {
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var lockedProvider providerModel.Provider
		if err := tx.Select("id").Where("id = ?", providerID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProvider).Error; err != nil {
			return fmt.Errorf("锁定Provider IPv6地址删除失败: %w", err)
		}
		var entry providerModel.ProviderIPv6Pool
		if err := tx.Where("id = ? AND provider_id = ? AND deleted_at IS NULL", entryID, providerID).First(&entry).Error; err != nil {
			return fmt.Errorf("地址不存在")
		}
		if entry.IsAllocated {
			return fmt.Errorf("地址已分配，无法删除")
		}
		if entry.IsRange {
			var allocatedChildren int64
			if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
				Where("parent_id = ? AND is_allocated = ? AND deleted_at IS NULL", entry.ID, true).
				Count(&allocatedChildren).Error; err != nil {
				return err
			}
			if allocatedChildren > 0 {
				return fmt.Errorf("地址范围仍有已分配地址，无法删除")
			}
			if err := tx.Where("parent_id = ? AND deleted_at IS NULL", entry.ID).
				Delete(&providerModel.ProviderIPv6Pool{}).Error; err != nil {
				return fmt.Errorf("删除地址范围已释放子项失败: %w", err)
			}
		}
		return tx.Delete(&entry).Error
	})
}

// AllocateIPv6Address performs only bounded, short database work. It returns
// an existing binding for idempotent task retries and reuses released children
// before advancing a range cursor.
func (s *Service) AllocateIPv6Address(providerID, instanceID uint) (string, error) {
	for {
		var allocated string
		advancedCursor := false
		exhausted := false
		err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
			// This short provider-row lock serializes allocation with pool sync and
			// makes same-instance retries deterministic without holding any remote I/O.
			var lockedProvider providerModel.Provider
			if err := tx.Select("id").Where("id = ?", providerID).
				Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProvider).Error; err != nil {
				return fmt.Errorf("锁定Provider IPv6分配失败: %w", err)
			}

			var existing providerModel.ProviderIPv6Pool
			existingResult := tx.Where("provider_id = ? AND instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", providerID, instanceID, true).
				Order("id ASC").First(&existing)
			if existingResult.Error == nil {
				allocated = existing.Address
				return nil
			}
			if !errors.Is(existingResult.Error, gorm.ErrRecordNotFound) {
				return fmt.Errorf("查询实例IPv6地址失败: %w", existingResult.Error)
			}

			// Released discrete rows and materialized range children are reused
			// before a range cursor advances.
			var entry providerModel.ProviderIPv6Pool
			query := tx.Where("provider_id = ? AND is_allocated = ? AND is_range = ? AND pending_retire = ? AND deleted_at IS NULL", providerID, false, false, false).
				Order("id ASC").First(&entry)
			if query.Error == nil {
				update := tx.Model(&providerModel.ProviderIPv6Pool{}).
					Where("id = ? AND is_allocated = ? AND pending_retire = ?", entry.ID, false, false).
					Updates(map[string]interface{}{"is_allocated": true, "instance_id": instanceID})
				if update.Error != nil {
					return fmt.Errorf("分配IPv6地址失败: %w", update.Error)
				}
				if update.RowsAffected != 1 {
					return fmt.Errorf("IPv6地址并发分配冲突")
				}
				allocated = entry.Address
				return nil
			}
			if !errors.Is(query.Error, gorm.ErrRecordNotFound) {
				return fmt.Errorf("查询可用IPv6地址失败: %w", query.Error)
			}

			var source providerModel.ProviderIPv6Pool
			rangeResult := tx.Where("provider_id = ? AND is_range = ? AND pending_retire = ? AND range_next <> '' AND deleted_at IS NULL", providerID, true, false).
				Order("id ASC").Clauses(clause.Locking{Strength: "UPDATE"}).First(&source)
			if errors.Is(rangeResult.Error, gorm.ErrRecordNotFound) {
				exhausted = true
				return nil
			}
			if rangeResult.Error != nil {
				return fmt.Errorf("查询IPv6地址范围失败: %w", rangeResult.Error)
			}

			candidates, nextWindow, err := ipv6RangeCandidateWindow(source, rangeScanWindowSize)
			if err != nil {
				return err
			}
			var occupied []string
			if err := tx.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).
				Where("provider_id = ? AND address IN ?", providerID, candidates).
				Pluck("address", &occupied).Error; err != nil {
				return fmt.Errorf("批量读取IPv6地址占用状态失败: %w", err)
			}
			used := make(map[string]struct{}, len(occupied))
			for _, address := range occupied {
				used[address] = struct{}{}
			}

			candidate := ""
			candidateIndex := -1
			for index, address := range candidates {
				if _, exists := used[address]; !exists {
					candidate = address
					candidateIndex = index
					break
				}
			}

			newCursor := nextWindow
			if candidateIndex >= 0 && candidateIndex+1 < len(candidates) {
				newCursor = candidates[candidateIndex+1]
			}
			if candidate != "" {
				child := providerModel.ProviderIPv6Pool{
					ProviderID: providerID, Address: candidate, PrefixLength: 128,
					ParentID: &source.ID, IsAllocated: true, InstanceID: &instanceID,
					Source: SourceRangeChild,
				}
				createResult := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&child)
				if createResult.Error != nil {
					return fmt.Errorf("写入已分配IPv6地址失败: %w", createResult.Error)
				}
				if createResult.RowsAffected == 1 {
					allocated = candidate
				} else {
					// A defensive fallback for a concurrent unique-key winner. The
					// provider lock normally makes this path unreachable.
					var concurrent providerModel.ProviderIPv6Pool
					if err := tx.Where("provider_id = ? AND instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", providerID, instanceID, true).
						First(&concurrent).Error; err == nil {
						allocated = concurrent.Address
					} else if !errors.Is(err, gorm.ErrRecordNotFound) {
						return fmt.Errorf("查询并发IPv6分配结果失败: %w", err)
					} else {
						return fmt.Errorf("IPv6地址并发分配冲突")
					}
				}
			}

			if err := tx.Model(&providerModel.ProviderIPv6Pool{}).Where("id = ?", source.ID).
				Update("range_next", newCursor).Error; err != nil {
				return fmt.Errorf("更新IPv6地址范围游标失败: %w", err)
			}
			advancedCursor = true
			return nil
		})
		if err != nil {
			return "", err
		}
		if allocated != "" {
			return allocated, nil
		}
		if exhausted {
			return "", fmt.Errorf("IPv6地址池已耗尽，没有可用地址")
		}
		if !advancedCursor {
			return "", fmt.Errorf("IPv6地址池无法前进到下一个候选地址")
		}
	}
}

// TransferIPv6BindingWithDB moves an existing allocation to a replacement
// instance without making the address available to another allocator between
// reset/delete and create. The provider-row lock serializes the move with pool
// sync, allocation and release, while the caller's transaction keeps the new
// instance record and the binding change atomic.
func (s *Service) TransferIPv6BindingWithDB(tx *gorm.DB, providerID, oldInstanceID, newInstanceID uint) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("IPv6地址迁移缺少数据库会话")
	}
	if providerID == 0 || oldInstanceID == 0 || newInstanceID == 0 {
		return "", fmt.Errorf("IPv6地址迁移参数无效")
	}

	var lockedProvider providerModel.Provider
	if err := tx.Select("id").Where("id = ?", providerID).
		Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProvider).Error; err != nil {
		return "", fmt.Errorf("锁定Provider IPv6地址迁移失败: %w", err)
	}

	var target providerModel.ProviderIPv6Pool
	targetResult := tx.Where("instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", newInstanceID, true).
		Clauses(clause.Locking{Strength: "UPDATE"}).First(&target)
	if targetResult.Error == nil {
		if target.ProviderID != providerID {
			return "", fmt.Errorf("新实例已绑定其他Provider的IPv6地址")
		}
		var source providerModel.ProviderIPv6Pool
		sourceResult := tx.Where("provider_id = ? AND instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", providerID, oldInstanceID, true).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&source)
		if sourceResult.Error == nil && source.ID != target.ID {
			return "", fmt.Errorf("新旧实例同时存在IPv6地址绑定，拒绝不明确的迁移")
		}
		if sourceResult.Error != nil && !errors.Is(sourceResult.Error, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("查询旧实例IPv6地址绑定失败: %w", sourceResult.Error)
		}
		return target.Address, nil
	}
	if !errors.Is(targetResult.Error, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("查询新实例IPv6地址绑定失败: %w", targetResult.Error)
	}

	var source providerModel.ProviderIPv6Pool
	sourceResult := tx.Where("provider_id = ? AND instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", providerID, oldInstanceID, true).
		Clauses(clause.Locking{Strength: "UPDATE"}).First(&source)
	if errors.Is(sourceResult.Error, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if sourceResult.Error != nil {
		return "", fmt.Errorf("查询旧实例IPv6地址绑定失败: %w", sourceResult.Error)
	}

	result := tx.Model(&providerModel.ProviderIPv6Pool{}).
		Where("id = ? AND instance_id = ? AND is_allocated = ?", source.ID, oldInstanceID, true).
		Update("instance_id", newInstanceID)
	if result.Error != nil {
		return "", fmt.Errorf("迁移实例IPv6地址绑定失败: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return "", fmt.Errorf("IPv6地址绑定在迁移期间发生并发变化")
	}
	return source.Address, nil
}

func ipv6RangeCandidateWindow(source providerModel.ProviderIPv6Pool, limit int) ([]string, string, error) {
	if limit <= 0 {
		return nil, "", fmt.Errorf("IPv6范围扫描窗口必须大于0")
	}
	candidate := net.ParseIP(strings.TrimSpace(source.RangeNext)).To16()
	_, network, err := net.ParseCIDR(source.Address)
	if candidate == nil || err != nil || network == nil || !network.Contains(candidate) {
		return nil, "", fmt.Errorf("IPv6地址范围游标无效: %s", source.Address)
	}
	candidates := make([]string, 0, limit)
	for len(candidates) < limit && network.Contains(candidate) {
		candidates = append(candidates, candidate.String())
		next, ok := incrementIPv6(candidate)
		if !ok || !network.Contains(next) {
			return candidates, "", nil
		}
		candidate = next
	}
	return candidates, candidate.String(), nil
}

func (s *Service) ReleaseIPv6(instanceID uint) error {
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		return s.ReleaseIPv6WithDB(tx, instanceID)
	})
}

// ReleaseIPv6WithDB centralizes normal release and pending-retire cleanup. The
// caller may pass an existing transaction so failure rollback paths do not
// bypass node-file retirement semantics.
func (s *Service) ReleaseIPv6WithDB(tx *gorm.DB, instanceID uint) error {
	if tx == nil {
		return fmt.Errorf("IPv6地址释放缺少数据库会话")
	}

	var providerIDs []uint
	if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
		Where("instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", instanceID, true).
		Distinct("provider_id").Pluck("provider_id", &providerIDs).Error; err != nil {
		return fmt.Errorf("查询IPv6地址绑定失败: %w", err)
	}
	if len(providerIDs) == 0 {
		return nil
	}
	sort.Slice(providerIDs, func(left, right int) bool { return providerIDs[left] < providerIDs[right] })
	var lockedProviders []providerModel.Provider
	if err := tx.Select("id").Where("id IN ?", providerIDs).Order("id ASC").
		Clauses(clause.Locking{Strength: "UPDATE"}).Find(&lockedProviders).Error; err != nil {
		return fmt.Errorf("锁定Provider IPv6地址释放失败: %w", err)
	}

	var bindings []providerModel.ProviderIPv6Pool
	if err := tx.Where("instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", instanceID, true).
		Order("id ASC").Clauses(clause.Locking{Strength: "UPDATE"}).Find(&bindings).Error; err != nil {
		return fmt.Errorf("锁定IPv6地址绑定失败: %w", err)
	}
	for _, binding := range bindings {
		purge := binding.PendingRetire
		var parent providerModel.ProviderIPv6Pool
		if binding.ParentID != nil {
			parentResult := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).First(&parent, *binding.ParentID)
			if errors.Is(parentResult.Error, gorm.ErrRecordNotFound) {
				purge = true
			} else if parentResult.Error != nil {
				return fmt.Errorf("查询IPv6范围父项失败: %w", parentResult.Error)
			} else if parent.PendingRetire || parent.DeletedAt != nil {
				purge = true
			}
		}

		if !purge {
			if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
				Where("id = ? AND is_allocated = ?", binding.ID, true).
				Updates(map[string]interface{}{"is_allocated": false, "instance_id": nil}).Error; err != nil {
				return fmt.Errorf("释放IPv6地址失败: %w", err)
			}
			continue
		}

		if err := tx.Unscoped().Delete(&providerModel.ProviderIPv6Pool{}, binding.ID).Error; err != nil {
			return fmt.Errorf("清理已退休IPv6地址失败: %w", err)
		}
		if binding.ParentID == nil || parent.ID == 0 {
			continue
		}
		var remainingAllocated int64
		if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
			Where("parent_id = ? AND is_allocated = ? AND deleted_at IS NULL", parent.ID, true).
			Count(&remainingAllocated).Error; err != nil {
			return fmt.Errorf("检查待退休IPv6范围剩余绑定失败: %w", err)
		}
		if parent.PendingRetire && remainingAllocated == 0 {
			if err := tx.Unscoped().Where("parent_id = ?", parent.ID).
				Delete(&providerModel.ProviderIPv6Pool{}).Error; err != nil {
				return fmt.Errorf("清理已退休IPv6范围子项失败: %w", err)
			}
			if err := tx.Unscoped().Delete(&providerModel.ProviderIPv6Pool{}, parent.ID).Error; err != nil {
				return fmt.Errorf("清理已退休IPv6范围失败: %w", err)
			}
		}
	}
	return nil
}

func (s *Service) GetAllocatedAddress(instanceID uint) (string, error) {
	var entry providerModel.ProviderIPv6Pool
	err := global.APP_DB.Where("instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", instanceID, true).
		Order("id ASC").First(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return entry.Address, err
}
