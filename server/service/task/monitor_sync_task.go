package task

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	agentService "oneclickvirt/service/agent"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	adminModel "oneclickvirt/model/admin"

	"go.uber.org/zap"
)

type monitorSyncAdminTaskData struct {
	MonitorSyncTaskID uint `json:"monitorSyncTaskId"`
	ProviderID        uint `json:"providerId"`
}

func (s *TaskService) executeMonitorSyncTask(ctx context.Context, task *adminModel.Task) error {
	var data monitorSyncAdminTaskData
	if err := json.Unmarshal([]byte(task.TaskData), &data); err != nil {
		return fmt.Errorf("解析监控同步任务数据失败: %w", err)
	}
	if data.ProviderID == 0 && task.ProviderID != nil {
		data.ProviderID = *task.ProviderID
	}
	if data.ProviderID == 0 || data.MonitorSyncTaskID == 0 {
		return fmt.Errorf("监控同步任务数据缺少providerId或monitorSyncTaskId")
	}

	utils.UpdateTaskProgress(task.ID, 5, "monitorSync.taskStarted")
	now := time.Now()
	_ = global.APP_DB.Model(&monitoringModel.MonitorSyncTask{}).
		Where("id = ?", data.MonitorSyncTaskID).
		Updates(map[string]interface{}{"status": "running", "started_at": &now, "admin_task_id": task.ID}).Error

	finish := func(status string, summary *agentService.MonitorSyncSummary, taskErr error) error {
		if summary == nil {
			summary = &agentService.MonitorSyncSummary{}
		}
		finished := time.Now()
		errorMessage := ""
		if taskErr != nil {
			errorMessage = taskErr.Error()
			if len(summary.Errors) == 0 || summary.Errors[len(summary.Errors)-1] != errorMessage {
				summary.Errors = append(summary.Errors, errorMessage)
			}
		}
		errorsJSON, _ := json.Marshal(summary.Errors)
		updates := map[string]interface{}{
			"status":        status,
			"total":         summary.Total,
			"created":       summary.Created,
			"updated":       summary.Updated,
			"unchanged":     summary.Unchanged,
			"failed":        summary.Failed,
			"cleaned":       summary.Cleaned,
			"error_message": errorMessage,
			"errors_json":   string(errorsJSON),
			"finished_at":   &finished,
		}
		if err := global.APP_DB.Model(&monitoringModel.MonitorSyncTask{}).
			Where("id = ?", data.MonitorSyncTaskID).
			Updates(updates).Error; err != nil && global.APP_LOG != nil {
			global.APP_LOG.Warn("update monitor sync task failed", zap.Uint("monitor_sync_task_id", data.MonitorSyncTaskID), zap.Error(err))
		}
		if taskErr != nil {
			utils.UpdateTaskProgress(task.ID, 95, "monitorSync.taskFailed")
			return taskErr
		}
		utils.UpdateTaskProgress(task.ID, 100, "monitorSync.taskCompleted")
		return nil
	}

	var config monitoringModel.MonitoringConfig
	if err := global.APP_DB.Where("provider_id = ?", data.ProviderID).First(&config).Error; err != nil {
		return finish("failed", &agentService.MonitorSyncSummary{Failed: 1}, fmt.Errorf("读取监控配置失败: %w", err))
	}
	providerInstance, err := providerService.GetProviderInstanceByID(data.ProviderID)
	if err != nil {
		return finish("failed", &agentService.MonitorSyncSummary{Failed: 1}, fmt.Errorf("Provider未连接: %w", err))
	}

	utils.UpdateTaskProgress(task.ID, 20, "monitorSync.ensureMonitors")
	monitorSvc := agentService.NewMonitorService(ctx, global.APP_DB)
	summary, err := monitorSvc.EnsureMonitorsForProvider(providerInstance, data.ProviderID, &config)
	if err != nil {
		return finish("failed", summary, err)
	}

	utils.UpdateTaskProgress(task.ID, 80, "monitorSync.cleanupStale")
	cleaned, cleanupErr := monitorSvc.CleanupStaleMonitors(data.ProviderID, &config)
	if cleanupErr != nil {
		summary.Errors = append(summary.Errors, "cleanup: "+cleanupErr.Error())
		summary.Failed++
	} else {
		summary.Cleaned = cleaned
	}

	if cleanupErr != nil {
		return finish("failed", summary, cleanupErr)
	}
	return finish("success", summary, nil)
}

// CreateMonitorSyncAdminTask creates a user-visible task-list task for a monitor
// sync operation. The actual execution goes through the provider worker pool, so
// user/admin initiated long operations respect Provider.AllowConcurrentTasks and
// Provider.MaxConcurrentTasks. Background collectors must not call this helper.
func CreateMonitorSyncAdminTask(providerID uint, monitorSyncTaskID uint, userID uint) (*adminModel.Task, error) {
	data, _ := json.Marshal(monitorSyncAdminTaskData{
		MonitorSyncTaskID: monitorSyncTaskID,
		ProviderID:        providerID,
	})
	task := &adminModel.Task{
		UserID:            userID,
		ProviderID:        &providerID,
		TaskType:          "monitor-sync",
		Status:            "pending",
		TaskData:          string(data),
		TimeoutDuration:   1800,
		EstimatedDuration: 300,
		IsForceStoppable:  true,
		StatusMessage:     "monitorSync.pending",
	}
	if err := global.APP_DB.Create(task).Error; err != nil {
		return nil, err
	}
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return task, nil
}
