package task

import (
	"context"
	"encoding/json"
	"fmt"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/resources"
	"time"
)

func waitTaskContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

// Persist intent before destructive I/O. A crash or cancellation during that
// I/O leaves an unknown remote state, never a restored "running" instance.
func (s *TaskService) recordLifecyclePhase(ctx context.Context, taskID uint, phase string) error {
	return s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		var task adminModel.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, taskID).Error; err != nil {
			return err
		}
		if task.Status != mainTaskStatusRunning {
			return fmt.Errorf("任务已停止，不能继续操作")
		}
		data := map[string]interface{}{}
		if err := json.Unmarshal([]byte(task.TaskData), &data); err != nil {
			return err
		}
		data["lifecyclePhase"] = phase
		encoded, err := json.Marshal(data)
		if err != nil {
			return err
		}
		return tx.Model(&task).Update("task_data", string(encoded)).Error
	})
}

// Retain resource/port metadata after failed destruction so deletion can be
// retried. "error" deliberately does not claim the remote guest still exists.
func (s *TaskService) reconcileDestructiveTask(task adminModel.Task) {
	if task.InstanceID == nil {
		return
	}
	var data struct {
		OriginalStatus       string `json:"originalStatus"`
		OriginalDesiredState string `json:"originalDesiredState"`
		Phase                string `json:"lifecyclePhase"`
	}
	_ = json.Unmarshal([]byte(task.TaskData), &data)
	status, desired := constant.InstanceStatusError, providerModel.InstanceDesiredStateStopped
	// The destructive boundary is persisted immediately before the first
	// provider mutation. If no phase was recorded, preflight failed before any
	// remote state could change, so the reservation can safely be restored.
	if data.Phase == "" && (data.OriginalStatus == "running" || data.OriginalStatus == "stopped") {
		status = data.OriginalStatus
		desired = data.OriginalDesiredState
		if desired == "" {
			desired = status
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := global.APP_DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var instance providerModel.Instance
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&instance, *task.InstanceID).Error; err != nil {
			return err
		}
		var newer int64
		if err := tx.Model(&adminModel.Task{}).Where("instance_id = ? AND id > ?", instance.ID, task.ID).Count(&newer).Error; err != nil {
			return err
		}
		if newer > 0 {
			return nil
		}
		return tx.Model(&instance).Where("status IN ?", []string{"deleting", "resetting", "rebuilding", "creating"}).Updates(map[string]interface{}{"status": status, "desired_state": desired}).Error
	})
	if err == nil && task.UserID > 0 && data.Phase == "replacement_created" {
		if quotaErr := resources.NewQuotaService().RecalculateUserQuota(task.UserID); quotaErr != nil {
			global.APP_LOG.Error("重建失败后重算配额失败", zap.Uint("taskId", task.ID), zap.Error(quotaErr))
		}
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		global.APP_LOG.Error("恢复实例操作状态失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}
}
