package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/provider"
	"oneclickvirt/provider/incus"
	"oneclickvirt/provider/lxd"
	"oneclickvirt/service/auth"
	"oneclickvirt/service/database"
	"oneclickvirt/service/images"
	"oneclickvirt/service/interfaces"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/traffic"
	"oneclickvirt/service/vnstat"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 处理用户提供商和配置相关功能
type Service struct {
	taskService interfaces.TaskServiceInterface
}

// taskServiceAdapter 任务服务适配器，避免循环导入
type taskServiceAdapter struct{}

// CreateTask 创建任务的适配器方法
func (tsa *taskServiceAdapter) CreateTask(userID uint, providerID *uint, instanceID *uint, taskType string, taskData string, timeoutDuration int) (*adminModel.Task, error) {
	// 使用延迟导入来避免循环依赖
	if globalTaskService == nil {
		return nil, fmt.Errorf("任务服务未初始化")
	}
	return globalTaskService.CreateTask(userID, providerID, instanceID, taskType, taskData, timeoutDuration)
}

// GetStateManager 获取状态管理器的适配器方法
func (tsa *taskServiceAdapter) GetStateManager() interfaces.TaskStateManagerInterface {
	if globalTaskService == nil {
		return nil
	}
	return globalTaskService.GetStateManager()
}

// 全局任务服务实例，在系统初始化时设置
var globalTaskService interfaces.TaskServiceInterface

// SetGlobalTaskService 设置全局任务服务实例
func SetGlobalTaskService(ts interfaces.TaskServiceInterface) {
	globalTaskService = ts
}

// NewService 创建提供商服务
func NewService() *Service {
	return &Service{
		taskService: &taskServiceAdapter{},
	}
}

// GetAvailableProviders 获取可用节点列表
func (s *Service) GetAvailableProviders(userID uint) ([]userModel.AvailableProviderResponse, error) {
	var dbProviders []providerModel.Provider

	// 获取允许申领且未冻结的Provider，包括部分在线的服务器
	err := global.APP_DB.Where("(status = ? OR status = ?) AND allow_claim = ? AND is_frozen = ?",
		"active", "partial", true, false).Find(&dbProviders).Error
	if err != nil {
		return nil, err
	}

	global.APP_LOG.Info("开始处理Provider列表",
		zap.Int("totalProviders", len(dbProviders)),
		zap.Uint("userID", userID))

	var providers []userModel.AvailableProviderResponse
	skippedCount := 0

	for _, provider := range dbProviders {
		// 只在资源信息完全缺失时才进行同步，避免阻塞用户请求
		if !provider.ResourceSynced && provider.NodeCPUCores == 0 && provider.NodeMemoryTotal == 0 && provider.NodeDiskTotal == 0 {
			global.APP_LOG.Info("节点资源信息缺失，跳过该节点",
				zap.String("provider", provider.Name),
				zap.Uint("id", provider.ID))

			// 跳过没有有效资源数据的Provider，不返回给用户
			skippedCount++
			continue
		}

		// 对于有可用资源的服务器，添加到返回列表
		if provider.ContainerEnabled || provider.VirtualMachineEnabled {
			// 检查是否有有效的资源数据，如果没有则跳过
			if provider.NodeCPUCores == 0 || provider.NodeMemoryTotal == 0 || provider.NodeDiskTotal == 0 {
				global.APP_LOG.Warn("节点资源数据不完整，跳过该节点",
					zap.String("provider", provider.Name),
					zap.Uint("id", provider.ID),
					zap.Int("cpu", provider.NodeCPUCores),
					zap.Int64("memory", provider.NodeMemoryTotal),
					zap.Int64("disk", provider.NodeDiskTotal))
				skippedCount++
				continue
			}

			// 计算实际可用资源（包含预留资源的占用）
			// 统计当前活跃的预留资源（新机制：基于过期时间）
			var activeReservations []resourceModel.ResourceReservation
			if err := global.APP_DB.Where("provider_id = ? AND expires_at > ?",
				provider.ID, time.Now()).Find(&activeReservations).Error; err != nil {
				global.APP_LOG.Warn("查询预留资源失败",
					zap.Uint("providerId", provider.ID),
					zap.Error(err))
			}

			// 计算预留资源占用
			reservedCPU := 0
			reservedMemory := int64(0)
			reservedDisk := int64(0)
			reservedContainers := 0
			reservedVMs := 0

			for _, reservation := range activeReservations {
				if reservation.InstanceType == "vm" {
					reservedCPU += reservation.CPU
					reservedVMs++
				} else {
					reservedContainers++
				}
				reservedMemory += reservation.Memory
				reservedDisk += reservation.Disk
			}

			// 使用真实的资源数据
			nodeCPU := provider.NodeCPUCores
			nodeMemory := provider.NodeMemoryTotal
			nodeDisk := provider.NodeDiskTotal

			// 计算实际使用的资源 = 已分配的 + 预留的
			actualUsedCPU := provider.UsedCPUCores + reservedCPU
			actualUsedMemory := provider.UsedMemory + reservedMemory
			actualUsedDisk := provider.UsedDisk + reservedDisk
			actualUsedContainers := provider.ContainerCount + reservedContainers
			actualUsedVMs := provider.VMCount + reservedVMs

			// 计算可用资源
			availableCPU := nodeCPU - actualUsedCPU
			availableMemory := nodeMemory - actualUsedMemory
			availableDisk := nodeDisk - actualUsedDisk

			// 确保不出现负数
			if availableCPU < 0 {
				availableCPU = 0
			}
			if availableMemory < 0 {
				availableMemory = 0
			}
			if availableDisk < 0 {
				availableDisk = 0
			}

			// 计算资源使用率
			cpuUsage := float64(0)
			memoryUsage := float64(0)
			if nodeCPU > 0 {
				cpuUsage = float64(actualUsedCPU) / float64(nodeCPU) * 100
			}
			if nodeMemory > 0 {
				memoryUsage = float64(actualUsedMemory) / float64(nodeMemory) * 100
			}

			// 计算可用实例槽位 - 基于容器和虚拟机的单独限制，考虑预留
			availableSlots := 0
			availableContainerSlots := 0
			availableVMSlots := 0

			if provider.MaxContainerInstances > 0 {
				availableContainerSlots = provider.MaxContainerInstances - actualUsedContainers
				if availableContainerSlots < 0 {
					availableContainerSlots = 0
				}
				availableSlots += availableContainerSlots
			}

			if provider.MaxVMInstances > 0 {
				availableVMSlots = provider.MaxVMInstances - actualUsedVMs
				if availableVMSlots < 0 {
					availableVMSlots = 0
				}
				availableSlots += availableVMSlots
			}

			if availableSlots == 0 && provider.MaxContainerInstances == 0 && provider.MaxVMInstances == 0 {
				// 如果没有设置限制，基于资源动态计算
				// 假设每个实例最少需要1核CPU和512MB内存
				cpuBasedMax := availableCPU
				memoryBasedMax := int(availableMemory / 512) // 512MB最低内存

				if cpuBasedMax > 0 && memoryBasedMax > 0 {
					if cpuBasedMax < memoryBasedMax {
						availableSlots = cpuBasedMax
					} else {
						availableSlots = memoryBasedMax
					}
				} else {
					availableSlots = 0 // 资源不足
				}
			}

			providerResp := userModel.AvailableProviderResponse{
				ID:               provider.ID,
				Name:             provider.Name,
				Type:             provider.Type,
				Region:           provider.Region,
				Country:          provider.Country,
				CountryCode:      provider.CountryCode,
				Status:           provider.Status,
				CPU:              nodeCPU,
				Memory:           int(nodeMemory), // 返回MB单位
				Disk:             int(nodeDisk),   // 返回MB单位
				AvailableSlots:   availableSlots,
				CPUUsage:         cpuUsage,
				MemoryUsage:      memoryUsage,
				ContainerEnabled: provider.ContainerEnabled,
				VmEnabled:        provider.VirtualMachineEnabled,
			}
			providers = append(providers, providerResp)
		}
	}

	global.APP_LOG.Info("Provider列表处理完成",
		zap.Int("totalProviders", len(dbProviders)),
		zap.Int("availableProviders", len(providers)),
		zap.Int("skippedProviders", skippedCount),
		zap.Uint("userID", userID))

	return providers, nil
}

// GetSystemImages 获取系统镜像列表
func (s *Service) GetSystemImages(userID uint, req userModel.SystemImagesRequest) ([]userModel.SystemImageResponse, error) {
	var images []systemModel.SystemImage

	query := global.APP_DB.Where("status = ?", "active")

	if req.ProviderType != "" {
		query = query.Where("provider_type = ?", req.ProviderType)
	}
	if req.Architecture != "" {
		query = query.Where("architecture = ?", req.Architecture)
	}
	if req.OsType != "" {
		query = query.Where("os_type = ?", req.OsType)
	}
	if req.InstanceType != "" {
		query = query.Where("instance_type = ?", req.InstanceType)
	}

	err := query.Find(&images).Error
	if err != nil {
		return nil, err
	}

	var response []userModel.SystemImageResponse
	for _, img := range images {
		response = append(response, userModel.SystemImageResponse{
			ID:           img.ID,
			Name:         img.Name,
			DisplayName:  img.Name, // 使用name作为显示名称
			Version:      img.OSVersion,
			Architecture: img.Architecture,
			OsType:       img.OSType,
			ProviderType: img.ProviderType,
			InstanceType: img.InstanceType,
			ImageURL:     img.URL,
			Description:  img.Description,
			IsActive:     img.Status == "active",
		})
	}

	return response, nil
}

// GetInstanceConfig 获取实例配置选项 - 根据用户配额动态过滤
func (s *Service) GetInstanceConfig(userID uint) (*userModel.InstanceConfigResponse, error) {
	// 获取用户配额信息
	quotaService := resources.NewQuotaService()
	quotaInfo, err := quotaService.GetUserQuotaInfo(userID)
	if err != nil {
		return nil, fmt.Errorf("获取用户配额信息失败: %v", err)
	}

	// 获取所有预定义规格
	allCPUSpecs := constant.PredefinedCPUSpecs
	allMemorySpecs := constant.PredefinedMemorySpecs
	allDiskSpecs := constant.PredefinedDiskSpecs
	allBandwidthSpecs := constant.PredefinedBandwidthSpecs

	// 根据用户配额动态过滤规格
	var availableCPUSpecs []constant.CPUSpec
	for _, spec := range allCPUSpecs {
		if quotaInfo.CurrentResources.CPU+spec.Cores <= quotaInfo.MaxQuota.CPU {
			availableCPUSpecs = append(availableCPUSpecs, spec)
		}
	}

	var availableMemorySpecs []constant.MemorySpec
	for _, spec := range allMemorySpecs {
		if quotaInfo.CurrentResources.Memory+int64(spec.SizeMB) <= quotaInfo.MaxQuota.Memory {
			availableMemorySpecs = append(availableMemorySpecs, spec)
		}
	}

	var availableDiskSpecs []constant.DiskSpec
	for _, spec := range allDiskSpecs {
		if quotaInfo.CurrentResources.Disk+int64(spec.SizeMB) <= quotaInfo.MaxQuota.Disk {
			availableDiskSpecs = append(availableDiskSpecs, spec)
		}
	}

	var availableBandwidthSpecs []constant.BandwidthSpec
	for _, spec := range allBandwidthSpecs {
		if spec.SpeedMbps <= quotaInfo.MaxQuota.Bandwidth {
			availableBandwidthSpecs = append(availableBandwidthSpecs, spec)
		}
	}

	// 获取可用镜像（从数据库）
	images, err := s.GetSystemImages(userID, userModel.SystemImagesRequest{})
	if err != nil {
		return nil, fmt.Errorf("获取镜像列表失败: %v", err)
	}

	// 返回所有可用的磁盘规格，让前端根据选择的镜像类型动态过滤
	// 不再在后端预先过滤，这样前端可以更灵活地处理
	filteredDiskSpecs := availableDiskSpecs

	// 转换为前端期望的格式
	cpuOptions := make([]userModel.CPUSpecResponse, len(availableCPUSpecs))
	for i, spec := range availableCPUSpecs {
		cpuOptions[i] = userModel.CPUSpecResponse{
			ID:    spec.ID,
			Cores: spec.Cores,
			Name:  spec.Name,
		}
	}

	memoryOptions := make([]userModel.MemorySpecResponse, len(availableMemorySpecs))
	for i, spec := range availableMemorySpecs {
		memoryOptions[i] = userModel.MemorySpecResponse{
			ID:     spec.ID,
			SizeMB: spec.SizeMB,
			Name:   spec.Name,
		}
	}

	diskOptions := make([]userModel.DiskSpecResponse, len(filteredDiskSpecs))
	for i, spec := range filteredDiskSpecs {
		diskOptions[i] = userModel.DiskSpecResponse{
			ID:     spec.ID,
			SizeMB: spec.SizeMB,
			Name:   spec.Name,
		}
	}

	bandwidthOptions := make([]userModel.BandwidthSpecResponse, len(availableBandwidthSpecs))
	for i, spec := range availableBandwidthSpecs {
		bandwidthOptions[i] = userModel.BandwidthSpecResponse{
			ID:        spec.ID,
			SpeedMbps: spec.SpeedMbps,
			Name:      spec.Name,
		}
	}

	return &userModel.InstanceConfigResponse{
		Images:         images,
		CPUSpecs:       cpuOptions,
		MemorySpecs:    memoryOptions,
		DiskSpecs:      diskOptions,
		BandwidthSpecs: bandwidthOptions,
	}, nil
}

// GetFilteredSystemImages 根据Provider和实例类型获取过滤后的系统镜像列表
func (s *Service) GetFilteredSystemImages(userID uint, providerID uint, instanceType string) ([]userModel.SystemImageResponse, error) {
	// 验证Provider是否存在
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return nil, errors.New("Provider不存在")
	}

	// 验证Provider是否支持该实例类型
	resourceService := &resources.ResourceService{}
	if err := resourceService.ValidateInstanceTypeSupport(providerID, instanceType); err != nil {
		return nil, err
	}

	// 使用镜像服务获取过滤后的镜像
	imageService := &images.ImageService{}
	images, err := imageService.GetFilteredImages(providerID, instanceType)
	if err != nil {
		return nil, err
	}

	var response []userModel.SystemImageResponse
	for _, img := range images {
		response = append(response, userModel.SystemImageResponse{
			ID:           img.ID,
			Name:         img.Name,
			DisplayName:  img.Name,
			Version:      img.OSVersion,
			Architecture: img.Architecture,
			OsType:       img.OSType,
			ProviderType: img.ProviderType,
			InstanceType: img.InstanceType,
			ImageURL:     img.URL,
			Description:  img.Description,
			IsActive:     img.Status == "active",
		})
	}

	return response, nil
}

// CreateUserInstance 创建用户实例 - 异步处理版本
func (s *Service) CreateUserInstance(userID uint, req userModel.CreateInstanceRequest) (*adminModel.Task, error) {
	global.APP_LOG.Info("开始创建用户实例",
		zap.Uint("userID", userID),
		zap.Uint("providerId", req.ProviderId),
		zap.Uint("imageId", req.ImageId),
		zap.String("cpuId", req.CPUId),
		zap.String("memoryId", req.MemoryId),
		zap.String("diskId", req.DiskId),
		zap.String("bandwidthId", req.BandwidthId),
		zap.String("description", req.Description))

	// 快速验证基本参数
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ProviderId).Error; err != nil {
		global.APP_LOG.Error("节点不存在", zap.Uint("providerId", req.ProviderId), zap.Error(err))
		return nil, errors.New("节点不存在")
	}

	if !provider.AllowClaim || provider.IsFrozen {
		global.APP_LOG.Error("服务器不可用",
			zap.Uint("providerId", req.ProviderId),
			zap.Bool("allowClaim", provider.AllowClaim),
			zap.Bool("isFrozen", provider.IsFrozen))
		return nil, errors.New("服务器不可用")
	}

	var systemImage systemModel.SystemImage
	if err := global.APP_DB.Where("id = ?", req.ImageId).First(&systemImage).Error; err != nil {
		global.APP_LOG.Error("无效的镜像ID", zap.Uint("imageId", req.ImageId), zap.Error(err))
		return nil, errors.New("无效的镜像ID")
	}

	if systemImage.Status != "active" {
		global.APP_LOG.Error("所选镜像不可用",
			zap.Uint("imageId", req.ImageId),
			zap.String("imageStatus", systemImage.Status))
		return nil, errors.New("所选镜像不可用")
	}

	// 验证Provider和Image的匹配性
	if err := s.validateProviderImageCompatibility(&provider, &systemImage); err != nil {
		global.APP_LOG.Error("Provider和镜像不匹配",
			zap.Uint("providerId", req.ProviderId),
			zap.Uint("imageId", req.ImageId),
			zap.String("providerType", provider.Type),
			zap.String("imageProviderType", systemImage.ProviderType),
			zap.String("providerArch", provider.Architecture),
			zap.String("imageArch", systemImage.Architecture),
			zap.Error(err))
		return nil, err
	}

	// 验证规格ID并获取规格信息，同时验证用户权限
	global.APP_LOG.Info("开始验证规格ID",
		zap.String("cpuId", req.CPUId),
		zap.String("memoryId", req.MemoryId),
		zap.String("diskId", req.DiskId),
		zap.String("bandwidthId", req.BandwidthId))

	cpuSpec, err := constant.GetCPUSpecByID(req.CPUId)
	if err != nil {
		global.APP_LOG.Error("无效的CPU规格ID", zap.String("cpuId", req.CPUId), zap.Error(err))
		return nil, fmt.Errorf("无效的CPU规格ID: %v", err)
	}
	global.APP_LOG.Info("CPU规格验证成功", zap.String("cpuId", req.CPUId), zap.Int("cores", cpuSpec.Cores), zap.String("name", cpuSpec.Name))

	memorySpec, err := constant.GetMemorySpecByID(req.MemoryId)
	if err != nil {
		global.APP_LOG.Error("无效的内存规格ID", zap.String("memoryId", req.MemoryId), zap.Error(err))
		return nil, fmt.Errorf("无效的内存规格ID: %v", err)
	}
	global.APP_LOG.Info("内存规格验证成功", zap.String("memoryId", req.MemoryId), zap.Int("sizeMB", memorySpec.SizeMB), zap.String("name", memorySpec.Name))

	diskSpec, err := constant.GetDiskSpecByID(req.DiskId)
	if err != nil {
		global.APP_LOG.Error("无效的磁盘规格ID", zap.String("diskId", req.DiskId), zap.Error(err))
		return nil, fmt.Errorf("无效的磁盘规格ID: %v", err)
	}
	global.APP_LOG.Info("磁盘规格验证成功", zap.String("diskId", req.DiskId), zap.Int("sizeMB", diskSpec.SizeMB), zap.String("name", diskSpec.Name))

	bandwidthSpec, err := constant.GetBandwidthSpecByID(req.BandwidthId)
	if err != nil {
		global.APP_LOG.Error("无效的带宽规格ID", zap.String("bandwidthId", req.BandwidthId), zap.Error(err))
		return nil, fmt.Errorf("无效的带宽规格ID: %v", err)
	}
	global.APP_LOG.Info("带宽规格验证成功", zap.String("bandwidthId", req.BandwidthId), zap.Int("speedMbps", bandwidthSpec.SpeedMbps), zap.String("name", bandwidthSpec.Name))

	// 验证用户是否有权限使用所选规格
	if err := s.validateUserSpecPermissions(userID, cpuSpec, memorySpec, diskSpec, bandwidthSpec); err != nil {
		global.APP_LOG.Error("用户权限验证失败",
			zap.Uint("userID", userID),
			zap.String("cpuId", req.CPUId),
			zap.String("memoryId", req.MemoryId),
			zap.String("diskId", req.DiskId),
			zap.String("bandwidthId", req.BandwidthId),
			zap.Error(err))
		return nil, err
	}

	// 验证实例的最低硬件要求（统一验证虚拟机和容器）
	if err := s.validateInstanceMinimumRequirements(&systemImage, memorySpec, diskSpec, &provider); err != nil {
		global.APP_LOG.Error("实例最低硬件要求验证失败",
			zap.Uint("imageId", req.ImageId),
			zap.String("imageName", systemImage.Name),
			zap.String("instanceType", systemImage.InstanceType),
			zap.String("providerType", provider.Type),
			zap.Int("memoryMB", memorySpec.SizeMB),
			zap.Int("diskMB", diskSpec.SizeMB),
			zap.Error(err))
		return nil, err
	}

	// 进行三重验证：用户配额、Provider资源、Provider并发限制
	if err := s.validateCreateTaskPermissions(userID, req.ProviderId, systemImage.InstanceType,
		cpuSpec.Cores, int64(memorySpec.SizeMB), int64(diskSpec.SizeMB), bandwidthSpec.SpeedMbps); err != nil {
		global.APP_LOG.Error("任务创建验证失败",
			zap.Uint("userID", userID),
			zap.Uint("providerId", req.ProviderId),
			zap.Error(err))
		return nil, err
	}

	global.APP_LOG.Info("所有验证通过，开始创建实例",
		zap.Uint("userID", userID),
		zap.Uint("providerId", req.ProviderId),
		zap.Uint("imageId", req.ImageId))

	// 生成会话ID
	sessionID := resources.GenerateSessionID()

	// 使用新的原子化创建流程
	return s.createInstanceWithSessionReservation(userID, &req, sessionID, &systemImage, cpuSpec, memorySpec, diskSpec, bandwidthSpec)
}

// createInstanceWithSessionReservation 原子化的实例创建流程（新机制）
func (s *Service) createInstanceWithSessionReservation(userID uint, req *userModel.CreateInstanceRequest, sessionID string, systemImage *systemModel.SystemImage, cpuSpec *constant.CPUSpec, memorySpec *constant.MemorySpec, diskSpec *constant.DiskSpec, bandwidthSpec *constant.BandwidthSpec) (*adminModel.Task, error) {
	// 使用事务确保原子性
	var task *adminModel.Task
	err := database.GetDatabaseService().ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 1. 原子化预留并消费资源
		reservationService := resources.GetResourceReservationService()

		if err := reservationService.ReserveAndConsumeInTx(tx, userID, req.ProviderId, sessionID,
			systemImage.InstanceType, cpuSpec.Cores, int64(memorySpec.SizeMB), int64(diskSpec.SizeMB), bandwidthSpec.SpeedMbps); err != nil {
			global.APP_LOG.Error("原子化预留消费资源失败",
				zap.Uint("userID", userID),
				zap.Uint("providerId", req.ProviderId),
				zap.String("sessionId", sessionID),
				zap.Error(err))
			return fmt.Errorf("资源分配失败: %v", err)
		}

		// 2. 创建任务
		taskData := fmt.Sprintf(`{"providerId":%d,"imageId":%d,"cpuId":"%s","memoryId":"%s","diskId":"%s","bandwidthId":"%s","description":"%s","sessionId":"%s"}`,
			req.ProviderId, req.ImageId, req.CPUId, req.MemoryId, req.DiskId, req.BandwidthId, req.Description, sessionID)

		// 在事务中创建任务
		newTask := &adminModel.Task{
			UserID:          userID,
			ProviderID:      &req.ProviderId,
			TaskType:        "create",
			TaskData:        taskData,
			Status:          "pending",
			TimeoutDuration: 1800,
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

	global.APP_LOG.Info("原子化实例创建成功",
		zap.Uint("userID", userID),
		zap.Uint("taskId", task.ID),
		zap.String("sessionId", sessionID))

	return task, nil
}

// GetProviderCapabilities 获取Provider能力
func (s *Service) GetProviderCapabilities(userID uint, providerID uint) (map[string]interface{}, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return nil, errors.New("Provider不存在")
	}

	// 构建支持的实例类型列表
	var supportedTypes []string
	if provider.ContainerEnabled {
		supportedTypes = append(supportedTypes, "container")
	}
	if provider.VirtualMachineEnabled {
		supportedTypes = append(supportedTypes, "vm")
	}

	capabilities := map[string]interface{}{
		"containerEnabled": provider.ContainerEnabled,
		"vmEnabled":        provider.VirtualMachineEnabled,
		"supportedTypes":   supportedTypes,
		"maxCpu":           provider.NodeCPUCores,
		"maxMemory":        provider.NodeMemoryTotal,
		"maxDisk":          provider.NodeDiskTotal,
		"region":           provider.Region,
		"country":          provider.Country,
		"status":           provider.Status,
	}

	return capabilities, nil
}

// GetInstanceTypePermissions 获取实例类型权限
func (s *Service) GetInstanceTypePermissions(userID uint) (map[string]interface{}, error) {
	permissionService := auth.PermissionService{}
	effective, err := permissionService.GetUserEffectivePermission(userID)
	if err != nil {
		return nil, errors.New("获取用户权限失败")
	}

	// 获取配置中的权限要求
	permissions := global.APP_CONFIG.Quota.InstanceTypePermissions

	// 根据用户等级和配置判断权限
	result := map[string]interface{}{
		"canCreateContainer":         effective.EffectiveLevel >= permissions.MinLevelForContainer,
		"canCreateVM":                effective.EffectiveLevel >= permissions.MinLevelForVM,
		"canDeleteContainer":         effective.EffectiveLevel >= permissions.MinLevelForDeleteContainer,
		"canDeleteVM":                effective.EffectiveLevel >= permissions.MinLevelForDeleteVM,
		"canResetContainer":          effective.EffectiveLevel >= permissions.MinLevelForResetContainer,
		"canResetVM":                 effective.EffectiveLevel >= permissions.MinLevelForResetVM,
		"minLevelForContainer":       permissions.MinLevelForContainer,
		"minLevelForVM":              permissions.MinLevelForVM,
		"minLevelForDeleteContainer": permissions.MinLevelForDeleteContainer,
		"minLevelForDeleteVM":        permissions.MinLevelForDeleteVM,
		"minLevelForResetContainer":  permissions.MinLevelForResetContainer,
		"minLevelForResetVM":         permissions.MinLevelForResetVM,
	}

	// admin 权限可以执行所有操作
	if effective.EffectiveType == "admin" {
		result["canCreateContainer"] = true
		result["canCreateVM"] = true
		result["canDeleteContainer"] = true
		result["canDeleteVM"] = true
		result["canResetContainer"] = true
		result["canResetVM"] = true
	}

	return result, nil
}

// ProcessCreateInstanceTask 处理创建实例的后台任务 - 三阶段处理
func (s *Service) ProcessCreateInstanceTask(ctx context.Context, task *adminModel.Task) error {
	global.APP_LOG.Info("开始处理创建实例任务", zap.Uint("taskId", task.ID))

	// 初始化进度
	s.updateTaskProgress(task.ID, 10, "正在准备实例创建...")

	// 阶段1: 数据库预处理（快速事务）
	instance, err := s.prepareInstanceCreation(ctx, task)
	if err != nil {
		global.APP_LOG.Error("实例创建预处理失败", zap.Uint("taskId", task.ID), zap.Error(err))
		// 使用统一状态管理器
		stateManager := s.taskService.GetStateManager()
		if stateManager != nil {
			if err := stateManager.CompleteMainTask(task.ID, false, fmt.Sprintf("预处理失败: %v", err), nil); err != nil {
				global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
			}
		} else {
			global.APP_LOG.Error("状态管理器未初始化", zap.Uint("taskId", task.ID))
		}
		return err
	}

	// 更新进度到30%
	s.updateTaskProgress(task.ID, 30, "正在调用Provider API...")

	// 阶段2: Provider API调用（无事务）
	apiError := s.executeProviderCreation(ctx, task, instance)

	// 阶段3: 结果处理（快速事务）
	global.APP_LOG.Info("开始处理实例创建结果", zap.Uint("taskId", task.ID), zap.Bool("hasApiError", apiError != nil))
	if finalizeErr := s.finalizeInstanceCreation(context.Background(), task, instance, apiError); finalizeErr != nil {
		global.APP_LOG.Error("实例创建最终化失败", zap.Uint("taskId", task.ID), zap.Error(finalizeErr))
		return finalizeErr
	}
	global.APP_LOG.Info("实例创建结果处理完成", zap.Uint("taskId", task.ID), zap.Bool("hasApiError", apiError != nil))

	// 不再返回apiError，因为业务逻辑已经完全处理了任务状态
	if apiError != nil {
		global.APP_LOG.Error("Provider API调用失败", zap.Uint("taskId", task.ID), zap.Error(apiError))
	}

	global.APP_LOG.Info("实例创建任务处理完成", zap.Uint("taskId", task.ID), zap.Uint("instanceId", instance.ID))
	return nil
}

// prepareInstanceCreation 阶段1: 数据库预处理（新机制：不依赖预留资源）
func (s *Service) prepareInstanceCreation(ctx context.Context, task *adminModel.Task) (*providerModel.Instance, error) {
	// 解析任务数据
	var taskReq adminModel.CreateInstanceTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return nil, fmt.Errorf("解析任务数据失败: %v", err)
	}

	global.APP_LOG.Info("开始实例预处理（新机制）",
		zap.Uint("taskId", task.ID),
		zap.String("sessionId", taskReq.SessionId))

	// 初始化服务
	dbService := database.GetDatabaseService()

	// 验证各个规格ID
	cpuSpec, err := constant.GetCPUSpecByID(taskReq.CPUId)
	if err != nil {
		return nil, fmt.Errorf("无效的CPU规格ID: %v", err)
	}

	memorySpec, err := constant.GetMemorySpecByID(taskReq.MemoryId)
	if err != nil {
		return nil, fmt.Errorf("无效的内存规格ID: %v", err)
	}

	diskSpec, err := constant.GetDiskSpecByID(taskReq.DiskId)
	if err != nil {
		return nil, fmt.Errorf("无效的磁盘规格ID: %v", err)
	}

	bandwidthSpec, err := constant.GetBandwidthSpecByID(taskReq.BandwidthId)
	if err != nil {
		return nil, fmt.Errorf("无效的带宽规格ID: %v", err)
	}

	var instance providerModel.Instance

	// 在单个事务中完成所有数据库操作（新机制：不需要预留资源消费）
	err = dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// 重新验证镜像和服务器（防止状态变化）
		var systemImage systemModel.SystemImage
		if err := tx.Where("id = ? AND status = ?", taskReq.ImageId, "active").First(&systemImage).Error; err != nil {
			return fmt.Errorf("镜像不存在或已禁用")
		}

		var provider providerModel.Provider
		if err := tx.Where("id = ? AND status IN (?)", taskReq.ProviderId, []string{"active", "partial"}).First(&provider).Error; err != nil {
			return fmt.Errorf("服务器不存在或不可用")
		}

		if provider.IsFrozen {
			return fmt.Errorf("服务器已被冻结")
		}

		// 验证Provider是否过期
		if provider.ExpiresAt != nil && provider.ExpiresAt.Before(time.Now()) {
			return fmt.Errorf("服务器已过期")
		}

		// 生成实例名称
		instanceName := s.generateInstanceName(provider.Name)

		// 设置实例到期时间，与Provider的到期时间同步
		var expiredAt time.Time
		if provider.ExpiresAt != nil {
			// 如果Provider有到期时间，使用Provider的到期时间
			expiredAt = *provider.ExpiresAt
		} else {
			// 如果Provider没有到期时间，默认为1年后
			expiredAt = time.Now().AddDate(1, 0, 0)
		}

		// 创建实例记录
		instance = providerModel.Instance{
			Name:           instanceName,
			Provider:       provider.Name,
			ProviderID:     provider.ID,
			Image:          systemImage.Name,
			CPU:            cpuSpec.Cores,
			Memory:         int64(memorySpec.SizeMB),
			Disk:           int64(diskSpec.SizeMB),
			Bandwidth:      bandwidthSpec.SpeedMbps,
			InstanceType:   systemImage.InstanceType,
			UserID:         task.UserID,
			Status:         "creating",
			OSType:         systemImage.OSType,
			ExpiredAt:      expiredAt,
			TrafficLimited: false, // 显式设置为false，确保不会因流量误判为超限
		}

		// 创建实例
		if err := tx.Create(&instance).Error; err != nil {
			return fmt.Errorf("创建实例失败: %v", err)
		}

		// 更新任务关联的实例ID和状态
		if err := tx.Model(task).Updates(map[string]interface{}{
			"instance_id": instance.ID,
			"status":      "processing",
		}).Error; err != nil {
			return fmt.Errorf("更新任务状态失败: %v", err)
		}

		// 新机制：不需要消费预留资源，因为在创建时已经原子化处理了
		// 直接分配Provider资源（使用悲观锁）
		resourceService := &resources.ResourceService{}
		if err := resourceService.AllocateResourcesInTx(tx, provider.ID, systemImage.InstanceType,
			cpuSpec.Cores, int64(memorySpec.SizeMB), int64(diskSpec.SizeMB)); err != nil {
			return fmt.Errorf("分配Provider资源失败: %v", err)
		}

		return nil
	})

	if err != nil {
		global.APP_LOG.Error("实例预处理事务失败",
			zap.Uint("taskId", task.ID),
			zap.String("sessionId", taskReq.SessionId),
			zap.Error(err))
		return nil, err
	}

	global.APP_LOG.Info("实例预处理完成（新机制）",
		zap.Uint("taskId", task.ID),
		zap.String("sessionId", taskReq.SessionId),
		zap.Uint("instanceId", instance.ID))

	// 更新进度到25%
	s.updateTaskProgress(task.ID, 25, "数据库预处理完成")

	return &instance, nil
}

// executeProviderCreation 阶段2: Provider API调用
func (s *Service) executeProviderCreation(ctx context.Context, task *adminModel.Task, instance *providerModel.Instance) error {
	global.APP_LOG.Info("开始Provider API调用阶段", zap.Uint("taskId", task.ID))

	// 检查上下文状态
	if ctx.Err() != nil {
		global.APP_LOG.Warn("Provider API调用开始时上下文已取消", zap.Uint("taskId", task.ID), zap.Error(ctx.Err()))
		return ctx.Err()
	}

	// 解析任务数据获取创建实例所需的参数
	var taskReq adminModel.CreateInstanceTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		err := fmt.Errorf("解析任务数据失败: %v", err)
		global.APP_LOG.Error("解析任务数据失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return err
	}

	// 直接从数据库获取Provider配置
	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("name = ? AND status = ?", instance.Provider, "active").First(&dbProvider).Error; err != nil {
		err := fmt.Errorf("Provider %s 不存在或不可用", instance.Provider)
		global.APP_LOG.Error("Provider不存在", zap.Uint("taskId", task.ID), zap.String("provider", instance.Provider), zap.Error(err))
		return err
	}

	// 检查Provider是否过期或冻结
	if dbProvider.IsFrozen {
		err := fmt.Errorf("Provider %s 已被冻结", instance.Provider)
		global.APP_LOG.Error("Provider已冻结", zap.Uint("taskId", task.ID), zap.String("provider", instance.Provider))
		return err
	}

	if dbProvider.ExpiresAt != nil && dbProvider.ExpiresAt.Before(time.Now()) {
		err := fmt.Errorf("Provider %s 已过期", instance.Provider)
		global.APP_LOG.Error("Provider已过期", zap.Uint("taskId", task.ID), zap.String("provider", instance.Provider), zap.Time("expiresAt", *dbProvider.ExpiresAt))
		return err
	}

	// 实现实际的Provider API调用逻辑
	// 首先尝试从ProviderService获取已连接的Provider实例
	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProvider(instance.Provider)

	if !exists {
		// 如果Provider未连接，尝试动态加载
		global.APP_LOG.Info("Provider未连接，尝试动态加载", zap.String("provider", instance.Provider))
		if err := providerSvc.LoadProvider(dbProvider); err != nil {
			global.APP_LOG.Error("动态加载Provider失败", zap.String("provider", instance.Provider), zap.Error(err))
			err := fmt.Errorf("Provider %s 连接失败: %v", instance.Provider, err)
			return err
		}

		// 重新获取Provider实例
		providerInstance, exists = providerSvc.GetProvider(instance.Provider)
		if !exists {
			err := fmt.Errorf("Provider %s 连接后仍然不可用", instance.Provider)
			global.APP_LOG.Error("Provider连接后仍然不可用", zap.Uint("taskId", task.ID), zap.String("provider", instance.Provider))
			return err
		}
	}

	// 获取镜像名称
	var systemImage systemModel.SystemImage
	if err := global.APP_DB.Where("id = ?", taskReq.ImageId).First(&systemImage).Error; err != nil {
		err := fmt.Errorf("获取镜像信息失败: %v", err)
		global.APP_LOG.Error("获取镜像信息失败", zap.Uint("taskId", task.ID), zap.Uint("imageId", taskReq.ImageId), zap.Error(err))
		return err
	}

	// 将规格ID转换为实际数值
	cpuSpec, err := constant.GetCPUSpecByID(taskReq.CPUId)
	if err != nil {
		err := fmt.Errorf("获取CPU规格失败: %v", err)
		global.APP_LOG.Error("获取CPU规格失败", zap.Uint("taskId", task.ID), zap.String("cpuId", taskReq.CPUId), zap.Error(err))
		return err
	}

	memorySpec, err := constant.GetMemorySpecByID(taskReq.MemoryId)
	if err != nil {
		err := fmt.Errorf("获取内存规格失败: %v", err)
		global.APP_LOG.Error("获取内存规格失败", zap.Uint("taskId", task.ID), zap.String("memoryId", taskReq.MemoryId), zap.Error(err))
		return err
	}

	diskSpec, err := constant.GetDiskSpecByID(taskReq.DiskId)
	if err != nil {
		err := fmt.Errorf("获取磁盘规格失败: %v", err)
		global.APP_LOG.Error("获取磁盘规格失败", zap.Uint("taskId", task.ID), zap.String("diskId", taskReq.DiskId), zap.Error(err))
		return err
	}

	bandwidthSpec, err := constant.GetBandwidthSpecByID(taskReq.BandwidthId)
	if err != nil {
		err := fmt.Errorf("获取带宽规格失败: %v", err)
		global.APP_LOG.Error("获取带宽规格失败", zap.Uint("taskId", task.ID), zap.String("bandwidthId", taskReq.BandwidthId), zap.Error(err))
		return err
	}

	// 获取用户等级信息，用于带宽限制配置
	var user userModel.User
	if err := global.APP_DB.First(&user, task.UserID).Error; err != nil {
		err := fmt.Errorf("获取用户信息失败: %v", err)
		global.APP_LOG.Error("获取用户信息失败", zap.Uint("taskId", task.ID), zap.Uint("userID", task.UserID), zap.Error(err))
		return err
	}

	global.APP_LOG.Info("规格ID转换为实际数值",
		zap.Uint("taskId", task.ID),
		zap.String("cpuId", taskReq.CPUId), zap.Int("cpuCores", cpuSpec.Cores),
		zap.String("memoryId", taskReq.MemoryId), zap.Int("memorySizeMB", memorySpec.SizeMB),
		zap.String("diskId", taskReq.DiskId), zap.Int("diskSizeMB", diskSpec.SizeMB),
		zap.String("bandwidthId", taskReq.BandwidthId), zap.Int("bandwidthSpeedMbps", bandwidthSpec.SpeedMbps),
		zap.Int("userLevel", user.Level))

	// 构建实例配置，使用实际数值而非ID
	instanceConfig := provider.InstanceConfig{
		Name:         instance.Name,
		Image:        systemImage.Name,
		CPU:          fmt.Sprintf("%d", cpuSpec.Cores),      // 使用实际核心数
		Memory:       fmt.Sprintf("%dm", memorySpec.SizeMB), // 使用实际内存大小（MB格式）
		Disk:         fmt.Sprintf("%dm", diskSpec.SizeMB),   // 使用实际磁盘大小（MB格式）
		InstanceType: instance.InstanceType,
		ImageURL:     systemImage.URL, // 添加镜像URL用于下载
		Metadata: map[string]string{
			"user_level":               fmt.Sprintf("%d", user.Level),              // 用户等级，用于带宽限制配置
			"bandwidth_spec":           fmt.Sprintf("%d", bandwidthSpec.SpeedMbps), // 用户选择的带宽规格
			"ipv4_port_mapping_method": dbProvider.IPv4PortMappingMethod,           // IPv4端口映射方式（从Provider配置获取）
			"ipv6_port_mapping_method": dbProvider.IPv6PortMappingMethod,           // IPv6端口映射方式（从Provider配置获取）
			"network_type":             dbProvider.NetworkType,                     // 网络配置类型：nat_ipv4, nat_ipv4_ipv6, dedicated_ipv4, dedicated_ipv4_ipv6, ipv6_only
			"instance_id":              fmt.Sprintf("%d", instance.ID),             // 实例ID，用于端口分配
			"provider_id":              fmt.Sprintf("%d", dbProvider.ID),           // Provider ID，用于端口区间分配
		},
	}

	// 预分配端口映射（所有Provider类型都需要）
	portMappingService := &resources.PortMappingService{}

	// 预先创建端口映射记录，用于统一的端口管理
	if err := portMappingService.CreateDefaultPortMappings(instance.ID, dbProvider.ID); err != nil {
		global.APP_LOG.Warn("预分配端口映射失败",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
	} else {
		// 获取已分配的端口映射
		portMappings, err := portMappingService.GetInstancePortMappings(instance.ID)
		if err != nil {
			global.APP_LOG.Warn("获取端口映射失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		} else {
			// 对于Docker容器，将端口映射信息添加到实例配置中
			if dbProvider.Type == "docker" {
				// 将端口映射信息添加到实例配置中
				var ports []string
				for _, port := range portMappings {
					// 格式: "0.0.0.0:公网端口:容器端口/协议"
					portMapping := fmt.Sprintf("0.0.0.0:%d:%d/%s", port.HostPort, port.GuestPort, port.Protocol)
					ports = append(ports, portMapping)
				}
				instanceConfig.Ports = ports

				global.APP_LOG.Info("Docker容器端口映射预分配成功",
					zap.Uint("taskId", task.ID),
					zap.Uint("instanceId", instance.ID),
					zap.Int("portCount", len(ports)),
					zap.Strings("ports", ports))
			} else {
				// 对于LXD等其他Provider，端口映射信息已保存在数据库中，将在实例创建时读取
				global.APP_LOG.Info("端口映射预分配成功",
					zap.Uint("taskId", task.ID),
					zap.Uint("instanceId", instance.ID),
					zap.String("providerType", dbProvider.Type),
					zap.Int("portCount", len(portMappings)))
			}
		}
	}

	// 调用Provider API创建实例
	// 创建进度回调函数，与任务系统集成
	progressCallback := func(percentage int, message string) {
		// 将Provider内部进度（0-100）映射到任务进度（40-60）
		// Provider进度占用20%的总进度空间
		adjustedPercentage := 40 + (percentage * 20 / 100)
		s.updateTaskProgress(task.ID, adjustedPercentage, message)
	}

	// 使用带进度的创建方法
	if err := providerInstance.CreateInstanceWithProgress(ctx, instanceConfig, progressCallback); err != nil {
		err := fmt.Errorf("Provider API创建实例失败: %v", err)
		global.APP_LOG.Error("Provider API创建实例失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return err
	}

	global.APP_LOG.Info("Provider API调用成功", zap.Uint("taskId", task.ID), zap.String("instanceName", instance.Name))

	// 更新进度到60%
	s.updateTaskProgress(task.ID, 60, "Provider API调用成功")

	return nil
}

// finalizeInstanceCreation 阶段3: 结果处理
func (s *Service) finalizeInstanceCreation(ctx context.Context, task *adminModel.Task, instance *providerModel.Instance, apiError error) error {
	global.APP_LOG.Info("开始最终化实例创建", zap.Uint("taskId", task.ID), zap.Bool("hasApiError", apiError != nil))

	dbService := database.GetDatabaseService()

	// 在事务中处理结果
	err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		if apiError != nil {
			// API调用失败的处理
			global.APP_LOG.Error("Provider API调用失败，回滚实例创建", zap.Uint("taskId", task.ID), zap.Error(apiError))

			// 更新实例状态为失败
			if err := tx.Model(instance).Updates(map[string]interface{}{
				"status": "failed",
			}).Error; err != nil {
				return fmt.Errorf("更新实例状态失败: %v", err)
			}

			// 清理预分配的端口映射
			portMappingService := &resources.PortMappingService{}
			if err := portMappingService.DeleteInstancePortMappingsInTx(tx, instance.ID); err != nil {
				global.APP_LOG.Error("清理失败实例端口映射失败",
					zap.Uint("instanceId", instance.ID),
					zap.Error(err))
				// 不返回错误，继续其他清理操作
			} else {
				global.APP_LOG.Info("清理失败实例端口映射成功",
					zap.Uint("instanceId", instance.ID))
			}

			// 释放已分配的Provider资源
			resourceService := &resources.ResourceService{}
			if err := resourceService.ReleaseResourcesInTx(tx, instance.ProviderID, instance.InstanceType,
				instance.CPU, instance.Memory, instance.Disk); err != nil {
				global.APP_LOG.Error("释放Provider资源失败", zap.Uint("instanceId", instance.ID), zap.Error(err))
				// 不返回错误，因为这不是关键操作
			} else {
				global.APP_LOG.Info("Provider资源释放成功", zap.Uint("instanceId", instance.ID))
			}

			// 注释：新机制中资源预留已在创建时被原子化消费，无需额外释放

			// 更新任务状态为失败
			if err := tx.Model(task).Updates(map[string]interface{}{
				"status":        "failed",
				"completed_at":  time.Now(),
				"error_message": apiError.Error(),
			}).Error; err != nil {
				return fmt.Errorf("更新任务状态失败: %v", err)
			}

			// 启动延迟删除任务，10秒后自动删除失败的实例
			go s.delayedDeleteFailedInstance(instance.ID)

			return nil
		}

		// API调用成功的处理
		global.APP_LOG.Info("Provider API调用成功，获取实例详细信息", zap.Uint("taskId", task.ID))

		// 尝试从Provider获取实例详细信息
		actualInstance, err := s.getInstanceDetailsAfterCreation(ctx, instance)
		if err != nil {
			global.APP_LOG.Warn("获取实例详细信息失败，使用默认值",
				zap.Uint("taskId", task.ID),
				zap.Error(err))
		}
		// 构建实例更新数据
		instanceUpdates := map[string]interface{}{
			"status":   "running",
			"username": "root",
		}

		// 获取Provider信息以设置公网IP
		var dbProvider providerModel.Provider
		if err := global.APP_DB.First(&dbProvider, instance.ProviderID).Error; err == nil {
			// 从Provider的Endpoint中提取公网IP
			if endpoint := dbProvider.Endpoint; endpoint != "" {
				// 移除端口号获取纯IP地址
				if colonIndex := strings.LastIndex(endpoint, ":"); colonIndex > 0 {
					if strings.Count(endpoint, ":") > 1 && !strings.HasPrefix(endpoint, "[") {
						instanceUpdates["public_ip"] = endpoint // IPv6格式
					} else {
						instanceUpdates["public_ip"] = endpoint[:colonIndex] // IPv4格式，移除端口
					}
				} else {
					instanceUpdates["public_ip"] = endpoint
				}
			}
		}

		// 如果成功获取了实例详情，使用真实数据
		if actualInstance != nil {
			// 保存内网IP
			if actualInstance.IP != "" {
				instanceUpdates["private_ip"] = actualInstance.IP
			}
			if actualInstance.PrivateIP != "" {
				instanceUpdates["private_ip"] = actualInstance.PrivateIP
			}
			// 如果Provider返回了公网IP，优先使用
			if actualInstance.PublicIP != "" {
				instanceUpdates["public_ip"] = actualInstance.PublicIP
			}
			// 保存IPv6地址
			if actualInstance.IPv6Address != "" {
				instanceUpdates["ipv6_address"] = actualInstance.IPv6Address
			}
			// SSH端口使用默认值22
			instanceUpdates["ssh_port"] = 22
			if actualInstance.Status != "" {
				instanceUpdates["status"] = actualInstance.Status
			}
		} else {
			// 使用默认值
			instanceUpdates["ssh_port"] = 22
		}

		// 尝试获取IPv4和IPv6地址（针对LXD、Incus和Proxmox Provider）
		if actualInstance != nil {
			providerSvc := providerService.GetProviderService()
			if providerInstance, exists := providerSvc.GetProvider(instance.Provider); exists {
				if dbProvider.Type == "lxd" {
					if lxdProvider, ok := providerInstance.(*lxd.LXDProvider); ok {
						// 获取内网IPv4地址
						if ipv4Address, err := lxdProvider.GetInstanceIPv4(instance.Name); err == nil && ipv4Address != "" {
							instanceUpdates["private_ip"] = ipv4Address
							global.APP_LOG.Info("获取到实例内网IPv4地址",
								zap.String("instanceName", instance.Name),
								zap.String("ipv4Address", ipv4Address))
						} else {
							global.APP_LOG.Warn("获取内网IPv4地址失败",
								zap.String("instanceName", instance.Name),
								zap.Error(err))
						}
						// 获取内网IPv6地址
						if ipv6Address, err := lxdProvider.GetInstanceIPv6(instance.Name); err == nil && ipv6Address != "" {
							instanceUpdates["ipv6_address"] = ipv6Address
							global.APP_LOG.Info("获取到实例内网IPv6地址",
								zap.String("instanceName", instance.Name),
								zap.String("ipv6Address", ipv6Address))
						}
						// 获取公网IPv6地址
						if publicIPv6, err := lxdProvider.GetInstancePublicIPv6(instance.Name); err == nil && publicIPv6 != "" {
							instanceUpdates["public_ipv6"] = publicIPv6
							global.APP_LOG.Info("获取到实例公网IPv6地址",
								zap.String("instanceName", instance.Name),
								zap.String("publicIPv6", publicIPv6))
						} else {
							global.APP_LOG.Warn("获取公网IPv6地址失败",
								zap.String("instanceName", instance.Name),
								zap.Error(err))
						}
					}
				} else if dbProvider.Type == "incus" {
					if incusProvider, ok := providerInstance.(*incus.IncusProvider); ok {
						// 获取内网IPv4地址
						if ipv4Address, err := incusProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ipv4Address != "" {
							instanceUpdates["private_ip"] = ipv4Address
							global.APP_LOG.Info("获取到实例内网IPv4地址",
								zap.String("instanceName", instance.Name),
								zap.String("ipv4Address", ipv4Address))
						} else {
							global.APP_LOG.Warn("获取内网IPv4地址失败",
								zap.String("instanceName", instance.Name),
								zap.Error(err))
						}
						// 获取内网IPv6地址
						if ipv6Address, err := incusProvider.GetInstanceIPv6(ctx, instance.Name); err == nil && ipv6Address != "" {
							instanceUpdates["ipv6_address"] = ipv6Address
							global.APP_LOG.Info("获取到实例内网IPv6地址",
								zap.String("instanceName", instance.Name),
								zap.String("ipv6Address", ipv6Address))
						}
						// 获取公网IPv6地址
						if publicIPv6, err := incusProvider.GetInstancePublicIPv6(ctx, instance.Name); err == nil && publicIPv6 != "" {
							instanceUpdates["public_ipv6"] = publicIPv6
							global.APP_LOG.Info("获取到实例公网IPv6地址",
								zap.String("instanceName", instance.Name),
								zap.String("publicIPv6", publicIPv6))
						} else {
							global.APP_LOG.Warn("获取公网IPv6地址失败",
								zap.String("instanceName", instance.Name),
								zap.Error(err))
						}
					}
				} else if dbProvider.Type == "proxmox" {
					// 对于Proxmox Provider，优先使用专门的IPv4/IPv6方法获取地址
					if proxmoxProvider, ok := providerInstance.(interface {
						GetInstanceIPv4(ctx context.Context, instanceName string) (string, error)
						GetInstanceIPv6(ctx context.Context, instanceName string) (string, error)
						GetInstancePublicIPv6(ctx context.Context, instanceName string) (string, error)
					}); ok {
						// 获取内网IPv4地址
						if ipv4Address, err := proxmoxProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ipv4Address != "" {
							instanceUpdates["private_ip"] = ipv4Address
							global.APP_LOG.Info("获取到Proxmox实例内网IPv4地址",
								zap.String("instanceName", instance.Name),
								zap.String("ipv4Address", ipv4Address))
						} else {
							global.APP_LOG.Warn("获取Proxmox实例内网IPv4地址失败",
								zap.String("instanceName", instance.Name),
								zap.Error(err))
						}

						// 获取IPv6地址并根据网络类型决定存储位置
						if ipv6Address, err := proxmoxProvider.GetInstanceIPv6(ctx, instance.Name); err == nil && ipv6Address != "" {
							// 检查当前Provider的网络类型
							if dbProvider.NetworkType == "nat_ipv4_ipv6" {
								// NAT模式：获取到的是内网IPv6地址
								instanceUpdates["ipv6_address"] = ipv6Address
								global.APP_LOG.Info("获取到Proxmox实例内网IPv6地址（NAT模式）",
									zap.String("instanceName", instance.Name),
									zap.String("ipv6Address", ipv6Address))

								// 获取公网IPv6地址
								if publicIPv6, err := proxmoxProvider.GetInstancePublicIPv6(ctx, instance.Name); err == nil && publicIPv6 != "" {
									instanceUpdates["public_ipv6"] = publicIPv6
									global.APP_LOG.Info("获取到Proxmox实例公网IPv6地址（NAT模式）",
										zap.String("instanceName", instance.Name),
										zap.String("publicIPv6", publicIPv6))
								} else {
									global.APP_LOG.Warn("获取Proxmox实例公网IPv6地址失败（NAT模式）",
										zap.String("instanceName", instance.Name),
										zap.Error(err))
								}
							} else {
								// 直接分配模式（dedicated_ipv4_ipv6, ipv6_only）：获取到的就是公网IPv6地址
								instanceUpdates["public_ipv6"] = ipv6Address
								global.APP_LOG.Info("获取到Proxmox实例公网IPv6地址（直接分配模式）",
									zap.String("instanceName", instance.Name),
									zap.String("networkType", dbProvider.NetworkType),
									zap.String("publicIPv6", ipv6Address))
							}
						} else {
							global.APP_LOG.Warn("获取Proxmox实例IPv6地址失败",
								zap.String("instanceName", instance.Name),
								zap.Error(err))
						}
					} else {
						// 回退到原来的GetInstance方法
						if proxmoxProvider, ok := providerInstance.(interface {
							GetInstance(ctx context.Context, instanceID string) (*provider.Instance, error)
						}); ok {
							if proxmoxInstance, err := proxmoxProvider.GetInstance(ctx, instance.Name); err == nil && proxmoxInstance != nil {
								if proxmoxInstance.IP != "" {
									instanceUpdates["private_ip"] = proxmoxInstance.IP
									global.APP_LOG.Info("获取到Proxmox实例内网IPv4地址",
										zap.String("instanceName", instance.Name),
										zap.String("privateIP", proxmoxInstance.IP))
								} else if proxmoxInstance.PrivateIP != "" {
									instanceUpdates["private_ip"] = proxmoxInstance.PrivateIP
									global.APP_LOG.Info("获取到Proxmox实例内网IPv4地址",
										zap.String("instanceName", instance.Name),
										zap.String("privateIP", proxmoxInstance.PrivateIP))
								} else {
									global.APP_LOG.Warn("Proxmox实例返回的IP地址为空",
										zap.String("instanceName", instance.Name))
								}

								// 获取IPv6地址并根据网络类型决定存储位置（如果有）
								if proxmoxInstance.IPv6Address != "" {
									// 检查当前Provider的网络类型
									if dbProvider.NetworkType == "nat_ipv4_ipv6" {
										// NAT模式：这是内网IPv6地址
										instanceUpdates["ipv6_address"] = proxmoxInstance.IPv6Address
										global.APP_LOG.Info("获取到Proxmox实例内网IPv6地址（NAT模式）",
											zap.String("instanceName", instance.Name),
											zap.String("ipv6Address", proxmoxInstance.IPv6Address))
									} else {
										// 直接分配模式：这是公网IPv6地址
										instanceUpdates["public_ipv6"] = proxmoxInstance.IPv6Address
										global.APP_LOG.Info("获取到Proxmox实例公网IPv6地址（直接分配模式）",
											zap.String("instanceName", instance.Name),
											zap.String("networkType", dbProvider.NetworkType),
											zap.String("publicIPv6", proxmoxInstance.IPv6Address))
									}
								}
							} else {
								global.APP_LOG.Warn("无法从Proxmox Provider获取实例详情",
									zap.String("instanceName", instance.Name),
									zap.Error(err))
							}
						} else {
							global.APP_LOG.Warn("Proxmox Provider不支持必要的方法",
								zap.String("instanceName", instance.Name))
						}
					}
				}
			}
		}
		if err := tx.Model(instance).Updates(instanceUpdates).Error; err != nil {
			return fmt.Errorf("更新实例信息失败: %v", err)
		}
		// 更新用户配额
		quotaService := resources.NewQuotaService()
		resourceUsage := resources.ResourceUsage{
			CPU:       instance.CPU,
			Memory:    instance.Memory,
			Disk:      instance.Disk,
			Bandwidth: instance.Bandwidth,
		}
		if err := quotaService.UpdateUserQuotaAfterCreationWithTx(tx, task.UserID, resourceUsage); err != nil {
			global.APP_LOG.Error("更新用户配额失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("userId", task.UserID),
				zap.Error(err))
			return fmt.Errorf("更新用户配额失败: %v", err)
		}
		// 更新任务状态为处理中，等待后处理任务完成
		if err := tx.Model(task).Updates(map[string]interface{}{
			"status":   "running",
			"progress": 70, // API调用成功，但还需要后处理任务
		}).Error; err != nil {
			return fmt.Errorf("更新任务状态失败: %v", err)
		}
		return nil
	})
	if err != nil {
		global.APP_LOG.Error("最终化实例创建失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return err
	}

	// 如果任务在事务中已标记为失败，需要释放锁
	if apiError != nil {
		if global.APP_TASK_LOCK_RELEASER != nil {
			global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(task.ID)
		}
	}

	// 如果API调用成功，执行后处理任务（同步完成关键任务后再标记完成）
	if apiError == nil {
		go func(instanceID uint, providerID uint, taskID uint) {
			defer func() {
				if r := recover(); r != nil {
					global.APP_LOG.Error("实例创建后处理任务发生panic",
						zap.Uint("instanceId", instanceID),
						zap.Any("panic", r))
					// 即使后处理失败，也要标记任务完成，因为实例已经创建成功
					// 使用统一状态管理器
					stateManager := s.taskService.GetStateManager()
					if stateManager != nil {
						if err := stateManager.CompleteMainTask(taskID, true, "实例创建成功，但部分后处理任务失败", nil); err != nil {
							global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", taskID), zap.Error(err))
						}
					} else {
						global.APP_LOG.Error("状态管理器未初始化", zap.Uint("taskId", taskID))
					}
				}
			}()
			// 等待容器完全启动 - 增加等待时间确保容器充分初始化
			time.Sleep(45 * time.Second)
			// 在开始后处理前，检查任务状态，确保没有被其他地方标记为失败
			var currentTask adminModel.Task
			if err := global.APP_DB.Where("id = ?", taskID).First(&currentTask).Error; err != nil {
				global.APP_LOG.Error("获取任务状态失败，跳过后处理", zap.Uint("taskId", taskID), zap.Error(err))
				return
			}
			// 如果任务状态不是running，说明任务已经被其他地方处理（可能失败了），跳过后处理
			if currentTask.Status != "running" {
				global.APP_LOG.Info("任务状态已非running，跳过后处理任务",
					zap.Uint("taskId", taskID),
					zap.String("currentStatus", currentTask.Status))
				return
			}
			global.APP_LOG.Info("开始执行实例创建后处理任务", zap.Uint("instanceId", instanceID))

			// 更新进度到75%
			s.updateTaskProgress(taskID, 75, "正在配置端口映射...")

			// 1. 创建默认端口映射（对于非Docker或需要补充端口映射的情况）
			portMappingService := &resources.PortMappingService{}

			// 检查是否已经有端口映射（Docker在创建前已分配）
			existingPorts, _ := portMappingService.GetInstancePortMappings(instanceID)
			if len(existingPorts) == 0 {
				// 只有在没有端口映射时才创建
				if err := portMappingService.CreateDefaultPortMappings(instanceID, providerID); err != nil {
					global.APP_LOG.Warn("创建默认端口映射失败",
						zap.Uint("instanceId", instanceID),
						zap.Error(err))
				} else {
					global.APP_LOG.Info("默认端口映射创建成功",
						zap.Uint("instanceId", instanceID))
				}
			} else {
				global.APP_LOG.Info("实例已有端口映射，跳过创建",
					zap.Uint("instanceId", instanceID),
					zap.Int("existingPortCount", len(existingPorts)))
			}

			// 更新进度到80%
			s.updateTaskProgress(taskID, 80, "正在初始化监控...")

			// 2. 初始化vnStat监控
			vnstatService := &vnstat.Service{}
			vnstatInitSuccess := false
			if err := vnstatService.InitializeVnStatForInstance(instanceID); err != nil {
				global.APP_LOG.Warn("初始化vnStat监控失败",
					zap.Uint("instanceId", instanceID),
					zap.Error(err))
			} else {
				global.APP_LOG.Info("vnStat监控初始化成功",
					zap.Uint("instanceId", instanceID))
				vnstatInitSuccess = true
			}

			// 更新进度到85%
			s.updateTaskProgress(taskID, 85, "正在设置SSH密码...")

			// 3. 设置实例SSH密码（关键步骤）
			var currentInstance providerModel.Instance
			var passwordSetSuccess bool = false
			if err := global.APP_DB.Where("id = ?", instanceID).First(&currentInstance).Error; err != nil {
				global.APP_LOG.Error("获取实例信息失败，无法设置SSH密码",
					zap.Uint("instanceId", instanceID),
					zap.Error(err))
			} else if currentInstance.Password != "" {
				// 设置实例SSH密码，重试机制确保成功
				providerSvc := providerService.GetProviderService()
				maxRetries := 3
				for i := 0; i < maxRetries; i++ {
					if err := providerSvc.SetInstancePassword(context.Background(), currentInstance.ProviderID, currentInstance.Name, currentInstance.Password); err != nil {
						global.APP_LOG.Warn("设置实例SSH密码失败，正在重试",
							zap.Uint("instanceId", instanceID),
							zap.String("instanceName", currentInstance.Name),
							zap.Int("attempt", i+1),
							zap.Int("maxRetries", maxRetries),
							zap.Error(err))
						if i < maxRetries-1 {
							time.Sleep(15 * time.Second) // 增加重试间隔到15秒
						}
					} else {
						global.APP_LOG.Info("实例SSH密码设置成功",
							zap.Uint("instanceId", instanceID),
							zap.String("instanceName", currentInstance.Name))
						passwordSetSuccess = true
						break
					}
				}
			}

			// 更新进度到90%
			s.updateTaskProgress(taskID, 90, "正在配置网络监控...")

			// 4. 自动检测并设置vnstat接口（仅在vnStat初始化成功时执行）
			if vnstatInitSuccess {
				trafficService := &traffic.Service{}
				if err := trafficService.AutoDetectVnstatInterface(instanceID); err != nil {
					global.APP_LOG.Warn("自动检测vnstat接口失败",
						zap.Uint("instanceId", instanceID),
						zap.Error(err))
				} else {
					global.APP_LOG.Info("vnstat接口自动检测成功",
						zap.Uint("instanceId", instanceID))
				}
			} else {
				global.APP_LOG.Info("跳过vnstat接口检测（vnStat初始化失败）",
					zap.Uint("instanceId", instanceID))
			}

			// 更新进度到95%
			s.updateTaskProgress(taskID, 95, "正在启动流量同步...")

			// 5. 触发流量同步（仅在vnStat初始化成功时执行）
			if vnstatInitSuccess {
				syncTrigger := traffic.NewSyncTriggerService()
				syncTrigger.TriggerInstanceTrafficSync(instanceID, "实例创建后初始同步")

				global.APP_LOG.Info("实例流量同步已触发",
					zap.Uint("instanceId", instanceID))
			} else {
				global.APP_LOG.Info("跳过流量同步触发（vnStat初始化失败）",
					zap.Uint("instanceId", instanceID))
			}

			// 最终完成状态判断
			completionMessage := "实例创建成功"
			if !passwordSetSuccess && currentInstance.Password != "" {
				completionMessage = "实例创建成功，但SSH密码设置失败，请手动重置密码"
				global.APP_LOG.Warn("实例创建完成但SSH密码设置失败",
					zap.Uint("instanceId", instanceID),
					zap.String("instanceName", currentInstance.Name))
			}

			// 标记任务最终完成
			// 使用统一状态管理器
			stateManager := s.taskService.GetStateManager()
			if stateManager != nil {
				if err := stateManager.CompleteMainTask(taskID, true, completionMessage, nil); err != nil {
					global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", taskID), zap.Error(err))
				}
			} else {
				global.APP_LOG.Error("状态管理器未初始化", zap.Uint("taskId", taskID))
			}

			global.APP_LOG.Info("实例创建后处理任务完成",
				zap.Uint("instanceId", instanceID),
				zap.Bool("passwordSetSuccess", passwordSetSuccess))
		}(instance.ID, instance.ProviderID, task.ID)
	}
	global.APP_LOG.Info("实例创建最终化完成", zap.Uint("taskId", task.ID))
	return nil
}

// markTaskFailed 标记任务失败
func (s *Service) markTaskFailed(taskID uint, errorMessage string) {
	if err := global.APP_DB.Model(&adminModel.Task{}).Where("id = ?", taskID).Updates(map[string]interface{}{
		"status":        "failed",
		"completed_at":  time.Now(),
		"error_message": errorMessage,
	}).Error; err != nil {
		global.APP_LOG.Error("标记任务失败时出错", zap.Uint("taskId", taskID), zap.Error(err))
	}

	// 注释：新机制中资源预留已在创建时被原子化消费，无需额外释放

	// 释放并发控制锁
	if global.APP_TASK_LOCK_RELEASER != nil {
		global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(taskID)
	}
}

// generateInstanceName 生成实例名称
func (s *Service) generateInstanceName(providerName string) string {
	// 生成格式: provider-name-4位随机字符 (如: ifast-d73a)
	randomStr := fmt.Sprintf("%04x", rand.Intn(65536)) // 生成4位16进制随机字符

	// 清理provider名称，移除特殊字符
	cleanName := strings.ReplaceAll(strings.ToLower(providerName), " ", "-")
	cleanName = strings.ReplaceAll(cleanName, "_", "-")

	return fmt.Sprintf("%s-%s", cleanName, randomStr)
}

// generatePassword 生成随机密码
func (s *Service) generatePassword() string {
	return utils.GenerateStrongPassword(12)
}

// extractHost 从endpoint中提取主机地址
func (s *Service) extractHost(endpoint string) string {
	if strings.Contains(endpoint, "://") {
		parts := strings.Split(endpoint, "://")
		if len(parts) > 1 {
			hostPort := parts[1]
			if strings.Contains(hostPort, ":") {
				return strings.Split(hostPort, ":")[0]
			}
			return hostPort
		}
	}

	if strings.Contains(endpoint, ":") {
		return strings.Split(endpoint, ":")[0]
	}

	return endpoint
}

// getInstanceDetailsAfterCreation 创建实例后获取实例详细信息
func (s *Service) getInstanceDetailsAfterCreation(ctx context.Context, instance *providerModel.Instance) (*providerModel.ProviderInstance, error) {
	// 获取Provider信息
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, instance.ProviderID).Error; err != nil {
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 获取Provider实例
	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProvider(instance.Provider)

	if !exists {
		// 如果Provider未连接，尝试动态加载
		if err := providerSvc.LoadProvider(dbProvider); err != nil {
			return nil, fmt.Errorf("连接Provider失败: %w", err)
		}

		// 重新获取Provider实例
		providerInstance, exists = providerSvc.GetProvider(instance.Provider)
		if !exists {
			return nil, fmt.Errorf("Provider %s 连接后仍然不可用", instance.Provider)
		}
	}

	// 获取实例详细信息
	actualInstance, err := providerInstance.GetInstance(ctx, instance.Name)
	if err != nil {
		return nil, fmt.Errorf("从Provider获取实例详情失败: %w", err)
	}

	return actualInstance, nil
}

// validateProviderImageCompatibility 验证Provider和Image的兼容性
func (s *Service) validateProviderImageCompatibility(provider *providerModel.Provider, image *systemModel.SystemImage) error {
	// 验证Provider类型是否支持该镜像
	supportedProviders := strings.Split(image.ProviderType, ",")
	providerSupported := false
	for _, supportedType := range supportedProviders {
		if strings.TrimSpace(supportedType) == provider.Type {
			providerSupported = true
			break
		}
	}

	if !providerSupported {
		return fmt.Errorf("所选镜像不支持Provider类型 %s，支持的类型: %s", provider.Type, image.ProviderType)
	}

	// 验证架构兼容性
	if provider.Architecture != "" && image.Architecture != "" && provider.Architecture != image.Architecture {
		return fmt.Errorf("架构不匹配：Provider架构为 %s，镜像架构为 %s", provider.Architecture, image.Architecture)
	}

	// 验证实例类型支持
	if image.InstanceType == "vm" && !provider.VirtualMachineEnabled {
		return errors.New("该Provider不支持虚拟机实例")
	}

	if image.InstanceType == "container" && !provider.ContainerEnabled {
		return errors.New("该Provider不支持容器实例")
	}

	return nil
}

// validateUserSpecPermissions 验证用户是否有权限使用所选规格
func (s *Service) validateUserSpecPermissions(userID uint, cpuSpec *constant.CPUSpec, memorySpec *constant.MemorySpec, diskSpec *constant.DiskSpec, bandwidthSpec *constant.BandwidthSpec) error {
	// 获取用户权限信息
	permissionService := auth.PermissionService{}
	effective, err := permissionService.GetUserEffectivePermission(userID)
	if err != nil {
		return fmt.Errorf("获取用户权限失败: %v", err)
	}

	// 管理员可以使用所有规格
	if effective.EffectiveType == "admin" {
		return nil
	}

	// 基于资源使用量的等级验证策略
	// 这里可以根据实际业务需求调整等级要求

	// CPU规格验证 - 高核心数需要更高等级
	if cpuSpec.Cores > 8 && effective.EffectiveLevel < 4 {
		return fmt.Errorf("您的等级不足以使用CPU规格 %s（需要等级 4，当前等级 %d）",
			cpuSpec.Name, effective.EffectiveLevel)
	}
	if cpuSpec.Cores > 4 && effective.EffectiveLevel < 3 {
		return fmt.Errorf("您的等级不足以使用CPU规格 %s（需要等级 3，当前等级 %d）",
			cpuSpec.Name, effective.EffectiveLevel)
	}

	// 内存规格验证 - 大内存需要更高等级
	if memorySpec.SizeMB > 8192 && effective.EffectiveLevel < 4 {
		return fmt.Errorf("您的等级不足以使用内存规格 %s（需要等级 4，当前等级 %d）",
			memorySpec.Name, effective.EffectiveLevel)
	}
	if memorySpec.SizeMB > 4096 && effective.EffectiveLevel < 3 {
		return fmt.Errorf("您的等级不足以使用内存规格 %s（需要等级 3，当前等级 %d）",
			memorySpec.Name, effective.EffectiveLevel)
	}
	if memorySpec.SizeMB > 2048 && effective.EffectiveLevel < 2 {
		return fmt.Errorf("您的等级不足以使用内存规格 %s（需要等级 2，当前等级 %d）",
			memorySpec.Name, effective.EffectiveLevel)
	}

	// 磁盘规格验证 - 大磁盘需要更高等级
	if diskSpec.SizeMB > 51200 && effective.EffectiveLevel < 4 { // > 50GB
		return fmt.Errorf("您的等级不足以使用磁盘规格 %s（需要等级 4，当前等级 %d）",
			diskSpec.Name, effective.EffectiveLevel)
	}
	if diskSpec.SizeMB > 20480 && effective.EffectiveLevel < 3 { // > 20GB
		return fmt.Errorf("您的等级不足以使用磁盘规格 %s（需要等级 3，当前等级 %d）",
			diskSpec.Name, effective.EffectiveLevel)
	}
	if diskSpec.SizeMB > 10240 && effective.EffectiveLevel < 2 { // > 10GB
		return fmt.Errorf("您的等级不足以使用磁盘规格 %s（需要等级 2，当前等级 %d）",
			diskSpec.Name, effective.EffectiveLevel)
	}

	// 带宽规格验证 - 高带宽需要更高等级
	if bandwidthSpec.SpeedMbps > 5000 && effective.EffectiveLevel < 4 {
		return fmt.Errorf("您的等级不足以使用带宽规格 %s（需要等级 4，当前等级 %d）",
			bandwidthSpec.Name, effective.EffectiveLevel)
	}
	if bandwidthSpec.SpeedMbps > 2000 && effective.EffectiveLevel < 3 {
		return fmt.Errorf("您的等级不足以使用带宽规格 %s（需要等级 3，当前等级 %d）",
			bandwidthSpec.Name, effective.EffectiveLevel)
	}
	if bandwidthSpec.SpeedMbps > 1000 && effective.EffectiveLevel < 2 {
		return fmt.Errorf("您的等级不足以使用带宽规格 %s（需要等级 2，当前等级 %d）",
			bandwidthSpec.Name, effective.EffectiveLevel)
	}

	return nil
}

// updateTaskProgress 更新任务进度
func (s *Service) updateTaskProgress(taskID uint, progress int, message string) {
	updates := map[string]interface{}{
		"progress": progress,
	}
	if message != "" {
		updates["status_message"] = message
	}

	if err := global.APP_DB.Model(&adminModel.Task{}).Where("id = ?", taskID).Updates(updates).Error; err != nil {
		global.APP_LOG.Error("更新任务进度失败",
			zap.Uint("taskId", taskID),
			zap.Int("progress", progress),
			zap.String("message", message),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("任务进度更新成功",
			zap.Uint("taskId", taskID),
			zap.Int("progress", progress),
			zap.String("message", message))
	}
}

// markTaskCompleted 标记任务最终完成
func (s *Service) markTaskCompleted(taskID uint, message string) {
	updates := map[string]interface{}{
		"status":       "completed",
		"completed_at": time.Now(),
		"progress":     100,
	}
	if message != "" {
		updates["status_message"] = message
	}

	// 只在任务状态为running时才更新为completed，避免覆盖failed状态
	result := global.APP_DB.Model(&adminModel.Task{}).Where("id = ? AND status = ?", taskID, "running").Updates(updates)
	if result.Error != nil {
		global.APP_LOG.Error("标记任务完成失败",
			zap.Uint("taskId", taskID),
			zap.String("message", message),
			zap.Error(result.Error))
	} else if result.RowsAffected == 0 {
		// 没有更新任何行，说明任务状态不是running（可能已经是failed或其他状态）
		global.APP_LOG.Warn("任务状态不是running，跳过标记为完成",
			zap.Uint("taskId", taskID),
			zap.String("message", message))
	} else {
		global.APP_LOG.Info("任务标记为完成",
			zap.Uint("taskId", taskID),
			zap.String("message", message))

		// 释放并发控制锁
		if global.APP_TASK_LOCK_RELEASER != nil {
			global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(taskID)
		}
	}
}

// delayedDeleteFailedInstance 延迟删除失败的实例
func (s *Service) delayedDeleteFailedInstance(instanceID uint) {
	global.APP_LOG.Info("启动延迟删除任务",
		zap.Uint("instanceId", instanceID),
		zap.String("reason", "创建失败自动清理"))

	time.Sleep(10 * time.Second)

	// 使用反射动态导入避免循环依赖问题
	// 导入路径: oneclickvirt/service/admin/instance
	adminInstanceSvc := struct {
		taskService interfaces.TaskServiceInterface
	}{
		taskService: s.taskService,
	}

	// 模拟管理员删除实例的逻辑
	if err := s.executeAdminDeleteInstance(instanceID, adminInstanceSvc.taskService); err != nil {
		global.APP_LOG.Error("延迟删除失败实例失败",
			zap.Uint("instanceId", instanceID),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("延迟删除失败实例成功",
			zap.Uint("instanceId", instanceID))
	}
}

// executeAdminDeleteInstance 执行管理员删除实例操作
func (s *Service) executeAdminDeleteInstance(instanceID uint, taskService interfaces.TaskServiceInterface) error {
	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在")
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}

	// 检查实例状态，避免重复删除
	if instance.Status == "deleting" {
		return fmt.Errorf("实例正在删除中")
	}

	// 检查是否已有进行中的删除任务
	var existingTask adminModel.Task
	if err := global.APP_DB.Where("instance_id = ? AND task_type = 'delete' AND status IN ('pending', 'running')", instance.ID).First(&existingTask).Error; err == nil {
		return fmt.Errorf("实例已有删除任务正在进行")
	}

	// 创建管理员删除任务数据
	taskData := map[string]interface{}{
		"instanceId":     instanceID,
		"providerId":     instance.ProviderID,
		"adminOperation": true, // 标记为管理员操作
	}

	taskDataJSON, err := json.Marshal(taskData)
	if err != nil {
		return fmt.Errorf("序列化任务数据失败: %v", err)
	}

	// 创建删除任务，设置为不可被用户取消
	task, err := taskService.CreateTask(instance.UserID, &instance.ProviderID, &instanceID, "delete", string(taskDataJSON), 1800)
	if err != nil {
		return fmt.Errorf("创建删除任务失败: %v", err)
	}

	// 标记任务为管理员操作，不允许用户取消
	if err := global.APP_DB.Model(task).Update("is_force_stoppable", false).Error; err != nil {
		global.APP_LOG.Warn("更新任务可取消状态失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	// 更新实例状态为删除中
	if err := global.APP_DB.Model(&instance).Update("status", "deleting").Error; err != nil {
		global.APP_LOG.Warn("更新实例状态失败", zap.Uint("instanceId", instanceID), zap.Error(err))
	}

	global.APP_LOG.Info("管理员创建删除任务成功",
		zap.Uint("instanceId", instanceID),
		zap.String("instanceName", instance.Name),
		zap.Uint("taskId", task.ID))

	return nil
}

// validateInstanceMinimumRequirements 验证实例的最低硬件要求（统一验证）
func (s *Service) validateInstanceMinimumRequirements(image *systemModel.SystemImage, memorySpec *constant.MemorySpec, diskSpec *constant.DiskSpec, provider *providerModel.Provider) error {
	if image == nil {
		return fmt.Errorf("镜像信息不能为空")
	}

	var minMemoryMB, minDiskMB int

	if image.InstanceType == "vm" {
		// 虚拟机统一要求：512MB内存，5GB硬盘
		minMemoryMB = 512
		minDiskMB = 5000 // 5000MB
	} else if image.InstanceType == "container" {
		// 容器统一要求：128MB内存，基础1GB硬盘
		minMemoryMB = 128
		minDiskMB = 1000 // 1000MB

		// Proxmox容器特殊要求：4GB硬盘
		if provider != nil && provider.Type == "proxmox" {
			minDiskMB = 4000 // 4000MB
		}
	} else {
		return fmt.Errorf("未知的实例类型: %s", image.InstanceType)
	}

	// 验证内存要求
	if memorySpec.SizeMB < minMemoryMB {
		instanceTypeDesc := "虚拟机"
		if image.InstanceType == "container" {
			instanceTypeDesc = "容器"
		}
		return fmt.Errorf("%s最少需要%dMB内存，当前选择%dMB不足",
			instanceTypeDesc, minMemoryMB, memorySpec.SizeMB)
	}

	// 验证磁盘要求
	if diskSpec.SizeMB < minDiskMB {
		diskGB := minDiskMB / 1024
		instanceTypeDesc := "虚拟机"
		if image.InstanceType == "container" {
			instanceTypeDesc = "容器"
		}
		return fmt.Errorf("%s最少需要%dGB硬盘，当前选择%dMB不足",
			instanceTypeDesc, diskGB, diskSpec.SizeMB)
	}

	global.APP_LOG.Info("实例最低硬件要求验证通过",
		zap.String("imageName", image.Name),
		zap.String("instanceType", image.InstanceType),
		zap.String("providerType", provider.Type),
		zap.Int("requiredMemoryMB", minMemoryMB),
		zap.Int("requiredDiskMB", minDiskMB),
		zap.Int("selectedMemoryMB", memorySpec.SizeMB),
		zap.Int("selectedDiskMB", diskSpec.SizeMB))

	return nil
}

// validateCreateTaskPermissions 验证任务创建权限（三重验证）
func (s *Service) validateCreateTaskPermissions(userID uint, providerID uint, instanceType string,
	cpu int, memory int64, disk int64, bandwidth int) error {

	// 1. 用户配额验证
	quotaService := resources.NewQuotaService()
	quotaReq := resources.ResourceRequest{
		UserID:       userID,
		CPU:          cpu,
		Memory:       memory,
		Disk:         disk,
		Bandwidth:    bandwidth,
		InstanceType: instanceType,
	}

	quotaResult, err := quotaService.ValidateInstanceCreation(quotaReq)
	if err != nil {
		return fmt.Errorf("用户配额验证失败: %v", err)
	}

	if !quotaResult.Allowed {
		return fmt.Errorf("用户配额不足: %s", quotaResult.Reason)
	}

	// 2. Provider资源验证
	resourceService := &resources.ResourceService{}
	resourceReq := resourceModel.ResourceCheckRequest{
		ProviderID:   providerID,
		InstanceType: instanceType,
		CPU:          cpu,
		Memory:       memory,
		Disk:         disk,
	}

	resourceResult, err := resourceService.CheckProviderResources(resourceReq)
	if err != nil {
		return fmt.Errorf("Provider资源检查失败: %v", err)
	}

	if !resourceResult.Allowed {
		return fmt.Errorf("Provider资源不足: %s", resourceResult.Reason)
	}

	// 3. Provider并发任务数验证
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return fmt.Errorf("查询Provider失败: %v", err)
	}

	// 检查Provider的并发任务限制
	if err := s.validateProviderConcurrencyLimit(providerID, provider.MaxConcurrentTasks, provider.AllowConcurrentTasks); err != nil {
		return fmt.Errorf("Provider并发限制验证失败: %v", err)
	}

	global.APP_LOG.Info("任务创建三重验证通过",
		zap.Uint("userID", userID),
		zap.Uint("providerID", providerID),
		zap.String("instanceType", instanceType),
		zap.Int("cpu", cpu),
		zap.Int64("memory", memory),
		zap.Int64("disk", disk),
		zap.Int("bandwidth", bandwidth))

	return nil
}

// validateProviderConcurrencyLimit 验证Provider并发任务限制
func (s *Service) validateProviderConcurrencyLimit(providerID uint, maxConcurrentTasks int, allowConcurrentTasks bool) error {
	// 分别统计running和pending任务数
	var runningTaskCount int64
	var pendingTaskCount int64

	err := global.APP_DB.Model(&adminModel.Task{}).
		Where("provider_id = ? AND status = 'running'", providerID).
		Count(&runningTaskCount).Error
	if err != nil {
		return fmt.Errorf("查询Provider当前running任务数失败: %v", err)
	}

	err = global.APP_DB.Model(&adminModel.Task{}).
		Where("provider_id = ? AND status = 'pending'", providerID).
		Count(&pendingTaskCount).Error
	if err != nil {
		return fmt.Errorf("查询Provider当前pending任务数失败: %v", err)
	}

	// 确定最大允许并发执行任务数
	var maxRunningTasks int
	if allowConcurrentTasks {
		maxRunningTasks = maxConcurrentTasks
		if maxRunningTasks <= 0 {
			maxRunningTasks = 1 // 默认值
		}
	} else {
		maxRunningTasks = 1 // 串行模式只允许1个运行中的任务
	}

	// pending任务可以排队，可无限制排队

	global.APP_LOG.Info("Provider并发验证通过",
		zap.Uint("providerID", providerID),
		zap.Int64("runningTasks", runningTaskCount),
		zap.Int64("pendingTasks", pendingTaskCount),
		zap.Int("maxRunningTasks", maxRunningTasks),
		zap.Bool("allowConcurrent", allowConcurrentTasks))

	return nil
}
