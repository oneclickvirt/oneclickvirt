package task

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	dashboardModel "oneclickvirt/model/dashboard"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CreateTask 创建任务
func (s *TaskService) CreateTask(userID uint, providerID *uint, instanceID *uint, taskType string, taskData string, timeoutDuration int) (*adminModel.Task, error) {
	if timeoutDuration <= 0 {
		timeoutDuration = s.getDefaultTimeout(taskType)
	}

	task := &adminModel.Task{
		UserID:           userID,
		ProviderID:       providerID,
		InstanceID:       instanceID,
		TaskType:         taskType,
		Status:           "pending",
		TaskData:         taskData,
		TimeoutDuration:  timeoutDuration,
		IsForceStoppable: true,
	}

	err := s.dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(task).Error
	})

	if err != nil {
		return nil, fmt.Errorf("创建任务失败: %v", err)
	}

	global.APP_LOG.Info("任务创建成功",
		zap.Uint("taskId", task.ID),
		zap.String("taskType", taskType),
		zap.Uint("userId", userID))

	return task, nil
}

// GetUserTasks 获取用户任务列表
func (s *TaskService) GetUserTasks(userID uint, req userModel.UserTasksRequest) ([]userModel.TaskResponse, int64, error) {
	var tasks []adminModel.Task
	var total int64

	err := s.dbService.ExecuteQuery(context.Background(), func() error {
		query := global.APP_DB.Model(&adminModel.Task{}).Where("user_id = ?", userID)

		// 应用筛选条件
		if req.ProviderId != 0 {
			query = query.Where("provider_id = ?", req.ProviderId)
		}
		if req.TaskType != "" {
			query = query.Where("task_type = ?", req.TaskType)
		}
		if req.Status != "" {
			query = query.Where("status = ?", req.Status)
		}

		// 获取总数
		if err := query.Count(&total).Error; err != nil {
			return err
		}

		// 获取任务列表
		offset := (req.Page - 1) * req.PageSize
		return query.Preload("Provider").
			Order("created_at DESC").
			Offset(offset).Limit(req.PageSize).
			Find(&tasks).Error
	})

	if err != nil {
		return nil, 0, err
	}

	// 转换为响应格式
	var taskResponses []userModel.TaskResponse
	for _, task := range tasks {
		taskResponse := userModel.TaskResponse{
			ID:              task.ID,
			UUID:            task.UUID,
			TaskType:        task.TaskType,
			Status:          task.Status,
			Progress:        task.Progress,
			ErrorMessage:    task.ErrorMessage,
			CancelReason:    task.CancelReason,
			CreatedAt:       task.CreatedAt,
			StartedAt:       task.StartedAt,
			CompletedAt:     task.CompletedAt,
			TimeoutDuration: task.TimeoutDuration,
			StatusMessage:   task.StatusMessage,
		}

		// 设置ProviderId和ProviderName
		if task.ProviderID != nil {
			taskResponse.ProviderId = *task.ProviderID
		}
		if task.Provider != nil {
			taskResponse.ProviderName = task.Provider.Name
		}

		// 设置InstanceID和InstanceName
		if task.InstanceID != nil {
			taskResponse.InstanceID = task.InstanceID
			// 获取实例名称
			var instance providerModel.Instance
			if err := global.APP_DB.First(&instance, *task.InstanceID).Error; err == nil {
				taskResponse.InstanceName = instance.Name
			}
		}

		// 计算剩余时间
		if task.Status == "running" && task.StartedAt != nil {
			elapsed := time.Since(*task.StartedAt).Seconds()
			remaining := float64(task.TimeoutDuration) - elapsed
			if remaining > 0 {
				taskResponse.RemainingTime = int(remaining)
			}
		}

		// 设置是否可取消（考虑任务状态和是否允许被用户取消）
		taskResponse.CanCancel = (task.Status == "pending" || task.Status == "running") && task.IsForceStoppable

		taskResponses = append(taskResponses, taskResponse)
	}

	return taskResponses, total, nil
}

// GetAdminTasks 获取管理员任务列表
func (s *TaskService) GetAdminTasks(req adminModel.AdminTaskListRequest) ([]adminModel.AdminTaskResponse, int64, error) {
	var tasks []adminModel.Task
	var total int64

	err := s.dbService.ExecuteQuery(context.Background(), func() error {
		query := global.APP_DB.Model(&adminModel.Task{})

		// 应用筛选条件
		if req.ProviderID != 0 {
			query = query.Where("provider_id = ?", req.ProviderID)
		}
		if req.Username != "" {
			// 通过用户名搜索，需要连接 users 表
			query = query.Joins("LEFT JOIN users ON users.id = tasks.user_id").
				Where("users.username LIKE ?", "%"+req.Username+"%")
		}
		if req.TaskType != "" {
			query = query.Where("task_type = ?", req.TaskType)
		}
		if req.Status != "" {
			query = query.Where("status = ?", req.Status)
		}
		if req.InstanceType != "" {
			query = query.Joins("LEFT JOIN instances ON instances.id = tasks.instance_id").
				Where("instances.instance_type = ?", req.InstanceType)
		}

		// 获取总数
		if err := query.Count(&total).Error; err != nil {
			return err
		}

		// 获取任务列表
		offset := (req.Page - 1) * req.PageSize
		return query.Order("created_at DESC").
			Offset(offset).Limit(req.PageSize).
			Find(&tasks).Error
	})

	if err != nil {
		return nil, 0, err
	}

	// 转换为响应格式
	var taskResponses []adminModel.AdminTaskResponse
	for _, task := range tasks {
		var providerID uint
		if task.ProviderID != nil {
			providerID = *task.ProviderID
		}

		// 计算剩余时间
		remainingTime := 0
		if task.Status == "running" && task.StartedAt != nil {
			elapsed := time.Since(*task.StartedAt).Seconds()
			remaining := float64(task.TimeoutDuration) - elapsed
			if remaining > 0 {
				remainingTime = int(remaining)
			}
		}

		taskResponse := adminModel.AdminTaskResponse{
			ID:              task.ID,
			UUID:            task.UUID,
			TaskType:        task.TaskType,
			Status:          task.Status,
			Progress:        task.Progress,
			ErrorMessage:    task.ErrorMessage,
			CancelReason:    task.CancelReason,
			CreatedAt:       task.CreatedAt,
			StartedAt:       task.StartedAt,
			CompletedAt:     task.CompletedAt,
			TimeoutDuration: task.TimeoutDuration,
			StatusMessage:   task.StatusMessage,
			UserID:          task.UserID,
			ProviderID:      &providerID,
			// 管理员可以强制停止processing, running, cancelling状态的任务
			CanForceStop:     (task.Status == "processing" || task.Status == "running" || task.Status == "cancelling"),
			IsForceStoppable: task.IsForceStoppable,
			RemainingTime:    remainingTime,
		}

		if task.UserID != 0 {
			var user userModel.User
			if err := global.APP_DB.First(&user, task.UserID).Error; err == nil {
				taskResponse.UserName = user.Username
			}
		}

		if task.ProviderID != nil {
			var provider providerModel.Provider
			if err := global.APP_DB.First(&provider, *task.ProviderID).Error; err == nil {
				taskResponse.ProviderName = provider.Name
			}
		}

		if task.InstanceID != nil {
			var instance providerModel.Instance
			if err := global.APP_DB.First(&instance, *task.InstanceID).Error; err == nil {
				taskResponse.InstanceID = &instance.ID
				taskResponse.InstanceName = instance.Name
				taskResponse.InstanceType = instance.InstanceType
			}
		}

		taskResponses = append(taskResponses, taskResponse)
	}

	return taskResponses, total, nil
}

// GetTaskStats 获取任务统计信息
func (s *TaskService) GetTaskStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// 统计各状态任务数量
	var statusCounts []dashboardModel.TaskStatusCount

	err := global.APP_DB.Model(&adminModel.Task{}).
		Select("status, count(*) as count").
		Group("status").
		Find(&statusCounts).Error

	if err != nil {
		return nil, fmt.Errorf("统计任务状态失败: %w", err)
	}

	taskStats := make(map[string]int64)
	for _, sc := range statusCounts {
		taskStats[sc.Status] = sc.Count
	}

	stats["task_counts"] = taskStats
	stats["last_update"] = time.Now()

	return stats, nil
}

// GetTaskOverallStats 获取任务总体统计信息
func (s *TaskService) GetTaskOverallStats() (*adminModel.TaskStatsResponse, error) {
	var stats adminModel.TaskStatsResponse

	// 统计总任务数
	if err := global.APP_DB.Model(&adminModel.Task{}).Count(&stats.TotalTasks).Error; err != nil {
		return nil, fmt.Errorf("统计总任务数失败: %w", err)
	}

	// 统计各状态的任务数
	statusQueries := map[string]*int64{
		"pending":   &stats.PendingTasks,
		"running":   &stats.RunningTasks,
		"completed": &stats.CompletedTasks,
		"failed":    &stats.FailedTasks,
		"timeout":   &stats.TimeoutTasks,
	}

	for status, count := range statusQueries {
		if err := global.APP_DB.Model(&adminModel.Task{}).
			Where("status = ?", status).
			Count(count).Error; err != nil {
			return nil, fmt.Errorf("统计%s状态任务数失败: %w", status, err)
		}
	}

	// 同时统计processing状态的任务到运行中
	var processingTasks int64
	if err := global.APP_DB.Model(&adminModel.Task{}).
		Where("status = ?", "processing").
		Count(&processingTasks).Error; err != nil {
		return nil, fmt.Errorf("统计processing状态任务数失败: %w", err)
	}
	stats.RunningTasks += processingTasks

	// 统计cancelled和cancelling状态的任务到失败中
	var cancelledTasks int64
	if err := global.APP_DB.Model(&adminModel.Task{}).
		Where("status IN (?)", []string{"cancelled", "cancelling"}).
		Count(&cancelledTasks).Error; err != nil {
		return nil, fmt.Errorf("统计cancelled状态任务数失败: %w", err)
	}
	stats.FailedTasks += cancelledTasks

	return &stats, nil
}
