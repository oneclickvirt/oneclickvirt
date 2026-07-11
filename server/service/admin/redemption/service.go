package redemption

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/taskgate"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 兑换码管理服务
type Service struct {
	taskService interfaces.TaskServiceInterface
}

// NewService 创建兑换码管理服务
func NewService(taskService interfaces.TaskServiceInterface) *Service {
	return &Service{taskService: taskService}
}

// GetList 获取兑换码列表（分页+筛选）
func (s *Service) GetList(req adminModel.RedemptionCodeListRequest, ownerAdminID uint) ([]adminModel.RedemptionCodeResponse, int64, error) {
	var codes []systemModel.RedemptionCode
	var total int64

	query := global.APP_DB.Model(&systemModel.RedemptionCode{})

	// 普通管理员数据隔离：只能看到归属自己的Provider的兑换码
	if ownerAdminID > 0 {
		providerIDs := global.APP_DB.Model(&providerModel.Provider{}).
			Select("id").
			Where("owner_admin_id = ?", ownerAdminID)
		query = query.Where("provider_id IN (?)", providerIDs)
	}

	if req.Code != "" {
		query = query.Where("code LIKE ?", "%"+req.Code+"%")
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}
	if req.ProviderID != 0 {
		query = query.Where("provider_id = ?", req.ProviderID)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(req.PageSize).Find(&codes).Error; err != nil {
		return nil, 0, err
	}

	// 批量查询创建者用户名，避免 N+1
	creatorIDSet := make(map[uint]bool)
	for _, c := range codes {
		if c.CreatedBy != 0 {
			creatorIDSet[c.CreatedBy] = true
		}
	}
	creatorIDs := make([]uint, 0, len(creatorIDSet))
	for id := range creatorIDSet {
		creatorIDs = append(creatorIDs, id)
	}

	userMap := make(map[uint]string)
	if len(creatorIDs) > 0 {
		var users []userModel.User
		if err := global.APP_DB.Select("id, username").Where("id IN ?", creatorIDs).Limit(500).Find(&users).Error; err != nil {
			// 查询用户失败时记录日志但不中断流程，仅返回没有用户名的兑换码列表
			global.APP_LOG.Warn("查询兑换码创建者用户信息失败，将返回不含用户名的列表",
				zap.Error(err),
				zap.Int("creatorCount", len(creatorIDs)))
		} else {
			for _, u := range users {
				userMap[u.ID] = u.Username
			}
		}
	}

	// 批量查询关联实例名称，避免 N+1
	instanceIDSet := make(map[uint]bool)
	for _, c := range codes {
		if c.InstanceID != nil && *c.InstanceID != 0 {
			instanceIDSet[*c.InstanceID] = true
		}
	}
	instanceIDs := make([]uint, 0, len(instanceIDSet))
	for id := range instanceIDSet {
		instanceIDs = append(instanceIDs, id)
	}

	instanceNameMap := make(map[uint]string)
	if len(instanceIDs) > 0 {
		var instances []providerModel.Instance
		if err := global.APP_DB.Select("id, name").Where("id IN ?", instanceIDs).Limit(500).Find(&instances).Error; err != nil {
			global.APP_LOG.Warn("查询兑换码关联实例名称失败",
				zap.Error(err),
				zap.Int("instanceCount", len(instanceIDs)))
		} else {
			for _, inst := range instances {
				instanceNameMap[inst.ID] = inst.Name
			}
		}
	}

	result := make([]adminModel.RedemptionCodeResponse, 0, len(codes))
	for _, c := range codes {
		resp := adminModel.RedemptionCodeResponse{
			RedemptionCode: c,
			CreatedByUser:  userMap[c.CreatedBy],
		}
		if c.InstanceID != nil && *c.InstanceID != 0 {
			resp.InstanceName = instanceNameMap[*c.InstanceID]
		}
		if spec, err := constant.GetCPUSpecByID(c.CPUId); err == nil && spec != nil {
			resp.CPUName = spec.Name
		}
		if spec, err := constant.GetMemorySpecByID(c.MemoryId); err == nil && spec != nil {
			resp.MemoryName = spec.Name
		}
		if spec, err := constant.GetDiskSpecByID(c.DiskId); err == nil && spec != nil {
			resp.DiskName = spec.Name
		}
		if spec, err := constant.GetBandwidthSpecByID(c.BandwidthId); err == nil && spec != nil {
			resp.BandwidthName = spec.Name
		}
		result = append(result, resp)
	}

	return result, total, nil
}

// BatchCreate 批量创建兑换码：生成 Code -> 插入 DB (status=pending_create) -> 创建 create_redemption_instance 任务
func (s *Service) BatchCreate(req adminModel.BatchCreateRedemptionCodesRequest, adminID, ownerAdminID uint) error {
	dbService := database.GetDatabaseService()

	if req.CreationMode == "" {
		req.CreationMode = "standard"
	}
	if req.CreationMode != "standard" && req.CreationMode != "copy" {
		return fmt.Errorf("无效的创建模式")
	}

	// 验证 Provider 存在
	var provider providerModel.Provider
	providerQuery := global.APP_DB.Where("id = ?", req.ProviderID)
	if ownerAdminID > 0 {
		providerQuery = providerQuery.Where("owner_admin_id = ?", ownerAdminID)
	}
	if err := providerQuery.First(&provider).Error; err != nil {
		return fmt.Errorf("节点不存在或不可用")
	}
	providerAvailable := (provider.ConnectionType == "agent" && provider.AgentStatus == "online") ||
		(provider.ConnectionType != "agent" && (provider.Status == "active" || provider.Status == "partial"))
	if !providerAvailable {
		return fmt.Errorf("节点不存在或不可用")
	}

	// 复制模式：LXD/Incus 和 Docker 家族容器节点支持
	if req.CreationMode == "copy" {
		if !utils.SupportsContainerCopyProvider(provider.Type) {
			return fmt.Errorf("复制模式仅支持 LXD/Incus/Docker/Podman/Containerd/Orbstack 类型的节点")
		}
		req.InstanceType = "container"
		req.ImageId = 0
		req.CPUId = ""
		req.MemoryId = ""
		req.DiskId = ""
		req.BandwidthId = ""
		if req.SourceContainer == "" {
			return fmt.Errorf("复制模式必须指定源容器名称")
		}
		if utils.IsDockerFamilyProvider(provider.Type) {
			if !utils.IsValidContainerRuntimeName(req.SourceContainer) {
				return fmt.Errorf("源容器名称格式无效")
			}
		} else if !utils.IsValidLXDInstanceName(req.SourceContainer) {
			return fmt.Errorf("源容器名称格式无效")
		}
	} else {
		if req.ImageId == 0 {
			return fmt.Errorf("请选择镜像")
		}
		if req.CPUId == "" || req.MemoryId == "" || req.DiskId == "" || req.BandwidthId == "" {
			return fmt.Errorf("请选择完整的实例规格")
		}
		req.SourceContainer = ""
	}

	// GPU 直通支持 LXD/Incus 原生设备配置，Docker 家族使用 best-effort run 参数
	isContainerTarget := req.InstanceType == "container" || req.CreationMode == "copy"
	if req.GpuEnabled && (!utils.SupportsContainerGPUProvider(provider.Type, "container") || !isContainerTarget) {
		return fmt.Errorf("GPU 直通仅支持 LXD/Incus/Docker/Podman/Containerd/Orbstack 的容器实例或容器复制模式")
	}
	if !req.GpuEnabled {
		req.GpuDeviceIds = ""
	} else if err := validateGPUDeviceIDs(req.GpuDeviceIds); err != nil {
		return err
	}

	// 验证规格 ID 并计算本次批量创建所需的总资源量
	// 复制模式无需规格（资源继承自源容器），跳过规格验证和容量检查
	isCopyMode := req.CreationMode == "copy"

	var cpuSpec *constant.CPUSpec
	var memorySpec *constant.MemorySpec
	var diskSpec *constant.DiskSpec
	var bandwidthSpec *constant.BandwidthSpec

	if !isCopyMode {
		var err error
		cpuSpec, err = constant.GetCPUSpecByID(req.CPUId)
		if err != nil {
			return fmt.Errorf("无效的CPU规格: %v", err)
		}
		memorySpec, err = constant.GetMemorySpecByID(req.MemoryId)
		if err != nil {
			return fmt.Errorf("无效的内存规格: %v", err)
		}
		diskSpec, err = constant.GetDiskSpecByID(req.DiskId)
		if err != nil {
			return fmt.Errorf("无效的磁盘规格: %v", err)
		}
		bandwidthSpec, err = constant.GetBandwidthSpecByID(req.BandwidthId)
		if err != nil {
			return fmt.Errorf("无效的带宽规格: %v", err)
		}
	}

	// 根据实例类型和节点的超分配配置，决定哪些资源项需要做容量检查
	// ContainerLimitCPU/Memory/Disk 和 VMLimitCPU/Memory/Disk 为 true 时表示该资源不允许超开
	isContainer := req.InstanceType == "container"
	checkCPU := !isCopyMode && ((isContainer && provider.ContainerLimitCPU) || (!isContainer && provider.VMLimitCPU))
	checkMemory := !isCopyMode && ((isContainer && provider.ContainerLimitMemory) || (!isContainer && provider.VMLimitMemory))
	checkDisk := !isCopyMode && ((isContainer && provider.ContainerLimitDisk) || (!isContainer && provider.VMLimitDisk))

	var requiredCPU int
	var requiredMemoryMB, requiredDiskMB int64
	if !isCopyMode && cpuSpec != nil && memorySpec != nil && diskSpec != nil {
		requiredCPU = cpuSpec.Cores * req.Count
		requiredMemoryMB = int64(memorySpec.SizeMB) * int64(req.Count)
		requiredDiskMB = int64(diskSpec.SizeMB) * int64(req.Count)
	}

	if checkCPU && provider.NodeCPUCores > 0 {
		availCPU := provider.NodeCPUCores - provider.UsedCPUCores
		if requiredCPU > availCPU {
			return fmt.Errorf("节点CPU资源不足：需要 %d 核，当前可用 %d 核", requiredCPU, availCPU)
		}
	}
	if checkMemory && provider.NodeMemoryTotal > 0 {
		availMemMB := provider.NodeMemoryTotal - provider.UsedMemory
		if requiredMemoryMB > availMemMB {
			return fmt.Errorf("节点内存资源不足：需要 %d MB，当前可用 %d MB", requiredMemoryMB, availMemMB)
		}
	}
	if checkDisk && provider.NodeDiskTotal > 0 {
		availDiskMB := provider.NodeDiskTotal - provider.UsedDisk
		if requiredDiskMB > availDiskMB {
			return fmt.Errorf("节点磁盘资源不足：需要 %d MB，当前可用 %d MB", requiredDiskMB, availDiskMB)
		}
	}

	codes, err := s.generateUniqueCodes(req.Count)
	if err != nil {
		return err
	}
	reserveCPU := 0
	reserveMemory := int64(0)
	reserveDisk := int64(0)
	reserveBandwidth := 0
	if !isCopyMode {
		reserveCPU = cpuSpec.Cores
		reserveMemory = int64(memorySpec.SizeMB)
		reserveDisk = int64(diskSpec.SizeMB)
		reserveBandwidth = bandwidthSpec.SpeedMbps
	}
	sessionIDs := make([]string, req.Count)
	for i := range sessionIDs {
		sessionIDs[i] = resources.GenerateSessionID()
	}

	resourceService := &resources.ResourceService{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		resourceResult, err := resourceService.CheckProviderResourcesWithTx(tx, resourceModel.ResourceCheckRequest{
			ProviderID:   req.ProviderID,
			InstanceType: req.InstanceType,
			CPU:          reserveCPU * req.Count,
			Memory:       reserveMemory * int64(req.Count),
			Disk:         reserveDisk * int64(req.Count),
		})
		if err != nil {
			return fmt.Errorf("Provider资源检查失败: %w", err)
		}
		if !resourceResult.Allowed {
			return fmt.Errorf("Provider资源不足: %s", resourceResult.Reason)
		}

		redemptionCodes := make([]systemModel.RedemptionCode, req.Count)
		for i := range redemptionCodes {
			redemptionCodes[i] = systemModel.RedemptionCode{
				Code:            codes[i],
				Status:          systemModel.RedemptionStatusPendingCreate,
				ProviderID:      req.ProviderID,
				ProviderName:    provider.Name,
				InstanceType:    req.InstanceType,
				ImageId:         req.ImageId,
				CPUId:           req.CPUId,
				MemoryId:        req.MemoryId,
				DiskId:          req.DiskId,
				BandwidthId:     req.BandwidthId,
				CreatedBy:       adminID,
				Remark:          req.Remark,
				CreationMode:    req.CreationMode,
				SourceContainer: req.SourceContainer,
				GpuEnabled:      req.GpuEnabled,
				GpuDeviceIds:    req.GpuDeviceIds,
			}
		}
		if err := tx.CreateInBatches(&redemptionCodes, 100).Error; err != nil {
			return fmt.Errorf("批量创建兑换码失败: %w", err)
		}

		expiresAt := time.Now().Add(time.Hour)
		reservations := make([]resourceModel.ResourceReservation, req.Count)
		tasks := make([]adminModel.Task, req.Count)
		for i := range redemptionCodes {
			reservations[i] = resourceModel.ResourceReservation{
				UserID:       0,
				ProviderID:   req.ProviderID,
				SessionID:    sessionIDs[i],
				InstanceType: req.InstanceType,
				CPU:          reserveCPU,
				Memory:       reserveMemory,
				Disk:         reserveDisk,
				Bandwidth:    reserveBandwidth,
				ExpiresAt:    expiresAt,
			}
			taskDataReq := adminModel.CreateRedemptionInstanceTaskRequest{
				ProviderId:       req.ProviderID,
				ImageId:          req.ImageId,
				CPUId:            req.CPUId,
				MemoryId:         req.MemoryId,
				DiskId:           req.DiskId,
				BandwidthId:      req.BandwidthId,
				SessionId:        sessionIDs[i],
				CreationMode:     req.CreationMode,
				SourceContainer:  req.SourceContainer,
				GpuEnabled:       req.GpuEnabled,
				GpuDeviceIds:     req.GpuDeviceIds,
				RedemptionCodeID: redemptionCodes[i].ID,
			}
			taskDataJSON, err := json.Marshal(taskDataReq)
			if err != nil {
				return fmt.Errorf("第 %d 个兑换码任务数据序列化失败: %w", i+1, err)
			}

			providerID := req.ProviderID
			timeoutDuration := utils.GetDefaultTaskTimeout("create_redemption_instance")
			if timeoutDuration <= 0 {
				timeoutDuration = 2400
			}
			estimatedDuration := 300
			if req.InstanceType == "vm" {
				estimatedDuration = 600
			}
			tasks[i] = adminModel.Task{
				UserID:                adminID,
				ProviderID:            &providerID,
				TaskType:              "create_redemption_instance",
				TaskData:              string(taskDataJSON),
				Status:                "pending",
				TimeoutDuration:       timeoutDuration,
				IsForceStoppable:      true,
				EstimatedDuration:     estimatedDuration,
				PreallocatedCPU:       reserveCPU,
				PreallocatedMemory:    int(reserveMemory),
				PreallocatedDisk:      int(reserveDisk),
				PreallocatedBandwidth: reserveBandwidth,
			}
		}
		if err := tx.CreateInBatches(&reservations, 100).Error; err != nil {
			return fmt.Errorf("批量预留兑换码资源失败: %w", err)
		}
		if err := tx.CreateInBatches(&tasks, 100).Error; err != nil {
			return fmt.Errorf("批量创建兑换码任务失败: %w", err)
		}
		for i := range redemptionCodes {
			redemptionCodes[i].Status = systemModel.RedemptionStatusCreating
			redemptionCodes[i].TaskID = &tasks[i].ID
		}
		if err := tx.Save(&redemptionCodes).Error; err != nil {
			return fmt.Errorf("批量关联兑换码任务失败: %w", err)
		}
		return nil
	})
	if err != nil {
		global.APP_LOG.Error("批量创建兑换码失败", zap.Error(err))
		return err
	}

	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	return nil
}

// BatchDelete 批量删除兑换码（硬删除），同时清理对应实例
// - pending_use: 创建实例删除任务 + 硬删除兑换码
// - pending_create / creating: 取消任务 + 硬删除兑换码
// - used: 创建实例删除任务 + 硬删除兑换码（无论已兑换与否，实例一并删除）
// - deleting: 跳过（已在处理中）
func (s *Service) BatchDelete(ids []uint, adminID, ownerAdminID uint) error {
	if len(ids) == 0 {
		return fmt.Errorf("请选择要删除的兑换码")
	}

	ids = uniqueUintIDs(ids)
	if len(ids) == 0 || len(ids) > 100 {
		return fmt.Errorf("兑换码数量必须在1到100之间")
	}
	var codes []systemModel.RedemptionCode
	query := global.APP_DB.Where("redemption_codes.id IN ?", ids)
	if ownerAdminID > 0 {
		providerIDs := global.APP_DB.Model(&providerModel.Provider{}).
			Select("id").
			Where("owner_admin_id = ?", ownerAdminID)
		query = query.Where("redemption_codes.provider_id IN (?)", providerIDs)
	}
	if err := query.Find(&codes).Error; err != nil {
		return fmt.Errorf("查询兑换码失败: %v", err)
	}
	if len(codes) != len(ids) {
		return fmt.Errorf("部分兑换码不存在或无权限")
	}
	for _, code := range codes {
		if code.Status == systemModel.RedemptionStatusDeleting {
			return fmt.Errorf("兑换码 %d 正在删除中，请稍后重试", code.ID)
		}
	}

	linkedTaskIDs := make([]uint, 0, len(codes))
	instanceIDSet := make(map[uint]struct{}, len(codes))
	for _, code := range codes {
		if code.TaskID != nil {
			linkedTaskIDs = append(linkedTaskIDs, *code.TaskID)
		}
		if code.InstanceID != nil {
			instanceIDSet[*code.InstanceID] = struct{}{}
		}
	}

	var linkedTasks []adminModel.Task
	if len(linkedTaskIDs) > 0 {
		if err := global.APP_DB.Where("id IN ?", linkedTaskIDs).Find(&linkedTasks).Error; err != nil {
			return fmt.Errorf("批量查询兑换码任务失败: %w", err)
		}
	}
	linkedTaskMap := make(map[uint]adminModel.Task, len(linkedTasks))
	for _, linkedTask := range linkedTasks {
		linkedTaskMap[linkedTask.ID] = linkedTask
		if linkedTask.Status == "completed" && linkedTask.InstanceID != nil {
			instanceIDSet[*linkedTask.InstanceID] = struct{}{}
		}
	}

	instanceIDs := make([]uint, 0, len(instanceIDSet))
	for instanceID := range instanceIDSet {
		instanceIDs = append(instanceIDs, instanceID)
	}
	var instances []providerModel.Instance
	if len(instanceIDs) > 0 {
		instanceQuery := global.APP_DB.Where("instances.id IN ?", instanceIDs)
		if ownerAdminID > 0 {
			providerIDs := global.APP_DB.Model(&providerModel.Provider{}).Select("id").Where("owner_admin_id = ?", ownerAdminID)
			instanceQuery = instanceQuery.Where("instances.provider_id IN (?)", providerIDs)
		}
		if err := instanceQuery.Find(&instances).Error; err != nil {
			return fmt.Errorf("批量查询兑换码实例失败: %w", err)
		}
	}
	instanceMap := make(map[uint]providerModel.Instance, len(instances))
	for _, instance := range instances {
		instanceMap[instance.ID] = instance
	}

	activeDeleteInstances := make(map[uint]struct{})
	if len(instanceIDs) > 0 {
		var activeDeleteTasks []adminModel.Task
		if err := global.APP_DB.Select("instance_id").
			Where("instance_id IN ? AND task_type = ? AND status IN ?", instanceIDs, "delete", []string{"pending", "processing", "running", "cancelling"}).
			Find(&activeDeleteTasks).Error; err != nil {
			return fmt.Errorf("查询实例删除任务失败: %w", err)
		}
		for _, activeTask := range activeDeleteTasks {
			if activeTask.InstanceID != nil {
				activeDeleteInstances[*activeTask.InstanceID] = struct{}{}
			}
		}
	}

	cancelTaskIDs := make([]uint, 0, len(codes))
	deleteTasks := make([]adminModel.Task, 0, len(codes))
	deletingInstanceIDs := make([]uint, 0, len(codes))
	queuedInstances := make(map[uint]struct{}, len(codes))
	for _, code := range codes {
		var instanceID uint
		if code.Status == systemModel.RedemptionStatusPendingCreate || code.Status == systemModel.RedemptionStatusCreating {
			if code.TaskID != nil {
				if linkedTask, exists := linkedTaskMap[*code.TaskID]; exists {
					switch linkedTask.Status {
					case "pending", "processing", "running", "cancelling":
						cancelTaskIDs = append(cancelTaskIDs, linkedTask.ID)
					case "completed":
						if linkedTask.InstanceID != nil {
							instanceID = *linkedTask.InstanceID
						}
					}
				}
			}
		} else if code.InstanceID != nil {
			instanceID = *code.InstanceID
		}
		if instanceID == 0 {
			continue
		}
		instance, exists := instanceMap[instanceID]
		if !exists {
			continue
		}
		if _, exists := queuedInstances[instance.ID]; exists {
			continue
		}
		queuedInstances[instance.ID] = struct{}{}
		deletingInstanceIDs = append(deletingInstanceIDs, instance.ID)
		if _, exists := activeDeleteInstances[instance.ID]; exists {
			continue
		}
		taskDataJSON, err := json.Marshal(map[string]interface{}{
			"instanceId":     instance.ID,
			"providerId":     instance.ProviderID,
			"adminOperation": true,
		})
		if err != nil {
			return err
		}
		providerID := instance.ProviderID
		id := instance.ID
		deleteTasks = append(deleteTasks, adminModel.Task{
			UserID:            adminID,
			ProviderID:        &providerID,
			InstanceID:        &id,
			TaskType:          "delete",
			Status:            "pending",
			TaskData:          string(taskDataJSON),
			TimeoutDuration:   utils.GetDefaultTaskTimeout("delete"),
			IsForceStoppable:  false,
			EstimatedDuration: utils.GetEstimatedTaskDuration("delete", instance.InstanceType),
		})
	}

	if len(deleteTasks) > 0 {
		if err := taskgate.EnsureAccepting(); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := database.GetDatabaseService().ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		if len(deleteTasks) > 0 {
			if err := taskgate.EnsureAcceptingInTx(tx); err != nil {
				return err
			}
			tasksToCreate := append([]adminModel.Task(nil), deleteTasks...)
			if err := tx.CreateInBatches(&tasksToCreate, 100).Error; err != nil {
				return err
			}
		}
		if len(cancelTaskIDs) > 0 {
			now := time.Now()
			if err := tx.Model(&adminModel.Task{}).
				Where("id IN ? AND status IN ?", cancelTaskIDs, []string{"pending", "processing", "running", "cancelling"}).
				Updates(map[string]interface{}{
					"status":        "cancelled",
					"cancel_reason": "兑换码被管理员删除",
					"completed_at":  now,
				}).Error; err != nil {
				return err
			}
		}
		if len(deletingInstanceIDs) > 0 {
			if err := tx.Model(&providerModel.Instance{}).Where("id IN ?", deletingInstanceIDs).Update("status", "deleting").Error; err != nil {
				return err
			}
		}
		return tx.Unscoped().Where("id IN ?", ids).Delete(&systemModel.RedemptionCode{}).Error
	})
	if err != nil {
		return err
	}
	if s.taskService != nil {
		for _, taskID := range cancelTaskIDs {
			s.taskService.ReleaseTaskLocks(taskID)
		}
	}
	if len(deleteTasks) > 0 && global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return nil
}

// ExportByIDs 导出指定 ID 的兑换码详细信息
func (s *Service) ExportByIDs(ids []uint, ownerAdminID uint) ([]adminModel.RedemptionCodeResponse, error) {
	var codes []systemModel.RedemptionCode
	query := global.APP_DB.Model(&systemModel.RedemptionCode{})
	ids = uniqueUintIDs(ids)
	if ownerAdminID > 0 {
		providerIDs := global.APP_DB.Model(&providerModel.Provider{}).
			Select("id").
			Where("owner_admin_id = ?", ownerAdminID)
		query = query.Where("provider_id IN (?)", providerIDs)
	}
	if len(ids) > 0 {
		query = query.Where("id IN ?", ids)
	}
	if err := query.Find(&codes).Error; err != nil {
		return nil, err
	}
	if len(ids) > 0 && len(codes) != len(ids) {
		return nil, fmt.Errorf("部分兑换码不存在或无权限")
	}

	// 批量查询实例名称
	instanceIDSet := make(map[uint]bool)
	for _, c := range codes {
		if c.InstanceID != nil && *c.InstanceID != 0 {
			instanceIDSet[*c.InstanceID] = true
		}
	}
	instanceIDs := make([]uint, 0, len(instanceIDSet))
	for id := range instanceIDSet {
		instanceIDs = append(instanceIDs, id)
	}
	instanceNameMap := make(map[uint]string)
	if len(instanceIDs) > 0 {
		var instances []providerModel.Instance
		if err := global.APP_DB.Select("id, name").Where("id IN ?", instanceIDs).Limit(500).Find(&instances).Error; err == nil {
			for _, inst := range instances {
				instanceNameMap[inst.ID] = inst.Name
			}
		}
	}

	result := make([]adminModel.RedemptionCodeResponse, 0, len(codes))
	for _, c := range codes {
		resp := adminModel.RedemptionCodeResponse{
			RedemptionCode: c,
		}
		if c.InstanceID != nil && *c.InstanceID != 0 {
			resp.InstanceName = instanceNameMap[*c.InstanceID]
		}
		if spec, err := constant.GetCPUSpecByID(c.CPUId); err == nil && spec != nil {
			resp.CPUName = spec.Name
		}
		if spec, err := constant.GetMemorySpecByID(c.MemoryId); err == nil && spec != nil {
			resp.MemoryName = spec.Name
		}
		if spec, err := constant.GetDiskSpecByID(c.DiskId); err == nil && spec != nil {
			resp.DiskName = spec.Name
		}
		if spec, err := constant.GetBandwidthSpecByID(c.BandwidthId); err == nil && spec != nil {
			resp.BandwidthName = spec.Name
		}
		result = append(result, resp)
	}
	return result, nil
}

// FormatExportData 根据字段选择和语言格式化导出数据
func (s *Service) FormatExportData(codes []adminModel.RedemptionCodeResponse, fields []string, isEN bool) []map[string]string {
	// 所有可用字段定义
	allFields := []string{"code", "status", "provider", "instanceType", "cpu", "memory", "disk", "bandwidth", "instanceName", "createdBy", "createdAt", "redeemedAt", "remark"}

	// 如果没有指定字段，默认导出所有
	if len(fields) == 0 {
		fields = allFields
	}

	fieldSet := make(map[string]bool, len(fields))
	for _, f := range fields {
		fieldSet[f] = true
	}

	// 字段名称映射
	headerMap := map[string]string{
		"code": "兑换码", "status": "状态", "provider": "节点", "instanceType": "实例类型",
		"cpu": "CPU", "memory": "内存", "disk": "磁盘", "bandwidth": "带宽",
		"instanceName": "实例名称", "createdBy": "创建人", "createdAt": "创建时间",
		"redeemedAt": "兑换时间", "remark": "备注",
	}
	if isEN {
		headerMap = map[string]string{
			"code": "Code", "status": "Status", "provider": "Provider", "instanceType": "Instance Type",
			"cpu": "CPU", "memory": "Memory", "disk": "Disk", "bandwidth": "Bandwidth",
			"instanceName": "Instance Name", "createdBy": "Created By", "createdAt": "Created At",
			"redeemedAt": "Redeemed At", "remark": "Remark",
		}
	}

	// 状态映射
	statusMap := map[string]string{
		"pending_create": "待创建", "creating": "创建中", "pending_use": "待使用",
		"used": "已使用", "deleting": "删除中",
	}
	if isEN {
		statusMap = map[string]string{
			"pending_create": "Pending Create", "creating": "Creating", "pending_use": "Pending Use",
			"used": "Used", "deleting": "Deleting",
		}
	}

	instanceTypeMap := map[string]string{"container": "容器", "vm": "虚拟机"}
	if isEN {
		instanceTypeMap = map[string]string{"container": "Container", "vm": "VM"}
	}

	result := make([]map[string]string, 0, len(codes))
	for _, c := range codes {
		item := make(map[string]string)
		if fieldSet["code"] {
			item[headerMap["code"]] = c.RedemptionCode.Code
		}
		if fieldSet["status"] {
			s := c.RedemptionCode.Status
			if v, ok := statusMap[s]; ok {
				s = v
			}
			item[headerMap["status"]] = s
		}
		if fieldSet["provider"] {
			item[headerMap["provider"]] = c.RedemptionCode.ProviderName
		}
		if fieldSet["instanceType"] {
			t := c.RedemptionCode.InstanceType
			if v, ok := instanceTypeMap[t]; ok {
				t = v
			}
			item[headerMap["instanceType"]] = t
		}
		if fieldSet["cpu"] {
			item[headerMap["cpu"]] = c.CPUName
		}
		if fieldSet["memory"] {
			item[headerMap["memory"]] = c.MemoryName
		}
		if fieldSet["disk"] {
			item[headerMap["disk"]] = c.DiskName
		}
		if fieldSet["bandwidth"] {
			item[headerMap["bandwidth"]] = c.BandwidthName
		}
		if fieldSet["instanceName"] {
			item[headerMap["instanceName"]] = c.InstanceName
		}
		if fieldSet["createdBy"] {
			item[headerMap["createdBy"]] = c.CreatedByUser
		}
		if fieldSet["createdAt"] {
			if !c.RedemptionCode.CreatedAt.IsZero() {
				item[headerMap["createdAt"]] = c.RedemptionCode.CreatedAt.Format("2006-01-02 15:04:05")
			}
		}
		if fieldSet["redeemedAt"] {
			if c.RedemptionCode.RedeemedAt != nil {
				item[headerMap["redeemedAt"]] = c.RedemptionCode.RedeemedAt.Format("2006-01-02 15:04:05")
			}
		}
		if fieldSet["remark"] {
			item[headerMap["remark"]] = c.RedemptionCode.Remark
		}
		result = append(result, item)
	}
	return result
}

// generateUniqueCode 生成唯一的 16 位大写字母数字兑换码
func (s *Service) generateUniqueCode() (string, error) {
	const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const codeLen = 16
	const maxAttempts = 20

	for attempt := 0; attempt < maxAttempts; attempt++ {
		buf := make([]byte, codeLen)
		for i := range buf {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
			if err != nil {
				return "", err
			}
			buf[i] = charset[n.Int64()]
		}
		code := string(buf)

		// ORI 前缀保留给节点导入自动生成的兑换码，普通兑换码不允许使用该前缀
		if strings.HasPrefix(code, "ORI") {
			continue
		}
		return code, nil
	}
	return "", fmt.Errorf("无法生成唯一兑换码，请重试")
}

func (s *Service) generateUniqueCodes(count int) ([]string, error) {
	result := make([]string, 0, count)
	seen := make(map[string]struct{}, count)
	for len(result) < count {
		code, err := s.generateUniqueCode()
		if err != nil {
			return nil, err
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		result = append(result, code)
	}
	return result, nil
}

func validateGPUDeviceIDs(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			return fmt.Errorf("GPU 设备 ID 不能为空")
		}
		for _, r := range id {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
				r == '_' || r == '-' || r == '.' || r == ':' {
				continue
			}
			return fmt.Errorf("GPU 设备 ID 包含非法字符")
		}
	}
	return nil
}

func uniqueUintIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
