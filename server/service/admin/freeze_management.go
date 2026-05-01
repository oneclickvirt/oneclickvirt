package admin

import (
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"
	"oneclickvirt/service/cache"
	"oneclickvirt/service/scheduler"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// FreezeManagementService 冻结管理服务
type FreezeManagementService struct {
	expiryService *scheduler.ExpiryFreezeService
}

// NewFreezeManagementService 创建冻结管理服务
func NewFreezeManagementService() *FreezeManagementService {
	return &FreezeManagementService{
		expiryService: &scheduler.ExpiryFreezeService{},
	}
}

// SetUserExpiry 设置用户过期时间
func (s *FreezeManagementService) SetUserExpiry(userID uint, expiresAt time.Time) error {
	now := time.Now()

	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return fmt.Errorf("用户不存在")
	}

	updates := map[string]interface{}{
		"expires_at":       expiresAt,
		"is_manual_expiry": true,
	}

	// 如果用户因过期而被禁用，且新的过期时间晚于当前时间，自动启用
	if u.Status == 0 && expiresAt.After(now) {
		updates["status"] = 1
	}

	if err := global.APP_DB.Model(&u).Updates(updates).Error; err != nil {
		return err
	}

	// 清除用户认证缓存，确保状态变更克即生效
	cache.GetUserCacheService().InvalidateUserCache(userID)

	global.APP_LOG.Info("管理员设置用户过期时间",
		zap.Uint("user_id", userID),
		zap.Time("expires_at", expiresAt))

	return nil
}

// SetProviderExpiry 设置Provider过期时间
func (s *FreezeManagementService) SetProviderExpiry(providerID uint, expiresAt time.Time) error {
	now := time.Now()

	tx := global.APP_DB.Begin()
	if tx.Error != nil {
		return fmt.Errorf("开启事务失败: %w", tx.Error)
	}
	defer tx.Rollback()

	var p provider.Provider
	if err := tx.First(&p, providerID).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	updates := map[string]interface{}{
		"expires_at":       expiresAt,
		"is_manual_expiry": true,
	}

	// 如果Provider因过期而冻结，且新的过期时间晚于当前时间，自动解冻
	if p.IsFrozen && p.FrozenReason == "expired" && expiresAt.After(now) {
		updates["is_frozen"] = false
		updates["frozen_at"] = nil
		updates["frozen_reason"] = ""

		// 同时解冻因节点冻结而被冻结的实例
		if err := tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND frozen_reason = ?", providerID, "node_frozen").
			Updates(map[string]interface{}{
				"is_frozen":     false,
				"frozen_at":     nil,
				"frozen_reason": "",
			}).Error; err != nil {
			return fmt.Errorf("解冻实例失败: %w", err)
		}
	}

	// 更新Provider下所有非手动设置过期时间的实例，同步新的过期时间
	if err := tx.Model(&provider.Instance{}).
		Where("provider_id = ? AND is_manual_expiry = ?", providerID, false).
		Update("expires_at", expiresAt).Error; err != nil {
		return fmt.Errorf("同步实例过期时间失败: %w", err)
	}

	if err := tx.Model(&p).Updates(updates).Error; err != nil {
		return err
	}

	if err := tx.Commit().Error; err != nil {
		return err
	}

	global.APP_LOG.Info("管理员设置Provider过期时间",
		zap.Uint("provider_id", providerID),
		zap.Time("expires_at", expiresAt))

	return nil
}

// SetInstanceExpiry 设置实例过期时间
func (s *FreezeManagementService) SetInstanceExpiry(instanceID uint, expiresAt time.Time) error {
	now := time.Now()

	var inst provider.Instance
	if err := global.APP_DB.First(&inst, instanceID).Error; err != nil {
		return fmt.Errorf("实例不存在")
	}

	updates := map[string]interface{}{
		"expires_at":       expiresAt,
		"is_manual_expiry": true,
	}

	// 如果实例因过期而冻结，且新的过期时间晚于当前时间，自动解冻
	if inst.IsFrozen && inst.FrozenReason == "expired" && expiresAt.After(now) {
		updates["is_frozen"] = false
		updates["frozen_at"] = nil
		updates["frozen_reason"] = ""
	}

	if err := global.APP_DB.Model(&inst).Updates(updates).Error; err != nil {
		return err
	}

	global.APP_LOG.Info("管理员设置实例过期时间",
		zap.Uint("instance_id", instanceID),
		zap.Time("expires_at", expiresAt))

	return nil
}

// FreezeProvider 手动冻结Provider
func (s *FreezeManagementService) FreezeProvider(providerID uint, reason string) error {
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		now := time.Now()

		if reason == "" {
			reason = "manual"
		}

		// 冻结Provider
		result := tx.Model(&provider.Provider{}).
			Where("id = ?", providerID).
			Updates(map[string]interface{}{
				"is_frozen":     true,
				"frozen_at":     now,
				"frozen_reason": reason,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("Provider不存在")
		}

		// 冻结该Provider下所有未手动设置过期时间的实例
		if err := tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND is_manual_expiry = ? AND is_frozen = ?", providerID, false, false).
			Updates(map[string]interface{}{
				"is_frozen":     true,
				"frozen_at":     now,
				"frozen_reason": "node_frozen",
			}).Error; err != nil {
			return err
		}

		return nil
	})
}

// FreezeInstance 手动冻结实例
func (s *FreezeManagementService) FreezeInstance(instanceID uint, reason string) error {
	now := time.Now()

	if reason == "" {
		reason = "manual"
	}

	result := global.APP_DB.Model(&provider.Instance{}).
		Where("id = ?", instanceID).
		Updates(map[string]interface{}{
			"is_frozen":     true,
			"frozen_at":     now,
			"frozen_reason": reason,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("实例不存在")
	}
	return nil
}

// UnfreezeUser 解冻用户
func (s *FreezeManagementService) UnfreezeUser(userID uint) error {
	err := global.APP_DB.Model(&user.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"status": 1, // 恢复为正常状态
		}).Error
	if err != nil {
		return err
	}
	cache.GetUserCacheService().InvalidateUserCache(userID)
	return nil
}

// UnfreezeProvider 解冻Provider及其实例
func (s *FreezeManagementService) UnfreezeProvider(providerID uint) error {
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 解冻Provider
		result := tx.Model(&provider.Provider{}).
			Where("id = ?", providerID).
			Updates(map[string]interface{}{
				"is_frozen":     false,
				"frozen_at":     nil,
				"frozen_reason": "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("Provider不存在")
		}

		// 同步解冻由节点冻结导致的实例，避免Provider与实例状态不一致
		if err := tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND frozen_reason = ?", providerID, "node_frozen").
			Updates(map[string]interface{}{
				"is_frozen":     false,
				"frozen_at":     nil,
				"frozen_reason": "",
			}).Error; err != nil {
			return fmt.Errorf("解冻实例失败: %w", err)
		}

		return nil
	})
}

// UnfreezeInstance 解冻实例
func (s *FreezeManagementService) UnfreezeInstance(instanceID uint) error {
	return global.APP_DB.Model(&provider.Instance{}).
		Where("id = ?", instanceID).
		Updates(map[string]interface{}{
			"is_frozen":     false,
			"frozen_at":     nil,
			"frozen_reason": "",
		}).Error
}
