package resource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/constant"
	"oneclickvirt/service/cache"
	"oneclickvirt/service/database"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/taskgate"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service 处理用户资源相关功能
type Service struct{}

// NewService 创建资源服务
func NewService() *Service {
	return &Service{}
}

// GetAvailableResources 获取可用资源列表
func (s *Service) GetAvailableResources(req userModel.AvailableResourcesRequest) ([]userModel.AvailableResourceResponse, int64, error) {
	var providers []providerModel.Provider
	var total int64

	// 可用性口径：标准节点看 active/partial，agent 节点仅看在线状态
	query := global.APP_DB.Model(&providerModel.Provider{}).
		Where("((connection_type <> ? AND status IN (?, ?)) OR (connection_type = ? AND agent_status = ?)) AND allow_claim = ?", "agent", "active", "partial", "agent", "online", true)

	if req.Country != "" {
		query = query.Where("country = ?", req.Country)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Offset(offset).Limit(req.PageSize).Find(&providers).Error; err != nil {
		return nil, 0, err
	}

	// 批量查询活跃的预留资源
	var providerIDs []uint
	for _, provider := range providers {
		providerIDs = append(providerIDs, provider.ID)
	}

	var allReservations []resourceModel.ResourceReservation
	if len(providerIDs) > 0 {
		if err := global.APP_DB.Where("provider_id IN ? AND expires_at > ?",
			providerIDs, time.Now()).Find(&allReservations).Error; err != nil {
			global.APP_LOG.Warn("批量查询预留资源失败", zap.Error(err))
		}
	}

	// 按provider_id分组预留资源
	reservationsByProvider := make(map[uint][]resourceModel.ResourceReservation)
	for _, reservation := range allReservations {
		reservationsByProvider[reservation.ProviderID] = append(
			reservationsByProvider[reservation.ProviderID], reservation)
	}

	var resourceResponses []userModel.AvailableResourceResponse
	for _, provider := range providers {
		// 从预加载的数据中获取该provider的预留资源
		activeReservations := reservationsByProvider[provider.ID]

		// 计算预留资源占用
		reservedContainers := 0
		reservedVMs := 0
		for _, reservation := range activeReservations {
			if reservation.InstanceType == "vm" {
				reservedVMs++
			} else {
				reservedContainers++
			}
		}

		// 计算实际可用配额（考虑预留资源）
		actualUsedQuota := provider.UsedQuota
		reservedQuota := reservedContainers + reservedVMs
		availableQuota := provider.TotalQuota - actualUsedQuota - reservedQuota

		// 确保不出现负数
		if availableQuota < 0 {
			availableQuota = 0
		}

		resourceResponse := userModel.AvailableResourceResponse{
			ID:                    provider.ID,
			Name:                  provider.Name,
			Type:                  provider.Type,
			Region:                provider.Region,
			Country:               provider.Country,
			CountryCode:           provider.CountryCode,
			ContainerEnabled:      provider.ContainerEnabled,
			VirtualMachineEnabled: provider.VirtualMachineEnabled,
			AvailableQuota:        availableQuota, // 减去预留的配额
			Status:                provider.Status,
		}

		resourceResponses = append(resourceResponses, resourceResponse)
	}

	return resourceResponses, total, nil
}

// ClaimResource 申领资源。旧接口仍可用，但必须进入统一创建任务流水线，
// 避免直接落库 creating 实例后绕过 Provider 创建、回滚和任务池控制。
func (s *Service) ClaimResource(userID uint, req userModel.ClaimResourceRequest) (*adminModel.Task, error) {
	if err := taskgate.EnsureAccepting(); err != nil {
		return nil, err
	}
	if req.InstanceType != "container" && req.InstanceType != "vm" {
		return nil, errors.New("实例类型必须为container或vm")
	}
	if req.CPU <= 0 || req.Memory <= 0 || req.Disk <= 0 {
		return nil, errors.New("CPU、内存和磁盘必须大于0")
	}

	dbService := database.GetDatabaseService()
	quotaService := resources.NewQuotaService()
	reservationService := resources.GetResourceReservationService()
	sessionID := resources.GenerateSessionID()

	var task *adminModel.Task
	var provider providerModel.Provider
	err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := taskgate.EnsureAcceptingInTx(tx); err != nil {
			return err
		}

		var currentUser userModel.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&currentUser, userID).Error; err != nil {
			return fmt.Errorf("获取用户信息失败: %v", err)
		}
		if currentUser.Status != 1 {
			return errors.New("用户账户已被禁用")
		}

		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&provider, req.ProviderID).Error; err != nil {
			return errors.New("提供商不存在")
		}
		if !provider.AllowClaim {
			return errors.New("该提供商不允许申领")
		}
		if provider.RedeemCodeOnly {
			return errors.New("该提供商仅支持兑换码领取")
		}
		if provider.IsFrozen {
			return errors.New("提供商已被冻结")
		}
		if provider.ExpiresAt != nil && provider.ExpiresAt.Before(time.Now()) {
			return errors.New("提供商已过期")
		}
		if provider.TrafficLimited {
			return errors.New("该提供商因流量超限暂时不可用")
		}
		providerAvailable := (provider.ConnectionType == "agent" && provider.AgentStatus == "online") ||
			(provider.ConnectionType != "agent" && (provider.Status == "active" || provider.Status == "partial"))
		if !providerAvailable {
			return errors.New("提供商不可用")
		}
		if req.InstanceType == "container" && !provider.ContainerEnabled {
			return errors.New("该提供商未启用容器实例")
		}
		if req.InstanceType == "vm" && !provider.VirtualMachineEnabled {
			return errors.New("该提供商未启用虚拟机实例")
		}

		bandwidth := provider.DefaultInboundBandwidth
		if bandwidth < 0 {
			bandwidth = 0
		}
		quotaReq := resources.ResourceRequest{
			UserID:       userID,
			CPU:          req.CPU,
			Memory:       req.Memory,
			Disk:         req.Disk,
			Bandwidth:    bandwidth,
			InstanceType: req.InstanceType,
			ProviderID:   req.ProviderID,
		}
		quotaResult, err := quotaService.ValidateInTransaction(tx, quotaReq)
		if err != nil {
			return fmt.Errorf("配额验证失败: %v", err)
		}
		if !quotaResult.Allowed {
			return errors.New(quotaResult.Reason)
		}

		containerCount := provider.ContainerCount
		vmCount := provider.VMCount
		if provider.CountCacheExpiry == nil || time.Now().After(*provider.CountCacheExpiry) {
			var freshContainerCount, freshVMCount int64
			if err := tx.Model(&providerModel.Instance{}).
				Where("provider_id = ? AND instance_type = ? AND deleted_at IS NULL AND status NOT IN (?)",
					provider.ID, "container", constant.GetTerminalStatuses()).
				Count(&freshContainerCount).Error; err != nil {
				return fmt.Errorf("统计节点容器数量失败: %v", err)
			}
			if err := tx.Model(&providerModel.Instance{}).
				Where("provider_id = ? AND instance_type = ? AND deleted_at IS NULL AND status NOT IN (?)",
					provider.ID, "vm", constant.GetTerminalStatuses()).
				Count(&freshVMCount).Error; err != nil {
				return fmt.Errorf("统计节点虚拟机数量失败: %v", err)
			}
			containerCount = int(freshContainerCount)
			vmCount = int(freshVMCount)
		}

		var reservedByType struct {
			ReservedContainers int64
			ReservedVMs        int64
		}
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Model(&resourceModel.ResourceReservation{}).
			Select("COALESCE(SUM(CASE WHEN instance_type = 'vm' THEN 0 ELSE 1 END), 0) AS reserved_containers, COALESCE(SUM(CASE WHEN instance_type = 'vm' THEN 1 ELSE 0 END), 0) AS reserved_vms").
			Where("provider_id = ? AND expires_at > ?", provider.ID, time.Now()).
			Scan(&reservedByType).Error; err != nil {
			return fmt.Errorf("统计节点预留资源失败: %v", err)
		}
		containerCount += int(reservedByType.ReservedContainers)
		vmCount += int(reservedByType.ReservedVMs)

		if req.InstanceType == "container" && provider.MaxContainerInstances > 0 && containerCount >= provider.MaxContainerInstances {
			return fmt.Errorf("节点容器数量已达上限：%d/%d", containerCount, provider.MaxContainerInstances)
		}
		if req.InstanceType == "vm" && provider.MaxVMInstances > 0 && vmCount >= provider.MaxVMInstances {
			return fmt.Errorf("节点虚拟机数量已达上限：%d/%d", vmCount, provider.MaxVMInstances)
		}

		providerLevelLimits, err := quotaService.GetProviderLevelLimitsInTx(tx, req.ProviderID, currentUser.Level)
		if err == nil && providerLevelLimits != nil && providerLevelLimits.MaxInstances > 0 {
			currentProviderInstances, err := quotaService.GetCurrentProviderInstanceCountInTx(tx, userID, req.ProviderID)
			if err != nil {
				return fmt.Errorf("获取节点实例数量失败: %v", err)
			}
			if currentProviderInstances >= providerLevelLimits.MaxInstances {
				return fmt.Errorf("该节点实例数量已达上限：当前在此节点 %d/%d", currentProviderInstances, providerLevelLimits.MaxInstances)
			}
		}

		if err := reservationService.ReserveResourcesInTx(tx, userID, req.ProviderID, sessionID,
			req.InstanceType, req.CPU, req.Memory, req.Disk, bandwidth); err != nil {
			global.APP_LOG.Error("预留资源失败",
				zap.Uint("userID", userID),
				zap.String("sessionId", sessionID),
				zap.Error(err))
			return fmt.Errorf("资源分配失败: %v", err)
		}

		networkType := provider.NetworkType
		if networkType == "" {
			networkType = "nat_ipv4"
		}
		taskReq := adminModel.CreateInstanceTaskRequest{
			ProviderId:   provider.ID,
			AdminDirect:  true,
			Name:         req.Name,
			Image:        req.Image,
			CPU:          req.CPU,
			Memory:       req.Memory,
			DiskMB:       req.Disk,
			Bandwidth:    bandwidth,
			InstanceType: req.InstanceType,
			NetworkType:  networkType,
			SessionId:    sessionID,
		}
		taskData, err := json.Marshal(taskReq)
		if err != nil {
			return fmt.Errorf("序列化创建任务失败: %w", err)
		}

		estimatedDuration := 300
		if req.InstanceType == "vm" {
			estimatedDuration = 600
		}
		providerID := provider.ID
		newTask := &adminModel.Task{
			UserID:                userID,
			ProviderID:            &providerID,
			TaskType:              "create",
			TaskData:              string(taskData),
			Status:                "pending",
			TimeoutDuration:       2400,
			IsForceStoppable:      true,
			EstimatedDuration:     estimatedDuration,
			PreallocatedCPU:       req.CPU,
			PreallocatedMemory:    int(req.Memory),
			PreallocatedDisk:      int(req.Disk),
			PreallocatedBandwidth: bandwidth,
		}
		if err := tx.Create(newTask).Error; err != nil {
			return fmt.Errorf("创建任务失败: %v", err)
		}
		task = newTask
		return nil
	})

	if err != nil {
		return nil, err
	}

	cache.GetUserCacheService().InvalidateUserCache(userID)
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	global.APP_LOG.Info("申领资源任务已提交",
		zap.Uint("userId", userID),
		zap.Uint("providerId", req.ProviderID),
		zap.Uint("taskId", task.ID),
		zap.String("sessionId", sessionID))

	return task, nil
}
