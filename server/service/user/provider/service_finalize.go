package provider

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/provider/incus"
	"oneclickvirt/provider/lxd"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/traffic"
	"oneclickvirt/utils"

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
			case "qemu", "vmware", "virtualbox", "multipass", "vagrant":
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

	cancelledDuringFinalize := false

	// 在事务中仅执行DB写入操作（短事务）
	err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		var taskStatus string
		if fetchErr := tx.Model(&adminModel.Task{}).Select("status").Where("id = ?", task.ID).Scan(&taskStatus).Error; fetchErr == nil && taskStatus == "cancelled" {
			global.APP_LOG.Debug("实例创建任务已被取消，跳过最终化并安排实例清理",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID))
			cancelledDuringFinalize = true
			go s.delayedDeleteFailedInstance(instance.ID)
			return nil
		}

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

			if task.UserID > 0 {
				quotaService := resources.NewQuotaService()
				resourceUsage := resources.ResourceUsage{
					CPU:       instance.CPU,
					Memory:    instance.Memory,
					Disk:      instance.Disk,
					Bandwidth: instance.Bandwidth,
				}
				if err := quotaService.ReleasePendingQuota(tx, task.UserID, resourceUsage); err != nil {
					global.APP_LOG.Warn("释放失败实例待确认配额失败",
						zap.Uint("taskId", task.ID),
						zap.Uint("instanceId", instance.ID),
						zap.Uint("userId", task.UserID),
						zap.Error(err))
				}
			}

			// 资源预留已在创建时被原子化消费，无需额外释放
			if err := tx.Model(&providerModel.ProviderIPv4Pool{}).
				Where("instance_id = ?", instance.ID).
				Updates(map[string]interface{}{"is_allocated": false, "instance_id": nil}).Error; err != nil {
				global.APP_LOG.Warn("释放失败实例IPv4池地址失败",
					zap.Uint("instanceId", instance.ID),
					zap.Error(err))
			}

			// 更新任务状态为失败；若管理员已强制取消，保留取消终态。
			if err := tx.Model(&adminModel.Task{}).
				Where("id = ? AND status NOT IN ?", task.ID, []string{"completed", "failed", "cancelled", "timeout"}).
				Updates(map[string]interface{}{
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
		// 实例创建成功，将待确认配额转为已使用配额。管理端直连创建可没有用户归属，跳过用户配额确认。
		if task.UserID > 0 {
			if err := quotaService.ConfirmPendingQuota(tx, task.UserID, resourceUsage); err != nil {
				global.APP_LOG.Error("确认用户配额失败",
					zap.Uint("taskId", task.ID),
					zap.Uint("userId", task.UserID),
					zap.Error(err))
				return fmt.Errorf("确认用户配额失败: %v", err)
			}
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

	if cancelledDuringFinalize {
		s.taskService.ReleaseTaskLocks(task.ID)
		return nil
	}

	// 如果任务在事务中已标记为失败，需要释放锁
	if apiError != nil {
		if global.APP_TASK_LOCK_RELEASER != nil {
			global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(task.ID)
		}
	}

	// 如果Provider创建实例成功，执行后处理任务（同步完成关键任务后再标记完成）
	if apiError == nil {
		go func(taskCtx context.Context, instanceID uint, providerID uint, taskID uint) {
			defer func() {
				s.taskService.ReleaseTaskLocks(taskID)
			}()
			defer func() {
				if r := recover(); r != nil {
					global.APP_LOG.Error("实例创建后处理任务发生panic",
						zap.Uint("instanceId", instanceID),
						zap.Any("panic", r))
					panicErr := fmt.Errorf("实例创建后处理任务发生panic: %v", r)
					utils.AppendTaskError(taskID, 95, "step.createPostProcessFailed", panicErr)
					_ = global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Update("status", "error").Error
					// 后处理包含密码设置/网络信息等关键步骤；发生 panic 时不能标记 completed。
					stateManager := s.taskService.GetStateManager()
					if stateManager != nil {
						if err := stateManager.CompleteMainTask(taskID, false, panicErr.Error(), nil); err != nil {
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

			// 长耗时等待阶段单独占用 70-82%，避免 VM cloud-init/SSH 等待时进度条长期卡在小区间。
			s.updateTaskProgress(taskID, 70, "step.waitingSSHReady")
			// 根据Provider类型和实例类型确定SSH等待时长。
			sshWaitTimeout := 30 * time.Second // 容器默认30s
			var dbProviderForWait providerModel.Provider
			var instanceForWait providerModel.Instance
			_ = global.APP_DB.Select("instance_type").Where("id = ?", instanceID).First(&instanceForWait).Error
			if err := global.APP_DB.Select("type, pve_kvm_available").Where("id = ?", providerID).First(&dbProviderForWait).Error; err == nil {
				sshWaitTimeout = providerCreateSSHWaitTimeout(dbProviderForWait, instanceForWait)
			}

			// 智能等待实例SSH服务就绪，传入taskID以便更新进度。
			// 这里不直接判失败，因为 LXD/Incus 等容器可能不暴露外部 SSH，后续仍可通过 lxc/incus exec 设置密码；
			// 但必须把等待失败原因写入任务详情，避免排查时只看到进度停留。
			if err := s.waitForInstanceSSHReadyInRange(taskCtx, instanceID, providerID, taskID, sshWaitTimeout, 70, 82); err != nil {
				utils.AppendTaskError(taskID, 82, "step.waitingSSHReadyFailed", err)
				global.APP_LOG.Warn("等待实例SSH就绪超时",
					zap.Uint("instanceId", instanceID),
					zap.Error(err))
			}

			// 即使 SSH 检测可跳过，也必须确认 Provider 侧实例没有停在 STOPPED/FROZEN/ERROR。
			// 否则后续密码设置会失败，但任务可能被误判为完成。
			if err := s.ensureInstanceRunnableAfterCreate(taskCtx, instanceID, providerID, taskID, 83); err != nil {
				finalErr := fmt.Errorf("实例创建后状态检查失败: %w", err)
				_ = global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Update("status", "error").Error
				stateManager := s.taskService.GetStateManager()
				if stateManager != nil {
					if err := stateManager.CompleteMainTask(taskID, false, finalErr.Error(), nil); err != nil {
						global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", taskID), zap.Error(err))
					}
				}
				return
			}

			if err := s.ensureInstanceNetworkAddresses(taskCtx, instanceID, providerID); err != nil {
				global.APP_LOG.Warn("实例网络地址补齐失败",
					zap.Uint("instanceId", instanceID),
					zap.Error(err))
			}

			// 更新进度到84% (配置端口映射)
			s.updateTaskProgress(taskID, 84, "step.configuringPortMappings")
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

			s.updateTaskProgress(taskID, 87, "step.verifyingMonitorStatus")
			monitoringStatus := s.ensurePostCreateMonitoring(taskCtx, instanceID, providerID, "实例创建")

			// 更新进度到90% (设置SSH密码)
			s.updateTaskProgress(taskID, 90, "step.settingSSHPassword")
			// 3. 设置实例SSH密码（关键步骤）：创建任务不能在密码不可用时标记 completed。
			var currentInstance providerModel.Instance
			passwordSetSuccess := false
			var passwordSetErr error
			if err := global.APP_DB.Where("id = ?", instanceID).First(&currentInstance).Error; err != nil {
				passwordSetErr = fmt.Errorf("获取实例信息失败，无法设置SSH密码: %w", err)
				global.APP_LOG.Error("获取实例信息失败，无法设置SSH密码",
					zap.Uint("instanceId", instanceID),
					zap.Error(err))
			} else if currentInstance.Password == "" {
				passwordSetErr = fmt.Errorf("实例 %s 数据库中没有预生成密码，禁止标记创建完成", currentInstance.Name)
				global.APP_LOG.Error("实例缺少预生成密码，无法完成创建任务",
					zap.Uint("instanceId", instanceID),
					zap.String("instanceName", currentInstance.Name))
			} else {
				// 设置实例SSH密码，最多重试2次（总共2次尝试）
				providerSvc := providerService.GetProviderService()
				maxRetries := 2
				for i := 0; i < maxRetries; i++ {
					// 单次密码设置操作（echo/cloud-init注入），60s超时已足够
					ctxWithTimeout, cancel := context.WithTimeout(taskCtx, 60*time.Second)
					err := providerSvc.SetInstancePassword(ctxWithTimeout, currentInstance.ProviderID, currentInstance.Name, currentInstance.Password)
					cancel() // 立即释放context资源
					if err != nil {
						passwordSetErr = err
						utils.AppendTaskError(taskID, 90, fmt.Sprintf("step.settingPasswordRetry:%d", i+1), err)
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

				// 密码设置成功后，验证SSH密码认证是否真正可用；验证失败只记录，不覆盖 lxc/incus exec 已成功写入的结果。
				if passwordSetSuccess {
					s.updateTaskProgress(taskID, 93, "step.verifyingSSHPassword")
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

			if !passwordSetSuccess {
				if passwordSetErr == nil {
					passwordSetErr = fmt.Errorf("实例SSH密码未设置成功")
				}
				finalErr := fmt.Errorf("实例创建后处理失败，SSH密码不可用: %w", passwordSetErr)
				utils.AppendTaskError(taskID, 93, "step.createPostProcessFailed", finalErr)
				global.APP_LOG.Error("实例创建后处理失败，任务标记为失败",
					zap.Uint("taskId", taskID),
					zap.Uint("instanceId", instanceID),
					zap.String("instanceName", currentInstance.Name),
					zap.Error(finalErr))
				_ = global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Update("status", "error").Error

				stateManager := s.taskService.GetStateManager()
				if stateManager != nil {
					if err := stateManager.CompleteMainTask(taskID, false, finalErr.Error(), nil); err != nil {
						global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", taskID), zap.Error(err))
					}
				} else {
					global.APP_LOG.Error("状态管理器未初始化", zap.Uint("taskId", taskID))
				}
				return
			}

			s.updateTaskProgress(taskID, 95, "step.configuringNetworkMonitor")
			if monitoringStatus.TrafficEnabled && !monitoringStatus.PmacctReady && !monitoringStatus.AgentMonitorReady {
				global.APP_LOG.Debug("创建后未获得可用监控记录，后续调度或人工同步仍可补齐",
					zap.Uint("instanceId", instanceID),
					zap.Uint("providerId", providerID),
					zap.String("trafficMethod", monitoringStatus.TrafficMethod))
			}

			s.updateTaskProgress(taskID, 98, "step.startingTrafficSync")
			if monitoringStatus.PmacctReady {
				syncTrigger := traffic.NewSyncTriggerService()
				syncTrigger.TriggerInstanceTrafficSync(instanceID, "实例创建后初始同步")

				global.APP_LOG.Debug("实例流量同步已触发",
					zap.Uint("instanceId", instanceID))
			} else if monitoringStatus.TrafficEnabled {
				global.APP_LOG.Debug("跳过pmacct流量同步触发",
					zap.Uint("instanceId", instanceID),
					zap.Uint("providerId", providerID),
					zap.String("trafficMethod", monitoringStatus.TrafficMethod))
			}

			// 最终完成状态判断：走到这里说明实例后处理关键步骤均成功。
			completionMessage := "step.createCompleted"

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
		}(ctx, instance.ID, instance.ProviderID, task.ID)
	}
	global.APP_LOG.Info("实例创建最终化完成", zap.Uint("taskId", task.ID))

	if apiError == nil {
		// 后台goroutine已接管，通知worker pool跳过CompleteTask
		return interfaces.ErrAsyncCompletion
	}
	return nil
}
