package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/service/auth"
	"oneclickvirt/service/cache"
	"oneclickvirt/service/database"
	"oneclickvirt/service/task"
	trafficService "oneclickvirt/service/traffic"
	"oneclickvirt/utils"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 处理用户实例相关功能
type Service struct{}

// NewService 创建实例服务
func NewService() *Service {
	return &Service{}
}

// GetUserInstances 获取用户实例列表
func (s *Service) GetUserInstances(userID uint, req userModel.UserInstanceListRequest) ([]userModel.UserInstanceResponse, int64, error) {
	var instances []providerModel.Instance
	var total int64

	// 基础查询：过滤掉失败、创建中、删除中的实例，这些实例不应该在用户界面显示
	// 同时排除有进行中的reset任务的实例（包括新旧实例）
	query := global.APP_DB.Model(&providerModel.Instance{}).
		Where("user_id = ? AND status NOT IN (?)", userID, []string{"failed", "creating", "deleting"}).
		Where("id NOT IN (SELECT instance_id FROM tasks WHERE task_type = 'reset' AND status IN ('pending', 'running') AND instance_id IS NOT NULL)")

	if req.Name != "" {
		query = query.Where("name LIKE ?", "%"+req.Name+"%")
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}
	if req.InstanceType != "" {
		query = query.Where("instance_type = ?", req.InstanceType)
	}
	// 支持type字段（兼容前端）
	if req.Type != "" {
		query = query.Where("instance_type = ?", req.Type)
	}
	// 支持节点名称搜索
	if req.ProviderName != "" {
		query = query.Where("provider LIKE ?", "%"+req.ProviderName+"%")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Offset(offset).Limit(req.PageSize).Find(&instances).Error; err != nil {
		return nil, 0, err
	}

	// 批量预加载端口映射
	var instanceIDs []uint
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}

	// 批量查询所有实例的端口映射
	var allPorts []providerModel.Port
	if len(instanceIDs) > 0 {
		global.APP_DB.Where("instance_id IN ? AND status = 'active'", instanceIDs).
			Order("instance_id, is_ssh DESC, created_at ASC").Find(&allPorts)
	}

	// 将端口映射按instance_id分组
	portsByInstance := make(map[uint][]providerModel.Port)
	for _, port := range allPorts {
		portsByInstance[port.InstanceID] = append(portsByInstance[port.InstanceID], port)
	}

	// 批量查询Provider信息
	var providerIDs []uint
	providerIDSet := make(map[uint]bool)
	for _, instance := range instances {
		if instance.ProviderID > 0 && !providerIDSet[instance.ProviderID] {
			providerIDs = append(providerIDs, instance.ProviderID)
			providerIDSet[instance.ProviderID] = true
		}
	}

	var providers []providerModel.Provider
	if len(providerIDs) > 0 {
		global.APP_DB.Select("id, name, type, status, host, port_ip, endpoint").
			Where("id IN ?", providerIDs).
			Limit(1000).
			Find(&providers)
	}

	// 将Provider信息按ID映射
	providerMap := make(map[uint]providerModel.Provider)
	for _, provider := range providers {
		providerMap[provider.ID] = provider
	}

	var userInstances []userModel.UserInstanceResponse
	for _, instance := range instances {
		// 从预加载的数据中获取端口映射信息
		ports := portsByInstance[instance.ID]

		// 获取SSH端口（映射的公网端口）
		var sshPort int
		var providerType string
		var providerStatus string

		// 查找SSH端口映射
		for _, port := range ports {
			if port.IsSSH {
				sshPort = port.HostPort // 使用映射的公网端口而不是22
				break
			}
		}

		// 从预加载的Provider map中获取Provider信息
		if instance.ProviderID > 0 {
			if providerInfo, ok := providerMap[instance.ProviderID]; ok {
				providerType = providerInfo.Type
				providerStatus = providerInfo.Status

				// 如果实例状态是unavailable，检查provider是否已经恢复
				if instance.Status == "unavailable" && providerInfo.Status == "active" {
					global.APP_LOG.Debug("实例处于unavailable状态但provider已恢复",
						zap.Uint("instance_id", instance.ID),
						zap.String("instance_name", instance.Name),
						zap.String("provider_status", providerInfo.Status))
				}
			}
		}

		// 构建端口映射列表
		var portMappings []map[string]interface{}
		for _, port := range ports {
			portMappings = append(portMappings, map[string]interface{}{
				"id":          port.ID,
				"hostPort":    port.HostPort,  // 统一使用 hostPort
				"guestPort":   port.GuestPort, // 统一使用 guestPort
				"protocol":    port.Protocol,
				"description": port.Description,
				"isSSH":       port.IsSSH,
			})
		}

		// 创建修改后的实例副本，更新SSH端口
		modifiedInstance := instance
		if sshPort > 0 {
			modifiedInstance.SSHPort = sshPort // 使用映射的公网端口
		}

		userInstance := userModel.UserInstanceResponse{
			Instance:       modifiedInstance,
			CanStart:       instance.Status == "stopped" && !instance.TrafficLimited, // 流量受限时不能启动
			CanStop:        instance.Status == "running" || instance.Status == "unavailable",
			CanRestart:     instance.Status == "running" && !instance.TrafficLimited, // 流量受限时不能重启
			CanDelete:      instance.Status != "deleting",
			PortMappings:   portMappings,
			PublicIP:       instance.PublicIP, // 直接使用实例的PublicIP字段
			ProviderType:   providerType,
			ProviderStatus: providerStatus,
		}
		userInstances = append(userInstances, userInstance)
	}

	return userInstances, total, nil
}

// InstanceAction 执行实例操作
func (s *Service) InstanceAction(userID uint, req userModel.InstanceActionRequest) error {
	var instance providerModel.Instance
	if err := global.APP_DB.Where("id = ? AND user_id = ?", req.InstanceID, userID).First(&instance).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("实例不存在或无权限")
		}
		return err
	}

	// 操作完成后使缓存失效
	defer func() {
		cacheService := cache.GetUserCacheService()
		cacheService.InvalidateUserCache(userID)
		cacheService.InvalidateInstanceCache(req.InstanceID)
	}()

	switch req.Action {
	case "start":
		if instance.Status != "stopped" {
			return errors.New("实例状态不允许启动")
		}

		// 检查是否已有进行中的启动任务
		var existingTask adminModel.Task
		if err := global.APP_DB.Where("instance_id = ? AND task_type = 'start' AND status IN ('pending', 'running')", instance.ID).First(&existingTask).Error; err == nil {
			return errors.New("实例已有启动任务正在进行")
		}

		// 创建启动任务
		taskService := getTaskService()
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, instance.ProviderID)
		_, err := taskService.CreateTask(userID, &instance.ProviderID, &instance.ID, "start", taskData, 1800)
		if err != nil {
			return fmt.Errorf("创建启动任务失败: %v", err)
		}

		instance.Status = "starting"
	case "stop":
		if instance.Status != "running" {
			return errors.New("实例状态不允许停止")
		}

		// 检查是否已有进行中的停止任务
		var existingTask adminModel.Task
		if err := global.APP_DB.Where("instance_id = ? AND task_type = 'stop' AND status IN ('pending', 'running')", instance.ID).First(&existingTask).Error; err == nil {
			return errors.New("实例已有停止任务正在进行")
		}

		// 创建停止任务
		taskService := getTaskService()
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, instance.ProviderID)
		_, err := taskService.CreateTask(userID, &instance.ProviderID, &instance.ID, "stop", taskData, 1800)
		if err != nil {
			return fmt.Errorf("创建停止任务失败: %v", err)
		}

		instance.Status = "stopping"
	case "restart":
		if instance.Status != "running" {
			return errors.New("实例状态不允许重启")
		}

		// 检查是否已有进行中的重启任务
		var existingTask adminModel.Task
		if err := global.APP_DB.Where("instance_id = ? AND task_type = 'restart' AND status IN ('pending', 'running')", instance.ID).First(&existingTask).Error; err == nil {
			return errors.New("实例已有重启任务正在进行")
		}

		// 创建重启任务
		taskService := getTaskService()
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, instance.ProviderID)
		_, err := taskService.CreateTask(userID, &instance.ProviderID, &instance.ID, "restart", taskData, 1800)
		if err != nil {
			return fmt.Errorf("创建重启任务失败: %v", err)
		}

		instance.Status = "restarting"
	case "reset":
		if instance.Status != "running" && instance.Status != "stopped" {
			return errors.New("实例状态不允许重置")
		}

		// 检查用户重置权限
		permissionService := auth.PermissionService{}
		if !permissionService.CheckInstanceResetPermission(userID, instance.InstanceType) {
			return errors.New("您的等级不足，无法自行重置系统，请联系管理员处理")
		}

		// 检查是否已有进行中的重置任务
		var existingTask adminModel.Task
		if err := global.APP_DB.Where("instance_id = ? AND task_type = 'reset' AND status IN ('pending', 'running')", instance.ID).First(&existingTask).Error; err == nil {
			return errors.New("实例已有重置任务正在进行")
		}

		// 创建重置任务，记录原始状态
		originalStatus := instance.Status
		taskService := getTaskService()
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d,"originalStatus":"%s"}`, instance.ID, instance.ProviderID, originalStatus)
		_, err := taskService.CreateTask(userID, &instance.ProviderID, &instance.ID, "reset", taskData, 1800)
		if err != nil {
			return fmt.Errorf("创建重置任务失败: %v", err)
		}

		instance.Status = "resetting"
	case "delete":
		if instance.Status == "deleting" {
			return errors.New("实例正在删除中")
		}

		// 检查用户删除权限
		permissionService := auth.PermissionService{}
		if !permissionService.CheckInstanceDeletePermission(userID, instance.InstanceType) {
			return errors.New("您的等级不足，无法自行删除实例，请联系管理员处理")
		}

		// 检查是否已有进行中的删除任务
		var existingTask adminModel.Task
		if err := global.APP_DB.Where("instance_id = ? AND task_type = 'delete' AND status IN ('pending', 'running')", instance.ID).First(&existingTask).Error; err == nil {
			return errors.New("实例已有删除任务正在进行")
		}

		// 创建删除任务
		taskService := getTaskService()
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, instance.ProviderID)
		_, err := taskService.CreateTask(userID, &instance.ProviderID, &instance.ID, "delete", taskData, 1800)
		if err != nil {
			return fmt.Errorf("创建删除任务失败: %v", err)
		}

		instance.Status = "deleting"
	default:
		return errors.New("不支持的操作")
	}

	// 使用数据库抽象层保存
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&instance).Error
	})
}

// GetInstanceDetail 获取实例详情
func (s *Service) GetInstanceDetail(userID, instanceID uint) (*userModel.UserInstanceDetailResponse, error) {
	var instance providerModel.Instance
	err := global.APP_DB.Where("id = ? AND user_id = ?", instanceID, userID).
		First(&instance).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("实例不存在")
		}
		return nil, err
	}

	// 获取SSH端口映射的公网端口
	var sshPort int
	var sshPortMapping providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'", instanceID).First(&sshPortMapping).Error; err == nil {
		sshPort = sshPortMapping.HostPort // 使用映射的公网端口
	} else {
		sshPort = instance.SSHPort // fallback到默认值
	}

	detail := &userModel.UserInstanceDetailResponse{
		ID:          instance.ID,
		Name:        instance.Name,
		Type:        instance.InstanceType,
		Status:      instance.Status,
		CPU:         instance.CPU,
		Memory:      int(instance.Memory),
		Disk:        int(instance.Disk),
		Bandwidth:   instance.Bandwidth,
		OsType:      instance.OSType,
		PrivateIP:   instance.PrivateIP,   // 使用实例的内网IP
		PublicIP:    instance.PublicIP,    // 使用实例的公网IP
		IPv6Address: instance.IPv6Address, // 内网IPv6地址
		PublicIPv6:  instance.PublicIPv6,  // 公网IPv6地址
		SSHPort:     sshPort,              // 使用映射的公网端口
		Username:    instance.Username,
		Password:    instance.Password,
		CreatedAt:   instance.CreatedAt,
		ExpiresAt:   instance.ExpiresAt,
	}

	// 查询关联的 Provider 信息
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err == nil {
		detail.ProviderName = provider.Name
		detail.ProviderType = provider.Type // Provider虚拟化类型
		detail.ProviderStatus = provider.Status
		// 只有当实例没有公网IP时，才使用Provider的endpoint作为fallback
		if detail.PublicIP == "" {
			detail.PublicIP = s.extractIPFromEndpoint(provider.Endpoint)
		}
		detail.PortRangeStart = provider.PortRangeStart // 端口范围起始
		detail.PortRangeEnd = provider.PortRangeEnd     // 端口范围结束
		detail.NetworkType = provider.NetworkType       // 网络配置类型
	}

	// 查询关联的最新任务（如果有正在进行或待处理的任务）
	var task adminModel.Task
	if err := global.APP_DB.Where("instance_id = ? AND status IN (?, ?, ?)", instanceID, "pending", "processing", "running").
		Order("created_at DESC").
		First(&task).Error; err == nil {
		// 有关联任务，添加到响应中
		detail.RelatedTask = &userModel.UserTaskResponse{
			ID:            task.ID,
			UUID:          task.UUID,
			TaskType:      task.TaskType,
			Status:        task.Status,
			Progress:      task.Progress,
			StatusMessage: task.StatusMessage,
			CreatedAt:     task.CreatedAt,
			UpdatedAt:     task.UpdatedAt,
			StartedAt:     task.StartedAt,
			CompletedAt:   task.CompletedAt,
			ErrorMessage:  task.ErrorMessage,
			CancelReason:  task.CancelReason,
		}
		if task.ProviderID != nil {
			detail.RelatedTask.ProviderId = *task.ProviderID
			detail.RelatedTask.ProviderName = provider.Name
		}
		if task.InstanceID != nil {
			detail.RelatedTask.InstanceID = task.InstanceID
			detail.RelatedTask.InstanceName = instance.Name
		}
	}

	return detail, nil
}

// extractIPFromEndpoint 从endpoint中提取纯IP地址（使用全局函数）
func (s *Service) extractIPFromEndpoint(endpoint string) string {
	return utils.ExtractIPFromEndpoint(endpoint)
}

// GetInstanceMonitoring 获取实例监控数据
func (s *Service) GetInstanceMonitoring(userID, instanceID uint) (*userModel.InstanceMonitoringResponse, error) {
	// 首先验证实例是否属于该用户
	var instance providerModel.Instance
	if err := global.APP_DB.Where("id = ? AND user_id = ?", instanceID, userID).First(&instance).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("实例不存在或无权限访问")
		}
		return nil, fmt.Errorf("验证实例权限失败: %v", err)
	}

	// 获取用户信息
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return nil, fmt.Errorf("获取用户信息失败: %v", err)
	}

	// 计算用户总流量使用情况 - 用于流量限制判断
	trafficQueryService := trafficService.NewQueryService()
	year, month, _ := time.Now().Date()
	userMonthlyTrafficStats, err := trafficQueryService.GetUserMonthlyTraffic(userID, year, int(month))

	var userTotalMonthTraffic int64
	var usagePercent float64

	if err != nil {
		global.APP_LOG.Warn("获取用户总流量数据失败，使用默认值",
			zap.Uint("userID", userID),
			zap.Error(err))
		userTotalMonthTraffic = 0
		usagePercent = 0
	} else {
		userTotalMonthTraffic = int64(userMonthlyTrafficStats.ActualUsageMB)
		if user.TotalTraffic > 0 {
			usagePercent = float64(userTotalMonthTraffic) / float64(user.TotalTraffic) * 100
		}
	}

	// 获取当前实例的流量数据 - 用于显示
	instanceMonthlyTrafficStats, err := trafficQueryService.GetInstanceMonthlyTraffic(instanceID, year, int(month))
	var currentInstanceTraffic int64
	if err != nil {
		global.APP_LOG.Warn("获取实例流量数据失败，使用默认值",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		currentInstanceTraffic = 0
	} else {
		currentInstanceTraffic = int64(instanceMonthlyTrafficStats.ActualUsageMB)
	}

	// 检查流量限制状态
	var limitType, limitReason string

	// 检查实例是否因流量超限被限制
	if instance.TrafficLimited {
		// 判断限制类型
		userLimited := userTotalMonthTraffic >= user.TotalTraffic && user.TotalTraffic > 0
		var providerLimited bool

		// 检查Provider流量限制（使用统一的流量查询服务）
		var provider providerModel.Provider
		if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err == nil {
			providerMonthlyStats, providerErr := trafficQueryService.GetProviderMonthlyTraffic(provider.ID, year, int(month))
			if providerErr == nil && provider.MaxTraffic > 0 {
				providerLimited = int64(providerMonthlyStats.ActualUsageMB) >= provider.MaxTraffic
			}
		}

		if userLimited {
			limitType = "user"
			limitReason = "当前实例因用户流量已超限被系统自动限制，请等待下月自动重置或联系管理员。"
		} else if providerLimited {
			limitType = "provider"
			limitReason = "当前实例因Provider流量已超限被系统自动限制，请等待下月自动重置或联系管理员。"
		} else {
			limitType = "unknown"
			limitReason = "当前实例因流量超限被系统自动限制，请等待下月自动重置或联系管理员。"
		}
	}

	// 确保使用百分比被正确计算
	if usagePercent == 0.0 && user.TotalTraffic > 0 {
		usagePercent = float64(userTotalMonthTraffic) / float64(user.TotalTraffic) * 100
	}

	// 构建监控响应，显示实例流量数据
	monitoring := &userModel.InstanceMonitoringResponse{
		TrafficData: userModel.TrafficData{
			CurrentMonth: currentInstanceTraffic, // 显示实例流量，而非用户总流量
			TotalLimit:   user.TotalTraffic,
			UsagePercent: usagePercent,
			IsLimited:    instance.TrafficLimited,
			LimitType:    limitType,
			LimitReason:  limitReason,
			History:      []userModel.TrafficHistoryItem{},
		},
	}

	return monitoring, nil
}

// PerformInstanceAction 执行实例操作（兼容原方法名）
func (s *Service) PerformInstanceAction(userID uint, req userModel.InstanceActionRequest) error {
	return s.InstanceAction(userID, req)
}

// 获取外部服务的辅助函数
func getTaskService() interface {
	CreateTask(userID uint, providerID *uint, instanceID *uint, taskType string, taskData string, timeout int) (*adminModel.Task, error)
} {
	// 返回真实的 TaskService
	return task.GetTaskService()
}

// GetUserTrafficUsageWithPmacct 获取用户流量使用情况（使用pmacct数据）
func (s *Service) GetUserTrafficUsageWithPmacct(userID uint) (map[string]interface{}, error) {
	// 获取用户流量使用情况
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return nil, err
	}

	// 获取用户流量限制
	tService := trafficService.NewService()
	trafficLimit := tService.GetUserTrafficLimitByLevel(user.Level)

	// 简化的流量使用查询（包含已删除实例，保证累计值准确）
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 使用统一的流量查询服务（从pmacct_traffic_records实时聚合）
	queryService := trafficService.NewQueryService()
	monthlyStats, err := queryService.GetUserMonthlyTraffic(userID, year, month)
	if err != nil {
		return nil, err
	}

	totalUsed := int64(monthlyStats.ActualUsageMB)

	return map[string]interface{}{
		"used":      totalUsed,
		"limit":     trafficLimit,
		"remaining": trafficLimit - totalUsed,
	}, nil
}

// HasInstanceAccess 检查用户是否有权限访问实例
func (s *Service) HasInstanceAccess(userID, instanceID uint) bool {
	// 通过查询实例是否属于该用户来验证权限
	count := int64(0)
	err := global.APP_DB.Model(&providerModel.Instance{}).Where("id = ? AND user_id = ?", instanceID, userID).Count(&count).Error
	return err == nil && count > 0
}

// ResetInstancePassword 重置实例密码
func (s *Service) ResetInstancePassword(userID uint, instanceID uint) (uint, error) {
	// 验证实例所有权
	if !s.HasInstanceAccess(userID, instanceID) {
		return 0, errors.New("无权限访问此实例")
	}

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return 0, fmt.Errorf("实例不存在: %w", err)
	}

	// 检查实例状态
	if instance.Status != "running" {
		return 0, errors.New("只有运行中的实例才能重置密码")
	}

	// 创建重置密码任务
	taskService := task.GetTaskService()
	taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instanceID, instance.ProviderID)
	taskModel, err := taskService.CreateTask(userID, &instance.ProviderID, &instanceID, "reset-password", taskData, 1800)
	if err != nil {
		return 0, fmt.Errorf("创建重置密码任务失败: %w", err)
	}

	global.APP_LOG.Info("用户创建实例密码重置任务",
		zap.Uint("userID", userID),
		zap.Uint("instanceID", instanceID),
		zap.Uint("taskID", taskModel.ID))

	return taskModel.ID, nil
}

// GetInstanceNewPassword 获取实例新密码
func (s *Service) GetInstanceNewPassword(userID uint, instanceID uint, taskID uint) (string, int64, error) {
	// 验证实例所有权
	if !s.HasInstanceAccess(userID, instanceID) {
		return "", 0, errors.New("无权限访问此实例")
	}

	// 获取任务信息
	var taskModel adminModel.Task
	if err := global.APP_DB.Where("id = ? AND user_id = ? AND instance_id = ?", taskID, userID, instanceID).First(&taskModel).Error; err != nil {
		return "", 0, fmt.Errorf("任务不存在或无权限: %w", err)
	}

	// 检查任务类型
	if taskModel.TaskType != "reset-password" {
		return "", 0, errors.New("任务类型不正确")
	}

	// 检查任务状态
	if taskModel.Status != "completed" {
		return "", 0, errors.New("密码重置任务尚未完成")
	}

	// 解析任务结果获取新密码
	var taskResult map[string]interface{}
	if err := json.Unmarshal([]byte(taskModel.TaskData), &taskResult); err != nil {
		return "", 0, fmt.Errorf("解析任务结果失败: %w", err)
	}

	newPassword, exists := taskResult["newPassword"].(string)
	if !exists || newPassword == "" {
		return "", 0, errors.New("任务结果中未找到新密码")
	}

	// 获取重置时间
	var resetTime int64
	if resetTimeFloat, ok := taskResult["resetTime"].(float64); ok {
		resetTime = int64(resetTimeFloat)
	} else {
		// 如果没有重置时间，使用任务完成时间
		if taskModel.CompletedAt != nil {
			resetTime = taskModel.CompletedAt.Unix()
		}
	}

	global.APP_LOG.Info("用户获取实例新密码",
		zap.Uint("userID", userID),
		zap.Uint("instanceID", instanceID),
		zap.Uint("taskID", taskID))

	return newPassword, resetTime, nil
}
