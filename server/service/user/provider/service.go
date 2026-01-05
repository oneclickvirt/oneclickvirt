package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	monitoringModel "oneclickvirt/model/monitoring"
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

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
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

// GetProviderCapabilities 获取Provider能力
// GetInstanceTypePermissions 获取实例类型权限
// ProcessCreateInstanceTask 处理创建实例的后台任务 - 三阶段处理
func (s *Service) ProcessCreateInstanceTask(ctx context.Context, task *adminModel.Task) error {
	global.APP_LOG.Info("开始处理创建实例任务", zap.Uint("taskId", task.ID))

	// 初始化进度 (5%)
	s.updateTaskProgress(task.ID, 5, "正在准备实例创建...")

	// 阶段1: 数据库预处理（快速事务） (5% -> 25%)
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

	// 更新进度到30% (开始调用Provider API)
	s.updateTaskProgress(task.ID, 30, "正在调用Provider API...")

	// 阶段2: Provider API调用（无事务）(30% -> 60%)
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

	// 在单个事务中完成所有数据库操作（不需要预留资源消费）
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

		// 设置实例到期时间
		// 默认与Provider的到期时间同步，但如果Provider没有到期时间则使用1年后
		var expiredAt *time.Time
		if provider.ExpiresAt != nil {
			// 如果Provider有到期时间，使用Provider的到期时间
			expiredAt = provider.ExpiresAt
		}
		// 如果Provider没有到期时间，实例也不设置到期时间（由管理员手动管理）

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
			ExpiresAt:          expiredAt,
			IsManualExpiry:     false, // 默认非手动设置，跟随节点
			MaxTraffic:         0,     // 默认为0，表示继承用户等级限制，不单独限制实例
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
			global.APP_LOG.Error("消费预留资源失败，回滚事务",
				zap.String("sessionId", taskReq.SessionId),
				zap.Error(err))
			// 消费失败必须返回错误，触发事务回滚，避免资源重复计算
			return fmt.Errorf("消费预留资源失败: %v", err)
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

	global.APP_LOG.Info("实例预处理完成",
		zap.Uint("taskId", task.ID),
		zap.String("sessionId", taskReq.SessionId),
		zap.Uint("instanceId", instance.ID))

	// 更新进度到25% (数据库预处理完成)
	s.updateTaskProgress(task.ID, 25, "数据库预处理完成")

	return &instance, nil
}

// executeProviderCreation 阶段2: Provider API调用 (30% -> 60%)
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

	// 直接从数据库获取Provider配置（使用ProviderID而不是Name）
	// 允许 active 和 partial 状态的Provider执行任务（与GetAvailableProviders保持一致）
	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("id = ? AND (status = ? OR status = ?)", instance.ProviderID, "active", "partial").First(&dbProvider).Error; err != nil {
		err := fmt.Errorf("Provider ID %d 不存在或不可用", instance.ProviderID)
		global.APP_LOG.Error("Provider不存在", zap.Uint("taskId", task.ID), zap.Uint("providerId", instance.ProviderID), zap.Error(err))
		return err
	}

	// 复制副本避免共享状态，立即创建Provider字段的本地副本
	localProviderID := dbProvider.ID
	localProviderName := dbProvider.Name
	localProviderType := dbProvider.Type
	localProviderIsFrozen := dbProvider.IsFrozen
	localProviderExpiresAt := dbProvider.ExpiresAt
	localProviderIPv4PortMappingMethod := dbProvider.IPv4PortMappingMethod
	localProviderIPv6PortMappingMethod := dbProvider.IPv6PortMappingMethod
	localProviderNetworkType := dbProvider.NetworkType

	// 检查Provider是否过期或冻结
	if localProviderIsFrozen {
		err := fmt.Errorf("Provider ID %d 已被冻结", localProviderID)
		global.APP_LOG.Error("Provider已冻结", zap.Uint("taskId", task.ID), zap.Uint("providerId", localProviderID))
		return err
	}

	if localProviderExpiresAt != nil && localProviderExpiresAt.Before(time.Now()) {
		err := fmt.Errorf("Provider ID %d 已过期", localProviderID)
		global.APP_LOG.Error("Provider已过期", zap.Uint("taskId", task.ID), zap.Uint("providerId", localProviderID), zap.Time("expiresAt", *localProviderExpiresAt))
		return err
	}

	// 实现实际的Provider API调用逻辑
	// 首先尝试从ProviderService获取已连接的Provider实例（使用ID）
	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProviderByID(instance.ProviderID)

	if !exists {
		// 如果Provider未连接，尝试动态加载
		global.APP_LOG.Info("Provider未连接，尝试动态加载", zap.Uint("providerId", localProviderID), zap.String("name", localProviderName))
		if err := providerSvc.LoadProvider(dbProvider); err != nil {
			global.APP_LOG.Error("动态加载Provider失败", zap.Uint("providerId", localProviderID), zap.String("name", localProviderName), zap.Error(err))
			err := fmt.Errorf("Provider ID %d 连接失败: %v", localProviderID, err)
			return err
		}

		// 重新获取Provider实例
		providerInstance, exists = providerSvc.GetProviderByID(instance.ProviderID)
		if !exists {
			err := fmt.Errorf("Provider ID %d 连接后仍然不可用", localProviderID)
			global.APP_LOG.Error("Provider连接后仍然不可用", zap.Uint("taskId", task.ID), zap.Uint("providerId", localProviderID))
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
			"ipv4_port_mapping_method": localProviderIPv4PortMappingMethod,         // IPv4端口映射方式（从Provider配置获取）
			"ipv6_port_mapping_method": localProviderIPv6PortMappingMethod,         // IPv6端口映射方式（从Provider配置获取）
			"network_type":             localProviderNetworkType,                   // 网络配置类型：nat_ipv4, nat_ipv4_ipv6, dedicated_ipv4, dedicated_ipv4_ipv6, ipv6_only
			"instance_id":              fmt.Sprintf("%d", instance.ID),             // 实例ID，用于端口分配
			"provider_id":              fmt.Sprintf("%d", localProviderID),         // Provider ID，用于端口区间分配
		},
		// 容器特殊配置选项（从Provider继承，仅用于LXD/Incus容器）
		Privileged:   boolPtr(dbProvider.ContainerPrivileged),
		AllowNesting: boolPtr(dbProvider.ContainerAllowNesting),
		EnableLXCFS:  boolPtr(dbProvider.ContainerEnableLXCFS),
		CPUAllowance: stringPtr(dbProvider.ContainerCPUAllowance),
		MemorySwap:   boolPtr(dbProvider.ContainerMemorySwap),
		MaxProcesses: intPtr(dbProvider.ContainerMaxProcesses),
		DiskIOLimit:  stringPtr(dbProvider.ContainerDiskIOLimit),
	}

	// 预分配端口映射（所有Provider类型都需要）
	portMappingService := &resources.PortMappingService{}

	// 预先创建端口映射记录，用于统一的端口管理
	if err := portMappingService.CreateDefaultPortMappings(instance.ID, localProviderID); err != nil {
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
			if localProviderType == "docker" {
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
					zap.String("providerType", localProviderType),
					zap.Int("portCount", len(portMappings)))
			}
		}
	}

	// 调用Provider API创建实例
	// 创建进度回调函数，与任务系统集成
	progressCallback := func(percentage int, message string) {
		// 将Provider内部进度（0-100）映射到任务进度（30-70）
		// Provider进度占用40%的总进度空间
		adjustedPercentage := 30 + (percentage * 40 / 100)
		s.updateTaskProgress(task.ID, adjustedPercentage, message)
	}

	global.APP_LOG.Info("准备调用Provider创建实例方法",
		zap.Uint("taskId", task.ID),
		zap.String("instanceName", instance.Name),
		zap.String("providerName", localProviderName),
		zap.String("providerType", localProviderType))

	// 使用带进度的创建方法
	global.APP_LOG.Info("开始调用CreateInstanceWithProgress",
		zap.Uint("taskId", task.ID),
		zap.String("instanceName", instance.Name))

	if err := providerInstance.CreateInstanceWithProgress(ctx, instanceConfig, progressCallback); err != nil {
		err := fmt.Errorf("Provider API创建实例失败: %v", err)
		global.APP_LOG.Error("Provider API创建实例失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return err
	}

	global.APP_LOG.Info("Provider API调用成功", zap.Uint("taskId", task.ID), zap.String("instanceName", instance.Name))

	// 更新进度到70%
	s.updateTaskProgress(task.ID, 70, "Provider API调用成功")

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

			// 资源预留已在创建时被原子化消费，无需额外释放

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
			// 优先使用PortIP（端口映射专用IP），这是用户明确指定的公网IP
			// 如果PortIP为空，则使用Endpoint（SSH连接地址）
			publicIPSource := dbProvider.PortIP
			if publicIPSource == "" {
				publicIPSource = dbProvider.Endpoint
			}

			// 从IP源中提取纯IP地址
			if publicIPSource != "" {
				// 移除端口号获取纯IP地址
				if colonIndex := strings.LastIndex(publicIPSource, ":"); colonIndex > 0 {
					if strings.Count(publicIPSource, ":") > 1 && !strings.HasPrefix(publicIPSource, "[") {
						instanceUpdates["public_ip"] = publicIPSource // IPv6格式
					} else {
						instanceUpdates["public_ip"] = publicIPSource[:colonIndex] // IPv4格式，移除端口
					}
				} else {
					instanceUpdates["public_ip"] = publicIPSource
				}

				global.APP_LOG.Info("设置实例公网IP",
					zap.String("instanceName", instance.Name),
					zap.String("portIP", dbProvider.PortIP),
					zap.String("endpoint", dbProvider.Endpoint),
					zap.String("publicIPSource", publicIPSource),
					zap.Any("publicIP", instanceUpdates["public_ip"]))
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
			// 标准化实例状态：将Provider返回的各种运行状态统一为"running"
			if actualInstance.Status != "" {
				// 将Provider返回的状态转换为小写进行比较
				providerStatus := strings.ToLower(actualInstance.Status)
				// 如果Provider返回的是运行状态（running/active），统一设置为running
				// 其他状态（如stopped）保持原样
				if providerStatus == "running" || providerStatus == "active" {
					instanceUpdates["status"] = "running"
				} else if providerStatus == "stopped" {
					instanceUpdates["status"] = "stopped"
				} else {
					// 对于其他未知状态，记录日志但保持默认的running状态
					global.APP_LOG.Warn("Provider返回了非标准状态",
						zap.String("instanceName", instance.Name),
						zap.String("providerStatus", actualInstance.Status))
					// 保持默认的running状态
				}
			}
		} else {
			// 使用默认值
			instanceUpdates["ssh_port"] = 22
		}

		// 尝试获取IPv4和IPv6地址（针对LXD、Incus和Proxmox Provider）
		if actualInstance != nil {
			providerSvc := providerService.GetProviderService()
			if providerInstance, exists := providerSvc.GetProviderByID(instance.ProviderID); exists {
				if dbProvider.Type == "lxd" {
					if lxdProvider, ok := providerInstance.(*lxd.LXDProvider); ok {
						// 获取内网IPv4地址
						ctx := context.Background()
						if ipv4Address, err := lxdProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ipv4Address != "" {
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
		// 确认待确认配额（将pending_quota转为used_quota）
		quotaService := resources.NewQuotaService()
		resourceUsage := resources.ResourceUsage{
			CPU:       instance.CPU,
			Memory:    instance.Memory,
			Disk:      instance.Disk,
			Bandwidth: instance.Bandwidth,
		}
		// 实例创建成功，将待确认配额转为已使用配额
		if err := quotaService.ConfirmPendingQuota(tx, task.UserID, resourceUsage); err != nil {
			global.APP_LOG.Error("确认用户配额失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("userId", task.UserID),
				zap.Error(err))
			return fmt.Errorf("确认用户配额失败: %v", err)
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

			// 更新进度到75% (等待实例SSH服务就绪)
			s.updateTaskProgress(taskID, 75, "等待实例SSH服务就绪...")

			// 智能等待实例SSH服务就绪，传入taskID以便更新进度
			if err := s.waitForInstanceSSHReady(instanceID, providerID, taskID, 120*time.Second); err != nil {
				global.APP_LOG.Warn("等待实例SSH就绪超时",
					zap.Uint("instanceId", instanceID),
					zap.Error(err))
				// 继续执行，但后续SSH相关操作可能失败
			}

			// 更新进度到80% (配置端口映射)
			s.updateTaskProgress(taskID, 80, "正在配置端口映射...")

			// 创建默认端口映射（对于非Docker或需要补充端口映射的情况）
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

			// 更新进度到85% (验证监控状态)
			s.updateTaskProgress(taskID, 85, "正在验证监控状态...")

			// 2. 验证pmacct监控状态（所有 Provider 在创建实例时已经初始化）
			// Docker/Incus/LXD/Proxmox Provider 在实例创建流程中都已调用 InitializePmacctForInstance
			// 后处理任务只需验证监控是否存在，避免重复初始化导致数据库约束冲突
			pmacctInitSuccess := false
			trafficEnabled := false

			// 先检查Provider是否启用了流量统计
			var dbProvider providerModel.Provider
			if err := global.APP_DB.Where("id = ?", providerID).First(&dbProvider).Error; err == nil {
				trafficEnabled = dbProvider.EnableTrafficControl
			}

			// 检查pmacct监控是否已存在
			var existingMonitor monitoringModel.PmacctMonitor
			if err := global.APP_DB.Where("instance_id = ?", instanceID).First(&existingMonitor).Error; err == nil {
				global.APP_LOG.Info("pmacct监控已在实例创建时初始化",
					zap.Uint("instanceId", instanceID),
					zap.Uint("monitorId", existingMonitor.ID))
				pmacctInitSuccess = true
			} else {
				if trafficEnabled {
					global.APP_LOG.Warn("pmacct监控未找到（可能在实例创建时失败）",
						zap.Uint("instanceId", instanceID),
						zap.Error(err))
				} else {
					global.APP_LOG.Debug("Provider未启用流量统计，无pmacct监控记录",
						zap.Uint("instanceId", instanceID),
						zap.Uint("providerId", providerID))
				}
			}

			// 更新进度到90% (设置SSH密码)
			s.updateTaskProgress(taskID, 90, "正在设置SSH密码...")
			// 3. 设置实例SSH密码（关键步骤）
			var currentInstance providerModel.Instance
			var passwordSetSuccess bool = false
			if err := global.APP_DB.Where("id = ?", instanceID).First(&currentInstance).Error; err != nil {
				global.APP_LOG.Error("获取实例信息失败，无法设置SSH密码",
					zap.Uint("instanceId", instanceID),
					zap.Error(err))
			} else if currentInstance.Password != "" {
				// 设置实例SSH密码，最多重试2次（总共2次尝试）
				providerSvc := providerService.GetProviderService()
				maxRetries := 2
				for i := 0; i < maxRetries; i++ {
					// 创建带2分钟超时的context
					ctxWithTimeout, cancel := context.WithTimeout(context.Background(), 200*time.Second)
					err := providerSvc.SetInstancePassword(ctxWithTimeout, currentInstance.ProviderID, currentInstance.Name, currentInstance.Password)
					cancel() // 立即释放context资源
					if err != nil {
						global.APP_LOG.Warn("设置实例SSH密码失败",
							zap.Uint("instanceId", instanceID),
							zap.String("instanceName", currentInstance.Name),
							zap.Int("attempt", i+1),
							zap.Int("maxRetries", maxRetries),
							zap.Error(err))
						if i < maxRetries-1 {
							global.APP_LOG.Info("等待10秒后重试设置SSH密码",
								zap.Uint("instanceId", instanceID))
							time.Sleep(10 * time.Second) // 重试间隔10秒
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

			// 更新进度到95% (配置网络监控)
			s.updateTaskProgress(taskID, 95, "正在配置网络监控...")

			// 4. pmacct监控已在初始化时完成配置，无需额外步骤
			if !pmacctInitSuccess {
				if trafficEnabled {
					global.APP_LOG.Info("跳过流量监控（pmacct初始化失败）",
						zap.Uint("instanceId", instanceID))
				} else {
					global.APP_LOG.Info("跳过流量监控（Provider未启用流量统计）",
						zap.Uint("instanceId", instanceID),
						zap.Uint("providerId", providerID))
				}
			}

			// 更新进度到98%
			s.updateTaskProgress(taskID, 98, "正在启动流量同步...")

			// 5. 触发流量同步（仅在pmacct初始化成功时执行）
			if pmacctInitSuccess {
				syncTrigger := traffic.NewSyncTriggerService()
				syncTrigger.TriggerInstanceTrafficSync(instanceID, "实例创建后初始同步")

				global.APP_LOG.Info("实例流量同步已触发",
					zap.Uint("instanceId", instanceID))
			} else {
				if trafficEnabled {
					global.APP_LOG.Info("跳过流量同步触发（pmacct初始化失败）",
						zap.Uint("instanceId", instanceID))
				} else {
					global.APP_LOG.Debug("跳过流量同步触发（Provider未启用流量统计）",
						zap.Uint("instanceId", instanceID),
						zap.Uint("providerId", providerID))
				}
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

// waitForInstanceSSHReady 智能等待实例SSH服务就绪
// 通过轮询检查SSH端口是否可连接，而不是盲目等待固定时间
func (s *Service) waitForInstanceSSHReady(instanceID, providerID, taskID uint, maxWaitTime time.Duration) error {
	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	// 获取Provider信息
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 获取SSH端口映射
	var sshPort int
	var sshPortMapping providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'", instanceID).First(&sshPortMapping).Error; err == nil {
		sshPort = sshPortMapping.HostPort
	} else {
		sshPort = instance.SSHPort
		if sshPort == 0 {
			sshPort = 22 // 默认端口
		}
	}

	// 确定SSH连接地址
	var sshHost string
	if provider.PortIP != "" {
		sshHost = provider.PortIP
	} else {
		sshHost = provider.Endpoint
	}

	// 如果sshHost包含端口，去掉端口部分
	if colonIndex := strings.LastIndex(sshHost, ":"); colonIndex > 0 {
		if strings.Count(sshHost, ":") == 1 || strings.HasPrefix(sshHost, "[") {
			sshHost = sshHost[:colonIndex]
		}
	}

	global.APP_LOG.Info("开始等待实例SSH服务就绪",
		zap.Uint("instanceId", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("sshHost", sshHost),
		zap.Int("sshPort", sshPort),
		zap.Duration("maxWaitTime", maxWaitTime))

	checkInterval := 5 * time.Second
	startTime := time.Now()
	attemptCount := 0

	// 进度范围：62% - 68%，根据等待时间百分比更新
	progressStart := 62
	progressEnd := 68

	for {
		attemptCount++
		elapsed := time.Since(startTime)

		// 检查是否超时
		if elapsed >= maxWaitTime {
			return fmt.Errorf("等待SSH服务超时 (%v), 尝试次数: %d", maxWaitTime, attemptCount)
		}

		// 计算当前进度（62-68%范围内）
		progressPercent := float64(elapsed) / float64(maxWaitTime)
		currentProgress := progressStart + int(float64(progressEnd-progressStart)*progressPercent)
		if currentProgress > progressEnd {
			currentProgress = progressEnd
		}

		// 更新进度和消息
		waitMsg := fmt.Sprintf("等待实例SSH服务就绪... (尝试 %d次, 已等待 %ds)", attemptCount, int(elapsed.Seconds()))
		s.updateTaskProgress(taskID, currentProgress, waitMsg)

		// 尝试连接SSH
		address := fmt.Sprintf("%s:%d", sshHost, sshPort)
		config := &ssh.ClientConfig{
			User: instance.Username,
			Auth: []ssh.AuthMethod{
				ssh.Password(instance.Password),
			},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         5 * time.Second,
		}

		client, err := ssh.Dial("tcp", address, config)
		if err == nil {
			// SSH连接成功
			client.Close()
			global.APP_LOG.Info("实例SSH服务已就绪",
				zap.Uint("instanceId", instanceID),
				zap.String("instanceName", instance.Name),
				zap.Duration("waitTime", elapsed),
				zap.Int("attempts", attemptCount))

			// 确保进度达到68%
			s.updateTaskProgress(taskID, progressEnd, "实例SSH服务已就绪")
			return nil
		}

		// 连接失败，记录日志并等待重试
		global.APP_LOG.Debug("等待实例SSH就绪",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instance.Name),
			zap.Int("attempt", attemptCount),
			zap.Duration("elapsed", elapsed),
			zap.String("error", err.Error()))

		// 等待后重试
		time.Sleep(checkInterval)
	}
}

// 辅助函数：创建 bool 指针
func boolPtr(b bool) *bool {
	return &b
}

// 辅助函数：创建 string 指针
func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// 辅助函数：创建 int 指针
func intPtr(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}
