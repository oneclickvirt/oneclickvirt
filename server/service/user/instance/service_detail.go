package instance

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	trafficService "oneclickvirt/service/traffic"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

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

	// 并发查询 SSH端口映射、Provider信息、关联任务（消除N+1问题）
	var (
		sshPortMapping providerModel.Port
		provider       providerModel.Provider
		task           adminModel.Task
		hasSshMapping  bool
		hasProvider    bool
		hasTask        bool
	)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'", instanceID).First(&sshPortMapping).Error; err == nil {
			hasSshMapping = true
		}
	}()
	go func() {
		defer wg.Done()
		if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err == nil {
			hasProvider = true
		}
	}()
	go func() {
		defer wg.Done()
		if err := global.APP_DB.Where("instance_id = ? AND status IN (?, ?, ?)", instanceID, "pending", "processing", "running").
			Order("created_at DESC").
			First(&task).Error; err == nil {
			hasTask = true
		}
	}()
	wg.Wait()

	// 确定SSH端口
	var sshPort int
	if hasSshMapping {
		sshPort = sshPortMapping.HostPort // 使用映射的公网端口
	} else {
		sshPort = instance.SSHPort // fallback到默认值
	}

	detail := &userModel.UserInstanceDetailResponse{
		ID:            instance.ID,
		Name:          instance.Name,
		Type:          instance.InstanceType,
		InstanceType:  instance.InstanceType,
		Status:        instance.Status,
		CPU:           instance.CPU,
		Memory:        int(instance.Memory),
		Disk:          int(instance.Disk),
		Bandwidth:     instance.Bandwidth,
		OsType:        instance.OSType,
		Image:         instance.Image,
		ProviderID:    instance.ProviderID,
		PrivateIP:     instance.PrivateIP,   // 使用实例的内网IP
		PublicIP:      instance.PublicIP,    // 使用实例的公网IP
		IPv6Address:   instance.IPv6Address, // 内网IPv6地址
		PublicIPv6:    instance.PublicIPv6,  // 公网IPv6地址
		SSHPort:       sshPort,              // 使用映射的公网端口
		Username:      instance.Username,
		Password:      instance.Password,
		ProviderName:  instance.Provider,
		HasSshMapping: hasSshMapping,        // 是否有可用的SSH端口映射
		NetworkType:   instance.NetworkType, // 默认使用实例的网络类型（创建时从Provider继承）
		CreatedAt:     instance.CreatedAt,
		ExpiresAt:     instance.ExpiresAt,
	}

	if hasProvider {
		detail.ProviderName = provider.Name
		detail.ProviderType = provider.Type // Provider虚拟化类型
		detail.ProviderStatus = provider.Status

		// agent录入模式+无端口映射模式：不显示公网IP
		// 因为该模式下的端口转发是通过控制端内网穿透实现的，节点本身没有对外的公网IP
		if provider.ConnectionType == "agent" && provider.NetworkType == "no_port_mapping" {
			detail.PublicIP = ""
		} else if detail.PublicIP == "" {
			// 只有当实例没有公网IP且不是agent+no_port_mapping时，才使用Provider的endpoint作为fallback
			detail.PublicIP = s.extractIPFromEndpoint(provider.Endpoint)
		}

		detail.PortRangeStart = provider.PortRangeStart // 端口范围起始
		detail.PortRangeEnd = provider.PortRangeEnd     // 端口范围结束
		// 优先使用Provider的网络类型（权威来源），兼容旧实例未设置NetworkType的情况
		if provider.NetworkType != "" {
			detail.NetworkType = provider.NetworkType
		}
	}

	if hasTask {
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
			detail.RelatedTask.ProviderName = detail.ProviderName
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
