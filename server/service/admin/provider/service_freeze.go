package provider

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/database"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (s *Service) FreezeProvider(req admin.FreezeProviderRequest) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ID).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	provider.IsFrozen = true
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&provider).Error
	})
}

// UnfreezeProvider 解冻Provider
func (s *Service) UnfreezeProvider(req admin.UnfreezeProviderRequest) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ID).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 解析新的过期时间
	if req.ExpiresAt != "" {
		// 尝试解析多种时间格式
		var t time.Time
		var err error

		// 首先尝试ISO 8601格式（前端默认格式）
		t, err = time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			// 尝试标准日期时间格式
			t, err = time.Parse("2006-01-02 15:04:05", req.ExpiresAt)
			if err != nil {
				// 尝试日期格式
				t, err = time.Parse("2006-01-02", req.ExpiresAt)
				if err != nil {
					return fmt.Errorf("过期时间格式错误，请使用 'YYYY-MM-DD HH:MM:SS' 或 'YYYY-MM-DD' 格式")
				}
			}
		}
		// 检查新的过期时间必须是未来时间
		if t.Before(time.Now()) {
			return fmt.Errorf("过期时间必须是未来时间")
		}
		provider.ExpiresAt = &t
	} else {
		// 如果没有指定新的过期时间，设置为31天后
		defaultExpiry := time.Now().AddDate(0, 0, 31)
		provider.ExpiresAt = &defaultExpiry
	}

	provider.IsFrozen = false
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 保存Provider更新
		if err := tx.Save(&provider).Error; err != nil {
			return err
		}

		// 同步更新该Provider下所有非手动设置过期时间的实例的到期时间
		if provider.ExpiresAt != nil {
			if err := tx.Model(&providerModel.Instance{}).
				Where("provider_id = ? AND is_manual_expiry = ? AND status NOT IN (?)", provider.ID, false, []string{"deleting", "deleted"}).
				Update("expires_at", *provider.ExpiresAt).Error; err != nil {
				global.APP_LOG.Error("同步实例到期时间失败",
					zap.Uint("providerID", provider.ID),
					zap.Time("newExpiresAt", *provider.ExpiresAt),
					zap.Error(err))
				return fmt.Errorf("同步实例到期时间失败: %v", err)
			}
			global.APP_LOG.Info("已同步非手动设置过期时间的实例到期时间",
				zap.Uint("providerID", provider.ID),
				zap.Time("newExpiresAt", *provider.ExpiresAt))
		}

		return nil
	})
}
