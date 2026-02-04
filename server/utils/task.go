package utils

import (
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"time"

	"go.uber.org/zap"
)

// UpdateTaskProgress 更新任务进度（全局统一函数）
func UpdateTaskProgress(taskID uint, progress int, message string) {
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

// MarkTaskCompleted 标记任务最终完成（全局统一函数）
func MarkTaskCompleted(taskID uint, message string) {
	updates := map[string]interface{}{
		"status":       "completed",
		"completed_at": time.Now(),
		"progress":     100,
	}
	if message != "" {
		updates["status_message"] = message
	}

	// 只在任务状态为running时才更新为completed，避免覆盖failed状态
	result := global.APP_DB.Model(&adminModel.Task{}).Where("id = ? AND status = ?", taskID, "running").Updates(updates)
	if result.Error != nil {
		global.APP_LOG.Error("标记任务完成失败",
			zap.Uint("taskId", taskID),
			zap.String("message", message),
			zap.Error(result.Error))
	} else if result.RowsAffected == 0 {
		// 没有更新任何行，说明任务状态不是running（可能已经是failed或其他状态）
		global.APP_LOG.Warn("任务状态不是running，跳过标记为完成",
			zap.Uint("taskId", taskID),
			zap.String("message", message))
	} else {
		global.APP_LOG.Info("任务标记为完成",
			zap.Uint("taskId", taskID),
			zap.String("message", message))

		// 释放并发控制锁
		if global.APP_TASK_LOCK_RELEASER != nil {
			global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(taskID)
		}
	}
}

// MarkTaskFailed 标记任务失败（全局统一函数）
func MarkTaskFailed(taskID uint, errorMessage string) {
	if err := global.APP_DB.Model(&adminModel.Task{}).Where("id = ?", taskID).Updates(map[string]interface{}{
		"status":        "failed",
		"completed_at":  time.Now(),
		"error_message": errorMessage,
	}).Error; err != nil {
		global.APP_LOG.Error("标记任务失败时出错", zap.Uint("taskId", taskID), zap.Error(err))
	}

	// 释放并发控制锁
	if global.APP_TASK_LOCK_RELEASER != nil {
		global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(taskID)
	}
}

// GetDefaultTaskTimeout 获取默认任务超时时间（秒）
func GetDefaultTaskTimeout(taskType string) int {
	timeouts := map[string]int{
		"create":              1800, // 30分钟
		"start":               300,  // 5分钟
		"stop":                300,  // 5分钟
		"restart":             600,  // 10分钟
		"reset":               1200, // 20分钟
		"delete":              1800, // 30分钟 - 删除操作需要更长时间处理重试和清理
		"create-port-mapping": 600,  // 10分钟
		"delete-port-mapping": 300,  // 5分钟
		"reset-password":      600,  // 10分钟
	}

	if timeout, exists := timeouts[taskType]; exists {
		return timeout
	}
	return 1800 // 默认30分钟
}
