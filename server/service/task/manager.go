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
	dashboardModel "oneclickvirt/model/dashboard"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// calculateEstimatedDuration 计算任务预计执行时长（秒）
// 所有任务都需要设置执行时长，用于准确计算排队等待时间
// VM创建: 5分钟 (300秒)
// 容器创建: 3分钟 (180秒)
// VM重置: 7.5分钟 (创建的1.5倍)
// 容器重置: 4.5分钟 (创建的1.5倍)
// 其他操作: 根据操作类型设置合理时长
func (s *TaskService) calculateEstimatedDuration(taskType string, instanceType string) int {
	switch taskType {
	case "create":
		if instanceType == "vm" {
			return 300 // 5分钟 - VM创建较慢
		}
		return 180 // 3分钟 - 容器创建较快
	case "reset":
		if instanceType == "vm" {
			return 450 // 7.5分钟 - VM重置 (创建的1.5倍)
		}
		return 270 // 4.5分钟 - 容器重置 (创建的1.5倍)
	case "start":
		if instanceType == "vm" {
			return 90 // 1.5分钟 - VM启动较慢
		}
		return 30 // 30秒 - 容器启动快
	case "stop":
		if instanceType == "vm" {
			return 60 // 1分钟 - VM停止需要优雅关机
		}
		return 30 // 30秒 - 容器停止快
	case "restart":
		if instanceType == "vm" {
			return 150 // 2.5分钟 - VM重启 (stop + start)
		}
		return 60 // 1分钟 - 容器重启
	case "delete":
		return 300 // 5分钟 - 删除操作
	case "reset-password":
		return 30 // 30秒 - 密码重置操作快
	default:
		return 120 // 默认2分钟 - 保守估计
	}
}

// parseTaskDataForConfig 解析taskData获取实例配置信息
func (s *TaskService) parseTaskDataForConfig(taskData string) (cpu int, memory int, disk int, bandwidth int, instanceType string) {
	var taskReq adminModel.CreateInstanceTaskRequest
	if err := json.Unmarshal([]byte(taskData), &taskReq); err != nil {
		return 0, 0, 0, 0, ""
	}

	// 解析规格ID获取实际配置
	if cpuSpec, err := constant.GetCPUSpecByID(taskReq.CPUId); err == nil {
		cpu = cpuSpec.Cores
	}
	if memorySpec, err := constant.GetMemorySpecByID(taskReq.MemoryId); err == nil {
		memory = memorySpec.SizeMB
	}
	if diskSpec, err := constant.GetDiskSpecByID(taskReq.DiskId); err == nil {
		disk = diskSpec.SizeMB
	}
	if bandwidthSpec, err := constant.GetBandwidthSpecByID(taskReq.BandwidthId); err == nil {
		bandwidth = bandwidthSpec.SpeedMbps
	}

	// 从镜像ID获取实例类型
	if taskReq.ImageId > 0 {
		var systemImage struct {
			InstanceType string
		}
		if err := global.APP_DB.Table("system_images").
			Select("instance_type").
			Where("id = ?", taskReq.ImageId).
			First(&systemImage).Error; err == nil {
			instanceType = systemImage.InstanceType
		}
	}

	return
}

// CreateTask 创建任务
func (s *TaskService) CreateTask(userID uint, providerID *uint, instanceID *uint, taskType string, taskData string, timeoutDuration int) (*adminModel.Task, error) {
	if timeoutDuration <= 0 {
		timeoutDuration = s.getDefaultTimeout(taskType)
	}

	// 解析taskData获取配置信息
	cpu, memory, disk, bandwidth, instanceType := s.parseTaskDataForConfig(taskData)

	// 如果是非create任务，从instance获取实例类型
	if instanceType == "" && instanceID != nil {
		var instance providerModel.Instance
		if err := global.APP_DB.Select("instance_type").First(&instance, *instanceID).Error; err == nil {
			instanceType = instance.InstanceType
		}
	}

	// 计算预计执行时长
	estimatedDuration := s.calculateEstimatedDuration(taskType, instanceType)

	task := &adminModel.Task{
		UserID:                userID,
		ProviderID:            providerID,
		InstanceID:            instanceID,
		TaskType:              taskType,
		Status:                "pending",
		TaskData:              taskData,
		TimeoutDuration:       timeoutDuration,
		IsForceStoppable:      true,
		EstimatedDuration:     estimatedDuration,
		PreallocatedCPU:       cpu,
		PreallocatedMemory:    memory,
		PreallocatedDisk:      disk,
		PreallocatedBandwidth: bandwidth,
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
		zap.Uint("userId", userID),
		zap.Int("estimatedDuration", estimatedDuration),
		zap.Int("cpu", cpu),
		zap.Int("memory", memory))

	return task, nil
}

// GetUserTasks 获取用户任务列表
func (s *TaskService) GetUserTasks(userID uint, req userModel.UserTasksRequest) ([]userModel.TaskResponse, int64, error) {
	var tasks []adminModel.Task
	var total int64

	// 构建查询 - 不使用事务包装
	query := global.APP_DB.Model(&adminModel.Task{}).Where("user_id = ?", userID)

	// 应用筛选条件
	hasFilter := false
	if req.ProviderId != 0 {
		query = query.Where("provider_id = ?", req.ProviderId)
		hasFilter = true
	}
	if req.TaskType != "" {
		query = query.Where("task_type = ?", req.TaskType)
		hasFilter = true
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
		hasFilter = true
	}

	// 获取总数
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 获取任务列表
	// 无筛选条件时：返回所有任务（最多100条），优先展示有活跃任务的节点
	// 有筛选条件时：使用分页
	if !hasFilter {
		// 无筛选：返回所有任务（最多100条）
		// 优先返回 pending 和 running 状态的任务，然后是其他状态
		// 使用Preload预加载Provider
		if err := query.Preload("Provider", func(db *gorm.DB) *gorm.DB {
			return db.Select("id", "name") // 只选择需要的字段
		}).
			Order("CASE WHEN status IN ('pending', 'running', 'processing') THEN 0 ELSE 1 END").
			Order("created_at DESC").
			Limit(100).
			Find(&tasks).Error; err != nil {
			return nil, 0, err
		}
	} else {
		// 有筛选：使用分页
		// 使用Preload预加载Provider
		offset := (req.Page - 1) * req.PageSize
		if err := query.Preload("Provider", func(db *gorm.DB) *gorm.DB {
			return db.Select("id", "name")
		}).
			Order("created_at DESC").
			Offset(offset).Limit(req.PageSize).
			Find(&tasks).Error; err != nil {
			return nil, 0, err
		}
	}

	// 批量查询所有涉及的 provider 的任务
	providerIDs := make(map[uint]bool)
	for _, task := range tasks {
		if task.ProviderID != nil && (task.Status == "pending" || task.Status == "running") {
			providerIDs[*task.ProviderID] = true
		}
	}

	providerTasksMap := make(map[uint][]adminModel.Task)
	if len(providerIDs) > 0 {
		var providerIDList []uint
		for pid := range providerIDs {
			providerIDList = append(providerIDList, pid)
		}

		// 一次性查询所有 provider 的 pending 和 running 任务
		var allProviderTasks []adminModel.Task
		if err := global.APP_DB.Select("id", "provider_id", "status", "created_at", "estimated_duration", "started_at").
			Where("provider_id IN ? AND status IN (?, ?)", providerIDList, "pending", "running").
			Order("provider_id ASC, created_at ASC").
			Find(&allProviderTasks).Error; err == nil {
			// 按 provider_id 分组
			for _, pt := range allProviderTasks {
				if pt.ProviderID != nil {
					providerTasksMap[*pt.ProviderID] = append(providerTasksMap[*pt.ProviderID], pt)
				}
			}
		}
	}

	// 批量查询实例信息 - 包含instance_type字段
	var instanceIDs []uint
	instanceIDSet := make(map[uint]bool)
	for _, task := range tasks {
		if task.InstanceID != nil && !instanceIDSet[*task.InstanceID] {
			instanceIDs = append(instanceIDs, *task.InstanceID)
			instanceIDSet[*task.InstanceID] = true
		}
	}

	instanceMap := make(map[uint]providerModel.Instance) // instanceID -> Instance
	if len(instanceIDs) > 0 {
		var instances []providerModel.Instance
		if err := global.APP_DB.Model(&providerModel.Instance{}).
			Select("id", "name", "instance_type").
			Where("id IN ?", instanceIDs).
			Limit(500). // 限制最多500条，防止单次查询过大
			Find(&instances).Error; err == nil {
			for _, instance := range instances {
				instanceMap[instance.ID] = instance
			}
		}
	}

	// 转换为响应格式
	var taskResponses []userModel.TaskResponse
	for _, task := range tasks {
		taskResponse := userModel.TaskResponse{
			ID:                    task.ID,
			UUID:                  task.UUID,
			TaskType:              task.TaskType,
			Status:                task.Status,
			Progress:              task.Progress,
			ErrorMessage:          task.ErrorMessage,
			CancelReason:          task.CancelReason,
			CreatedAt:             task.CreatedAt,
			StartedAt:             task.StartedAt,
			CompletedAt:           task.CompletedAt,
			TimeoutDuration:       task.TimeoutDuration,
			StatusMessage:         task.StatusMessage,
			PreallocatedCPU:       task.PreallocatedCPU,
			PreallocatedMemory:    task.PreallocatedMemory,
			PreallocatedDisk:      task.PreallocatedDisk,
			PreallocatedBandwidth: task.PreallocatedBandwidth,
		}

		// 设置ProviderId和ProviderName
		if task.ProviderID != nil {
			taskResponse.ProviderId = *task.ProviderID
		}
		if task.Provider != nil {
			taskResponse.ProviderName = task.Provider.Name
		}

		// 设置InstanceID、InstanceName和InstanceType
		if task.InstanceID != nil {
			taskResponse.InstanceID = task.InstanceID
			// 从预加载的map中获取实例信息
			if instance, exists := instanceMap[*task.InstanceID]; exists {
				taskResponse.InstanceName = instance.Name
				taskResponse.InstanceType = instance.InstanceType
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

		// 计算排队信息
		// 只有 pending 状态的任务才显示排队位置
		// running 状态的任务不显示排队位置（queuePosition = -1 表示正在执行）
		if task.ProviderID != nil && task.Status == "pending" {
			if providerTasks, exists := providerTasksMap[*task.ProviderID]; exists {
				queuePosition := -1 // 默认值，表示找不到或正在执行
				estimatedWaitTime := 0
				runningCount := 0

				// 先统计有多少个 running 任务
				for _, pt := range providerTasks {
					if pt.Status == "running" || pt.Status == "processing" {
						runningCount++
					}
				}

				// 找到当前任务在队列中的位置
				pendingIndex := 0
				for _, pt := range providerTasks {
					if pt.Status == "pending" {
						if pt.ID == task.ID {
							// 找到了当前任务
							// queuePosition 从 0 开始：0 表示第一个等待的任务
							queuePosition = pendingIndex

							// 计算预计等待时间：
							// 1. 所有 running 任务的剩余时间
							for _, rpt := range providerTasks {
								if rpt.Status == "running" || rpt.Status == "processing" {
									if rpt.StartedAt != nil {
										elapsed := time.Since(*rpt.StartedAt).Seconds()
										remaining := float64(rpt.EstimatedDuration) - elapsed
										if remaining > 0 {
											estimatedWaitTime += int(remaining)
										}
									} else {
										// 如果没有开始时间，使用预计执行时长
										estimatedWaitTime += rpt.EstimatedDuration
									}
								}
							}

							// 2. 前面所有 pending 任务的预计执行时长
							pendingIdx := 0
							for _, ppt := range providerTasks {
								if ppt.Status == "pending" {
									if ppt.ID == task.ID {
										break
									}
									estimatedWaitTime += ppt.EstimatedDuration
									pendingIdx++
								}
							}
							break
						}
						pendingIndex++
					}
				}

				taskResponse.QueuePosition = queuePosition
				taskResponse.EstimatedWaitTime = estimatedWaitTime
			}
		} else if task.ProviderID != nil && (task.Status == "running" || task.Status == "processing") {
			// running 状态：不显示排队位置（设为-1）
			taskResponse.QueuePosition = -1
			taskResponse.EstimatedWaitTime = 0
		}

		// 设置是否可取消（考虑任务状态和是否允许被用户取消）
		taskResponse.CanCancel = (task.Status == "pending" || task.Status == "running") && task.IsForceStoppable
		taskResponse.IsForceStoppable = task.IsForceStoppable

		taskResponses = append(taskResponses, taskResponse)
	}

	return taskResponses, total, nil
}

// GetAdminTasks 获取管理员任务列表
func (s *TaskService) GetAdminTasks(req adminModel.AdminTaskListRequest) ([]adminModel.AdminTaskResponse, int64, error) {
	var tasks []adminModel.Task
	var total int64

	// 构建查询 - 不使用事务包装
	query := global.APP_DB.Model(&adminModel.Task{})

	// 应用筛选条件
	if req.ProviderID != 0 {
		query = query.Where("tasks.provider_id = ?", req.ProviderID)
	}
	if req.Username != "" {
		// 通过用户名搜索，需要连接 users 表
		query = query.Joins("LEFT JOIN users ON users.id = tasks.user_id").
			Where("users.username LIKE ?", "%"+req.Username+"%")
	}
	if req.TaskType != "" {
		query = query.Where("tasks.task_type = ?", req.TaskType)
	}
	if req.Status != "" {
		query = query.Where("tasks.status = ?", req.Status)
	}
	if req.InstanceType != "" {
		query = query.Joins("LEFT JOIN instances ON instances.id = tasks.instance_id").
			Where("instances.instance_type = ?", req.InstanceType)
	}

	// 获取总数
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 获取任务列表 - 只查询必要字段，避免加载大字段
	offset := (req.Page - 1) * req.PageSize
	if err := query.Select("tasks.*").Order("tasks.created_at DESC").
		Offset(offset).Limit(req.PageSize).
		Find(&tasks).Error; err != nil {
		return nil, 0, err
	}

	// 批量预加载 user, provider, instance
	var userIDs, providerIDs, instanceIDs []uint
	userIDSet := make(map[uint]bool)
	providerIDSet := make(map[uint]bool)
	instanceIDSet := make(map[uint]bool)

	for _, task := range tasks {
		if task.UserID > 0 && !userIDSet[task.UserID] {
			userIDs = append(userIDs, task.UserID)
			userIDSet[task.UserID] = true
		}
		if task.ProviderID != nil && *task.ProviderID > 0 && !providerIDSet[*task.ProviderID] {
			providerIDs = append(providerIDs, *task.ProviderID)
			providerIDSet[*task.ProviderID] = true
		}
		if task.InstanceID != nil && *task.InstanceID > 0 && !instanceIDSet[*task.InstanceID] {
			instanceIDs = append(instanceIDs, *task.InstanceID)
			instanceIDSet[*task.InstanceID] = true
		}
	}

	// 批量查询users（只选择需要的字段）
	userMap := make(map[uint]userModel.User)
	if len(userIDs) > 0 {
		var users []userModel.User
		if err := global.APP_DB.Select("id", "username").
			Where("id IN ?", userIDs).
			Limit(500). // 限制最多500条
			Find(&users).Error; err == nil {
			for _, user := range users {
				userMap[user.ID] = user
			}
		}
	}

	// 批量查询providers（只选择需要的字段）
	providerMap := make(map[uint]providerModel.Provider)
	if len(providerIDs) > 0 {
		var providers []providerModel.Provider
		if err := global.APP_DB.Select("id", "name").
			Where("id IN ?", providerIDs).
			Limit(500).
			Find(&providers).Error; err == nil {
			for _, provider := range providers {
				providerMap[provider.ID] = provider
			}
		}
	}

	// 批量查询instances（只选择需要的字段）
	instanceMap := make(map[uint]providerModel.Instance)
	if len(instanceIDs) > 0 {
		var instances []providerModel.Instance
		if err := global.APP_DB.Select("id", "name", "instance_type").
			Where("id IN ?", instanceIDs).
			Limit(500).
			Find(&instances).Error; err == nil {
			for _, instance := range instances {
				instanceMap[instance.ID] = instance
			}
		}
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
			ID:                    task.ID,
			UUID:                  task.UUID,
			TaskType:              task.TaskType,
			Status:                task.Status,
			Progress:              task.Progress,
			ErrorMessage:          task.ErrorMessage,
			CancelReason:          task.CancelReason,
			CreatedAt:             task.CreatedAt,
			StartedAt:             task.StartedAt,
			CompletedAt:           task.CompletedAt,
			TimeoutDuration:       task.TimeoutDuration,
			StatusMessage:         task.StatusMessage,
			UserID:                task.UserID,
			ProviderID:            &providerID,
			CanForceStop:          (task.Status == "processing" || task.Status == "running" || task.Status == "cancelling"),
			IsForceStoppable:      task.IsForceStoppable,
			RemainingTime:         remainingTime,
			PreallocatedCPU:       task.PreallocatedCPU,
			PreallocatedMemory:    task.PreallocatedMemory,
			PreallocatedDisk:      task.PreallocatedDisk,
			PreallocatedBandwidth: task.PreallocatedBandwidth,
		}

		if task.UserID != 0 {
			if user, ok := userMap[task.UserID]; ok {
				taskResponse.UserName = user.Username
			}
		}

		if task.ProviderID != nil {
			if provider, ok := providerMap[*task.ProviderID]; ok {
				taskResponse.ProviderName = provider.Name
			}
		}

		if task.InstanceID != nil {
			if instance, ok := instanceMap[*task.InstanceID]; ok {
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

	// 使用单次GROUP BY查询统计所有状态
	type StatusCount struct {
		Status string
		Count  int64
	}
	var statusCounts []StatusCount
	if err := global.APP_DB.Model(&adminModel.Task{}).
		Select("status, COUNT(*) as count").
		Group("status").
		Scan(&statusCounts).Error; err != nil {
		return nil, fmt.Errorf("统计任务状态失败: %w", err)
	}

	// 将状态统计映射到响应结构
	for _, sc := range statusCounts {
		switch sc.Status {
		case "pending":
			stats.PendingTasks = sc.Count
		case "running":
			stats.RunningTasks = sc.Count
		case "processing":
			stats.RunningTasks += sc.Count // processing算作运行中
		case "completed":
			stats.CompletedTasks = sc.Count
		case "failed":
			stats.FailedTasks = sc.Count
		case "timeout":
			stats.TimeoutTasks = sc.Count
		case "cancelled", "cancelling":
			stats.FailedTasks += sc.Count // cancelled算作失败
		}
	}

	return &stats, nil
}

// GetTaskDetail 获取任务详情
func (s *TaskService) GetTaskDetail(taskID uint) (*adminModel.AdminTaskDetailResponse, error) {
	var task adminModel.Task
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("任务不存在")
		}
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}

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

	response := adminModel.AdminTaskDetailResponse{
		AdminTaskResponse: adminModel.AdminTaskResponse{
			ID:                    task.ID,
			UUID:                  task.UUID,
			TaskType:              task.TaskType,
			Status:                task.Status,
			Progress:              task.Progress,
			ErrorMessage:          task.ErrorMessage,
			CancelReason:          task.CancelReason,
			CreatedAt:             task.CreatedAt,
			StartedAt:             task.StartedAt,
			CompletedAt:           task.CompletedAt,
			TimeoutDuration:       task.TimeoutDuration,
			StatusMessage:         task.StatusMessage,
			UserID:                task.UserID,
			ProviderID:            &providerID,
			CanForceStop:          (task.Status == "processing" || task.Status == "running" || task.Status == "cancelling"),
			IsForceStoppable:      task.IsForceStoppable,
			RemainingTime:         remainingTime,
			PreallocatedCPU:       task.PreallocatedCPU,
			PreallocatedMemory:    task.PreallocatedMemory,
			PreallocatedDisk:      task.PreallocatedDisk,
			PreallocatedBandwidth: task.PreallocatedBandwidth,
		},
		TaskData: task.TaskData,
	}

	// 获取用户信息 - 只查询需要的字段
	if task.UserID != 0 {
		var user userModel.User
		if err := global.APP_DB.Select("id, username").First(&user, task.UserID).Error; err == nil {
			response.UserName = user.Username
		}
	}

	// 获取Provider信息 - 只查询需要的字段
	if task.ProviderID != nil {
		var provider providerModel.Provider
		if err := global.APP_DB.Select("id, name").First(&provider, *task.ProviderID).Error; err == nil {
			response.ProviderName = provider.Name
		}
	}

	// 获取实例信息 - 只查询需要的字段
	if task.InstanceID != nil {
		var instance providerModel.Instance
		if err := global.APP_DB.Select("id, name, instance_type").First(&instance, *task.InstanceID).Error; err == nil {
			response.InstanceID = &instance.ID
			response.InstanceName = instance.Name
			response.InstanceType = instance.InstanceType
		}
	}

	return &response, nil
}
