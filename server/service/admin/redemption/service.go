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
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
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
		var providerIDs []uint
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("owner_admin_id = ?", ownerAdminID).
			Pluck("id", &providerIDs).Error; err != nil {
			return nil, 0, err
		}
		if len(providerIDs) == 0 {
			return []adminModel.RedemptionCodeResponse{}, 0, nil
		}
		query = query.Where("provider_id IN ?", providerIDs)
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
func (s *Service) BatchCreate(req adminModel.BatchCreateRedemptionCodesRequest, adminID uint) error {
	dbService := database.GetDatabaseService()

	if req.CreationMode == "" {
		req.CreationMode = "standard"
	}
	if req.CreationMode != "standard" && req.CreationMode != "copy" {
		return fmt.Errorf("无效的创建模式")
	}

	// 验证 Provider 存在
	var provider providerModel.Provider
	if err := global.APP_DB.Where("id = ?", req.ProviderID).First(&provider).Error; err != nil {
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

	for i := 0; i < req.Count; i++ {
		code, err := s.generateUniqueCode()
		if err != nil {
			return fmt.Errorf("生成兑换码失败: %v", err)
		}

		// 构造任务数据（字段名与 CreateInstanceTaskRequest 的 JSON 标签一致，便于 executeProviderCreation 复用）
		taskDataReq := adminModel.CreateRedemptionInstanceTaskRequest{
			ProviderId:      req.ProviderID,
			ImageId:         req.ImageId,
			CPUId:           req.CPUId,
			MemoryId:        req.MemoryId,
			DiskId:          req.DiskId,
			BandwidthId:     req.BandwidthId,
			CreationMode:    req.CreationMode,
			SourceContainer: req.SourceContainer,
			GpuEnabled:      req.GpuEnabled,
			GpuDeviceIds:    req.GpuDeviceIds,
		}

		var redemptionCode systemModel.RedemptionCode

		err = dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
			redemptionCode = systemModel.RedemptionCode{
				Code:            code,
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
			if err := tx.Create(&redemptionCode).Error; err != nil {
				return fmt.Errorf("创建兑换码记录失败: %v", err)
			}

			// 将 RedemptionCodeID 写入任务数据
			taskDataReq.RedemptionCodeID = redemptionCode.ID
			taskDataJSON, err := json.Marshal(taskDataReq)
			if err != nil {
				return fmt.Errorf("序列化任务数据失败: %v", err)
			}

			// 使用管理员 ID 创建任务（避免 executeProviderCreation 中用户查询失败）
			task, err := s.taskService.CreateTask(adminID, &req.ProviderID, nil, "create_redemption_instance", string(taskDataJSON), 0)
			if err != nil {
				return fmt.Errorf("创建任务失败: %v", err)
			}

			// 更新兑换码状态为 creating，记录 TaskID
			taskID := task.ID
			if err := tx.Model(&redemptionCode).Updates(map[string]interface{}{
				"status":  systemModel.RedemptionStatusCreating,
				"task_id": taskID,
			}).Error; err != nil {
				return fmt.Errorf("更新兑换码状态失败: %v", err)
			}

			return nil
		})

		if err != nil {
			global.APP_LOG.Error("创建兑换码失败", zap.Int("index", i), zap.Error(err))
			return err
		}
	}

	return nil
}

// BatchDelete 批量删除兑换码（硬删除），同时清理对应实例
// - pending_use: 创建实例删除任务 + 硬删除兑换码
// - pending_create / creating: 取消任务 + 硬删除兑换码
// - used: 创建实例删除任务 + 硬删除兑换码（无论已兑换与否，实例一并删除）
// - deleting: 跳过（已在处理中）
func (s *Service) BatchDelete(ids []uint, adminID uint) error {
	if len(ids) == 0 {
		return fmt.Errorf("请选择要删除的兑换码")
	}

	var codes []systemModel.RedemptionCode
	if err := global.APP_DB.Where("id IN ?", ids).Find(&codes).Error; err != nil {
		return fmt.Errorf("查询兑换码失败: %v", err)
	}

	dbService := database.GetDatabaseService()

	for _, code := range codes {
		codeID := code.ID

		switch code.Status {
		case systemModel.RedemptionStatusDeleting:
			continue

		case systemModel.RedemptionStatusPendingUse:
			if code.InstanceID != nil {
				var instance providerModel.Instance
				if err := global.APP_DB.First(&instance, *code.InstanceID).Error; err == nil {
					taskData := map[string]interface{}{
						"instanceId":     instance.ID,
						"providerId":     instance.ProviderID,
						"adminOperation": true,
					}
					taskDataJSON, err := json.Marshal(taskData)
					if err == nil {
						if _, tErr := s.taskService.CreateTask(adminID, &instance.ProviderID, &instance.ID, "delete", string(taskDataJSON), 0); tErr != nil {
							global.APP_LOG.Warn("创建实例删除任务失败",
								zap.Uint("codeId", codeID),
								zap.Uint("instanceId", instance.ID),
								zap.Error(tErr))
						} else {
							global.APP_DB.Model(&instance).Update("status", "deleting")
						}
					}
				}
			}
			if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Unscoped().Delete(&systemModel.RedemptionCode{}, codeID).Error
			}); err != nil {
				global.APP_LOG.Warn("硬删除兑换码失败", zap.Uint("codeId", codeID), zap.Error(err))
			}

		case systemModel.RedemptionStatusPendingCreate, systemModel.RedemptionStatusCreating:
			if code.TaskID != nil {
				taskID := *code.TaskID
				var t adminModel.Task
				if err := global.APP_DB.First(&t, taskID).Error; err == nil {
					// 取消仍在运行的任务（含 processing：阶段1已创建实例记录但尚未最终化）
					if t.Status == "pending" || t.Status == "running" || t.Status == "processing" {
						global.APP_DB.Model(&t).Updates(map[string]interface{}{
							"status":        "cancelled",
							"cancel_reason": "兑换码被管理员删除",
							"completed_at":  time.Now(),
						})
						// finalizeRedemptionInstanceCreation 检测到 cancelled 后会调用
						// delayedDeleteFailedInstance 清理已创建的实例，无需在此重复处理
					} else if t.Status == "completed" && t.InstanceID != nil {
						// 竞态：任务在我们取消前已完成，但兑换码未切换为 pending_use（极窄窗口）
						// 此时实例已存在于 DB，需手动创建删除任务
						var instance providerModel.Instance
						if err := global.APP_DB.First(&instance, *t.InstanceID).Error; err == nil {
							taskData := map[string]interface{}{
								"instanceId":     instance.ID,
								"providerId":     instance.ProviderID,
								"adminOperation": true,
							}
							taskDataJSON, marshalErr := json.Marshal(taskData)
							if marshalErr == nil {
								if _, tErr := s.taskService.CreateTask(adminID, &instance.ProviderID, &instance.ID, "delete", string(taskDataJSON), 0); tErr != nil {
									global.APP_LOG.Warn("创建孤儿实例删除任务失败",
										zap.Uint("codeId", codeID),
										zap.Uint("instanceId", instance.ID),
										zap.Error(tErr))
								} else {
									global.APP_DB.Model(&instance).Update("status", "deleting")
								}
							}
						}
					}
				}
			}
			if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Unscoped().Delete(&systemModel.RedemptionCode{}, codeID).Error
			}); err != nil {
				global.APP_LOG.Warn("硬删除兑换码失败", zap.Uint("codeId", codeID), zap.Error(err))
			}

		default:
			// used 及未知状态：如果关联了实例，创建删除任务
			if code.InstanceID != nil {
				var instance providerModel.Instance
				if err := global.APP_DB.First(&instance, *code.InstanceID).Error; err == nil {
					taskData := map[string]interface{}{
						"instanceId":     instance.ID,
						"providerId":     instance.ProviderID,
						"adminOperation": true,
					}
					taskDataJSON, marshalErr := json.Marshal(taskData)
					if marshalErr == nil {
						if _, tErr := s.taskService.CreateTask(adminID, &instance.ProviderID, &instance.ID, "delete", string(taskDataJSON), 0); tErr != nil {
							global.APP_LOG.Warn("创建已兑换实例删除任务失败",
								zap.Uint("codeId", codeID),
								zap.Uint("instanceId", instance.ID),
								zap.Error(tErr))
						} else {
							global.APP_DB.Model(&instance).Update("status", "deleting")
						}
					}
				}
			}
			if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Unscoped().Delete(&systemModel.RedemptionCode{}, codeID).Error
			}); err != nil {
				global.APP_LOG.Warn("硬删除兑换码失败", zap.Uint("codeId", codeID), zap.Error(err))
			}
		}
	}

	return nil
}

// ExportByIDs 导出指定 ID 的兑换码详细信息
func (s *Service) ExportByIDs(ids []uint) ([]adminModel.RedemptionCodeResponse, error) {
	var codes []systemModel.RedemptionCode
	query := global.APP_DB.Model(&systemModel.RedemptionCode{})
	if len(ids) > 0 {
		query = query.Where("id IN ?", ids)
	}
	if err := query.Find(&codes).Error; err != nil {
		return nil, err
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

		var existing systemModel.RedemptionCode
		if err := global.APP_DB.Where("code = ?", code).First(&existing).Error; err != nil {
			return code, nil
		}
	}
	return "", fmt.Errorf("无法生成唯一兑换码，请重试")
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
