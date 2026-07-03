package task

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	snapshotSvc "oneclickvirt/service/snapshot"
	"oneclickvirt/utils"
)

// updateTaskProgress 更新任务进度（使用全局工具函数）
func (s *TaskService) updateTaskProgress(taskID uint, progress int, message string) {
	utils.UpdateTaskProgress(taskID, progress, message)
}

// getDefaultTimeout 获取默认超时时间（使用全局工具函数）
func (s *TaskService) getDefaultTimeout(taskType string) int {
	return utils.GetDefaultTaskTimeout(taskType)
}

// CleanupTimeoutTasksWithLockRelease 清理超时任务并释放锁
func (s *TaskService) CleanupTimeoutTasksWithLockRelease(timeoutThreshold time.Time) (int64, int64) {
	var runningTasks []adminModel.Task
	var timeoutRunningTasks []adminModel.Task
	var timeoutCancellingTasks []adminModel.Task

	// 只清理真正已经开始执行的 running 任务。processing 表示已入 provider 队列但
	// 可能仍在等待 worker，等待时间不能计入任务执行超时。
	if err := global.APP_DB.Where("status = ?", mainTaskStatusRunning).Find(&runningTasks).Error; err == nil {
		now := time.Now()
		for _, task := range runningTasks {
			timeoutSeconds := task.TimeoutDuration
			if timeoutSeconds <= 0 {
				timeoutSeconds = s.getDefaultTimeout(task.TaskType)
			}
			startAt := task.UpdatedAt
			if task.StartedAt != nil {
				startAt = *task.StartedAt
			}
			if startAt.Add(time.Duration(timeoutSeconds) * time.Second).Before(now) {
				timeoutRunningTasks = append(timeoutRunningTasks, task)
			}
		}
	}

	// 获取超时的cancelling任务
	global.APP_DB.Where("status = ? AND updated_at < ?", mainTaskStatusCancelling, timeoutThreshold).Find(&timeoutCancellingTasks)

	now := time.Now()
	// 更新超时的执行中任务
	var count1 int64
	if len(timeoutRunningTasks) > 0 {
		timeoutTaskIDs := make([]uint, 0, len(timeoutRunningTasks))
		for _, task := range timeoutRunningTasks {
			timeoutTaskIDs = append(timeoutTaskIDs, task.ID)
		}
		result1 := global.APP_DB.Model(&adminModel.Task{}).
			Where("id IN ? AND status = ?", timeoutTaskIDs, mainTaskStatusRunning).
			Updates(map[string]interface{}{
				"status":        mainTaskStatusTimeout,
				"cancel_reason": "Task timeout - exceeded configured execution timeout",
				"completed_at":  &now,
			})
		if result1.Error == nil {
			count1 = result1.RowsAffected
		}
	}

	// 更新超时的cancelling任务
	result2 := global.APP_DB.Model(&adminModel.Task{}).
		Where("status = ? AND updated_at < ?", mainTaskStatusCancelling, timeoutThreshold).
		Updates(map[string]interface{}{
			"status":        mainTaskStatusCancelled,
			"cancel_reason": "Force cancelled - cancelling timeout",
			"completed_at":  &now,
		})

	var count2 int64
	if result2.Error == nil {
		count2 = result2.RowsAffected
	}

	// 异步清理超时任务的实例状态
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// 清理running超时任务的实例状态
		for _, task := range timeoutRunningTasks {
			s.handleCancelledTaskCleanup(task.ID)
		}
		// 清理cancelling超时任务的实例状态
		for _, task := range timeoutCancellingTasks {
			s.handleCancelledTaskCleanup(task.ID)
		}
	}()

	return count1, count2
}

// executeTaskLogic 执行具体的任务逻辑
func (s *TaskService) executeTaskLogic(ctx context.Context, task *adminModel.Task) error {
	switch task.TaskType {
	case "create":
		return s.executeCreateInstanceTask(ctx, task)
	case "create_redemption_instance":
		return s.executeCreateRedemptionInstanceTask(ctx, task)
	case "start":
		return s.executeStartInstanceTask(ctx, task)
	case "stop":
		return s.executeStopInstanceTask(ctx, task)
	case "restart":
		return s.executeRestartInstanceTask(ctx, task)
	case "delete":
		return s.executeDeleteInstanceTask(ctx, task)
	case "reset", "rebuild":
		return s.executeResetInstanceTask(ctx, task)
	case "reset-password":
		return s.executeResetPasswordTask(ctx, task)
	case "create-port-mapping":
		return s.executeCreatePortMappingTask(ctx, task)
	case "delete-port-mapping":
		return s.executeDeletePortMappingTask(ctx, task)
	case "sync-port-mappings":
		return s.executeSyncPortMappingsTask(ctx, task)
	case "snapshot-create", "snapshot-delete", "snapshot-restore":
		service := &snapshotSvc.Service{}
		return service.ExecuteSnapshotAdminTask(ctx, task)
	case "monitor-sync":
		return s.executeMonitorSyncTask(ctx, task)
	case "agent-deploy", "agent-uninstall":
		return s.executeAgentMonitoringTask(ctx, task)
	case "traffic-monitor-enable", "traffic-monitor-disable", "traffic-monitor-detect":
		return s.executeTrafficMonitorTask(ctx, task)
	case "provider-image-cleanup":
		return s.executeProviderImageCleanupTask(ctx, task)
	default:
		return fmt.Errorf("未知的任务类型: %s", task.TaskType)
	}
}
