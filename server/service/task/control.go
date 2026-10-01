package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CompleteTask 完成任务
func (s *TaskService) CompleteTask(taskID uint, success bool, errorMessage string, resultData map[string]interface{}) error {
	now := time.Now()
	status := "completed"
	if !success {
		status = "failed"
	}
	var taskCtx *TaskContext
	hasTaskContext := false
	if s.contextManager != nil {
		taskCtx, hasTaskContext = s.contextManager.Get(taskID)
	}
	deadlineExceeded := hasTaskContext && errors.Is(taskCtx.Context.Err(), context.DeadlineExceeded)
	if deadlineExceeded {
		success = false
		status = mainTaskStatusTimeout
		if errorMessage == "" {
			errorMessage = "任务执行超时"
		}
	}

	var rowsAffected int64
	wasCancelled := false
	currentStatus := ""
	dbCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := s.dbService.ExecuteTransaction(dbCtx, func(tx *gorm.DB) error {
		// Retries must derive the whole transition from the newly locked row.
		rowsAffected, wasCancelled, currentStatus = 0, deadlineExceeded, ""
		var currentTask adminModel.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "status", "cancel_reason").First(&currentTask, taskID).Error; err != nil {
			return err
		}
		currentStatus = currentTask.Status
		if isMainTaskTerminalStatus(currentStatus) {
			return nil
		}
		updates := map[string]interface{}{"status": status, "completed_at": &now}
		if success {
			updates["progress"] = 100
		} else if errorMessage != "" {
			updates["error_message"] = errorMessage
		}
		if currentStatus == mainTaskStatusCancelling {
			wasCancelled = true
			if currentTask.CancelReason == taskTimeoutCancelReason || deadlineExceeded {
				updates["status"] = mainTaskStatusTimeout
				updates["error_message"] = taskTimeoutCancelReason
			} else {
				updates["status"] = mainTaskStatusCancelled
				delete(updates, "error_message")
			}
			if currentTask.CancelReason == "" {
				updates["cancel_reason"] = "任务已取消"
			}
		}
		if wasCancelled {
			delete(updates, "progress")
		}
		result := tx.Model(&adminModel.Task{}).
			Where("id = ? AND status = ?", taskID, currentStatus).Updates(updates)
		rowsAffected = result.RowsAffected
		return result.Error
	})

	if err != nil {
		global.APP_LOG.Error("完成任务失败",
			zap.Uint("taskId", taskID),
			zap.Error(err))
		return err
	}

	// RowsAffected == 0 说明任务已处于终态（幂等，不报错）
	if rowsAffected == 0 {
		global.APP_LOG.Debug("任务已处于终态，跳过重复更新",
			zap.Uint("taskId", taskID),
			zap.Bool("requestedSuccess", success))
		return nil
	}

	s.invalidateTaskInstanceCaches(taskID)

	if !wasCancelled && !success && errorMessage != "" {
		var currentTask adminModel.Task
		progress := 0
		if err := global.APP_DB.Select("progress").First(&currentTask, taskID).Error; err == nil {
			progress = currentTask.Progress
		}
		utils.AppendTaskError(taskID, progress, "step.taskFailedDetail", fmt.Errorf("%s", errorMessage))
	}

	// 若任务失败且无关联实例，释放预留资源
	if !success || wasCancelled {
		var task adminModel.Task
		if err := global.APP_DB.Select("instance_id").First(&task, taskID).Error; err == nil && task.InstanceID == nil {
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.releaseTaskResources(taskID)
			}()
		}
	}
	if wasCancelled {
		s.handleCancelledTaskCleanup(taskID)
	} else if !success {
		var failedTask adminModel.Task
		if err := global.APP_DB.First(&failedTask, taskID).Error; err == nil && (failedTask.TaskType == "delete" || failedTask.TaskType == "reset" || failedTask.TaskType == "rebuild") {
			s.reconcileDestructiveTask(failedTask)
		}
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

// ReleaseTaskLocks releases the task context retained for cancellation.
func (s *TaskService) ReleaseTaskLocks(taskID uint) {
	if s.contextManager != nil {
		s.contextManager.Delete(taskID)
	}
}

// CancelTask 用户取消任务
func (s *TaskService) CancelTask(taskID uint, userID uint) error {
	cleanup := cancellationCleanupNone
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		cleanup = cancellationCleanupNone
		var task adminModel.Task
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error
		if err != nil {
			return fmt.Errorf("任务不存在或无权限")
		}

		// 检查任务是否允许被用户取消
		if !task.IsForceStoppable {
			return fmt.Errorf("此任务不允许取消（管理员操作）")
		}

		switch task.Status {
		case "pending":
			if err := s.cancelPendingTask(tx, taskID, "用户取消"); err != nil {
				return err
			}
			cleanup = cancellationCleanupPending
			return nil
		case "processing":
			// `processing` means the task is claimed for a provider queue but
			// no worker context exists yet. Mark it terminal immediately so a
			// later worker cannot leave it stuck in `cancelling`.
			if err := s.cancelQueuedTask(tx, taskID, "用户取消"); err != nil {
				return err
			}
			cleanup = cancellationCleanupPending
			return nil
		case "running":
			if err := s.cancelRunningTask(tx, taskID, "用户取消"); err != nil {
				return err
			}
			cleanup = cancellationCleanupRunning
			return nil
		case "cancelling":
			cleanup = cancellationCleanupRunning
			return nil
		case "cancelled":
			return nil
		default:
			return fmt.Errorf("任务状态[%s]不允许取消", task.Status)
		}
	})
	if err != nil {
		return err
	}
	if cleanup == cancellationCleanupRunning {
		s.cancelTaskContext(taskID)
	}
	s.scheduleCancellationCleanup(taskID, cleanup)
	return nil
}

// CancelTaskByAdmin 管理员取消/强制停止任务
func (s *TaskService) CancelTaskByAdmin(taskID uint, reason string) error {
	return s.CancelTaskByAdminScoped(taskID, reason, 0)
}

// CancelTaskByAdminScoped 取消任务，ownerAdminID非零时限定任务必须属于该管理员的Provider。
func (s *TaskService) CancelTaskByAdminScoped(taskID uint, reason string, ownerAdminID uint) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cleanup := cancellationCleanupNone
	err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		cleanup = cancellationCleanupNone
		var task adminModel.Task
		query := tx.Where("tasks.id = ?", taskID)
		if ownerAdminID > 0 {
			providerIDs := tx.Model(&providerModel.Provider{}).
				Select("id").
				Where("owner_admin_id = ?", ownerAdminID)
			query = query.Where("tasks.provider_id IN (?)", providerIDs)
		}
		err := query.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task).Error
		if err != nil {
			return fmt.Errorf("任务不存在或无权限")
		}
		if task.Status == mainTaskStatusProcessing || task.Status == mainTaskStatusRunning || task.Status == mainTaskStatusCancelling {
			if !task.IsForceStoppable {
				return fmt.Errorf("此任务不允许强制停止")
			}
		}

		switch task.Status {
		case "pending":
			if err := s.cancelPendingTask(tx, taskID, fmt.Sprintf("管理员取消: %s", reason)); err != nil {
				return err
			}
			cleanup = cancellationCleanupPending
			return nil
		case "processing":
			if err := s.cancelQueuedTask(tx, taskID, fmt.Sprintf("管理员取消: %s", reason)); err != nil {
				return err
			}
			cleanup = cancellationCleanupPending
			return nil
		case "running":
			if err := s.cancelRunningTask(tx, taskID, fmt.Sprintf("管理员强制停止: %s", reason)); err != nil {
				return err
			}
			cleanup = cancellationCleanupRunning
			return nil
		case "cancelling":
			cleanup = cancellationCleanupRunning
			return nil
		case "cancelled":
			return nil
		default:
			return fmt.Errorf("参数错误: 任务状态[%s]不允许操作", task.Status)
		}
	})

	if err != nil {
		return err
	}
	if cleanup == cancellationCleanupRunning {
		s.cancelTaskContext(taskID)
	}
	s.scheduleCancellationCleanup(taskID, cleanup)
	return nil
}

func (s *TaskService) cancelTaskContext(taskID uint) {
	if s.contextManager == nil {
		return
	}
	if taskCtx, exists := s.contextManager.Get(taskID); exists && taskCtx.CancelFunc != nil {
		taskCtx.CancelFunc()
	}
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

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("任务状态已变更，无法取消")
	}

	return nil
}

// cancelQueuedTask cancels a task that has been claimed by the scheduler but
// has not entered a worker yet. No execution context exists for this state.
func (s *TaskService) cancelQueuedTask(tx *gorm.DB, taskID uint, reason string) error {
	now := time.Now()
	result := tx.Model(&adminModel.Task{}).
		Where("id = ? AND status = ?", taskID, mainTaskStatusProcessing).
		Updates(map[string]interface{}{
			"status":        mainTaskStatusCancelled,
			"cancel_reason": reason,
			"completed_at":  &now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("任务状态已变更，无法取消")
	}
	return nil
}

// cancelRunningTask 取消running状态的任务
func (s *TaskService) cancelRunningTask(tx *gorm.DB, taskID uint, reason string) error {
	// 1. 更新状态为cancelling
	result := tx.Model(&adminModel.Task{}).
		Where("id = ? AND status IN ?", taskID, []string{"running", "processing"}).
		Updates(map[string]interface{}{
			"status":        "cancelling",
			"cancel_reason": reason,
		})

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("任务状态已变更，无法取消")
	}

	return nil
}

// forceStopRunningTask 强制停止running状态的任务
type cancellationCleanup uint8

// cancellationGracePeriod bounds how long a worker gets to observe a running
// task cancellation before the cleanup path finalizes the task. It is a
// variable so focused lifecycle tests can use a short grace period without
// waiting five seconds for every cancellation.
var cancellationGracePeriod = 5 * time.Second

const (
	cancellationCleanupNone cancellationCleanup = iota
	cancellationCleanupPending
	cancellationCleanupRunning
)

func (s *TaskService) scheduleCancellationCleanup(taskID uint, cleanup cancellationCleanup) bool {
	if cleanup == cancellationCleanupNone {
		return false
	}
	marker := new(struct{})
	if _, alreadyScheduled := s.cancellationCleanup.LoadOrStore(taskID, marker); alreadyScheduled {
		return false
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.cancellationCleanup.Delete(taskID)
		switch cleanup {
		case cancellationCleanupPending:
			s.releaseTaskResources(taskID)
			s.handleCancelledTaskCleanup(taskID)
		case cancellationCleanupRunning:
			s.cancelTaskContext(taskID)
			// Give provider calls a short grace period. If the worker did not
			// return, keep the task and instance locks. Marking it terminal while
			// the provider call is still active would allow a conflicting task.
			time.Sleep(cancellationGracePeriod)
			var currentTask adminModel.Task
			if err := global.APP_DB.Select("status").First(&currentTask, taskID).Error; err != nil || currentTask.Status != mainTaskStatusCancelling {
				return
			}
			if s.contextManager != nil {
				if _, stillRunning := s.contextManager.Get(taskID); stillRunning {
					global.APP_LOG.Warn("取消仍在等待Provider操作退出，保留实例锁以避免并发操作",
						zap.Uint("taskId", taskID))
					return
				}
			}
			s.finalizeCancelledTask(taskID)
		}
	}()
	return true
}

func (s *TaskService) finalizeCancelledTask(taskID uint) {
	if s.contextManager != nil {
		s.contextManager.Delete(taskID)
	}

	var current adminModel.Task
	if err := global.APP_DB.Select("id", "cancel_reason").
		Where("id = ? AND status = ?", taskID, mainTaskStatusCancelling).
		First(&current).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			global.APP_LOG.Warn("读取取消任务终态信息失败", zap.Uint("taskId", taskID), zap.Error(err))
		}
		return
	}

	terminalStatus := mainTaskStatusCancelled
	if current.CancelReason == taskTimeoutCancelReason {
		terminalStatus = mainTaskStatusTimeout
	}
	now := time.Now()
	result := global.APP_DB.Model(&adminModel.Task{}).
		Where("id = ? AND status = ? AND cancel_reason = ?", taskID, mainTaskStatusCancelling, current.CancelReason).
		Updates(map[string]interface{}{"status": terminalStatus, "completed_at": &now})
	if result.Error != nil {
		global.APP_LOG.Warn("写入取消任务终态失败，将由超时回收继续处理", zap.Uint("taskId", taskID), zap.Error(result.Error))
		return
	}
	if result.RowsAffected == 0 {
		return
	}

	s.releaseTaskResources(taskID)
	s.handleCancelledTaskCleanup(taskID)
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
}

// ForceStopTask 强制停止任务（管理员专用）
func (s *TaskService) ForceStopTask(taskID uint, reason string) error {
	return s.ForceStopTaskScoped(taskID, reason, 0)
}

// ForceStopTaskScoped 强制停止任务，ownerAdminID非零时应用Provider归属隔离。
func (s *TaskService) ForceStopTaskScoped(taskID uint, reason string, ownerAdminID uint) error {
	if reason == "" {
		reason = "管理员强制停止"
	}
	return s.CancelTaskByAdminScoped(taskID, reason, ownerAdminID)
}

// handleCancelledTaskCleanup 处理被取消任务的清理工作
// 无论任务在什么状态被取消，都需要恢复实例状态，避免状态锁死
func (s *TaskService) handleCancelledTaskCleanup(taskID uint) {
	defer s.invalidateTaskInstanceCaches(taskID)

	var task adminModel.Task
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		global.APP_LOG.Error("获取被取消任务信息失败", zap.Uint("taskId", taskID), zap.Error(err))
		return
	}

	global.APP_LOG.Debug("开始清理被取消任务",
		zap.Uint("taskId", taskID),
		zap.String("taskType", task.TaskType),
		zap.Bool("wasRunning", task.StartedAt != nil))

	// A create task can already have an instance row when it is cancelled.
	// Leaving that row in `creating` permanently blocks deletion and quota
	// recovery. Keep the resource accounting until the normal delete task runs,
	// but move only the still-owned provisioning row to `error`; a late provider
	// completion must not overwrite a terminal state or make the instance appear
	// successfully created.
	if isCreateTaskType(task.TaskType) && task.InstanceID != nil {
		result := global.APP_DB.Model(&providerModel.Instance{}).
			Where("id = ? AND status = ?", *task.InstanceID, constant.InstanceStatusCreating).
			Updates(map[string]interface{}{
				"status":        constant.InstanceStatusError,
				"desired_state": providerModel.InstanceDesiredStateStopped,
			})
		if result.Error != nil {
			global.APP_LOG.Warn("恢复被取消创建实例状态失败",
				zap.Uint("taskId", taskID),
				zap.Uint("instanceId", *task.InstanceID),
				zap.Error(result.Error))
		} else if result.RowsAffected > 0 {
			global.APP_LOG.Info("已将被取消创建实例标记为error，等待正常删除回收远端资源",
				zap.Uint("taskId", taskID),
				zap.Uint("instanceId", *task.InstanceID))
		}
	}

	if task.TaskType == "delete" || task.TaskType == "reset" || task.TaskType == "rebuild" {
		s.reconcileDestructiveTask(task)
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
		originalStatus, restoredDesiredState, ok := cancelledOperationRestore(task)
		shouldRevert := ok && instance.Status == cancelledOperationTransitionStatus(task.TaskType)

		if shouldRevert {
			result := global.APP_DB.Model(&providerModel.Instance{}).
				Where("id = ? AND status = ?", instance.ID, cancelledOperationTransitionStatus(task.TaskType)).
				Updates(map[string]interface{}{
					"status":        originalStatus,
					"desired_state": restoredDesiredState,
				})
			if result.Error != nil {
				global.APP_LOG.Error("恢复实例状态失败",
					zap.Uint("instanceId", instance.ID),
					zap.String("taskType", task.TaskType),
					zap.String("newStatus", originalStatus),
					zap.Error(result.Error))
			} else {
				global.APP_LOG.Debug("已恢复被取消任务的实例状态",
					zap.Uint("instanceId", instance.ID),
					zap.String("taskType", task.TaskType),
					zap.String("status", originalStatus))
			}
		}
	}
}

func isCreateTaskType(taskType string) bool {
	switch taskType {
	case "create", "create_instance", "create_redemption_instance":
		return true
	default:
		return false
	}
}

// cancelledOperationRestore returns the controller state that corresponds to
// an explicitly cancelled lifecycle operation. Internal start tasks are
// different from user starts: they are queued to restore a previously running
// instance, so cancellation must keep desired_state=running for a later retry.
func cancelledOperationRestore(task adminModel.Task) (status, desiredState string, ok bool) {
	switch task.TaskType {
	case "start":
		if cancelledStartPreservesRunningIntent(task) {
			return "stopped", providerModel.InstanceDesiredStateRunning, true
		}
		return "stopped", providerModel.InstanceDesiredStateStopped, true
	case "stop", "restart":
		return "running", providerModel.InstanceDesiredStateRunning, true
	default:
		return "", "", false
	}
}

// cancelledStartPreservesRunningIntent identifies system-generated start
// tasks without adding another database read to cancellation cleanup. The
// explicit task-data marker is used for newly queued tasks; the exact legacy
// status messages keep already persisted traffic/check-in tasks compatible.
func cancelledStartPreservesRunningIntent(task adminModel.Task) bool {
	var taskData struct {
		Recovery     bool   `json:"recovery"`
		DesiredState string `json:"desiredState"`
	}
	if err := json.Unmarshal([]byte(task.TaskData), &taskData); err == nil {
		if taskData.Recovery || taskData.DesiredState == providerModel.InstanceDesiredStateRunning {
			return true
		}
	}

	switch task.StatusMessage {
	case "流量限制已解除，自动恢复因流量策略停机的实例", "签到续期后自动启动实例":
		return true
	default:
		return false
	}
}

func cancelledOperationTransitionStatus(taskType string) string {
	switch taskType {
	case "start":
		return "starting"
	case "stop":
		return "stopping"
	case "restart":
		return "restarting"
	default:
		return ""
	}
}

// releaseTaskResources 释放任务资源（包括待确认配额）
func (s *TaskService) releaseTaskResources(taskID uint) {
	// 获取任务信息
	var task adminModel.Task
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		global.APP_LOG.Error("获取任务信息失败", zap.Uint("taskId", taskID), zap.Error(err))
		return
	}

	// 解析任务数据
	var taskData map[string]interface{}
	if err := json.Unmarshal([]byte(task.TaskData), &taskData); err != nil {
		global.APP_LOG.Error("解析任务数据失败", zap.Uint("taskId", taskID), zap.Error(err))
		return
	}

	// 1. 释放预留资源（Provider资源）
	sessionID, ok := taskData["sessionId"].(string)
	if ok && sessionID != "" {
		reservationService := resources.GetResourceReservationService()
		if err := reservationService.ReleaseReservationBySession(sessionID); err != nil {
			global.APP_LOG.Warn("释放预留资源失败",
				zap.Uint("taskId", taskID),
				zap.String("sessionId", sessionID),
				zap.Error(err))
		} else {
			global.APP_LOG.Debug("任务预留资源已释放",
				zap.Uint("taskId", taskID),
				zap.String("sessionId", sessionID))
		}
	}

	// 2. 释放待确认配额（用户配额）
	// 对于创建任务，如果实例没有创建成功，需要释放已分配的待确认配额
	if (task.TaskType == "create" || task.TaskType == "create_instance" || task.TaskType == "create_redemption_instance") && task.InstanceID == nil {
		// 从 taskData 中提取资源信息
		cpu, cpuOk := taskData["cpu"].(float64)
		memory, memOk := taskData["memory"].(float64)
		disk, diskOk := taskData["disk"].(float64)
		bandwidth, bwOk := taskData["bandwidth"].(float64)

		if cpuOk && memOk && diskOk && bwOk {
			resourceUsage := resources.ResourceUsage{
				CPU:       int(cpu),
				Memory:    int64(memory),
				Disk:      int64(disk),
				Bandwidth: int(bandwidth),
			}

			quotaService := resources.NewQuotaService()
			err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
				return quotaService.ReleasePendingQuota(tx, task.UserID, resourceUsage)
			})

			if err != nil {
				global.APP_LOG.Warn("释放待确认配额失败",
					zap.Uint("taskId", taskID),
					zap.Uint("userId", task.UserID),
					zap.Error(err))
			} else {
				global.APP_LOG.Debug("任务待确认配额已释放",
					zap.Uint("taskId", taskID),
					zap.Uint("userId", task.UserID))
			}
		}
	}
}
