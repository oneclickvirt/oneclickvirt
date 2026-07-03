package scheduler

import (
	"encoding/json"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"
	"oneclickvirt/service/cache"
	"oneclickvirt/service/taskgate"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ExpiryFreezeService 过期冻结服务
type ExpiryFreezeService struct{}

// CheckAndFreezeExpiredProviders 检查并冻结过期的Provider节点
// 节点过期后自动冻结节点和对应的所有实例（未手动设置过期时间的实例）
// 使用游标分页避免一次性加载所有Provider导致内存溢出
func (s *ExpiryFreezeService) CheckAndFreezeExpiredProviders() error {
	const batchSize = 100
	var lastID uint = 0
	totalFrozen := 0
	now := time.Now()

	for {
		var providers []provider.Provider
		err := global.APP_DB.
			Where("id > ? AND expires_at IS NOT NULL AND expires_at <= ? AND is_frozen = ?", lastID, now, false).
			Order("id ASC").
			Limit(batchSize).
			Find(&providers).Error
		if err != nil {
			global.APP_LOG.Error("查询过期Provider失败", zap.Error(err))
			return err
		}
		if len(providers) == 0 {
			break
		}

		// 批量处理过期的Provider
		for _, p := range providers {
			if err := s.freezeProvider(&p); err != nil {
				global.APP_LOG.Warn("冻结Provider失败",
					zap.Uint("provider_id", p.ID),
					zap.String("provider_name", p.Name),
					zap.Error(err))
			} else {
				totalFrozen++
			}
			lastID = p.ID
		}

		if len(providers) < batchSize {
			break
		}
	}

	if totalFrozen > 0 {
		global.APP_LOG.Info("已冻结过期Provider", zap.Int("count", totalFrozen))
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
// 使用单次批量UPDATE避免逐行处理，防止内存溢出
func (s *ExpiryFreezeService) CheckAndFreezeExpiredInstances() error {
	now := time.Now()

	type expiredInstance struct {
		ID                       uint
		UserID                   uint
		ProviderID               uint
		Status                   string
		InstanceExpiryAction     string
		InstanceExpiryExtendDays int
	}

	const batchSize = 200
	var lastID uint
	totalHandled := 0
	for {
		var instances []expiredInstance
		if err := global.APP_DB.Table("instances i").
			Joins("INNER JOIN providers p ON i.provider_id = p.id").
			Select("i.id, i.user_id, i.provider_id, i.status, COALESCE(p.instance_expiry_action, 'delete') AS instance_expiry_action, COALESCE(p.instance_expiry_extend_days, 0) AS instance_expiry_extend_days").
			Where("i.id > ? AND i.expires_at IS NOT NULL AND i.expires_at <= ? AND i.is_frozen = ? AND i.status NOT IN ?", lastID, now, false, []string{"deleted", "deleting"}).
			Order("i.id ASC").
			Limit(batchSize).
			Find(&instances).Error; err != nil {
			global.APP_LOG.Error("查询过期实例失败", zap.Error(err))
			return err
		}
		if len(instances) == 0 {
			break
		}

		for _, inst := range instances {
			if err := s.handleExpiredInstance(inst, now); err != nil {
				global.APP_LOG.Warn("处理过期实例失败",
					zap.Uint("instanceID", inst.ID),
					zap.String("action", inst.InstanceExpiryAction),
					zap.Error(err))
			} else {
				totalHandled++
			}
			lastID = inst.ID
		}

		if len(instances) < batchSize {
			break
		}
	}

	if totalHandled > 0 {
		global.APP_LOG.Info("已处理过期实例", zap.Int("count", totalHandled))
	}
	return nil
}

func (s *ExpiryFreezeService) handleExpiredInstance(inst struct {
	ID                       uint
	UserID                   uint
	ProviderID               uint
	Status                   string
	InstanceExpiryAction     string
	InstanceExpiryExtendDays int
}, now time.Time) error {
	switch inst.InstanceExpiryAction {
	case provider.InstanceExpiryActionExtend:
		extendDays := inst.InstanceExpiryExtendDays
		if extendDays <= 0 {
			extendDays = 1
		}
		nextExpiry := now.Add(time.Duration(extendDays) * 24 * time.Hour)
		return global.APP_DB.Model(&provider.Instance{}).
			Where("id = ?", inst.ID).
			Update("expires_at", nextExpiry).Error
	case provider.InstanceExpiryActionStop:
		return s.stopExpiredInstance(inst.ID, inst.UserID, inst.ProviderID, inst.Status, now)
	case provider.InstanceExpiryActionFreeze, provider.InstanceExpiryActionDelete:
		fallthrough
	default:
		return global.APP_DB.Model(&provider.Instance{}).
			Where("id = ?", inst.ID).
			Updates(map[string]interface{}{
				"is_frozen":     true,
				"frozen_at":     now,
				"frozen_reason": "expired",
			}).Error
	}
}

func (s *ExpiryFreezeService) stopExpiredInstance(instanceID, userID, providerID uint, status string, now time.Time) error {
	updates := map[string]interface{}{
		"is_frozen":     true,
		"frozen_at":     now,
		"frozen_reason": "expired",
	}
	if status == "running" {
		if err := taskgate.EnsureAccepting(); err != nil {
			return err
		}
		updates["status"] = "stopped"
		updates["expiry_stopped"] = true
		updates["expiry_stopped_at"] = now
	}
	if err := global.APP_DB.Model(&provider.Instance{}).Where("id = ?", instanceID).Updates(updates).Error; err != nil {
		return err
	}
	if status != "running" {
		return nil
	}

	taskData, err := json.Marshal(map[string]interface{}{
		"instanceId": instanceID,
		"providerId": providerID,
	})
	if err != nil {
		return err
	}
	task := &adminModel.Task{
		TaskType:         "stop",
		Status:           "pending",
		Progress:         0,
		StatusMessage:    "实例到期自动关机",
		TaskData:         string(taskData),
		UserID:           userID,
		ProviderID:       &providerID,
		InstanceID:       &instanceID,
		TimeoutDuration:  600,
		IsForceStoppable: true,
		CanForceStop:     false,
	}
	if err := global.APP_DB.Create(task).Error; err != nil {
		return err
	}
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return nil
}

// CheckAndFreezeExpiredUsers 检查并冻结过期的用户
// 用户过期后自动冻结禁用，不支持登录操作
// 使用游标分页避免一次性加载所有用户导致内存溢出
func (s *ExpiryFreezeService) CheckAndFreezeExpiredUsers() error {
	const batchSize = 200
	var lastID uint = 0
	totalDisabled := 0
	now := time.Now()

	for {
		// 仅加载 ID 用于批量更新，避免加载全部字段
		var userIDs []uint
		err := global.APP_DB.Model(&user.User{}).
			Select("id").
			Where("id > ? AND expires_at IS NOT NULL AND expires_at <= ? AND status != ?", lastID, now, 0).
			Order("id ASC").
			Limit(batchSize).
			Pluck("id", &userIDs).Error
		if err != nil {
			global.APP_LOG.Error("查询过期用户失败", zap.Error(err))
			return err
		}
		if len(userIDs) == 0 {
			break
		}

		// 批量禁用
		if err := global.APP_DB.Model(&user.User{}).
			Where("id IN ?", userIDs).
			Update("status", 0).Error; err != nil {
			global.APP_LOG.Error("批量禁用过期用户失败", zap.Error(err))
			return err
		}

		// 批量清除认证缓存
		cacheService := cache.GetUserCacheService()
		for _, uid := range userIDs {
			cacheService.InvalidateUserCache(uid)
		}

		totalDisabled += len(userIDs)
		lastID = userIDs[len(userIDs)-1]

		if len(userIDs) < batchSize {
			break
		}
	}

	if totalDisabled > 0 {
		global.APP_LOG.Info("已禁用过期用户", zap.Int("count", totalDisabled))
	}
	return nil
}

// CheckAndFreezeAll 检查并冻结所有过期的资源
// 按照优先级顺序：用户 -> Provider -> 实例
func (s *ExpiryFreezeService) CheckAndFreezeAll() error {
	// 1. 先冻结过期用户
	if err := s.CheckAndFreezeExpiredUsers(); err != nil {
		global.APP_LOG.Warn("检查过期用户失败", zap.Error(err))
	}

	// 2. 再冻结过期Provider
	if err := s.CheckAndFreezeExpiredProviders(); err != nil {
		global.APP_LOG.Warn("检查过期Provider失败", zap.Error(err))
	}

	// 3. 最后冻结过期实例
	if err := s.CheckAndFreezeExpiredInstances(); err != nil {
		global.APP_LOG.Warn("检查过期实例失败", zap.Error(err))
	}

	return nil
}
