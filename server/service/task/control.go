package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/resources"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CompleteTask 完成任务
func (s *TaskService) CompleteTask(taskID uint, success bool, errorMessage string, resultData map[string]interface{}) error {
	// 首先获取任务信息
	var task adminModel.Task
	err := global.APP_DB.First(&task, taskID).Error
	if err != nil {
		global.APP_LOG.Error("获取任务信息失败",
			zap.Uint("taskId", taskID),
			zap.Error(err))
		return err
	}

	// 幂等性检查：如果任务已经是完成状态，避免重复处理
	if task.Status == "completed" || task.Status == "failed" || task.Status == "cancelled" {
		global.APP_LOG.Info("任务已经是完成状态，跳过重复处理",
			zap.Uint("taskId", taskID),
			zap.String("currentStatus", task.Status),
			zap.Bool("requestedSuccess", success))
		return nil
	}

	now := time.Now()
	status := "completed"
	if !success {
		status = "failed"
	}

	err = s.dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"status":       status,
			"completed_at": &now,
		}

		// 只在失败时设置 error_message，成功时不设置
		if !success && errorMessage != "" {
			updates["error_message"] = errorMessage
		}

		return tx.Model(&adminModel.Task{}).Where("id = ?", taskID).Updates(updates).Error
	})

	if err != nil {
		global.APP_LOG.Error("完成任务失败",
			zap.Uint("taskId", taskID),
			zap.Error(err))
		return err
	}

	// 如果任务失败且没有创建实例，释放预留资源
	if !success && task.InstanceID == nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.releaseTaskResources(taskID)
		}()
	}

	global.APP_LOG.Info("任务完成",
		zap.Uint("taskId", taskID),
		zap.Bool("success", success),
		zap.String("errorMessage", errorMessage))

	// 任务完成后，立即触发调度器检查pending任务
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
		global.APP_LOG.Debug("任务完成后触发调度器检查pending任务", zap.Uint("taskId", taskID))
	}

	return nil
}

// ReleaseTaskLocks 空实现 - channel池架构无需显式释放锁
func (s *TaskService) ReleaseTaskLocks(taskID uint) {
	// channel池架构自动处理并发控制，无需显式释放
}

// CancelTask 用户取消任务
func (s *TaskService) CancelTask(taskID uint, userID uint) error {
	err := s.dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		var task adminModel.Task
		err := tx.Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error
		if err != nil {
			return fmt.Errorf("任务不存在或无权限")
		}

		// 检查任务是否允许被用户取消
		if !task.IsForceStoppable {
			return fmt.Errorf("此任务不允许取消（管理员操作）")
		}

		switch task.Status {
		case "pending":
			return s.cancelPendingTask(tx, taskID, "用户取消")
		case "running":
			return s.cancelRunningTask(tx, taskID, "用户取消")
		default:
			return fmt.Errorf("任务状态[%s]不允许取消", task.Status)
		}
	})

	return err
}

// CancelTaskByAdmin 管理员取消/强制停止任务
func (s *TaskService) CancelTaskByAdmin(taskID uint, reason string) error {
	err := s.dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		var task adminModel.Task
		err := tx.First(&task, taskID).Error
		if err != nil {
			return fmt.Errorf("任务不存在")
		}

		switch task.Status {
		case "pending":
			return s.cancelPendingTask(tx, taskID, fmt.Sprintf("管理员取消: %s", reason))
		case "processing", "running":
			// processing和running状态都使用强制停止
			return s.forceStopRunningTask(tx, taskID, fmt.Sprintf("管理员强制停止: %s", reason))
		case "cancelling":
			return s.forceKillTask(tx, taskID, fmt.Sprintf("管理员强制终止: %s", reason))
		default:
			return fmt.Errorf("任务状态[%s]不允许操作", task.Status)
		}
	})

	// 对于running状态的任务，不在这里调用handleCancelledTaskCleanup
	// 因为任务可能已经部分执行，不应该简单恢复状态
	// 只有pending状态的任务取消才会在cancelPendingTask中恢复状态

	return err
}

// cancelPendingTask 取消pending状态的任务
func (s *TaskService) cancelPendingTask(tx *gorm.DB, taskID uint, reason string) error {
	now := time.Now()
	result := tx.Model(&adminModel.Task{}).
		Where("id = ? AND status = ?", taskID, "pending").
		Updates(map[string]interface{}{
			"status":        "cancelled",
			"cancel_reason": reason,
			"completed_at":  &now,
		})

	if result.RowsAffected == 0 {
		return fmt.Errorf("任务状态已变更，无法取消")
	}

	// 释放预留资源
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.releaseTaskResources(taskID)
	}()

	// pending状态的任务取消后，需要恢复实例状态
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.handleCancelledTaskCleanup(taskID)
	}()

	return nil
}

// cancelRunningTask 取消running状态的任务
func (s *TaskService) cancelRunningTask(tx *gorm.DB, taskID uint, reason string) error {
	// 1. 更新状态为cancelling
	result := tx.Model(&adminModel.Task{}).
		Where("id = ? AND status = ?", taskID, "running").
		Updates(map[string]interface{}{
			"status":        "cancelling",
			"cancel_reason": reason,
		})

	if result.RowsAffected == 0 {
		return fmt.Errorf("任务状态已变更，无法取消")
	}

	// 2. 发送取消信号并清理实例状态（异步处理，避免阻塞事务）
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if taskCtx, exists := s.contextManager.Get(taskID); exists {
			taskCtx.CancelFunc()
		}
		// 等待一小段时间让任务有机会正常退出
		time.Sleep(5 * time.Second)
		// 清理实例状态
		s.handleCancelledTaskCleanup(taskID)
	}()

	return nil
}

// forceStopRunningTask 强制停止running状态的任务
func (s *TaskService) forceStopRunningTask(tx *gorm.DB, taskID uint, reason string) error {
	return s.forceKillTask(tx, taskID, reason)
}

// forceKillTask 强制终止任务
func (s *TaskService) forceKillTask(tx *gorm.DB, taskID uint, reason string) error {
	now := time.Now()
	err := tx.Model(&adminModel.Task{}).Where("id = ?", taskID).Updates(map[string]interface{}{
		"status":        "cancelled",
		"cancel_reason": reason,
		"completed_at":  &now,
	}).Error

	if err != nil {
		return err
	}

	// 强制清理上下文和实例状态（异步处理）
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// 获取任务信息以便记录日志
		var task adminModel.Task
		if err := global.APP_DB.First(&task, taskID).Error; err == nil {
			if task.ProviderID != nil {
				global.APP_LOG.Debug("强制取消任务",
					zap.Uint("task_id", taskID),
					zap.Uint("provider_id", *task.ProviderID))
			}
		}

		// 取消运行中的context
		if taskCtx, exists := s.contextManager.Get(taskID); exists {
			taskCtx.CancelFunc()
			s.contextManager.Delete(taskID)
		}

		// 释放资源
		s.releaseTaskResources(taskID)

		// 清理实例状态
		s.handleCancelledTaskCleanup(taskID)
	}()

	return nil
}

// ForceStopTask 强制停止任务（管理员专用）
func (s *TaskService) ForceStopTask(taskID uint, reason string) error {
	if reason == "" {
		reason = "管理员强制停止"
	}
	return s.CancelTaskByAdmin(taskID, reason)
}

// handleCancelledTaskCleanup 处理被取消任务的清理工作
// 无论任务在什么状态被取消，都需要恢复实例状态，避免状态锁死
func (s *TaskService) handleCancelledTaskCleanup(taskID uint) {
	var task adminModel.Task
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		global.APP_LOG.Error("获取被取消任务信息失败", zap.Uint("taskId", taskID), zap.Error(err))
		return
	}

	global.APP_LOG.Info("开始清理被取消任务",
		zap.Uint("taskId", taskID),
		zap.String("taskType", task.TaskType),
		zap.Bool("wasRunning", task.StartedAt != nil))

	// 处理删除任务的清理
	if task.TaskType == "delete" && task.InstanceID != nil {
		global.APP_LOG.Info("开始清理被取消的删除任务的资源",
			zap.Uint("taskId", taskID),
			zap.Uint("instanceId", *task.InstanceID))

		// 解析任务数据
		var taskReq adminModel.DeleteInstanceTaskRequest
		if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
			global.APP_LOG.Error("解析删除任务数据失败", zap.Uint("taskId", taskID), zap.Error(err))
			return
		}

		// 获取实例信息
		var instance providerModel.Instance
		if err := global.APP_DB.First(&instance, *task.InstanceID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				global.APP_LOG.Error("获取实例信息失败", zap.Uint("instanceId", *task.InstanceID), zap.Error(err))
			}
			return
		}

		// 恢复实例状态（如果是deleting状态）
		if instance.Status == "deleting" {
			// 尝试恢复到之前的状态，如果无法确定则设为stopped
			newStatus := "stopped"
			if err := global.APP_DB.Model(&instance).Update("status", newStatus).Error; err != nil {
				global.APP_LOG.Error("恢复实例状态失败",
					zap.Uint("instanceId", instance.ID),
					zap.String("newStatus", newStatus),
					zap.Error(err))
			} else {
				global.APP_LOG.Info("已恢复被取消删除任务的实例状态",
					zap.Uint("instanceId", instance.ID),
					zap.String("status", newStatus))
			}
		}
	}

	// 处理重置任务的清理
	if task.TaskType == "reset" && task.InstanceID != nil {
		global.APP_LOG.Info("开始清理被取消的重置任务的资源",
			zap.Uint("taskId", taskID),
			zap.Uint("instanceId", *task.InstanceID))

		// 解析任务数据获取原始状态
		var taskData map[string]interface{}
		if err := json.Unmarshal([]byte(task.TaskData), &taskData); err != nil {
			global.APP_LOG.Error("解析重置任务数据失败", zap.Uint("taskId", taskID), zap.Error(err))
			return
		}

		// 获取实例信息
		var instance providerModel.Instance
		if err := global.APP_DB.First(&instance, *task.InstanceID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				global.APP_LOG.Error("获取实例信息失败", zap.Uint("instanceId", *task.InstanceID), zap.Error(err))
			}
			return
		}

		// 恢复实例状态（如果是resetting状态）
		if instance.Status == "resetting" {
			// 尝试从任务数据中获取原始状态
			originalStatus := "stopped"
			if origStatus, ok := taskData["originalStatus"].(string); ok && origStatus != "" {
				originalStatus = origStatus
			}

			if err := global.APP_DB.Model(&instance).Update("status", originalStatus).Error; err != nil {
				global.APP_LOG.Error("恢复实例状态失败",
					zap.Uint("instanceId", instance.ID),
					zap.String("newStatus", originalStatus),
					zap.Error(err))
			} else {
				global.APP_LOG.Info("已恢复被取消重置任务的实例状态",
					zap.Uint("instanceId", instance.ID),
					zap.String("status", originalStatus))
			}
		}
	}

	// 处理其他操作任务（start、stop、restart）的清理
	if (task.TaskType == "start" || task.TaskType == "stop" || task.TaskType == "restart") && task.InstanceID != nil {
		// 获取实例信息
		var instance providerModel.Instance
		if err := global.APP_DB.First(&instance, *task.InstanceID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				global.APP_LOG.Error("获取实例信息失败", zap.Uint("instanceId", *task.InstanceID), zap.Error(err))
			}
			return
		}

		// 根据任务类型和当前状态恢复实例状态
		shouldRevert := false
		var originalStatus string

		switch task.TaskType {
		case "start":
			if instance.Status == "starting" {
				originalStatus = "stopped"
				shouldRevert = true
			}
		case "stop":
			if instance.Status == "stopping" {
				originalStatus = "running"
				shouldRevert = true
			}
		case "restart":
			if instance.Status == "restarting" {
				originalStatus = "running"
				shouldRevert = true
			}
		}

		if shouldRevert {
			if err := global.APP_DB.Model(&instance).Update("status", originalStatus).Error; err != nil {
				global.APP_LOG.Error("恢复实例状态失败",
					zap.Uint("instanceId", instance.ID),
					zap.String("taskType", task.TaskType),
					zap.String("newStatus", originalStatus),
					zap.Error(err))
			} else {
				global.APP_LOG.Info("已恢复被取消任务的实例状态",
					zap.Uint("instanceId", instance.ID),
					zap.String("taskType", task.TaskType),
					zap.String("status", originalStatus))
			}
		}
	}
}

// releaseTaskResources 释放任务资源
func (s *TaskService) releaseTaskResources(taskID uint) {
	// 获取任务信息以提取sessionId
	var task adminModel.Task
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		global.APP_LOG.Error("获取任务信息失败", zap.Uint("taskId", taskID), zap.Error(err))
		return
	}

	// 解析任务数据以获取sessionId
	var taskData map[string]interface{}
	if err := json.Unmarshal([]byte(task.TaskData), &taskData); err != nil {
		global.APP_LOG.Error("解析任务数据失败", zap.Uint("taskId", taskID), zap.Error(err))
		return
	}

	sessionID, ok := taskData["sessionId"].(string)
	if !ok || sessionID == "" {
		global.APP_LOG.Warn("任务数据中没有sessionId", zap.Uint("taskId", taskID))
		return
	}

	// 释放预留资源
	reservationService := resources.GetResourceReservationService()
	if err := reservationService.ReleaseReservationBySession(sessionID); err != nil {
		global.APP_LOG.Warn("释放预留资源失败",
			zap.Uint("taskId", taskID),
			zap.String("sessionId", sessionID),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("任务预留资源已释放",
			zap.Uint("taskId", taskID),
			zap.String("sessionId", sessionID))
	}
}
