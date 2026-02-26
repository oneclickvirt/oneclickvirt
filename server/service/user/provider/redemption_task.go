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
	"oneclickvirt/provider/incus"
	lxd "oneclickvirt/provider/lxd"
	"oneclickvirt/service/database"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	traffic "oneclickvirt/service/traffic"

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

	s.updateTaskProgress(task.ID, 5, "正在准备兑换码实例创建...")

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

	s.updateTaskProgress(task.ID, 30, "正在调用Provider API创建实例...")

	// 阶段2: Provider API 调用（30% -> 70%）—— 直接复用
	apiError := s.executeProviderCreation(ctx, task, instance)

	// 阶段3: 结果处理
	global.APP_LOG.Info("开始处理兑换码实例创建结果",
		zap.Uint("taskId", task.ID),
		zap.Bool("hasApiError", apiError != nil))

	if finalizeErr := s.finalizeRedemptionInstanceCreation(context.Background(), task, instance, apiError); finalizeErr != nil {
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

	global.APP_LOG.Info("开始兑换码实例预处理",
		zap.Uint("taskId", task.ID),
		zap.Uint("redemptionCodeId", taskReq.RedemptionCodeID))

	// 验证规格 ID
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

	dbService := database.GetDatabaseService()
	var instance providerModel.Instance

	err = dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// 验证镜像
		var systemImage systemModel.SystemImage
		if err := tx.Where("id = ? AND status = ?", taskReq.ImageId, "active").First(&systemImage).Error; err != nil {
			return fmt.Errorf("镜像不存在或已禁用")
		}

		// 验证节点
		var provider providerModel.Provider
		if err := tx.Where("id = ? AND status IN (?)", taskReq.ProviderId, []string{"active", "partial"}).First(&provider).Error; err != nil {
			return fmt.Errorf("节点不存在或不可用")
		}
		if provider.IsFrozen {
			return fmt.Errorf("节点已被冻结")
		}
		if provider.ExpiresAt != nil && provider.ExpiresAt.Before(time.Now()) {
			return fmt.Errorf("节点已过期")
		}

		instanceName := s.generateInstanceName(provider.Name)

		var expiredAt *time.Time
		if provider.ExpiresAt != nil {
			expiredAt = provider.ExpiresAt
		}

		// 实例归属系统用户（UserID = 0），兑换后再转移
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
			UserID:             0, // 系统用户占位
			Status:             "creating",
			OSType:             systemImage.OSType,
			ExpiresAt:          expiredAt,
			IsManualExpiry:     false,
			MaxTraffic:         0,
			TrafficLimited:     false,
			TrafficLimitReason: "",
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

		// 分配节点资源（占用 Provider 的 CPU/内存/磁盘计数）
		resourceService := &resources.ResourceService{}
		if err := resourceService.AllocateResourcesInTx(tx, provider.ID, systemImage.InstanceType,
			cpuSpec.Cores, int64(memorySpec.SizeMB), int64(diskSpec.SizeMB)); err != nil {
			return fmt.Errorf("分配节点资源失败: %v", err)
		}

		return nil
	})

	if err != nil {
		global.APP_LOG.Error("兑换码实例预处理事务失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return nil, err
	}

	global.APP_LOG.Info("兑换码实例预处理完成",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID))

	s.updateTaskProgress(task.ID, 25, "数据库预处理完成")
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

	err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// 检查任务是否已被管理员取消（防止竞争条件导致孤儿实例）
		var taskStatus string
		if fetchErr := tx.Model(&adminModel.Task{}).Select("status").Where("id = ?", task.ID).Scan(&taskStatus).Error; fetchErr == nil && taskStatus == "cancelled" {
			global.APP_LOG.Info("兑换码实例任务已被管理员取消，跳过最终化并清理实例",
				zap.Uint("taskId", task.ID))
			go s.delayedDeleteFailedInstance(instance.ID)
			return nil
		}

		if apiError != nil {
			// ——— 失败处理 ———
			global.APP_LOG.Error("Provider API调用失败，回滚兑换码实例",
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

			// 更新任务为失败
			if err := tx.Model(task).Updates(map[string]interface{}{
				"status":        "failed",
				"completed_at":  time.Now(),
				"error_message": apiError.Error(),
			}).Error; err != nil {
				return fmt.Errorf("更新任务状态失败: %v", err)
			}

			// 硬删除兑换码记录（实例创建失败则兑换码无效）
			if taskReq.RedemptionCodeID != 0 {
				if err := tx.Unscoped().Delete(&systemModel.RedemptionCode{}, taskReq.RedemptionCodeID).Error; err != nil {
					global.APP_LOG.Error("删除失败兑换码记录失败",
						zap.Uint("codeId", taskReq.RedemptionCodeID), zap.Error(err))
					// 不阻断主流程
				}
			}

			// 延迟异步删除失败实例
			go s.delayedDeleteFailedInstance(instance.ID)

			return nil
		}

		// ——— 成功处理 ———
		global.APP_LOG.Info("Provider API调用成功，处理兑换码实例",
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
				} else if dbProvider.Type == "proxmox" {
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
			if err := tx.Model(&systemModel.RedemptionCode{}).
				Where("id = ?", taskReq.RedemptionCodeID).
				Updates(map[string]interface{}{
					"status":      systemModel.RedemptionStatusPendingUse,
					"instance_id": instance.ID,
				}).Error; err != nil {
				return fmt.Errorf("更新兑换码状态失败: %v", err)
			}
		}

		return nil
	})

	if err != nil {
		global.APP_LOG.Error("兑换码实例最终化失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return err
	}

	if apiError != nil {
		if global.APP_TASK_LOCK_RELEASER != nil {
			global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(task.ID)
		}
		return nil
	}

	// 成功后的异步后处理（端口映射配置 + SSH 就绪检测 + 任务完成标记）
	go func(instanceID uint, providerID uint, taskID uint) {
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("兑换码实例后处理发生panic",
					zap.Uint("instanceId", instanceID),
					zap.Any("panic", r))
				stateManager := s.taskService.GetStateManager()
				if stateManager != nil {
					_ = stateManager.CompleteMainTask(taskID, true, "兑换码实例创建成功，但部分后处理任务失败", nil)
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

		s.updateTaskProgress(taskID, 75, "等待实例SSH服务就绪...")
		_ = s.waitForInstanceSSHReady(instanceID, providerID, taskID, 120*time.Second)

		s.updateTaskProgress(taskID, 80, "正在配置端口映射...")
		portMappingService := &resources.PortMappingService{}
		existingPorts, _ := portMappingService.GetInstancePortMappings(instanceID)
		if len(existingPorts) == 0 {
			_ = portMappingService.CreateDefaultPortMappings(instanceID, providerID)
		}

		s.updateTaskProgress(taskID, 85, "正在验证监控状态...")

		// 验证 pmacct 监控状态（与 finalizeInstanceCreation 保持一致）
		pmacctInitSuccess := false
		var trafficEnabled bool
		var dbProv providerModel.Provider
		if err := global.APP_DB.Where("id = ?", providerID).First(&dbProv).Error; err == nil {
			trafficEnabled = dbProv.EnableTrafficControl
		}
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

		s.updateTaskProgress(taskID, 98, "正在启动流量同步...")

		// 触发流量同步（仅在 pmacct 初始化成功时执行）
		if pmacctInitSuccess {
			syncTrigger := traffic.NewSyncTriggerService()
			syncTrigger.TriggerInstanceTrafficSync(instanceID, "兑换码实例创建后初始同步")
			global.APP_LOG.Info("兑换码实例流量同步已触发", zap.Uint("instanceId", instanceID))
		} else if trafficEnabled {
			global.APP_LOG.Info("跳过流量同步触发（pmacct初始化失败）", zap.Uint("instanceId", instanceID))
		}

		s.updateTaskProgress(taskID, 99, "兑换码实例创建完成")

		stateManager := s.taskService.GetStateManager()
		if stateManager != nil {
			_ = stateManager.CompleteMainTask(taskID, true, "兑换码实例创建成功", nil)
		}

		global.APP_LOG.Info("兑换码实例后处理完成", zap.Uint("instanceId", instanceID))
	}(instance.ID, instance.ProviderID, task.ID)

	return nil
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
