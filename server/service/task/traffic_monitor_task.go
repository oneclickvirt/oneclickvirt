package task

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	trafficMonitorService "oneclickvirt/service/admin/traffic_monitor"
	"oneclickvirt/service/taskgate"
	"oneclickvirt/utils"
)

type trafficMonitorAdminTaskData struct {
	TrafficMonitorTaskID uint   `json:"trafficMonitorTaskId"`
	ProviderID           uint   `json:"providerId"`
	Operation            string `json:"operation"`
}

var trafficMonitorCreateMu sync.Mutex

// CreateTrafficMonitorTask rejects overlapping enable/disable/detect operations
// for a provider before creating either task row. The database check also
// catches unfinished tasks retained across controller restarts.
func CreateTrafficMonitorTask(providerID uint, operation string, userID uint) (*adminModel.TrafficMonitorTask, *adminModel.Task, error) {
	if err := taskgate.EnsureAccepting(); err != nil {
		return nil, nil, err
	}
	trafficMonitorCreateMu.Lock()
	defer trafficMonitorCreateMu.Unlock()

	if providerID == 0 {
		return nil, nil, common.NewError(common.CodeValidationError, "Provider ID无效")
	}
	var trafficTaskType string
	switch operation {
	case "enable":
		trafficTaskType = "enable_all"
	case "disable":
		trafficTaskType = "disable_all"
	case "detect":
		trafficTaskType = "detect_all"
	default:
		return nil, nil, common.NewError(common.CodeValidationError, "不支持的流量监控操作")
	}

	var activeCount int64
	if err := global.APP_DB.Model(&adminModel.Task{}).
		Where("provider_id = ? AND task_type IN ? AND status IN ?", providerID,
			[]string{"traffic-monitor-enable", "traffic-monitor-disable", "traffic-monitor-detect"},
			[]string{"pending", "processing", "running", "cancelling"}).
		Count(&activeCount).Error; err != nil {
		return nil, nil, fmt.Errorf("检查流量监控后台任务失败: %w", err)
	}
	if activeCount > 0 {
		return nil, nil, common.NewError(common.CodeConflict, "该节点已有流量监控后台任务，请在任务列表查看进度")
	}

	trafficTask := &adminModel.TrafficMonitorTask{
		ProviderID: providerID,
		TaskType:   trafficTaskType,
		Status:     "pending",
		Progress:   0,
		Message:    "任务已创建，等待执行",
	}
	if err := global.APP_DB.Create(trafficTask).Error; err != nil {
		return nil, nil, err
	}
	created, err := createTrafficMonitorAdminTask(providerID, trafficTask.ID, operation, userID)
	if err != nil {
		_ = global.APP_DB.Model(trafficTask).Updates(map[string]interface{}{"status": "failed", "message": err.Error()}).Error
		return nil, nil, err
	}
	_ = global.APP_DB.Model(trafficTask).Update("admin_task_id", created.ID).Error
	return trafficTask, created, nil
}

func createTrafficMonitorAdminTask(providerID uint, trafficTaskID uint, operation string, userID uint) (*adminModel.Task, error) {

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
