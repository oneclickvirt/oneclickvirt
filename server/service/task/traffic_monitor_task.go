package task

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	trafficMonitorService "oneclickvirt/service/admin/traffic_monitor"
	"oneclickvirt/utils"
)

type trafficMonitorAdminTaskData struct {
	TrafficMonitorTaskID uint   `json:"trafficMonitorTaskId"`
	ProviderID           uint   `json:"providerId"`
	Operation            string `json:"operation"`
}

func CreateTrafficMonitorAdminTask(providerID uint, trafficTaskID uint, operation string, userID uint) (*adminModel.Task, error) {
	taskType := "traffic-monitor-" + operation
	data, _ := json.Marshal(trafficMonitorAdminTaskData{
		TrafficMonitorTaskID: trafficTaskID,
		ProviderID:           providerID,
		Operation:            operation,
	})
	task := &adminModel.Task{
		UserID:            userID,
		ProviderID:        &providerID,
		TaskType:          taskType,
		Status:            "pending",
		TaskData:          string(data),
		TimeoutDuration:   1800,
		EstimatedDuration: 300,
		CanForceStop:      true,
		IsForceStoppable:  true,
		StatusMessage:     "trafficMonitor.pending",
	}
	if err := global.APP_DB.Create(task).Error; err != nil {
		return nil, err
	}
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return task, nil
}

func (s *TaskService) executeTrafficMonitorTask(ctx context.Context, task *adminModel.Task) error {
	var data trafficMonitorAdminTaskData
	if err := json.Unmarshal([]byte(task.TaskData), &data); err != nil {
		return fmt.Errorf("解析流量监控任务数据失败: %w", err)
	}
	if data.ProviderID == 0 && task.ProviderID != nil {
		data.ProviderID = *task.ProviderID
	}
	if data.ProviderID == 0 || data.TrafficMonitorTaskID == 0 || data.Operation == "" {
		return fmt.Errorf("流量监控任务缺少providerId、trafficMonitorTaskId或operation")
	}

	utils.UpdateTaskProgress(task.ID, 5, "trafficMonitor.taskStarted")
	_ = global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).
		Where("id = ?", data.TrafficMonitorTaskID).
		Update("admin_task_id", task.ID).Error

	done := make(chan struct{})
	go mirrorTrafficMonitorTaskProgress(ctx, task.ID, data.TrafficMonitorTaskID, done)
	defer close(done)

	manager := trafficMonitorService.GetManager()
	var err error
	switch data.Operation {
	case "enable":
		err = manager.BatchEnableMonitoring(ctx, data.ProviderID, data.TrafficMonitorTaskID)
	case "disable":
		err = manager.BatchDisableMonitoring(ctx, data.ProviderID, data.TrafficMonitorTaskID)
	case "detect":
		err = manager.BatchDetectMonitoring(ctx, data.ProviderID, data.TrafficMonitorTaskID)
	default:
		err = fmt.Errorf("不支持的流量监控操作: %s", data.Operation)
	}
	if err != nil {
		utils.UpdateTaskProgress(task.ID, 95, "trafficMonitor.taskFailed")
		return err
	}

	var trafficTask adminModel.TrafficMonitorTask
	if err := global.APP_DB.First(&trafficTask, data.TrafficMonitorTaskID).Error; err == nil {
		if trafficTask.Status == "failed" {
			return fmt.Errorf("%s", trafficTask.ErrorMsg)
		}
		if trafficTask.Progress > 0 {
			utils.UpdateTaskProgress(task.ID, trafficTask.Progress, trafficTask.Message)
		}
	}
	utils.UpdateTaskProgress(task.ID, 100, "trafficMonitor.taskCompleted")
	return nil
}

func mirrorTrafficMonitorTaskProgress(ctx context.Context, adminTaskID uint, trafficTaskID uint, done <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			var trafficTask adminModel.TrafficMonitorTask
			if err := global.APP_DB.Select("progress", "message", "status", "error_msg").First(&trafficTask, trafficTaskID).Error; err != nil {
				continue
			}
			progress := trafficTask.Progress
			if progress <= 0 {
				progress = 10
			}
			if progress >= 100 && trafficTask.Status != "completed" {
				progress = 99
			}
			utils.UpdateTaskProgress(adminTaskID, progress, trafficTask.Message)
			if trafficTask.Status == "failed" && trafficTask.ErrorMsg != "" {
				utils.AppendTaskError(adminTaskID, progress, "trafficMonitor.taskFailed", fmt.Errorf("%s", trafficTask.ErrorMsg))
			}
		}
	}
}
