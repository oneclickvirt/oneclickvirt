package admin

import (
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"
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

	global.APP_LOG.Info("管理员设置用户过期时间",
		zap.Uint("user_id", userID),
		zap.Time("expires_at", expiresAt))

	return nil
}

// SetProviderExpiry 设置Provider过期时间
func (s *FreezeManagementService) SetProviderExpiry(providerID uint, expiresAt time.Time) error {
	now := time.Now()

	var p provider.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
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
		global.APP_DB.Model(&provider.Instance{}).
			Where("provider_id = ? AND frozen_reason = ?", providerID, "node_frozen").
			Updates(map[string]interface{}{
				"is_frozen":     false,
				"frozen_at":     nil,
				"frozen_reason": "",
			})
	}

	// 更新Provider下所有非手动设置过期时间的实例，同步新的过期时间
	global.APP_DB.Model(&provider.Instance{}).
		Where("provider_id = ? AND is_manual_expiry = ?", providerID, false).
		Update("expires_at", expiresAt)

	if err := global.APP_DB.Model(&p).Updates(updates).Error; err != nil {
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
		if err := tx.Model(&provider.Provider{}).
			Where("id = ?", providerID).
			Updates(map[string]interface{}{
				"is_frozen":     true,
				"frozen_at":     now,
				"frozen_reason": reason,
			}).Error; err != nil {
			return err
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

	return global.APP_DB.Model(&provider.Instance{}).
		Where("id = ?", instanceID).
		Updates(map[string]interface{}{
			"is_frozen":     true,
			"frozen_at":     now,
			"frozen_reason": reason,
		}).Error
}

// UnfreezeUser 解冻用户
func (s *FreezeManagementService) UnfreezeUser(userID uint) error {
	return global.APP_DB.Model(&user.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"is_frozen":     false,
			"frozen_at":     nil,
			"frozen_reason": "",
			"status":        1, // 恢复为正常状态
		}).Error
}

// UnfreezeProvider 解冻Provider及其实例
func (s *FreezeManagementService) UnfreezeProvider(providerID uint) error {
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 解冻Provider
		if err := tx.Model(&provider.Provider{}).
			Where("id = ?", providerID).
			Updates(map[string]interface{}{
				"is_frozen":     false,
				"frozen_at":     nil,
				"frozen_reason": "",
			}).Error; err != nil {
			return err
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
