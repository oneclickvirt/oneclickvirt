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
