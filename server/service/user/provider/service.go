package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/provider"
	"oneclickvirt/provider/incus"
	"oneclickvirt/provider/lxd"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/traffic"
	"oneclickvirt/service/vnstat"

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

	// 【核心校验】验证用户等级限制和资源规格权限
	// 包含：全局等级限制 + Provider节点等级限制（取最小值）
	// 验证：CPU、内存、磁盘、带宽规格是否超过限制
	// 注意：实例数量限制在事务内验证（防止并发问题）
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

	// 使用优化的原子化创建流程（最小化事务范围）
	return s.createInstanceWithMinimalTransaction(userID, &req, sessionID, &systemImage, cpuSpec, memorySpec, diskSpec, bandwidthSpec)
}

// createInstanceWithMinimalTransaction 优化的原子化实例创建流程
// 只在真正需要原子性的操作中持有事务和行锁，最小化锁持有时间
// 注意：资源规格限制（CPU、内存、磁盘、带宽）已在事务外的 validateUserSpecPermissions 中验证
// 这里只需验证并发敏感的实例数量限制
func (s *Service) createInstanceWithMinimalTransaction(userID uint, req *userModel.CreateInstanceRequest, sessionID string, systemImage *systemModel.SystemImage, cpuSpec *constant.CPUSpec, memorySpec *constant.MemorySpec, diskSpec *constant.DiskSpec, bandwidthSpec *constant.BandwidthSpec) (*adminModel.Task, error) {
	// 使用事务确保原子性，但只在关键操作中持有锁
	var task *adminModel.Task
	err := database.GetDatabaseService().ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 【关键】在事务中验证实例数量限制（防止并发超配）
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
			if systemImage.InstanceType == "container" && provider.MaxContainerInstances > 0 {
				if provider.ContainerCount >= provider.MaxContainerInstances {
					return fmt.Errorf("节点容器数量已达上限：%d/%d", provider.ContainerCount, provider.MaxContainerInstances)
				}
			} else if systemImage.InstanceType == "vm" && provider.MaxVMInstances > 0 {
				if provider.VMCount >= provider.MaxVMInstances {
					return fmt.Errorf("节点虚拟机数量已达上限：%d/%d", provider.VMCount, provider.MaxVMInstances)
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
// GetInstanceTypePermissions 获取实例类型权限
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
			Name:               instanceName,
			Provider:           provider.Name,
			ProviderID:         provider.ID,
			Image:              systemImage.Name,
			CPU:                cpuSpec.Cores,
			Memory:             int64(memorySpec.SizeMB),
			Disk:               int64(diskSpec.SizeMB),
			Bandwidth:          bandwidthSpec.SpeedMbps,
			InstanceType:       systemImage.InstanceType,
			UserID:             task.UserID,
			Status:             "creating",
			OSType:             systemImage.OSType,
			ExpiredAt:          expiredAt,
			MaxTraffic:         0,     // 默认为0，表示继承用户等级限制，不单独限制实例
			UsedTraffic:        0,     // 初始已用流量为0
			TrafficLimited:     false, // 显式设置为false，确保不会因流量误判为超限
			TrafficLimitReason: "",    // 初始无限制原因
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

		// 分配Provider资源（使用悲观锁）
		resourceService := &resources.ResourceService{}
		if err := resourceService.AllocateResourcesInTx(tx, provider.ID, systemImage.InstanceType,
			cpuSpec.Cores, int64(memorySpec.SizeMB), int64(diskSpec.SizeMB)); err != nil {
			return fmt.Errorf("分配Provider资源失败: %v", err)
		}

		// 消费预留资源（实例已创建成功）
		reservationService := resources.GetResourceReservationService()
		if err := reservationService.ConsumeReservationBySessionInTx(tx, taskReq.SessionId); err != nil {
			global.APP_LOG.Warn("消费预留资源失败（可能已过期）",
				zap.String("sessionId", taskReq.SessionId),
				zap.Error(err))
			// 注意：这里不返回错误，因为实例已经创建成功，预留资源可能已过期
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
	// 允许 active 和 partial 状态的Provider执行任务（与GetAvailableProviders保持一致）
	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("name = ? AND (status = ? OR status = ?)", instance.Provider, "active", "partial").First(&dbProvider).Error; err != nil {
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
		ImageURL:     systemImage.URL, // 镜像URL用于下载
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
					// 如果协议是 both，需要创建两个端口映射（tcp 和 udp）
					if port.Protocol == "both" {
						tcpMapping := fmt.Sprintf("0.0.0.0:%d:%d/tcp", port.HostPort, port.GuestPort)
						udpMapping := fmt.Sprintf("0.0.0.0:%d:%d/udp", port.HostPort, port.GuestPort)
						ports = append(ports, tcpMapping, udpMapping)
					} else {
						portMapping := fmt.Sprintf("0.0.0.0:%d:%d/%s", port.HostPort, port.GuestPort, port.Protocol)
						ports = append(ports, portMapping)
					}
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

							// 对于内网节点（NAT模式），公网IPv4使用Provider的Endpoint（已在前面设置）
							// 对于独立IP模式（dedicated），实例获取到的内网IP就是公网IP
							if dbProvider.NetworkType == "dedicated_ipv4" || dbProvider.NetworkType == "dedicated_ipv4_ipv6" {
								// 独立IP模式：内网IP就是公网IP
								instanceUpdates["public_ip"] = ipv4Address
								global.APP_LOG.Info("Proxmox独立IP模式，使用实例IP作为公网IP",
									zap.String("instanceName", instance.Name),
									zap.String("networkType", dbProvider.NetworkType),
									zap.String("publicIP", ipv4Address))
							}
							// NAT模式下，public_ip已经在前面从Provider的Endpoint设置，这里不需要覆盖
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
							} else if dbProvider.NetworkType == "dedicated_ipv4_ipv6" || dbProvider.NetworkType == "ipv6_only" {
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

									// 对于独立IP模式，内网IP就是公网IP
									if dbProvider.NetworkType == "dedicated_ipv4" || dbProvider.NetworkType == "dedicated_ipv4_ipv6" {
										instanceUpdates["public_ip"] = proxmoxInstance.IP
										global.APP_LOG.Info("Proxmox独立IP模式，使用实例IP作为公网IP",
											zap.String("instanceName", instance.Name),
											zap.String("networkType", dbProvider.NetworkType),
											zap.String("publicIP", proxmoxInstance.IP))
									}
								} else if proxmoxInstance.PrivateIP != "" {
									instanceUpdates["private_ip"] = proxmoxInstance.PrivateIP
									global.APP_LOG.Info("获取到Proxmox实例内网IPv4地址",
										zap.String("instanceName", instance.Name),
										zap.String("privateIP", proxmoxInstance.PrivateIP))

									// 对于独立IP模式，内网IP就是公网IP
									if dbProvider.NetworkType == "dedicated_ipv4" || dbProvider.NetworkType == "dedicated_ipv4_ipv6" {
										instanceUpdates["public_ip"] = proxmoxInstance.PrivateIP
										global.APP_LOG.Info("Proxmox独立IP模式，使用实例IP作为公网IP",
											zap.String("instanceName", instance.Name),
											zap.String("networkType", dbProvider.NetworkType),
											zap.String("publicIP", proxmoxInstance.PrivateIP))
									}
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
									} else if dbProvider.NetworkType == "dedicated_ipv4_ipv6" || dbProvider.NetworkType == "ipv6_only" {
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
