package provider

import (
	"context"
	"errors"
	"fmt"
	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/cache"
	"oneclickvirt/service/database"
	"oneclickvirt/service/resources"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// GetAvailableProviders 获取可用节点列表
// GetSystemImages 获取系统镜像列表
// GetInstanceConfig 获取实例配置选项 - 根据用户配额和节点限制动态过滤
// GetFilteredSystemImages 根据Provider和实例类型获取过滤后的系统镜像列表
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

	// 检查Provider是否因流量超限被限制
	if provider.TrafficLimited {
		global.APP_LOG.Error("Provider因流量超限被限制，禁止申请新实例",
			zap.Uint("providerId", req.ProviderId),
			zap.String("providerName", provider.Name),
			zap.Bool("trafficLimited", provider.TrafficLimited))
		return nil, errors.New("该服务器因流量超限暂时不可用，请选择其他服务器或联系管理员")
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

	// 验证用户等级限制和资源规格权限
	// 包含：全局等级限制 + Provider节点等级限制（取最小值）
	// 验证：CPU、内存、磁盘、带宽规格是否超过限制
	// 实例数量限制在事务内验证（防止并发问题）
	if err := s.validateUserSpecPermissions(userID, req.ProviderId, cpuSpec, memorySpec, diskSpec, bandwidthSpec); err != nil {
		global.APP_LOG.Error("用户等级限制验证失败",
			zap.Uint("userID", userID),
			zap.Uint("providerId", req.ProviderId),
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

	global.APP_LOG.Info("所有验证通过，开始创建实例",
		zap.Uint("userID", userID),
		zap.Uint("providerId", req.ProviderId),
		zap.Uint("imageId", req.ImageId))

	// 生成会话ID
	sessionID := resources.GenerateSessionID()

	// 使用原子化创建流程（最小化事务范围）
	return s.createInstanceWithMinimalTransaction(userID, &req, sessionID, &systemImage, cpuSpec, memorySpec, diskSpec, bandwidthSpec)
}

// createInstanceWithMinimalTransaction 原子化实例创建流程
// 只在真正需要原子性的操作中持有事务和行锁，最小化锁持有时间
// 资源规格限制（CPU、内存、磁盘、带宽）已在事务外的 validateUserSpecPermissions 中验证
// 这里只需验证并发敏感的实例数量限制
func (s *Service) createInstanceWithMinimalTransaction(userID uint, req *userModel.CreateInstanceRequest, sessionID string, systemImage *systemModel.SystemImage, cpuSpec *constant.CPUSpec, memorySpec *constant.MemorySpec, diskSpec *constant.DiskSpec, bandwidthSpec *constant.BandwidthSpec) (*adminModel.Task, error) {
	// 使用事务确保原子性，但只在关键操作中持有锁
	var task *adminModel.Task
	err := database.GetDatabaseService().ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 在事务中验证实例数量限制（防止并发超配）
		// 使用行锁保护，确保原子性
		quotaService := resources.NewQuotaService()

		// 1. 获取用户记录并加锁（FOR UPDATE）
		var currentUser userModel.User
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&currentUser, userID).Error; err != nil {
			return fmt.Errorf("获取用户信息失败: %v", err)
		}

		// 快速检查用户状态
		if currentUser.Status != 1 {
			return fmt.Errorf("用户账户已被禁用")
		}

		// 2. 验证用户全局实例数量限制
		levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[currentUser.Level]
		if !exists {
			return fmt.Errorf("用户等级 %d 没有配置资源限制", currentUser.Level)
		}

		currentInstances, _, err := quotaService.GetCurrentResourceUsageInTx(tx, userID)
		if err != nil {
			return fmt.Errorf("获取当前实例数量失败: %v", err)
		}

		if currentInstances >= levelLimits.MaxInstances {
			return fmt.Errorf("实例数量已达上限：当前 %d/%d", currentInstances, levelLimits.MaxInstances)
		}

		// 3. 验证Provider节点级别的实例数量限制
		if req.ProviderId > 0 {
			// 获取Provider并加锁（防止并发超配）
			var provider providerModel.Provider
			if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&provider, req.ProviderId).Error; err != nil {
				return fmt.Errorf("获取节点信息失败: %v", err)
			}

			// 3.1 检查节点容器/虚拟机总数限制
			// 使用缓存的计数值（如果缓存有效），否则进行实时查询
			containerCount := provider.ContainerCount
			vmCount := provider.VMCount

			// 检查缓存是否过期
			if provider.CountCacheExpiry == nil || time.Now().After(*provider.CountCacheExpiry) {
				// 缓存过期，需要重新查询（排除deleted、deleting、failed状态）
				var freshContainerCount, freshVMCount int64
				tx.Model(&providerModel.Instance{}).
					Where("provider_id = ? AND instance_type = ? AND status NOT IN (?)",
						provider.ID, "container", []string{"deleted", "deleting", "failed"}).
					Count(&freshContainerCount)
				tx.Model(&providerModel.Instance{}).
					Where("provider_id = ? AND instance_type = ? AND status NOT IN (?)",
						provider.ID, "vm", []string{"deleted", "deleting", "failed"}).
					Count(&freshVMCount)

				containerCount = int(freshContainerCount)
				vmCount = int(freshVMCount)

				global.APP_LOG.Debug("使用实时查询的实例数量（缓存已过期）",
					zap.Uint("providerID", provider.ID),
					zap.Int("containerCount", containerCount),
					zap.Int("vmCount", vmCount))
			} else {
				global.APP_LOG.Debug("使用缓存的实例数量",
					zap.Uint("providerID", provider.ID),
					zap.Int("containerCount", containerCount),
					zap.Int("vmCount", vmCount))
			}

			if systemImage.InstanceType == "container" && provider.MaxContainerInstances > 0 {
				if containerCount >= provider.MaxContainerInstances {
					return fmt.Errorf("节点容器数量已达上限：%d/%d", containerCount, provider.MaxContainerInstances)
				}
			} else if systemImage.InstanceType == "vm" && provider.MaxVMInstances > 0 {
				if vmCount >= provider.MaxVMInstances {
					return fmt.Errorf("节点虚拟机数量已达上限：%d/%d", vmCount, provider.MaxVMInstances)
				}
			}

			// 3.2 检查该用户在此节点的等级实例数量限制
			providerLevelLimits, err := quotaService.GetProviderLevelLimitsInTx(tx, req.ProviderId, currentUser.Level)
			if err == nil && providerLevelLimits != nil && providerLevelLimits.MaxInstances > 0 {
				currentProviderInstances, err := quotaService.GetCurrentProviderInstanceCountInTx(tx, userID, req.ProviderId)
				if err != nil {
					return fmt.Errorf("获取节点实例数量失败: %v", err)
				}

				if currentProviderInstances >= providerLevelLimits.MaxInstances {
					return fmt.Errorf("该节点实例数量已达上限：当前在此节点 %d/%d", currentProviderInstances, providerLevelLimits.MaxInstances)
				}
			}
		}

		global.APP_LOG.Info("事务内实例数量验证通过",
			zap.Uint("userID", userID),
			zap.Int("currentInstances", currentInstances),
			zap.Int("maxInstances", levelLimits.MaxInstances))

		// 1. 只预留资源，不立即消费（等待实例创建成功后再消费）
		reservationService := resources.GetResourceReservationService()

		if err := reservationService.ReserveResourcesInTx(tx, userID, req.ProviderId, sessionID,
			systemImage.InstanceType, cpuSpec.Cores, int64(memorySpec.SizeMB), int64(diskSpec.SizeMB), bandwidthSpec.SpeedMbps); err != nil {
			global.APP_LOG.Error("预留资源失败",
				zap.Uint("userID", userID),
				zap.Uint("providerId", req.ProviderId),
				zap.String("sessionId", sessionID),
				zap.Error(err))
			return fmt.Errorf("资源分配失败: %v", err)
		}

		// 2. 创建任务
		taskData := fmt.Sprintf(`{"providerId":%d,"imageId":%d,"cpuId":"%s","memoryId":"%s","diskId":"%s","bandwidthId":"%s","description":"%s","sessionId":"%s"}`,
			req.ProviderId, req.ImageId, req.CPUId, req.MemoryId, req.DiskId, req.BandwidthId, req.Description, sessionID)

		// 计算预计执行时长
		estimatedDuration := 300 // 默认5分钟
		if systemImage.InstanceType == "vm" {
			estimatedDuration = 600 // 虚拟机需要更长时间
		}

		// 在事务中创建任务，包含预分配配置信息
		newTask := &adminModel.Task{
			UserID:                userID,
			ProviderID:            &req.ProviderId,
			TaskType:              "create",
			TaskData:              taskData,
			Status:                "pending",
			TimeoutDuration:       1800,
			IsForceStoppable:      true,
			EstimatedDuration:     estimatedDuration,
			PreallocatedCPU:       cpuSpec.Cores,
			PreallocatedMemory:    memorySpec.SizeMB,
			PreallocatedDisk:      diskSpec.SizeMB,
			PreallocatedBandwidth: bandwidthSpec.SpeedMbps,
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

	// 使用户缓存失效（实例创建任务已创建）
	cacheService := cache.GetUserCacheService()
	cacheService.InvalidateUserCache(userID)

	global.APP_LOG.Info("原子化实例创建成功",
		zap.Uint("userID", userID),
		zap.Uint("taskId", task.ID),
		zap.String("sessionId", sessionID))

	return task, nil
}
