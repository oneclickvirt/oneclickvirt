package task

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	snapshotSvc "oneclickvirt/service/snapshot"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	if global.APP_DB == nil {
		return 0, 0
	}
	parentCtx := context.Background()
	if s.ctx != nil {
		parentCtx = s.ctx
	}
	cleanupCtx, cancel := context.WithTimeout(parentCtx, 20*time.Second)
	defer cancel()
	const cleanupBatchSize = 500

	// Recover a failed dequeue/requeue write once the database is available.
	// Live queued tasks may legitimately wait a long time and must stay claimed.
	var orphanCandidates []adminModel.Task
	if err := global.APP_DB.WithContext(cleanupCtx).
		Where("status = ? AND updated_at < ?", mainTaskStatusProcessing, time.Now().Add(-time.Minute)).
		Order("updated_at ASC, id ASC").Limit(cleanupBatchSize).Find(&orphanCandidates).Error; err == nil {
		orphanIDs := make([]uint, 0, len(orphanCandidates))
		for _, task := range orphanCandidates {
			if _, queued := s.queuedTasks.Load(task.ID); queued {
				continue
			}
			if s.contextManager != nil {
				if _, running := s.contextManager.Get(task.ID); running {
					continue
				}
			}
			orphanIDs = append(orphanIDs, task.ID)
		}
		if len(orphanIDs) > 0 {
			if err := global.APP_DB.WithContext(cleanupCtx).Model(&adminModel.Task{}).
				Where("id IN ? AND status = ?", orphanIDs, mainTaskStatusProcessing).
				Update("status", mainTaskStatusPending).Error; err != nil {
				global.APP_LOG.Warn("批量恢复未执行任务失败", zap.Error(err))
			}
		}
	}
	var runningTasks []adminModel.Task
	var cleanupTasks []adminModel.Task
	var timedOut int64
	var staleCancelling int64

	// 只清理真正已经开始执行的 running 任务。processing 表示已入 provider 队列但
	// 可能仍在等待 worker，等待时间不能计入任务执行超时。
	if err := global.APP_DB.WithContext(cleanupCtx).Where("status = ?", mainTaskStatusRunning).
		Order("started_at ASC, id ASC").Limit(cleanupBatchSize).Find(&runningTasks).Error; err != nil {
		return 0, 0
	}

	now := time.Now()
	var contextTimeoutIDs []uint
	var terminalTimeoutIDs []uint
	for _, task := range runningTasks {
		timeoutSeconds := task.TimeoutDuration
		if timeoutSeconds <= 0 {
			timeoutSeconds = s.getDefaultTimeout(task.TaskType)
		}
		startAt := task.UpdatedAt
		if task.StartedAt != nil {
			startAt = *task.StartedAt
		}
		if !startAt.Add(time.Duration(timeoutSeconds) * time.Second).Before(now) {
			continue
		}

		if s.contextManager != nil {
			if _, exists := s.contextManager.Get(task.ID); exists {
				// Keep the task active until its worker confirms the provider call has
				// returned. A terminal row would let a conflicting operation start.
				contextTimeoutIDs = append(contextTimeoutIDs, task.ID)
				continue
			}
		}
		terminalTimeoutIDs = append(terminalTimeoutIDs, task.ID)
	}
	if len(contextTimeoutIDs) > 0 {
		transitioned, err := transitionTaskRows(cleanupCtx, contextTimeoutIDs, mainTaskStatusRunning, map[string]interface{}{
			"status":        mainTaskStatusCancelling,
			"cancel_reason": taskTimeoutCancelReason,
		})
		if err != nil {
			global.APP_LOG.Warn("批量标记超时任务为取消中失败", zap.Error(err))
		} else {
			timedOut += int64(len(transitioned))
			// Cancel only contexts whose row was still running when the CAS ran.
			// A task already moved by a user request remains governed by that
			// request, while an acknowledged timeout still stops its provider call.
			for _, task := range transitioned {
				taskID := task.ID
				if taskCtx, exists := s.contextManager.Get(taskID); exists {
					taskCtx.CancelFunc()
				}
			}
		}
	}
	if len(terminalTimeoutIDs) > 0 {
		transitioned, err := transitionTaskRows(cleanupCtx, terminalTimeoutIDs, mainTaskStatusRunning, map[string]interface{}{
			"status":        mainTaskStatusTimeout,
			"cancel_reason": taskTimeoutCancelReason,
			"completed_at":  &now,
		})
		if err != nil {
			global.APP_LOG.Warn("批量标记超时任务为终态失败", zap.Error(err))
		} else {
			timedOut += int64(len(transitioned))
			cleanupTasks = append(cleanupTasks, transitioned...)
		}
	}

	var cancellingTasks []adminModel.Task
	if err := global.APP_DB.WithContext(cleanupCtx).
		Where("status = ? AND updated_at < ?", mainTaskStatusCancelling, timeoutThreshold).
		Order("updated_at ASC, id ASC").Limit(cleanupBatchSize).Find(&cancellingTasks).Error; err == nil {
		var staleTimeoutIDs []uint
		var staleCancelledIDs []uint
		for _, task := range cancellingTasks {
			if s.contextManager != nil {
				if taskCtx, exists := s.contextManager.Get(task.ID); exists {
					// A context can be cancelled while its provider call is still active.
					taskCtx.CancelFunc()
					continue
				}
			}

			if task.CancelReason == taskTimeoutCancelReason {
				staleTimeoutIDs = append(staleTimeoutIDs, task.ID)
			} else {
				staleCancelledIDs = append(staleCancelledIDs, task.ID)
			}
		}
		for _, group := range []struct {
			ids    []uint
			status string
		}{
			{staleTimeoutIDs, mainTaskStatusTimeout},
			{staleCancelledIDs, mainTaskStatusCancelled},
		} {
			if len(group.ids) == 0 {
				continue
			}
			transitioned, err := transitionTaskRows(cleanupCtx, group.ids, mainTaskStatusCancelling, map[string]interface{}{
				"status":       group.status,
				"completed_at": &now,
			})
			if err != nil {
				global.APP_LOG.Warn("批量完成超时取消任务失败", zap.String("status", group.status), zap.Error(err))
				continue
			}
			staleCancelling += int64(len(transitioned))
			cleanupTasks = append(cleanupTasks, transitioned...)
		}
	}

	if len(cleanupTasks) > 0 {
		s.wg.Add(1)
		go func(tasks []adminModel.Task) {
			defer s.wg.Done()
			for _, task := range tasks {
				s.releaseTaskResources(task.ID)
				s.handleCancelledTaskCleanup(task.ID)
			}
		}(cleanupTasks)
	}

	return timedOut, staleCancelling
}

// transitionTaskRows locks the candidate rows, rechecks their source state,
// and returns exactly the rows changed by the update. Keeping the lock only
// around this short read/update transaction prevents resource cleanup from
// racing a user cancellation or worker completion.
func transitionTaskRows(ctx context.Context, ids []uint, sourceStatus string, updates map[string]interface{}) ([]adminModel.Task, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var transitioned []adminModel.Task
	err := global.APP_DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidates []adminModel.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id IN ? AND status = ?", ids, sourceStatus).
			Find(&candidates).Error; err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}
		result := tx.Model(&adminModel.Task{}).
			Where("id IN ? AND status = ?", ids, sourceStatus).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(candidates)) {
			return fmt.Errorf("任务状态转换数量不一致：读取 %d，更新 %d", len(candidates), result.RowsAffected)
		}
		transitioned = candidates
		return nil
	})
	return transitioned, err
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
	case "repair-port-mappings":
		return s.executeRepairPortMappingsTask(ctx, task)
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
		return executeExternalTaskHandler(ctx, task)
	}
}
