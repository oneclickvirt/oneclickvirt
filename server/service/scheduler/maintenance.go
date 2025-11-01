package scheduler

import (
	"context"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/provider"
	"oneclickvirt/service/system"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// cleanupTimeoutTasks 清理超时任务
func (s *SchedulerService) cleanupTimeoutTasks() {
	timeoutThreshold := time.Now().Add(-30 * time.Minute)

	// 使用TaskService的新方法清理超时任务并释放锁
	count1, count2 := s.taskService.CleanupTimeoutTasksWithLockRelease(timeoutThreshold)

	if count1 > 0 {
		global.APP_LOG.Info("Cleaned up timeout running tasks",
			zap.Int64("count", count1))
	}

	if count2 > 0 {
		global.APP_LOG.Info("Cleaned up timeout cancelling tasks",
			zap.Int64("count", count2))
	}
}

// performMaintenance 执行系统维护任务
func (s *SchedulerService) performMaintenance() {
	// 清理过期的Provider配置
	s.cleanupExpiredProviders()

	// 清理过期实例
	s.cleanupExpiredInstances()

	// 清理旧的任务记录（可选）
	s.cleanupOldTasks()
}

// cleanupExpiredInstances 清理过期实例
func (s *SchedulerService) cleanupExpiredInstances() {
	cleanupService := system.GetInstanceCleanupService()
	if err := cleanupService.CleanupExpiredInstances(); err != nil {
		global.APP_LOG.Error("清理过期实例时发生错误", zap.Error(err))
	}
}

// cleanupExpiredProviders 清理过期的Provider配置
func (s *SchedulerService) cleanupExpiredProviders() {
	// 检查数据库是否已初始化
	if global.APP_DB == nil {
		global.APP_LOG.Debug("数据库未初始化，跳过Provider清理")
		return
	}

	// 从配置读取不活动阈值，默认72小时（3天）
	inactiveHours := global.APP_CONFIG.System.ProviderInactiveHours
	if inactiveHours <= 0 {
		inactiveHours = 72 // 默认72小时
	}

	// 标记长时间未活动的Provider为不可用
	inactiveThreshold := time.Now().Add(-time.Duration(inactiveHours) * time.Hour)

	// 使用带重试的批量更新操作，避免长时间锁表
	err := utils.RetryableDBOperation(context.Background(), func() error {
		// 分批处理，每次最多处理100条记录
		var providers []provider.Provider

		// 首先查找需要更新的Provider（使用较短的锁超时）
		if err := global.APP_DB.
			Where("allow_claim = ? AND updated_at < ?", true, inactiveThreshold).
			Limit(100).
			Find(&providers).Error; err != nil {
			return err
		}

		if len(providers) == 0 {
			return nil
		}

		// 收集需要更新的ID
		var providerIDs []uint
		for _, p := range providers {
			providerIDs = append(providerIDs, p.ID)
		}

		// 批量更新，使用IN查询减少锁定时间
		result := global.APP_DB.
			Model(&provider.Provider{}).
			Where("id IN ?", providerIDs).
			Update("allow_claim", false)

		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected > 0 {
			global.APP_LOG.Info("禁用不活动的Provider",
				zap.Int64("count", result.RowsAffected),
				zap.Int("inactive_hours", inactiveHours))
		}

		return nil
	}, 3)

	if err != nil {
		global.APP_LOG.Error("Failed to cleanup inactive provider", zap.Error(err))
	}
}

// cleanupOldTasks 清理旧的任务记录
func (s *SchedulerService) cleanupOldTasks() {
	// 检查数据库是否已初始化
	if global.APP_DB == nil {
		global.APP_LOG.Debug("数据库未初始化，跳过旧任务清理")
		return
	}

	// 清理30天前的已完成任务
	oldThreshold := time.Now().Add(-30 * 24 * time.Hour)

	result := global.APP_DB.Where("status IN ? AND updated_at < ?",
		[]string{"completed", "failed", "cancelled"}, oldThreshold).
		Delete(&adminModel.Task{})

	if result.Error != nil {
		global.APP_LOG.Error("Failed to cleanup old tasks", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		global.APP_LOG.Info("Cleaned up old tasks",
			zap.Int64("count", result.RowsAffected))
	}
}
