package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	"oneclickvirt/service/database"
	"oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// prepareInstanceCreation 阶段1: 数据库预处理（不依赖预留资源）
func (s *Service) prepareInstanceCreation(ctx context.Context, task *adminModel.Task) (*providerModel.Instance, error) {
	// 解析任务数据
	var taskReq adminModel.CreateInstanceTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return nil, fmt.Errorf("解析任务数据失败: %v", err)
	}

	global.APP_LOG.Info("开始实例预处理",
		zap.Uint("taskId", task.ID),
		zap.String("sessionId", taskReq.SessionId))

	// 初始化服务
	dbService := database.GetDatabaseService()

	directCreate := taskReq.AdminDirect
	cpuCores := 0
	memoryMB := int64(0)
	diskMB := int64(0)
	bandwidthSpeedMbps := 0
	if directCreate {
		if taskReq.ProviderId == 0 {
			return nil, fmt.Errorf("缺少Provider ID")
		}
		if taskReq.Image == "" {
			return nil, fmt.Errorf("镜像不能为空")
		}
		if taskReq.InstanceType != "container" && taskReq.InstanceType != "vm" {
			return nil, fmt.Errorf("实例类型必须为container或vm")
		}
		if taskReq.CPU <= 0 {
			return nil, fmt.Errorf("CPU必须大于0")
		}
		if taskReq.Memory <= 0 {
			return nil, fmt.Errorf("内存必须大于0")
		}
		if taskReq.Disk <= 0 {
			return nil, fmt.Errorf("磁盘必须大于0")
		}
		cpuCores = taskReq.CPU
		memoryMB = taskReq.Memory
		diskMB = taskReq.Disk * 1024
		bandwidthSpeedMbps = taskReq.Bandwidth
	} else {
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
		cpuCores = cpuSpec.Cores
		memoryMB = int64(memorySpec.SizeMB)
		diskMB = int64(diskSpec.SizeMB)
		bandwidthSpeedMbps = bandwidthSpec.SpeedMbps
	}

	var instance providerModel.Instance

	// 在单个事务中完成所有数据库操作（不需要预留资源消费）
	err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// 重新验证镜像和服务器（防止状态变化）
		imageName := taskReq.Image
		instanceType := taskReq.InstanceType
		osType := ""
		if !directCreate {
			var systemImage systemModel.SystemImage
			if err := tx.Where("id = ? AND status = ?", taskReq.ImageId, "active").First(&systemImage).Error; err != nil {
				return fmt.Errorf("镜像不存在或已禁用")
			}
			imageName = systemImage.Name
			instanceType = systemImage.InstanceType
			osType = systemImage.OSType
		}

		var provider providerModel.Provider
		if err := tx.Where("id = ?", taskReq.ProviderId).First(&provider).Error; err != nil {
			return fmt.Errorf("服务器不存在或不可用")
		}
		providerAvailable := (provider.ConnectionType == "agent" && provider.AgentStatus == "online") ||
			(provider.ConnectionType != "agent" && (provider.Status == "active" || provider.Status == "partial"))
		if !providerAvailable {
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
		if directCreate && taskReq.Name != "" {
			instanceName = utils.SanitizeShellArg(taskReq.Name)
		}

		// 设置实例到期时间：节点到期优先；节点不过期且启用签到时，使用签到默认到期天数。
		expiredAt := determineInitialInstanceExpiryInTx(tx, &provider)
		gpuEnabled := provider.GpuEnabled && taskReq.GpuEnabled && utils.SupportsContainerGPUProvider(provider.Type, instanceType)
		gpuDeviceIDs := ""
		if gpuEnabled {
			gpuDeviceIDs = taskReq.GpuDeviceIds
		}
		networkType := provider.NetworkType
		if directCreate && taskReq.NetworkType != "" {
			networkType = taskReq.NetworkType
		}

		// 创建实例记录
		instance = providerModel.Instance{
			Name:               instanceName,
			Provider:           provider.Name,
			ProviderID:         provider.ID,
			Image:              imageName,
			CPU:                cpuCores,
			Memory:             memoryMB,
			Disk:               diskMB,
			Bandwidth:          bandwidthSpeedMbps,
			InstanceType:       instanceType,
			UserID:             task.UserID,
			Status:             "creating",
			Username:           "root",
			Password:           s.generatePassword(),
			OSType:             osType,
			NetworkType:        networkType, // 记录Provider的网络类型，用于reset时恢复IPv6配置
			ExpiresAt:          expiredAt,
			IsManualExpiry:     false,        // 默认非手动设置，跟随节点
			MaxTraffic:         0,            // 默认为0，表示继承用户等级限制，不单独限制实例
			TrafficLimited:     false,        // 显式设置为false，确保不会因流量误判为超限
			TrafficLimitReason: "",           // 初始无限制原因
			GpuEnabled:         gpuEnabled,   // GPU直通配置
			GpuDeviceIds:       gpuDeviceIDs, // GPU设备ID列表
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
		task.InstanceID = &instance.ID

		// 分配Provider资源（使用悲观锁）
		resourceService := &resources.ResourceService{}
		if err := resourceService.AllocateResourcesInTx(tx, provider.ID, instanceType,
			cpuCores, memoryMB, diskMB); err != nil {
			return fmt.Errorf("分配Provider资源失败: %v", err)
		}

		// 消费预留资源（实例已创建成功）
		if taskReq.SessionId != "" {
			reservationService := resources.GetResourceReservationService()
			if err := reservationService.ConsumeReservationBySessionInTx(tx, taskReq.SessionId); err != nil {
				global.APP_LOG.Error("消费预留资源失败，回滚事务",
					zap.String("sessionId", taskReq.SessionId),
					zap.Error(err))
				// 消费失败必须返回错误，触发事务回滚，避免资源重复计算
				return fmt.Errorf("消费预留资源失败: %v", err)
			}
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

	global.APP_LOG.Debug("实例预处理完成",
		zap.Uint("taskId", task.ID),
		zap.String("sessionId", taskReq.SessionId),
		zap.Uint("instanceId", instance.ID))

	// 更新进度到25% (数据库预处理完成)
	s.updateTaskProgress(task.ID, 25, "step.dbPreprocessing")

	return &instance, nil
}
