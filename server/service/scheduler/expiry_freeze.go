package scheduler

import (
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ExpiryFreezeService 过期冻结服务
type ExpiryFreezeService struct{}

// CheckAndFreezeExpiredProviders 检查并冻结过期的Provider节点
// 节点过期后自动冻结节点和对应的所有实例（未手动设置过期时间的实例）
func (s *ExpiryFreezeService) CheckAndFreezeExpiredProviders() error {
	now := time.Now()

	// 查询已过期但未冻结的Provider
	var providers []provider.Provider
	err := global.APP_DB.Where("expires_at IS NOT NULL AND expires_at <= ? AND is_frozen = ?", now, false).
		Find(&providers).Error
	if err != nil {
		global.APP_LOG.Error("查询过期Provider失败", zap.Error(err))
		return err
	}

	if len(providers) == 0 {
		return nil
	}

	global.APP_LOG.Info("发现过期的Provider", zap.Int("count", len(providers)))

	// 批量处理过期的Provider
	for _, p := range providers {
		if err := s.freezeProvider(&p); err != nil {
			global.APP_LOG.Error("冻结Provider失败",
				zap.Uint("provider_id", p.ID),
				zap.String("provider_name", p.Name),
				zap.Error(err))
			continue
		}

		global.APP_LOG.Info("已冻结过期Provider",
			zap.Uint("provider_id", p.ID),
			zap.String("provider_name", p.Name))
	}

	return nil
}

// freezeProvider 冻结Provider及其非手动设置过期时间的实例
func (s *ExpiryFreezeService) freezeProvider(p *provider.Provider) error {
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		now := time.Now()

		// 1. 冻结Provider
		if err := tx.Model(p).Updates(map[string]interface{}{
			"is_frozen":     true,
			"frozen_at":     now,
			"frozen_reason": "expired",
		}).Error; err != nil {
			return err
		}

		// 2. 冻结该Provider下所有未手动设置过期时间的实例
		// 手动设置了过期时间的实例不受节点冻结影响
		if err := tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND is_manual_expiry = ? AND is_frozen = ?", p.ID, false, false).
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

// CheckAndFreezeExpiredInstances 检查并冻结过期的实例
// 仅冻结手动设置了过期时间的实例，或节点未冻结但实例已过期的实例
func (s *ExpiryFreezeService) CheckAndFreezeExpiredInstances() error {
	now := time.Now()

	// 查询已过期但未冻结的实例
	var instances []provider.Instance
	err := global.APP_DB.Where("expires_at IS NOT NULL AND expires_at <= ? AND is_frozen = ?", now, false).
		Find(&instances).Error
	if err != nil {
		global.APP_LOG.Error("查询过期实例失败", zap.Error(err))
		return err
	}

	if len(instances) == 0 {
		return nil
	}

	global.APP_LOG.Info("发现过期的实例", zap.Int("count", len(instances)))

	// 批量处理过期的实例
	for _, inst := range instances {
		if err := s.freezeInstance(&inst); err != nil {
			global.APP_LOG.Error("冻结实例失败",
				zap.Uint("instance_id", inst.ID),
				zap.String("instance_name", inst.Name),
				zap.Error(err))
			continue
		}

		global.APP_LOG.Info("已冻结过期实例",
			zap.Uint("instance_id", inst.ID),
			zap.String("instance_name", inst.Name))
	}

	return nil
}

// freezeInstance 冻结单个实例
func (s *ExpiryFreezeService) freezeInstance(inst *provider.Instance) error {
	now := time.Now()

	return global.APP_DB.Model(inst).Updates(map[string]interface{}{
		"is_frozen":     true,
		"frozen_at":     now,
		"frozen_reason": "expired",
	}).Error
}

// CheckAndFreezeExpiredUsers 检查并冻结过期的用户
// 用户过期后自动冻结禁用，不支持登录操作
func (s *ExpiryFreezeService) CheckAndFreezeExpiredUsers() error {
	now := time.Now()

	// 查询已过期但未冻结的用户
	var users []user.User
	err := global.APP_DB.Where("expires_at IS NOT NULL AND expires_at <= ? AND is_frozen = ?", now, false).
		Find(&users).Error
	if err != nil {
		global.APP_LOG.Error("查询过期用户失败", zap.Error(err))
		return err
	}

	if len(users) == 0 {
		return nil
	}

	global.APP_LOG.Info("发现过期的用户", zap.Int("count", len(users)))

	// 批量处理过期的用户 - 禁用状态
	for _, u := range users {
		if err := s.disableUser(&u); err != nil {
			global.APP_LOG.Error("禁用过期用户失败",
				zap.Uint("user_id", u.ID),
				zap.String("username", u.Username),
				zap.Error(err))
			continue
		}

		global.APP_LOG.Info("已禁用过期用户",
			zap.Uint("user_id", u.ID),
			zap.String("username", u.Username))
	}

	return nil
}

// disableUser 禁用单个过期用户
func (s *ExpiryFreezeService) disableUser(u *user.User) error {
	return global.APP_DB.Model(u).Update("status", 0).Error
}

// CheckAndFreezeAll 检查并冻结所有过期的资源
// 按照优先级顺序：用户 -> Provider -> 实例
func (s *ExpiryFreezeService) CheckAndFreezeAll() error {
	// 1. 先冻结过期用户
	if err := s.CheckAndFreezeExpiredUsers(); err != nil {
		global.APP_LOG.Error("检查过期用户失败", zap.Error(err))
	}

	// 2. 再冻结过期Provider
	if err := s.CheckAndFreezeExpiredProviders(); err != nil {
		global.APP_LOG.Error("检查过期Provider失败", zap.Error(err))
	}

	// 3. 最后冻结过期实例
	if err := s.CheckAndFreezeExpiredInstances(); err != nil {
		global.APP_LOG.Error("检查过期实例失败", zap.Error(err))
	}

	return nil
}
