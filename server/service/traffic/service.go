package traffic

import (
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"

	"go.uber.org/zap"
)

// Service 流量管理服务
type Service struct{}

// TrafficLimitType 流量限制类型
type TrafficLimitType string

const (
	UserTrafficLimit     TrafficLimitType = "user"
	ProviderTrafficLimit TrafficLimitType = "provider"
)

// NewService 创建流量服务实例
func NewService() *Service {
	return &Service{}
}

// GetUserTrafficLimitByLevel 根据用户等级获取流量限制
func (s *Service) GetUserTrafficLimitByLevel(level int) int64 {
	configManager := global.APP_CONFIG.Quota.LevelLimits
	if levelConfig, exists := configManager[level]; exists {
		return levelConfig.MaxTraffic
	}
	return 102400 // 默认100GB
}

// InitUserTrafficQuota 初始化用户流量配额
func (s *Service) InitUserTrafficQuota(userID uint) error {
	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return err
	}

	trafficLimit := s.GetUserTrafficLimitByLevel(u.Level)
	now := time.Now()
	resetTime := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())

	return global.APP_DB.Model(&u).Updates(map[string]interface{}{
		"total_traffic":    trafficLimit,
		"traffic_reset_at": resetTime,
		"traffic_limited":  false,
	}).Error
}

// CheckProviderTrafficLimit 检查Provider流量限制（使用QueryService）
func (s *Service) CheckProviderTrafficLimit(providerID uint) (bool, error) {
	var p provider.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return false, err
	}

	now := time.Now()

	// 初始化TrafficResetAt
	if p.TrafficResetAt == nil {
		nextReset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
		p.TrafficResetAt = &nextReset
		if err := global.APP_DB.Model(&p).Update("traffic_reset_at", nextReset).Error; err != nil {
			global.APP_LOG.Error("初始化Provider流量重置时间失败",
				zap.Uint("providerID", providerID),
				zap.Error(err))
		}
		return false, nil
	}

	// 检查是否到了重置时间
	if !now.Before(*p.TrafficResetAt) {
		nextReset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
		updates := map[string]interface{}{
			"traffic_reset_at": nextReset,
			"traffic_limited":  false,
		}
		if err := global.APP_DB.Model(&p).Updates(updates).Error; err != nil {
			return false, err
		}
		return false, s.resumeProviderInstances(providerID)
	}

	// 使用QueryService查询当月流量
	queryService := NewQueryService()
	year, month, _ := now.Date()
	stats, err := queryService.GetProviderMonthlyTraffic(providerID, year, int(month))
	if err != nil {
		return false, fmt.Errorf("查询Provider流量失败: %w", err)
	}

	// 检查是否超限
	if p.MaxTraffic > 0 && int64(stats.ActualUsageMB) >= p.MaxTraffic {
		return true, nil
	}

	return false, nil
}

// resumeProviderInstances 恢复Provider上的受限实例
func (s *Service) resumeProviderInstances(providerID uint) error {
	var instances []provider.Instance
	err := global.APP_DB.Where("provider_id = ? AND traffic_limited = ?", providerID, true).
		Find(&instances).Error
	if err != nil {
		return err
	}

	global.APP_LOG.Info("开始恢复Provider受限实例",
		zap.Uint("providerID", providerID),
		zap.Int("实例数量", len(instances)))

	successCount := 0
	// 批量创建启动任务，避免循环中的单次更新
	var taskBatch []adminModel.Task
	for _, instance := range instances {
		result := global.APP_DB.Model(&provider.Instance{}).
			Where("id = ? AND traffic_limited = ?", instance.ID, true).
			Updates(map[string]interface{}{
				"traffic_limited": false,
				"status":          "running",
			})

		if result.Error != nil {
			global.APP_LOG.Error("恢复Provider实例状态失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(result.Error))
			continue
		}

		if result.RowsAffected == 0 {
			global.APP_LOG.Debug("实例已被其他任务恢复，跳过",
				zap.Uint("instanceID", instance.ID))
			continue
		}

		// 构建任务对象，稍后批量创建
		taskBatch = append(taskBatch, adminModel.Task{
			TaskType:        "start",
			Status:          "pending",
			UserID:          instance.UserID,
			ProviderID:      &instance.ProviderID,
			InstanceID:      &instance.ID,
			TimeoutDuration: 300,
		})
		successCount++
	}

	// 批量创建任务
	if len(taskBatch) > 0 {
		if err := global.APP_DB.Create(&taskBatch).Error; err != nil {
			global.APP_LOG.Error("批量创建启动任务失败", zap.Error(err))
		} else {
			global.APP_LOG.Info("批量创建启动任务成功",
				zap.Int("count", len(taskBatch)))
		}
	}

	global.APP_LOG.Info("Provider实例恢复完成",
		zap.Uint("providerID", providerID),
		zap.Int("成功数量", successCount),
		zap.Int("总数量", len(instances)))

	return nil
}

// createStartTaskForInstance 创建启动实例的任务
func (s *Service) createStartTaskForInstance(instanceID, userID, providerID uint) error {
	taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instanceID, providerID)

	task := &adminModel.Task{
		TaskType:         "start",
		Status:           "pending",
		Progress:         0,
		StatusMessage:    "实例恢复启动中",
		TaskData:         taskData,
		UserID:           userID,
		ProviderID:       &providerID,
		InstanceID:       &instanceID,
		TimeoutDuration:  1800,
		IsForceStoppable: true,
		CanForceStop:     false,
	}

	if err := global.APP_DB.Create(task).Error; err != nil {
		return err
	}

	// 触发调度器立即处理任务
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	return nil
}
