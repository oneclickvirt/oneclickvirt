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
	"oneclickvirt/provider/incus"
	lxd "oneclickvirt/provider/lxd"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	traffic "oneclickvirt/service/traffic"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ProcessCreateRedemptionInstanceTask 处理兑换码实例创建任务（三阶段）
// 与 ProcessCreateInstanceTask 的区别：
//   - 不消费 SessionId 预留资源（兑换码流程无资源预留）
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

// prepareRedemptionInstanceCreation 阶段1: 数据库预处理（无 SessionId 消费 / 无用户配额检查）
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
		if err := tx.Model(task).Updates(map[string]interface{}{
			"instance_id": instance.ID,
			"status":      "processing",
		}).Error; err != nil {
			return fmt.Errorf("更新任务状态失败: %v", err)
		}

		// 分配节点资源（复制模式不分配资源，继承源容器资源）
		if !isCopyMode {
			resourceService := &resources.ResourceService{}
			if err := resourceService.AllocateResourcesInTx(tx, provider.ID, instanceType,
				cpuCores, memMB, diskMB); err != nil {
				return fmt.Errorf("分配节点资源失败: %v", err)
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

	dbService := database.GetDatabaseService()
	cancelledDuringFinalize := false

	err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// 检查任务是否已被管理员取消（防止竞争条件导致孤儿实例）
		var taskStatus string
		if fetchErr := tx.Model(&adminModel.Task{}).Select("status").Where("id = ?", task.ID).Scan(&taskStatus).Error; fetchErr == nil && taskStatus == "cancelled" {
			global.APP_LOG.Debug("兑换码实例任务已被管理员取消，跳过最终化并清理实例",
				zap.Uint("taskId", task.ID))
			cancelledDuringFinalize = true
			go s.delayedDeleteFailedInstance(instance.ID)
			return nil
		}

		if apiError != nil {
			// ——— 失败处理 ———
			global.APP_LOG.Error("Provider创建实例失败，回滚兑换码实例",
				zap.Uint("taskId", task.ID), zap.Error(apiError))

			// 更新实例状态为失败
			if err := tx.Model(instance).Updates(map[string]interface{}{
				"status": "failed",
			}).Error; err != nil {
				return fmt.Errorf("更新实例状态失败: %v", err)
			}

			// 清理预分配端口映射
			portMappingService := &resources.PortMappingService{}
			_ = portMappingService.DeleteInstancePortMappingsInTx(tx, instance.ID)

			// 释放节点资源
			resourceService := &resources.ResourceService{}
			_ = resourceService.ReleaseResourcesInTx(tx, instance.ProviderID, instance.InstanceType,
				instance.CPU, instance.Memory, instance.Disk)

			if err := tx.Model(&providerModel.ProviderIPv4Pool{}).
				Where("instance_id = ?", instance.ID).
				Updates(map[string]interface{}{"is_allocated": false, "instance_id": nil}).Error; err != nil {
				global.APP_LOG.Warn("释放失败兑换实例IPv4池地址失败",
					zap.Uint("instanceId", instance.ID),
					zap.Error(err))
			}

			// 更新任务为失败；若管理员已强制取消，保留取消终态。
			if err := tx.Model(&adminModel.Task{}).
				Where("id = ? AND status NOT IN ?", task.ID, []string{"completed", "failed", "cancelled", "timeout"}).
				Updates(map[string]interface{}{
					"status":        "failed",
					"completed_at":  time.Now(),
					"error_message": apiError.Error(),
				}).Error; err != nil {
				return fmt.Errorf("更新任务状态失败: %v", err)
			}

			// 硬删除兑换码记录（实例创建失败则兑换码无效）
			if taskReq.RedemptionCodeID != 0 {
				if err := tx.Unscoped().Delete(&systemModel.RedemptionCode{}, taskReq.RedemptionCodeID).Error; err != nil {
					global.APP_LOG.Warn("删除失败兑换码记录失败",
						zap.Uint("codeId", taskReq.RedemptionCodeID), zap.Error(err))
					// 不阻断主流程
				}
			}

			// 延迟异步删除失败实例
			go s.delayedDeleteFailedInstance(instance.ID)

			return nil
		}

		// ——— 成功处理 ———
		global.APP_LOG.Debug("Provider创建实例成功，处理兑换码实例",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID))

		// 构建实例更新数据
		instanceUpdates := map[string]interface{}{
			"status":   "running",
			"username": "root",
			"ssh_port": 22,
		}

		// 从 Provider 记录获取公网 IP
		var dbProvider providerModel.Provider
		if err := global.APP_DB.First(&dbProvider, instance.ProviderID).Error; err == nil {
			// agent录入模式+无端口映射模式：不设置公网IP
			// 因为该模式下的端口转发是通过控制端内网穿透实现的，节点本身没有对外的公网IP
			if !(dbProvider.ConnectionType == "agent" && dbProvider.NetworkType == "no_port_mapping") {
				publicIPSource := dbProvider.PortIP
				if publicIPSource == "" {
					publicIPSource = dbProvider.Endpoint
				}
				if publicIPSource != "" {
					if colonIndex := strings.LastIndex(publicIPSource, ":"); colonIndex > 0 {
						if strings.Count(publicIPSource, ":") > 1 && !strings.HasPrefix(publicIPSource, "[") {
							instanceUpdates["public_ip"] = publicIPSource
						} else {
							instanceUpdates["public_ip"] = publicIPSource[:colonIndex]
						}
					} else {
						instanceUpdates["public_ip"] = publicIPSource
					}
				}
			}
		}

		// 通过 Provider API 获取实例实际状态和 IP（与 finalizeInstanceCreation 保持一致）
		actualInstance, getErr := s.getInstanceDetailsAfterCreation(ctx, instance)
		if getErr != nil {
			global.APP_LOG.Warn("获取兑换码实例详情失败，使用Provider默认IP",
				zap.Uint("taskId", task.ID), zap.Error(getErr))
		} else if actualInstance != nil {
			if actualInstance.PublicIP != "" {
				instanceUpdates["public_ip"] = actualInstance.PublicIP
			}
			if actualInstance.IPv6Address != "" {
				instanceUpdates["ipv6_address"] = actualInstance.IPv6Address
			}
			instanceUpdates["ssh_port"] = 22
			if actualInstance.Status != "" {
				providerStatus := strings.ToLower(actualInstance.Status)
				if providerStatus == "running" || providerStatus == "active" {
					instanceUpdates["status"] = "running"
				} else if providerStatus == "stopped" {
					instanceUpdates["status"] = "stopped"
				} else {
					global.APP_LOG.Warn("Provider返回了非标准状态",
						zap.String("instanceName", instance.Name),
						zap.String("providerStatus", actualInstance.Status))
				}
			}
			// 获取各 Provider 类型的内网 IP（LXD / Incus / Proxmox）
			providerSvc := providerService.GetProviderService()
			if providerInstance, exists := providerSvc.GetProviderByID(instance.ProviderID); exists {
				if dbProvider.Type == "lxd" {
					if lxdProvider, ok := providerInstance.(*lxd.LXDProvider); ok {
						if ipv4Address, err := lxdProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ipv4Address != "" {
							instanceUpdates["private_ip"] = ipv4Address
						}
						if ipv6Address, err := lxdProvider.GetInstanceIPv6(instance.Name); err == nil && ipv6Address != "" {
							instanceUpdates["ipv6_address"] = ipv6Address
						}
						if publicIPv6, err := lxdProvider.GetInstancePublicIPv6(instance.Name); err == nil && publicIPv6 != "" {
							instanceUpdates["public_ipv6"] = publicIPv6
						}
					}
				} else if dbProvider.Type == "incus" {
					if incusProvider, ok := providerInstance.(*incus.IncusProvider); ok {
						if ipv4Address, err := incusProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ipv4Address != "" {
							instanceUpdates["private_ip"] = ipv4Address
						}
						if ipv6Address, err := incusProvider.GetInstanceIPv6(ctx, instance.Name); err == nil && ipv6Address != "" {
							instanceUpdates["ipv6_address"] = ipv6Address
						}
						if publicIPv6, err := incusProvider.GetInstancePublicIPv6(ctx, instance.Name); err == nil && publicIPv6 != "" {
							instanceUpdates["public_ipv6"] = publicIPv6
						}
					}
				} else if dbProvider.Type == "proxmox" || dbProvider.Type == "proxmoxve" {
					if proxmoxProvider, ok := providerInstance.(interface {
						GetInstanceIPv4(ctx context.Context, instanceName string) (string, error)
						GetInstanceIPv6(ctx context.Context, instanceName string) (string, error)
						GetInstancePublicIPv6(ctx context.Context, instanceName string) (string, error)
					}); ok {
						if ipv4Address, err := proxmoxProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ipv4Address != "" {
							instanceUpdates["private_ip"] = ipv4Address
							if dbProvider.NetworkType == "dedicated_ipv4" || dbProvider.NetworkType == "dedicated_ipv4_ipv6" {
								instanceUpdates["public_ip"] = ipv4Address
							}
						}
						if ipv6Address, err := proxmoxProvider.GetInstanceIPv6(ctx, instance.Name); err == nil && ipv6Address != "" {
							instanceUpdates["ipv6_address"] = ipv6Address
						}
						if publicIPv6, err := proxmoxProvider.GetInstancePublicIPv6(ctx, instance.Name); err == nil && publicIPv6 != "" {
							instanceUpdates["public_ipv6"] = publicIPv6
						}
					}
				}
			}
		} else {
			instanceUpdates["ssh_port"] = 22
		}

		if err := tx.Model(instance).Updates(instanceUpdates).Error; err != nil {
			return fmt.Errorf("更新实例信息失败: %v", err)
		}

		// 更新任务状态为 running（等待后处理完成）
		if err := tx.Model(task).Updates(map[string]interface{}{
			"status":   "running",
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
				// 兑换码已被管理员提前删除（竞态窗口）：实例已创建但归属码已消失
				// 触发异步清理，避免孤儿实例
				global.APP_LOG.Warn("兑换码已不存在，清理孤儿实例",
					zap.Uint("taskId", task.ID),
					zap.Uint("codeId", taskReq.RedemptionCodeID),
					zap.Uint("instanceId", instance.ID))
				go s.delayedDeleteFailedInstance(instance.ID)
			}
		}

		return nil
	})

	if err != nil {
		global.APP_LOG.Error("兑换码实例最终化失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return err
	}

	if cancelledDuringFinalize {
		s.taskService.ReleaseTaskLocks(task.ID)
		return nil
	}

	if apiError != nil {
		if global.APP_TASK_LOCK_RELEASER != nil {
			global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(task.ID)
		}
		return nil
	}

	// 成功后的异步后处理（端口映射配置 + SSH 就绪检测 + 任务完成标记）
	go func(taskCtx context.Context, instanceID uint, providerID uint, taskID uint) {
		defer func() {
			s.taskService.ReleaseTaskLocks(taskID)
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
