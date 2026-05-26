package provider

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/provider/incus"
	"oneclickvirt/provider/lxd"
	agentLifecycle "oneclickvirt/service/agent"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/traffic"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// gatherInstanceNetworkInfo 在事务外收集实例的网络地址信息（避免在事务中进行远程API调用）
func (s *Service) gatherInstanceNetworkInfo(ctx context.Context, instance *providerModel.Instance) (instanceUpdates map[string]interface{}, dbProvider providerModel.Provider) {
	instanceUpdates = map[string]interface{}{
		"status":   "running",
		"username": "root",
	}

	// 查询Provider信息（仅一次DB查询，在事务外执行）
	if err := global.APP_DB.First(&dbProvider, instance.ProviderID).Error; err != nil {
		global.APP_LOG.Warn("查询Provider信息失败，使用默认值",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
		return
	}

	// 设置公网IP
	if !(dbProvider.ConnectionType == "agent" && dbProvider.NetworkType == "no_port_mapping") {
		publicIPSource := dbProvider.PortIP
		if publicIPSource == "" {
			publicIPSource = dbProvider.Endpoint
		}
		if publicIPSource != "" {
			if strings.HasPrefix(publicIPSource, "[") {
				if host, _, err := net.SplitHostPort(publicIPSource); err == nil {
					instanceUpdates["public_ip"] = host
				} else {
					trimmed := strings.TrimPrefix(publicIPSource, "[")
					trimmed = strings.TrimSuffix(trimmed, "]")
					instanceUpdates["public_ip"] = trimmed
				}
			} else if colonIndex := strings.LastIndex(publicIPSource, ":"); colonIndex > 0 {
				if strings.Count(publicIPSource, ":") > 1 {
					instanceUpdates["public_ip"] = publicIPSource
				} else {
					instanceUpdates["public_ip"] = publicIPSource[:colonIndex]
				}
			} else {
				instanceUpdates["public_ip"] = publicIPSource
			}
			global.APP_LOG.Debug("设置实例公网IP",
				zap.String("instanceName", instance.Name),
				zap.String("portIP", dbProvider.PortIP),
				zap.String("endpoint", dbProvider.Endpoint),
				zap.Any("publicIP", instanceUpdates["public_ip"]))
		}
	} else {
		global.APP_LOG.Debug("agent+no_port_mapping模式，跳过设置实例公网IP",
			zap.String("instanceName", instance.Name),
			zap.String("connectionType", dbProvider.ConnectionType),
			zap.String("networkType", dbProvider.NetworkType))
	}

	// 尝试从Provider获取实例详细信息（远程API调用，必须在事务外执行）
	actualInstance, err := s.getInstanceDetailsAfterCreation(ctx, instance)
	if err != nil {
		global.APP_LOG.Warn("获取实例详细信息失败，使用默认值",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
	}

	if actualInstance != nil {
		if actualInstance.IP != "" {
			instanceUpdates["private_ip"] = actualInstance.IP
		}
		if actualInstance.PrivateIP != "" {
			instanceUpdates["private_ip"] = actualInstance.PrivateIP
		}
		if actualInstance.PublicIP != "" {
			instanceUpdates["public_ip"] = actualInstance.PublicIP
		}
		if actualInstance.IPv6Address != "" {
			instanceUpdates["ipv6_address"] = actualInstance.IPv6Address
		}
		if dbProvider.Type != "docker" && dbProvider.Type != "podman" && dbProvider.Type != "containerd" {
			instanceUpdates["ssh_port"] = 22
		}
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
	} else {
		if dbProvider.Type != "docker" && dbProvider.Type != "podman" && dbProvider.Type != "containerd" {
			instanceUpdates["ssh_port"] = 22
		}
	}

	// 通过Provider API获取详细的IPv4/IPv6地址（远程调用，必须在事务外）
	if actualInstance != nil {
		providerSvc := providerService.GetProviderService()
		if providerInstance, exists := providerSvc.GetProviderByID(instance.ProviderID); exists {
			switch dbProvider.Type {
			case "lxd":
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
			case "incus":
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
			case "proxmox", "proxmoxve":
				s.gatherProxmoxNetworkInfo(ctx, providerInstance, instance, &dbProvider, instanceUpdates)
			case "qemu", "kubevirt":
				if vmInstance, err := providerInstance.GetInstance(ctx, instance.Name); err == nil && vmInstance != nil {
					if vmInstance.PrivateIP != "" {
						instanceUpdates["private_ip"] = vmInstance.PrivateIP
					} else if vmInstance.IP != "" {
						instanceUpdates["private_ip"] = vmInstance.IP
					}
				}
			}
		}
	}

	return instanceUpdates, dbProvider
}

// gatherProxmoxNetworkInfo 收集Proxmox实例的网络信息（事务外调用）
func (s *Service) gatherProxmoxNetworkInfo(ctx context.Context, providerInstance provider.Provider, instance *providerModel.Instance, dbProvider *providerModel.Provider, instanceUpdates map[string]interface{}) {
	type proxmoxWithIPv interface {
		GetInstanceIPv4(ctx context.Context, instanceName string) (string, error)
		GetInstanceIPv6(ctx context.Context, instanceName string) (string, error)
		GetInstancePublicIPv6(ctx context.Context, instanceName string) (string, error)
	}
	if pxProvider, ok := providerInstance.(proxmoxWithIPv); ok {
		if ipv4Address, err := pxProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ipv4Address != "" {
			instanceUpdates["private_ip"] = ipv4Address
			if dbProvider.NetworkType == "dedicated_ipv4" || dbProvider.NetworkType == "dedicated_ipv4_ipv6" {
				instanceUpdates["public_ip"] = ipv4Address
			}
		}
		if ipv6Address, err := pxProvider.GetInstanceIPv6(ctx, instance.Name); err == nil && ipv6Address != "" {
			if dbProvider.NetworkType == "nat_ipv4_ipv6" {
				instanceUpdates["ipv6_address"] = ipv6Address
				if publicIPv6, err := pxProvider.GetInstancePublicIPv6(ctx, instance.Name); err == nil && publicIPv6 != "" {
					instanceUpdates["public_ipv6"] = publicIPv6
				}
			} else if dbProvider.NetworkType == "dedicated_ipv4_ipv6" || dbProvider.NetworkType == "ipv6_only" {
				instanceUpdates["public_ipv6"] = ipv6Address
			}
		}
		return
	}
	// 回退到 GetInstance
	type proxmoxWithGet interface {
		GetInstance(ctx context.Context, instanceID string) (*provider.Instance, error)
	}
	if pxProvider, ok := providerInstance.(proxmoxWithGet); ok {
		if proxmoxInstance, err := pxProvider.GetInstance(ctx, instance.Name); err == nil && proxmoxInstance != nil {
			ip := proxmoxInstance.IP
			if ip == "" {
				ip = proxmoxInstance.PrivateIP
			}
			if ip != "" {
				instanceUpdates["private_ip"] = ip
				if dbProvider.NetworkType == "dedicated_ipv4" || dbProvider.NetworkType == "dedicated_ipv4_ipv6" {
					instanceUpdates["public_ip"] = ip
				}
			}
			if proxmoxInstance.IPv6Address != "" {
				if dbProvider.NetworkType == "nat_ipv4_ipv6" {
					instanceUpdates["ipv6_address"] = proxmoxInstance.IPv6Address
				} else if dbProvider.NetworkType == "dedicated_ipv4_ipv6" || dbProvider.NetworkType == "ipv6_only" {
					instanceUpdates["public_ipv6"] = proxmoxInstance.IPv6Address
				}
			}
		}
	}
}

// finalizeInstanceCreation 阶段3: 结果处理
func (s *Service) finalizeInstanceCreation(ctx context.Context, task *adminModel.Task, instance *providerModel.Instance, apiError error) error {
	global.APP_LOG.Debug("开始最终化实例创建", zap.Uint("taskId", task.ID), zap.Bool("hasApiError", apiError != nil))

	dbService := database.GetDatabaseService()

	// 在事务外收集实例网络信息（避免长事务中进行远程API调用）
	var instanceUpdates map[string]interface{}
	if apiError == nil {
		global.APP_LOG.Debug("Provider创建实例成功，获取实例详细信息", zap.Uint("taskId", task.ID))
		instanceUpdates, _ = s.gatherInstanceNetworkInfo(ctx, instance)
	}

	// 在事务中仅执行DB写入操作（短事务）
	err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		if apiError != nil {
			// Provider创建实例失败的处理
			global.APP_LOG.Error("Provider创建实例失败，回滚实例创建", zap.Uint("taskId", task.ID), zap.Error(apiError))

			// 更新实例状态为失败
			if err := tx.Model(instance).Updates(map[string]interface{}{
				"status": "failed",
			}).Error; err != nil {
				return fmt.Errorf("更新实例状态失败: %v", err)
			}

			// 清理预分配的端口映射
			portMappingService := &resources.PortMappingService{}
			if err := portMappingService.DeleteInstancePortMappingsInTx(tx, instance.ID); err != nil {
				global.APP_LOG.Warn("清理失败实例端口映射失败",
					zap.Uint("instanceId", instance.ID),
					zap.Error(err))
				// 不返回错误，继续其他清理操作
			} else {
				global.APP_LOG.Debug("清理失败实例端口映射成功",
					zap.Uint("instanceId", instance.ID))
			}

			// 释放已分配的Provider资源
			resourceService := &resources.ResourceService{}
			if err := resourceService.ReleaseResourcesInTx(tx, instance.ProviderID, instance.InstanceType,
				instance.CPU, instance.Memory, instance.Disk); err != nil {
				global.APP_LOG.Warn("释放Provider资源失败", zap.Uint("instanceId", instance.ID), zap.Error(err))
				// 不返回错误，因为这不是关键操作
			} else {
				global.APP_LOG.Debug("Provider资源释放成功", zap.Uint("instanceId", instance.ID))
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

		// Provider创建实例成功，使用事务外预先收集的数据执行DB写入（短事务）
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
			"progress": 70, // Provider创建实例成功，还需要后处理任务
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

	// 如果Provider创建实例成功，执行后处理任务（同步完成关键任务后再标记完成）
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
				global.APP_LOG.Debug("任务状态已非running，跳过后处理任务",
					zap.Uint("taskId", taskID),
					zap.String("currentStatus", currentTask.Status))
				return
			}
			global.APP_LOG.Debug("开始执行实例创建后处理任务", zap.Uint("instanceId", instanceID))

			// 更新进度到75% (等待实例SSH服务就绪)
			s.updateTaskProgress(taskID, 75, "等待实例SSH服务就绪...")

			// 根据Provider类型确定SSH等待时长：QEMU/KubeVirt虚拟机启动慢，需要更长等待
			sshWaitTimeout := 120 * time.Second
			var dbProviderForWait providerModel.Provider
			if err := global.APP_DB.Select("type, pve_kvm_available").Where("id = ?", providerID).First(&dbProviderForWait).Error; err == nil {
				switch dbProviderForWait.Type {
				case "qemu", "kubevirt":
					sshWaitTimeout = 360 * time.Second // 6分钟等待VM cloud-init完成
				case "proxmox":
					// Proxmox使用QEMU软件模拟时启动更慢，需要更长等待
					if dbProviderForWait.PveKvmAvailable != nil && !*dbProviderForWait.PveKvmAvailable {
						sshWaitTimeout = 360 * time.Second // 6分钟：QEMU软件模拟
					} else {
						sshWaitTimeout = 240 * time.Second // 4分钟：KVM硬件加速或未知
					}
				}
			}

			// 智能等待实例SSH服务就绪，传入taskID以便更新进度
			if err := s.waitForInstanceSSHReady(instanceID, providerID, taskID, sshWaitTimeout); err != nil {
				global.APP_LOG.Warn("等待实例SSH就绪超时",
					zap.Uint("instanceId", instanceID),
					zap.Error(err))
				// 继续执行，但后续SSH相关操作可能失败
			}

			if err := s.ensureInstanceNetworkAddresses(context.Background(), instanceID, providerID); err != nil {
				global.APP_LOG.Warn("实例网络地址补齐失败",
					zap.Uint("instanceId", instanceID),
					zap.Error(err))
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
					global.APP_LOG.Debug("默认端口映射创建成功",
						zap.Uint("instanceId", instanceID))
				}
			} else {
				global.APP_LOG.Debug("实例已有端口映射，跳过创建",
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
				global.APP_LOG.Debug("pmacct监控已在实例创建时初始化",
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
							global.APP_LOG.Debug("等待10秒后重试设置SSH密码",
								zap.Uint("instanceId", instanceID))
							time.Sleep(10 * time.Second) // 重试间隔10秒
						}
					} else {
						global.APP_LOG.Debug("实例SSH密码设置成功",
							zap.Uint("instanceId", instanceID),
							zap.String("instanceName", currentInstance.Name))
						passwordSetSuccess = true
						break
					}
				}

				// 密码设置成功后，验证SSH密码认证是否真正可用
				if passwordSetSuccess {
					s.updateTaskProgress(taskID, 92, "正在验证SSH密码可用性...")
					var sshVerifyProvider providerModel.Provider
					if err := global.APP_DB.First(&sshVerifyProvider, providerID).Error; err == nil {
						verifyHost := sshVerifyProvider.PortIP
						if verifyHost == "" {
							verifyHost = sshVerifyProvider.Endpoint
						}
						if colonIndex := strings.LastIndex(verifyHost, ":"); colonIndex > 0 {
							if strings.Count(verifyHost, ":") == 1 || strings.HasPrefix(verifyHost, "[") {
								verifyHost = verifyHost[:colonIndex]
							}
						}
						verifyPort := currentInstance.SSHPort
						if verifyPort == 0 {
							verifyPort = 22
						}
						var sshPortMapping providerModel.Port
						if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'", instanceID).First(&sshPortMapping).Error; err == nil {
							verifyPort = sshPortMapping.HostPort
						}
						s.verifySSHPasswordAuth(instanceID, verifyHost, verifyPort, currentInstance.Username, currentInstance.Password)
					}
				}
			}

			// 更新进度到95% (配置网络监控)
			s.updateTaskProgress(taskID, 95, "正在配置网络监控...")

			// 4. pmacct监控已在初始化时完成配置，无需额外步骤
			if !pmacctInitSuccess {
				if trafficEnabled {
					global.APP_LOG.Debug("跳过流量监控（pmacct初始化失败）",
						zap.Uint("instanceId", instanceID))
				} else {
					global.APP_LOG.Debug("跳过流量监控（Provider未启用流量统计）",
						zap.Uint("instanceId", instanceID),
						zap.Uint("providerId", providerID))
				}
			}

			// 4.5 Agent监控：仅在 agent 监控模式下注册
			var monConfig monitoringModel.MonitoringConfig
			useAgent := true // 默认使用 agent 模式
			if err := global.APP_DB.Where("provider_id = ?", providerID).First(&monConfig).Error; err == nil {
				useAgent = monConfig.MonitoringMode != "pmacct"
			}
			if useAgent {
				agentCtx, agentCancel := context.WithTimeout(context.Background(), 2*time.Minute)
				agentLifecycle.OnInstanceCreated(agentCtx, global.APP_DB, instanceID)
				agentCancel()
			} else {
				global.APP_LOG.Debug("Provider使用pmacct监控模式，跳过Agent注册",
					zap.Uint("instanceId", instanceID),
					zap.Uint("providerId", providerID))
			}

			// 更新进度到98%
			s.updateTaskProgress(taskID, 98, "正在启动流量同步...")

			// 5. 触发流量同步（仅在pmacct初始化成功时执行）
			if pmacctInitSuccess {
				syncTrigger := traffic.NewSyncTriggerService()
				syncTrigger.TriggerInstanceTrafficSync(instanceID, "实例创建后初始同步")

				global.APP_LOG.Debug("实例流量同步已触发",
					zap.Uint("instanceId", instanceID))
			} else {
				if trafficEnabled {
					global.APP_LOG.Debug("跳过流量同步触发（pmacct初始化失败）",
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

			global.APP_LOG.Debug("实例创建后处理任务完成",
				zap.Uint("instanceId", instanceID),
				zap.Bool("passwordSetSuccess", passwordSetSuccess))
		}(instance.ID, instance.ProviderID, task.ID)
	}
	global.APP_LOG.Info("实例创建最终化完成", zap.Uint("taskId", task.ID))

	if apiError == nil {
		// 后台goroutine已接管，通知worker pool跳过CompleteTask
		return interfaces.ErrAsyncCompletion
	}
	return nil
}
