package scheduler

import (
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/provider"
	"oneclickvirt/service/system"

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
	// 清理过期的JWT黑名单
	s.cleanupExpiredJWTBlacklist()

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

// cleanupExpiredJWTBlacklist 清理过期的JWT黑名单
func (s *SchedulerService) cleanupExpiredJWTBlacklist() {
	// 检查数据库是否已初始化
	if global.APP_DB == nil {
		global.APP_LOG.Debug("数据库未初始化，跳过JWT黑名单清理")
		return
	}

	result := global.APP_DB.Where("expires_at < ?", time.Now()).
		Delete(&auth.JWTBlacklist{})

	if result.Error != nil {
		global.APP_LOG.Error("Failed to cleanup expired JWT blacklist", zap.Error(result.Error))
	} else if result.RowsAffected > 10 { // 只有清理数量较多时才记录
		global.APP_LOG.Info("Cleaned up expired JWT blacklist entries",
			zap.Int64("count", result.RowsAffected))
	}
}

// cleanupExpiredProviders 清理过期的Provider配置
func (s *SchedulerService) cleanupExpiredProviders() {
	// 检查数据库是否已初始化
	if global.APP_DB == nil {
		global.APP_LOG.Debug("数据库未初始化，跳过Provider清理")
		return
	}
	// 标记长时间未活动的Provider为不可用
	inactiveThreshold := time.Now().Add(-24 * time.Hour)

	result := global.APP_DB.Model(&provider.Provider{}).
		Where("allow_claim = ? AND updated_at < ?", true, inactiveThreshold).
		Update("allow_claim", false)

	if result.Error != nil {
		global.APP_LOG.Error("Failed to cleanup inactive provider", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		global.APP_LOG.Info("Disabled inactive provider",
			zap.Int64("count", result.RowsAffected))
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
