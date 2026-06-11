package task

import (
	"errors"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/resources"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// cleanupInterruptedTasks reconciles tasks that were active in a previous process.
// Worker-pool contexts and goroutines are process-local; after a restart there is no
// executor left that can finish records left in processing/running/cancelling.
func (s *TaskService) cleanupInterruptedTasks(reason string) {
	if global.APP_DB == nil {
		global.APP_LOG.Warn("数据库连接不存在，无法清理未完成任务")
		return
	}

	var interrupted []adminModel.Task
	if err := global.APP_DB.
		Select("id", "task_type", "status", "user_id", "provider_id", "instance_id", "task_data", "preallocated_cpu", "preallocated_memory", "preallocated_disk", "preallocated_bandwidth").
		Where("status IN ?", mainTaskInFlightStatuses).
		Find(&interrupted).Error; err != nil {
		global.APP_LOG.Error("查询未完成任务失败", zap.Error(err))
		return
	}
	if len(interrupted) == 0 {
		return
	}

	now := time.Now()
	runningResult := global.APP_DB.Model(&adminModel.Task{}).
		Where("status IN ?", []string{mainTaskStatusProcessing, mainTaskStatusRunning}).
		Updates(map[string]interface{}{
			"status":        mainTaskStatusFailed,
			"error_message": reason,
			"completed_at":  &now,
		})
	if runningResult.Error != nil {
		global.APP_LOG.Error("清理执行中任务失败", zap.Error(runningResult.Error))
	}

	cancellingResult := global.APP_DB.Model(&adminModel.Task{}).
		Where("status = ?", mainTaskStatusCancelling).
		Updates(map[string]interface{}{
			"status":        mainTaskStatusCancelled,
			"cancel_reason": reason,
			"completed_at":  &now,
		})
	if cancellingResult.Error != nil {
		global.APP_LOG.Error("清理取消中任务失败", zap.Error(cancellingResult.Error))
	}

	for _, task := range interrupted {
		s.contextManager.Delete(task.ID)
		if task.Status == mainTaskStatusCancelling {
			s.handleCancelledTaskCleanup(task.ID)
			continue
		}
		s.handleInterruptedTaskCleanupOnStartup(task)
	}

	global.APP_LOG.Info("已清理上一个进程遗留的未完成任务",
		zap.Int("count", len(interrupted)),
		zap.Int64("failed", rowsAffected(runningResult)),
		zap.Int64("cancelled", rowsAffected(cancellingResult)),
		zap.String("reason", reason))
}

func rowsAffected(result *gorm.DB) int64 {
	if result == nil || result.Error != nil {
		return 0
	}
	return result.RowsAffected
}

func (s *TaskService) handleInterruptedTaskCleanupOnStartup(task adminModel.Task) {
	s.invalidateTaskInstanceCaches(task.ID)

	if task.InstanceID == nil {
		s.releaseTaskResources(task.ID)
		return
	}

	switch task.TaskType {
	case "create", "create_instance", "create_redemption_instance":
		s.reconcileInterruptedCreateTask(task)
	case "start", "stop", "restart", "reset", "rebuild", "delete":
		s.handleCancelledTaskCleanup(task.ID)
	default:
		// Generic tasks do not have instance state transitions to repair.
	}
}

func (s *TaskService) reconcileInterruptedCreateTask(task adminModel.Task) {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, *task.InstanceID).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			global.APP_LOG.Warn("清理中断创建任务时读取实例失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", *task.InstanceID),
				zap.Error(err))
		}
		return
	}

	// 创建任务在 processing 阶段可能已经写入实例记录和资源占用。进程已退出后无法继续
	// 后处理，避免实例长期卡在 creating；保留 error 实例供管理员确认/删除。
	if instance.Status == constant.InstanceStatusCreating {
		if err := global.APP_DB.Model(&providerModel.Instance{}).
			Where("id = ?", instance.ID).
			Update("status", constant.InstanceStatusError).Error; err != nil {
			global.APP_LOG.Warn("中断创建任务的实例状态修复失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		}
	}

	if task.UserID > 0 {
		quotaService := resources.NewQuotaService()
		if err := quotaService.RecalculateUserQuota(task.UserID); err != nil {
			global.APP_LOG.Warn("中断创建任务后重算用户配额失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("userId", task.UserID),
				zap.Error(err))
		}
	}

	// Provider 资源在预处理阶段已按实例记录分配。这里不释放 Provider 资源，避免远端实例
	// 已创建但主控尚未完成后处理时产生超卖；管理员删除 error 实例时会走统一删除释放流程。
}
