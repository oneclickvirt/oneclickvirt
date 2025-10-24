package task

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"time"

	"go.uber.org/zap"
)

// getDefaultTimeout 获取默认超时时间
func (s *TaskService) getDefaultTimeout(taskType string) int {
	timeouts := map[string]int{
		"create":              1800, // 30分钟
		"start":               300,  // 5分钟
		"stop":                300,  // 5分钟
		"restart":             600,  // 10分钟
		"reset":               1200, // 20分钟
		"delete":              600,  // 10分钟
		"create-port-mapping": 600,  // 10分钟
		"delete-port-mapping": 300,  // 5分钟
		"reset-password":      600,  // 10分钟
	}

	if timeout, exists := timeouts[taskType]; exists {
		return timeout
	}
	return 1800 // 默认30分钟
}

// updateTaskProgress 更新任务进度
func (s *TaskService) updateTaskProgress(taskID uint, progress int, message string) {
	updates := map[string]interface{}{
		"progress": progress,
	}
	if message != "" {
		updates["status_message"] = message
	}

	if err := global.APP_DB.Model(&adminModel.Task{}).Where("id = ?", taskID).Updates(updates).Error; err != nil {
		global.APP_LOG.Error("更新任务进度失败",
			zap.Uint("taskId", taskID),
			zap.Int("progress", progress),
			zap.String("message", message),
			zap.Error(err))
	} else {
		global.APP_LOG.Debug("任务进度更新成功",
			zap.Uint("taskId", taskID),
			zap.Int("progress", progress),
			zap.String("message", message))
	}
}

// CleanupTimeoutTasksWithLockRelease 清理超时任务并释放锁
func (s *TaskService) CleanupTimeoutTasksWithLockRelease(timeoutThreshold time.Time) (int64, int64) {
	var timeoutRunningTasks []adminModel.Task
	var timeoutCancellingTasks []adminModel.Task

	// 获取超时的running任务
	global.APP_DB.Where("status = ? AND updated_at < ?", "running", timeoutThreshold).Find(&timeoutRunningTasks)

	// 获取超时的cancelling任务
	global.APP_DB.Where("status = ? AND updated_at < ?", "cancelling", timeoutThreshold).Find(&timeoutCancellingTasks)

	// 更新超时的running任务
	result1 := global.APP_DB.Model(&adminModel.Task{}).
		Where("status = ? AND updated_at < ?", "running", timeoutThreshold).
		Updates(map[string]interface{}{
			"status":        "timeout",
			"cancel_reason": "Task timeout - exceeded 30 minutes",
			"updated_at":    time.Now(),
		})

	// 更新超时的cancelling任务
	result2 := global.APP_DB.Model(&adminModel.Task{}).
		Where("status = ? AND updated_at < ?", "cancelling", timeoutThreshold).
		Updates(map[string]interface{}{
			"status":        "cancelled",
			"cancel_reason": "Force cancelled - cancelling timeout",
			"updated_at":    time.Now(),
		})

	var count1, count2 int64
	if result1.Error == nil {
		count1 = result1.RowsAffected
	}
	if result2.Error == nil {
		count2 = result2.RowsAffected
	}

	return count1, count2
}

// executeTaskLogic 执行具体的任务逻辑
func (s *TaskService) executeTaskLogic(ctx context.Context, task *adminModel.Task) error {
	switch task.TaskType {
	case "create":
		return s.executeCreateInstanceTask(ctx, task)
	case "start":
		return s.executeStartInstanceTask(ctx, task)
	case "stop":
		return s.executeStopInstanceTask(ctx, task)
	case "restart":
		return s.executeRestartInstanceTask(ctx, task)
	case "delete":
		return s.executeDeleteInstanceTask(ctx, task)
	case "reset":
		return s.executeResetInstanceTask(ctx, task)
	case "reset-password":
		return s.executeResetPasswordTask(ctx, task)
	case "create-port-mapping":
		return s.executeCreatePortMappingTask(ctx, task)
	case "delete-port-mapping":
		return s.executeDeletePortMappingTask(ctx, task)
	default:
		return fmt.Errorf("未知的任务类型: %s", task.TaskType)
	}
}
