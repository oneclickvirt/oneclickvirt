package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
	"oneclickvirt/service/resources"
	traffic "oneclickvirt/service/traffic"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ProcessCreateRedemptionInstanceTask 处理兑换码实例创建任务（三阶段）
// 与 ProcessCreateInstanceTask 的区别：
//   - 兑换码流程只使用 Provider 资源预留，不占用具体用户配额
//   - 创建的实例 UserID = 0（归属系统，兑换后转移到用户）
//   - 任务创建者为管理员（task.UserID = adminID，非零）
//   - 阶段3额外更新 RedemptionCode 状态
func (s *Service) ProcessCreateRedemptionInstanceTask(ctx context.Context, task *adminModel.Task) error {
	global.APP_LOG.Info("开始处理兑换码实例创建任务", zap.Uint("taskId", task.ID))

	s.updateTaskProgress(task.ID, 5, "step.preparingRedemptionCreate")

	// 阶段1: 数据库预处理（5% -> 25%）
	instance, err := s.prepareRedemptionInstanceCreation(ctx, task)
	if err != nil {
		global.APP_LOG.Error("兑换码实例预处理失败", zap.Uint("taskId", task.ID), zap.Error(err))
		stateManager := s.taskService.GetStateManager()
		if stateManager != nil {
			_ = stateManager.CompleteMainTask(task.ID, false, fmt.Sprintf("预处理失败: %v", err), nil)
		}
		// 删除兑换码记录（预处理失败说明配置有问题）
		s.hardDeleteRedemptionCodeByTask(task)
		return err
	}

	s.updateTaskProgress(task.ID, 30, "step.callingProviderCreate")

	// 阶段2: Provider创建实例（30% -> 70%）—— 直接复用，根据ExecutionRule自动选择API或SSH
	apiError := s.executeProviderCreation(ctx, task, instance)

	// 阶段3: 结果处理
	global.APP_LOG.Debug("开始处理兑换码实例创建结果",
		zap.Uint("taskId", task.ID),
		zap.Bool("hasApiError", apiError != nil))

	if finalizeErr := s.finalizeRedemptionInstanceCreation(ctx, task, instance, apiError); finalizeErr != nil {
		// ErrAsyncCompletion 是正常的异步接管信号，不是错误
		if errors.Is(finalizeErr, interfaces.ErrAsyncCompletion) {
			global.APP_LOG.Info("兑换码实例创建已移交后台处理", zap.Uint("taskId", task.ID))
			return finalizeErr
		}
		global.APP_LOG.Error("兑换码实例创建最终化失败", zap.Uint("taskId", task.ID), zap.Error(finalizeErr))
		return finalizeErr
	}

	global.APP_LOG.Info("兑换码实例创建任务处理完成", zap.Uint("taskId", task.ID), zap.Uint("instanceId", instance.ID))
	return nil
}

// prepareRedemptionInstanceCreation 阶段1: 数据库预处理（无用户配额检查）
func (s *Service) prepareRedemptionInstanceCreation(ctx context.Context, task *adminModel.Task) (*providerModel.Instance, error) {
	var taskReq adminModel.CreateRedemptionInstanceTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return nil, fmt.Errorf("解析兑换码任务数据失败: %v", err)
	}

	global.APP_LOG.Debug("开始兑换码实例预处理",
		zap.Uint("taskId", task.ID),
		zap.Uint("redemptionCodeId", taskReq.RedemptionCodeID))

	isCopyMode := taskReq.CreationMode == "copy" && taskReq.SourceContainer != ""

	// 验证规格 ID（复制模式跳过，继承源容器规格）
	var cpuSpec *constant.CPUSpec
	var memorySpec *constant.MemorySpec
	var diskSpec *constant.DiskSpec
	var bandwidthSpec *constant.BandwidthSpec
	if !isCopyMode {
		var err error
		cpuSpec, err = constant.GetCPUSpecByID(taskReq.CPUId)
		if err != nil {
			return nil, fmt.Errorf("无效的CPU规格ID: %v", err)
		}
		memorySpec, err = constant.GetMemorySpecByID(taskReq.MemoryId)
		if err != nil {
			return nil, fmt.Errorf("无效的内存规格ID: %v", err)
		}
		diskSpec, err = constant.GetDiskSpecByID(taskReq.DiskId)
		if err != nil {
			return nil, fmt.Errorf("无效的磁盘规格ID: %v", err)
		}
		bandwidthSpec, err = constant.GetBandwidthSpecByID(taskReq.BandwidthId)
		if err != nil {
			return nil, fmt.Errorf("无效的带宽规格ID: %v", err)
		}
	}

	dbService := database.GetDatabaseService()
	var instance providerModel.Instance

	err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		if err := lockRunningCreate(tx, task.ID); err != nil {
			return err
		}
		// 验证镜像（复制模式跳过镜像验证，使用源容器名作为镜像标识）
		var systemImage systemModel.SystemImage
		imageName := "copy:" + taskReq.SourceContainer
		instanceType := "container"
		osType := "linux"
		if !isCopyMode {
			if err := tx.Where("id = ? AND status = ?", taskReq.ImageId, "active").First(&systemImage).Error; err != nil {
				return fmt.Errorf("镜像不存在或已禁用")
			}
			imageName = systemImage.Name
			instanceType = systemImage.InstanceType
			osType = systemImage.OSType
		}

		// 验证节点
		var provider providerModel.Provider
		if err := tx.Where("id = ?", taskReq.ProviderId).First(&provider).Error; err != nil {
			return fmt.Errorf("节点不存在或不可用")
		}
		providerAvailable := (provider.ConnectionType == "agent" && provider.AgentStatus == "online") ||
			(provider.ConnectionType != "agent" && (provider.Status == "active" || provider.Status == "partial"))
		if !providerAvailable {
			return fmt.Errorf("节点不存在或不可用")
		}
		if provider.IsFrozen {
			return fmt.Errorf("节点已被冻结")
		}
		if provider.ExpiresAt != nil && provider.ExpiresAt.Before(time.Now()) {
			return fmt.Errorf("节点已过期")
		}

		instanceName := s.generateInstanceName(provider.Name)

		expiredAt := determineInitialInstanceExpiryInTx(tx, &provider)
		gpuEnabled := provider.GpuEnabled && taskReq.GpuEnabled && utils.SupportsContainerGPUProvider(provider.Type, instanceType)
		gpuDeviceIDs := ""
		if gpuEnabled {
			gpuDeviceIDs = taskReq.GpuDeviceIds
		}

		// 实例归属系统用户（UserID = 0），兑换后再转移
		cpuCores := 0
		memMB := int64(0)
		diskMB := int64(0)
		bwMbps := 0
		if !isCopyMode {
			cpuCores = cpuSpec.Cores
			memMB = int64(memorySpec.SizeMB)
			diskMB = int64(diskSpec.SizeMB)
			bwMbps = bandwidthSpec.SpeedMbps
		}
		instance = providerModel.Instance{
			Name:               instanceName,
			Provider:           provider.Name,
			ProviderID:         provider.ID,
			Image:              imageName,
			CPU:                cpuCores,
			Memory:             memMB,
			Disk:               diskMB,
			Bandwidth:          bwMbps,
			InstanceType:       instanceType,
			UserID:             0, // 系统用户占位
			Status:             "creating",
			DesiredState:       providerModel.InstanceDesiredStateRunning,
			OSType:             osType,
			ExpiresAt:          expiredAt,
			IsManualExpiry:     false,
			MaxTraffic:         0,
			TrafficLimited:     false,
			TrafficLimitReason: "",
			GpuEnabled:         gpuEnabled,
			GpuDeviceIds:       gpuDeviceIDs,
			NetworkType:        provider.NetworkType, // 继承Provider的网络类型
		}
		if err := tx.Create(&instance).Error; err != nil {
			return fmt.Errorf("创建实例记录失败: %v", err)
		}

		// 更新任务关联实例 ID
		if err := tx.Model(&adminModel.Task{}).Where("id = ? AND status = ?", task.ID, "running").Updates(map[string]interface{}{
			"instance_id": instance.ID,
		}).Error; err != nil {
			return fmt.Errorf("更新任务状态失败: %v", err)
		}

		// 分配节点资源。复制模式此时还不知道源容器的 CPU/内存/磁盘，
		// 先占用实例数量，执行阶段检测源容器后再补资源用量。
		resourceService := &resources.ResourceService{}
		if err := resourceService.AllocateResourcesInTx(tx, provider.ID, instanceType,
			cpuCores, memMB, diskMB); err != nil {
			return fmt.Errorf("分配节点资源失败: %v", err)
		}

		if taskReq.SessionId != "" {
			reservationService := resources.GetResourceReservationService()
			if err := reservationService.ConsumeReservationBySessionInTx(tx, taskReq.SessionId); err != nil {
				return fmt.Errorf("消费兑换码资源预留失败: %v", err)
			}
		}

		return nil
	})

	if err != nil {
		global.APP_LOG.Error("兑换码实例预处理事务失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return nil, err
	}

	global.APP_LOG.Debug("兑换码实例预处理完成",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID))

	s.updateTaskProgress(task.ID, 25, "step.dbPreprocessing")
	return &instance, nil
}

// finalizeRedemptionInstanceCreation 阶段3: 结果处理
// 在 finalizeInstanceCreation 基础上跳过用户配额操作，并额外更新 RedemptionCode 状态
func (s *Service) finalizeRedemptionInstanceCreation(ctx context.Context, task *adminModel.Task, instance *providerModel.Instance, apiError error) error {
	// 解析兑换码 ID
	var taskReq adminModel.CreateRedemptionInstanceTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		global.APP_LOG.Error("解析兑换码任务数据失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	// Share the normal create endpoint resolution, outside the transaction.
	// SSH/API calls here can take minutes and must not hold database locks.
	var instanceUpdates map[string]interface{}
	if apiError == nil {
		instanceUpdates, _ = s.gatherInstanceNetworkInfo(ctx, instance)
	}
	dbService := database.GetDatabaseService()
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer dbCancel()
	err := dbService.ExecuteTransaction(dbCtx, func(tx *gorm.DB) error {
		if err := lockRunningCreate(tx, task.ID); err != nil {
			return err
		}
		if apiError != nil {
			if err := quarantineFailedCreate(tx, instance); err != nil {
				return err
			}
			if taskReq.RedemptionCodeID != 0 {
				return tx.Unscoped().Delete(&systemModel.RedemptionCode{}, taskReq.RedemptionCodeID).Error
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		// ——— 成功处理 ———
		global.APP_LOG.Debug("Provider创建实例成功，处理兑换码实例",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID))

		// Remote inspection and SSH mapping lookup completed before this transaction.
		if err := tx.Model(instance).Updates(instanceUpdates).Error; err != nil {
			return fmt.Errorf("更新实例信息失败: %v", err)
		}

		// 更新任务状态为 running（等待后处理完成）
		if err := tx.Model(&adminModel.Task{}).Where("id = ? AND status = ?", task.ID, "running").Updates(map[string]interface{}{
			"progress": 70,
		}).Error; err != nil {
			return fmt.Errorf("更新任务状态失败: %v", err)
		}

		// 更新兑换码状态：绑定实例 ID，状态改为 pending_use
		if taskReq.RedemptionCodeID != 0 {
			result := tx.Model(&systemModel.RedemptionCode{}).
				Where("id = ?", taskReq.RedemptionCodeID).
				Updates(map[string]interface{}{
					"status":      systemModel.RedemptionStatusPendingUse,
					"instance_id": instance.ID,
				})
			if result.Error != nil {
				return fmt.Errorf("更新兑换码状态失败: %v", result.Error)
			}
			if result.RowsAffected == 0 {
				return fmt.Errorf("兑换码已被删除，实例保留供清理")
			}
		}

		return nil
	})

	if err != nil {
		global.APP_LOG.Error("兑换码实例最终化失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return err
	}

	if apiError != nil {
		go s.delayedDeleteFailedInstance(instance.ID)
		return apiError
	}

	// 成功后的异步后处理（端口映射配置 + SSH 就绪检测 + 任务完成标记）
	go func(taskCtx context.Context, instanceID uint, providerID uint, taskID uint) {
		defer func() {
			s.taskService.ReleaseTaskLocks(taskID)
		}()
		defer func() {
			if taskCtx.Err() != nil {
				_ = s.taskService.GetStateManager().CompleteMainTask(taskID, false, taskCtx.Err().Error(), nil)
			}
		}()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("兑换码实例后处理发生panic",
					zap.Uint("instanceId", instanceID),
					zap.Any("panic", r))
				stateManager := s.taskService.GetStateManager()
				if stateManager != nil {
					_ = stateManager.CompleteMainTask(taskID, false, "兑换码实例创建成功，但后处理任务发生严重错误", nil)
				}
			}
		}()

		// 检查任务状态
		var currentTask adminModel.Task
		if err := global.APP_DB.Where("id = ?", taskID).First(&currentTask).Error; err != nil {
			return
		}
		if currentTask.Status != "running" {
			return
		}

		s.updateTaskProgress(taskID, 70, "step.waitingSSHReady")

		// 根据Provider类型和实例类型确定SSH等待时长。
		redeemSSHWait := 30 * time.Second
		var redeemProvider providerModel.Provider
		var redeemInstance providerModel.Instance
		_ = global.APP_DB.Select("instance_type").Where("id = ?", instanceID).First(&redeemInstance).Error
		if err := global.APP_DB.Select("type, pve_kvm_available").Where("id = ?", providerID).First(&redeemProvider).Error; err == nil {
			redeemSSHWait = providerCreateSSHWaitTimeout(redeemProvider, redeemInstance)
		}
		if err := s.waitForInstanceSSHReadyInRange(taskCtx, instanceID, providerID, taskID, redeemSSHWait, 70, 82); err != nil {
			utils.AppendTaskError(taskID, 82, "step.waitingSSHReadyFailed", err)
			global.APP_LOG.Warn("等待兑换码实例SSH就绪超时",
				zap.Uint("instanceId", instanceID),
				zap.Error(err))
		}

		if err := s.ensureInstanceRunnableAfterCreate(taskCtx, instanceID, providerID, taskID, 83); err != nil {
			finalErr := fmt.Errorf("兑换码实例创建后状态检查失败: %w", err)
			utils.AppendTaskError(taskID, 83, "step.createPostProcessFailed", finalErr)
			_ = global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Update("status", "error").Error
			if taskReq.RedemptionCodeID != 0 {
				_ = global.APP_DB.Unscoped().Delete(&systemModel.RedemptionCode{}, taskReq.RedemptionCodeID).Error
			}
			go s.delayedDeleteFailedInstance(instanceID)
			stateManager := s.taskService.GetStateManager()
			if stateManager != nil {
				_ = stateManager.CompleteMainTask(taskID, false, finalErr.Error(), nil)
			}
			return
		}
		if err := s.ensureInstanceNetworkAddresses(taskCtx, instanceID, providerID); err != nil {
			global.APP_LOG.Warn("兑换码实例网络地址补齐失败",
				zap.Uint("instanceId", instanceID),
				zap.Error(err))
		}

		s.updateTaskProgress(taskID, 84, "step.configuringPortMappings")
		portMappingService := &resources.PortMappingService{}
		if err := portMappingService.ActivatePendingControllerPortMappings(taskCtx, instanceID, providerID); err != nil {
			finalErr := fmt.Errorf("激活兑换码实例控制端端口映射失败: %w", err)
			utils.AppendTaskError(taskID, 84, "step.createPostProcessFailed", finalErr)
			_ = global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Update("status", "error").Error
			if taskReq.RedemptionCodeID != 0 {
				_ = global.APP_DB.Unscoped().Delete(&systemModel.RedemptionCode{}, taskReq.RedemptionCodeID).Error
			}
			go s.delayedDeleteFailedInstance(instanceID)
			stateManager := s.taskService.GetStateManager()
			if stateManager != nil {
				_ = stateManager.CompleteMainTask(taskID, false, finalErr.Error(), nil)
			}
			return
		}
		existingPorts, _ := portMappingService.GetInstancePortMappings(instanceID)
		if len(existingPorts) == 0 {
			if err := portMappingService.CreateDefaultPortMappings(instanceID, providerID); err != nil {
				global.APP_LOG.Warn("兑换码实例创建默认端口映射失败",
					zap.Uint("instanceId", instanceID),
					zap.Error(err))
			} else {
				global.APP_LOG.Debug("兑换码实例默认端口映射创建成功",
					zap.Uint("instanceId", instanceID))
			}
		} else {
			global.APP_LOG.Debug("兑换码实例已有端口映射，跳过创建",
				zap.Uint("instanceId", instanceID),
				zap.Int("existingPortCount", len(existingPorts)))
		}

		s.updateTaskProgress(taskID, 87, "step.verifyingMonitorStatus")
		monitoringStatus := s.ensurePostCreateMonitoring(taskCtx, instanceID, providerID, "兑换码实例创建")
		s.updateTaskProgress(taskID, 92, "step.configuringAgentMonitor")
		if monitoringStatus.TrafficEnabled && !monitoringStatus.PmacctReady && !monitoringStatus.AgentMonitorReady {
			global.APP_LOG.Debug("兑换码实例创建后未获得可用监控记录，后续调度或人工同步仍可补齐",
				zap.Uint("instanceId", instanceID),
				zap.Uint("providerId", providerID),
				zap.String("trafficMethod", monitoringStatus.TrafficMethod))
		}

		s.updateTaskProgress(taskID, 98, "step.startingTrafficSync")

		if monitoringStatus.PmacctReady {
			syncTrigger := traffic.NewSyncTriggerService()
			syncTrigger.TriggerInstanceTrafficSync(instanceID, "兑换码实例创建后初始同步")
			global.APP_LOG.Debug("兑换码实例流量同步已触发", zap.Uint("instanceId", instanceID))
		} else if monitoringStatus.TrafficEnabled {
			global.APP_LOG.Debug("跳过兑换码实例pmacct流量同步触发",
				zap.Uint("instanceId", instanceID),
				zap.Uint("providerId", providerID),
				zap.String("trafficMethod", monitoringStatus.TrafficMethod))
		}

		s.updateTaskProgress(taskID, 99, "step.redemptionCreateCompleted")

		stateManager := s.taskService.GetStateManager()
		if stateManager != nil {
			_ = stateManager.CompleteMainTask(taskID, true, "step.redemptionCreateCompleted", nil)
		}

		global.APP_LOG.Debug("兑换码实例后处理完成", zap.Uint("instanceId", instanceID))
	}(ctx, instance.ID, instance.ProviderID, task.ID)

	// 后台goroutine已接管，通知worker pool跳过CompleteTask
	return interfaces.ErrAsyncCompletion
}

// hardDeleteRedemptionCodeByTask 根据任务数据硬删除关联的兑换码（用于预处理失败场景）
func (s *Service) hardDeleteRedemptionCodeByTask(task *adminModel.Task) {
	var taskReq adminModel.CreateRedemptionInstanceTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return
	}
	if taskReq.RedemptionCodeID == 0 {
		return
	}
	if err := global.APP_DB.Unscoped().Delete(&systemModel.RedemptionCode{}, taskReq.RedemptionCodeID).Error; err != nil {
		global.APP_LOG.Error("删除兑换码记录失败",
			zap.Uint("codeId", taskReq.RedemptionCodeID),
			zap.Error(err))
	}
}
