package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider/incus"
	"oneclickvirt/provider/lxd"
	"oneclickvirt/provider/proxmox"
	agentSvc "oneclickvirt/service/agent"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CreateSyncPortMappingsTask 创建同步端口映射任务（为每个Provider创建独立任务）
func (s *TaskService) CreateSyncPortMappingsTask(userID uint, req *adminModel.SyncPortMappingsTaskRequest, ownerAdminID uint) ([]*adminModel.Task, error) {
	if req == nil {
		return nil, fmt.Errorf("同步参数不能为空")
	}
	if len(req.IncludedPortIDs) == 0 {
		return nil, fmt.Errorf("必须先预览并选择至少一条端口映射")
	}

	// 获取需要同步的Provider列表
	if len(req.ProviderIDs) == 0 && len(req.IncludedPortIDs) > 0 {
		if err := global.APP_DB.Model(&providerModel.Port{}).
			Where("id IN ?", req.IncludedPortIDs).
			Distinct("provider_id").
			Pluck("provider_id", &req.ProviderIDs).Error; err != nil {
			return nil, fmt.Errorf("查询待同步端口所属Provider失败: %v", err)
		}
	}
	var providers []providerModel.Provider
	query := global.APP_DB.Where("status = ?", "active")
	if ownerAdminID > 0 {
		query = query.Where("owner_admin_id = ?", ownerAdminID)
	}
	if len(req.ProviderIDs) > 0 {
		query = query.Where("id IN ?", req.ProviderIDs)
	}
	if err := query.Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("查询Provider列表失败: %v", err)
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("Provider不存在")
	}

	// 为每个Provider创建一个任务
	tasks := make([]*adminModel.Task, 0, len(providers))
	for _, prov := range providers {
		// 序列化任务数据
		taskData, err := json.Marshal(req)
		if err != nil {
			global.APP_LOG.Error("序列化任务数据失败",
				zap.Uint("providerId", prov.ID),
				zap.Error(err))
			continue
		}

		// 获取默认超时时间（30分钟）
		timeoutDuration := utils.GetDefaultTaskTimeout("sync-port-mappings")

		// 创建任务，绑定到特定Provider
		task, err := s.CreateTask(userID, &prov.ID, nil, "sync-port-mappings", string(taskData), timeoutDuration)
		if err != nil {
			global.APP_LOG.Error("创建同步任务失败",
				zap.Uint("providerId", prov.ID),
				zap.String("providerName", prov.Name),
				zap.Error(err))
			continue
		}

		// 立即启动任务
		if err := s.StartTask(task.ID); err != nil {
			global.APP_LOG.Error("启动同步端口映射任务失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("providerId", prov.ID),
				zap.String("providerName", prov.Name),
				zap.Error(err))
			continue
		}

		global.APP_LOG.Info("创建并启动同步任务",
			zap.Uint("taskId", task.ID),
			zap.Uint("providerId", prov.ID),
			zap.String("providerName", prov.Name))

		tasks = append(tasks, task)
	}

	if len(tasks) == 0 {
		return nil, fmt.Errorf("所有Provider的任务创建都失败了")
	}

	return tasks, nil
}

// executeCreatePortMappingTask 执行创建端口映射任务
func (s *TaskService) executeCreatePortMappingTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度 (5%)
	s.updateTaskProgress(task.ID, 5, "step.parseTaskData")

	// 解析任务数据
	var taskReq adminModel.CreatePortMappingTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度 (12%)
	s.updateTaskProgress(task.ID, 12, "step.getPortMappingInfo")

	// 获取端口映射记录
	var port providerModel.Port
	if err := global.APP_DB.First(&port, taskReq.PortID).Error; err != nil {
		return fmt.Errorf("端口映射记录不存在")
	}

	// 更新进度 (20%)
	s.updateTaskProgress(task.ID, 20, "step.getInstanceInfo")

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, taskReq.InstanceID).Error; err != nil {
		// 更新端口状态为失败
		global.APP_DB.Model(&port).Update("status", "failed")
		return fmt.Errorf("实例不存在")
	}

	// 更新进度 (28%)
	s.updateTaskProgress(task.ID, 28, "step.getProviderConfig")

	// 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, taskReq.ProviderID).Error; err != nil {
		// 更新端口状态为失败
		global.APP_DB.Model(&port).Update("status", "failed")
		return fmt.Errorf("Provider不存在")
	}

	// 复制副本避免共享状态，立即创建Provider字段的本地副本
	localProviderID := providerInfo.ID
	localProviderType := providerInfo.Type
	localIPv4PortMappingMethod := providerInfo.IPv4PortMappingMethod

	// 更新进度 (35%)
	s.updateTaskProgress(task.ID, 35, "step.getInstancePrivateIP")

	// 获取实例最新的内网IP地址
	var currentPrivateIP string
	providerApiService := &provider2.ProviderApiService{}
	prov, _, err := providerApiService.GetProviderByID(localProviderID)
	if err != nil {
		global.APP_LOG.Error("获取Provider实例失败",
			zap.Uint("providerId", localProviderID),
			zap.Error(err))
		// 更新端口状态为失败
		global.APP_DB.Model(&port).Update("status", "failed")
		return fmt.Errorf("获取Provider实例失败: %v", err)
	}

	// 根据不同的Provider类型获取内网IP
	switch localProviderType {
	case "lxd":
		if lxdProv, ok := prov.(*lxd.LXDProvider); ok {
			if ip, err := lxdProv.GetInstanceIPv4(ctx, instance.Name); err == nil {
				currentPrivateIP = ip
				global.APP_LOG.Debug("成功获取LXD实例最新内网IP",
					zap.String("instanceName", instance.Name),
					zap.String("privateIP", currentPrivateIP))
			} else {
				global.APP_LOG.Warn("获取LXD实例内网IP失败，使用数据库中的IP",
					zap.String("instanceName", instance.Name),
					zap.String("dbPrivateIP", instance.PrivateIP),
					zap.Error(err))
				currentPrivateIP = instance.PrivateIP
			}
		}
	case "incus":
		if incusProv, ok := prov.(*incus.IncusProvider); ok {
			if ip, err := incusProv.GetInstanceIPv4(ctx, instance.Name); err == nil {
				currentPrivateIP = ip
				global.APP_LOG.Debug("成功获取Incus实例最新内网IP",
					zap.String("instanceName", instance.Name),
					zap.String("privateIP", currentPrivateIP))
			} else {
				global.APP_LOG.Warn("获取Incus实例内网IP失败，使用数据库中的IP",
					zap.String("instanceName", instance.Name),
					zap.String("dbPrivateIP", instance.PrivateIP),
					zap.Error(err))
				currentPrivateIP = instance.PrivateIP
			}
		}
	case "proxmox":
		if proxmoxProv, ok := prov.(*proxmox.ProxmoxProvider); ok {
			if ip, err := proxmoxProv.GetInstanceIPv4(ctx, instance.Name); err == nil {
				currentPrivateIP = ip
				global.APP_LOG.Debug("成功获取Proxmox实例最新内网IP",
					zap.String("instanceName", instance.Name),
					zap.String("privateIP", currentPrivateIP))
			} else {
				global.APP_LOG.Warn("获取Proxmox实例内网IP失败，使用数据库中的IP",
					zap.String("instanceName", instance.Name),
					zap.String("dbPrivateIP", instance.PrivateIP),
					zap.Error(err))
				currentPrivateIP = instance.PrivateIP
			}
		}
	case "docker", "orbstack":
		// Docker/Orbstack通常不需要内网IP映射
		currentPrivateIP = instance.PrivateIP
	default:
		currentPrivateIP = instance.PrivateIP
	}

	// 如果获取到新的内网IP且与数据库不一致，更新数据库
	if currentPrivateIP != "" && currentPrivateIP != instance.PrivateIP {
		if err := global.APP_DB.Model(&instance).Update("private_ip", currentPrivateIP).Error; err != nil {
			global.APP_LOG.Error("更新实例内网IP到数据库失败",
				zap.Uint("instanceId", instance.ID),
				zap.String("oldPrivateIP", instance.PrivateIP),
				zap.String("newPrivateIP", currentPrivateIP),
				zap.Error(err))
		} else {
			global.APP_LOG.Debug("实例内网IP已更新到数据库",
				zap.Uint("instanceId", instance.ID),
				zap.String("oldPrivateIP", instance.PrivateIP),
				zap.String("newPrivateIP", currentPrivateIP))
			instance.PrivateIP = currentPrivateIP
		}
	}

	// 更新进度 (50%)
	s.updateTaskProgress(task.ID, 50, "step.configuringPortMapping")

	// controller 模式（Agent 隧道转发）：跳过节点侧规则（device_proxy / iptables 等），
	// 直接启动控制端监听并提前返回。
	// 非 agent 模式的 provider 不会产生 controller 类型的端口，因此该分支不会误触。
	if port.MappingType == "controller" {
		s.updateTaskProgress(task.ID, 70, "step.startingPortForward")

		targetHost, shouldPersist := agentSvc.ResolveControllerPortTarget(port.InternalHost, currentPrivateIP)
		if targetHost == "" {
			targetHost, shouldPersist = agentSvc.ResolveControllerPortTarget(port.InternalHost, instance.PrivateIP)
		}
		if targetHost == "" {
			global.APP_DB.Model(&port).Update("status", "failed")
			return fmt.Errorf("无法获取实例内网IP，无法启动控制端端口转发")
		}

		if shouldPersist {
			if err := global.APP_DB.Model(&port).Update("internal_host", targetHost).Error; err != nil {
				global.APP_LOG.Warn("更新控制端端口转发目标地址失败",
					zap.Uint("portId", port.ID),
					zap.String("targetHost", targetHost),
					zap.Error(err))
			}
			port.InternalHost = targetHost
		}

		if err := agentSvc.StartControllerPortForward(port.ID, localProviderID, port.HostPort, targetHost, port.GuestPort); err != nil {
			global.APP_LOG.Error("启动控制端端口转发失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("portId", port.ID),
				zap.Int("hostPort", port.HostPort),
				zap.Int("guestPort", port.GuestPort),
				zap.String("targetHost", targetHost),
				zap.Error(err))
			global.APP_DB.Model(&port).Update("status", "failed")
			return fmt.Errorf("启动控制端端口转发失败: %v", err)
		}

		global.APP_LOG.Info("控制端端口转发已启动",
			zap.Uint("portId", port.ID),
			zap.Int("hostPort", port.HostPort),
			zap.Int("guestPort", port.GuestPort),
			zap.String("targetHost", targetHost))

		// 更新端口状态为active
		if err := global.APP_DB.Model(&port).Updates(map[string]interface{}{
			"status":         "active",
			"mapping_method": "controller",
		}).Error; err != nil {
			global.APP_LOG.Error("更新端口状态失败", zap.Error(err))
			return fmt.Errorf("更新端口状态失败: %v", err)
		}

		stateManager := GetTaskStateManager()
		taskResult := map[string]interface{}{
			"portId":    port.ID,
			"hostPort":  port.HostPort,
			"guestPort": port.GuestPort,
			"protocol":  port.Protocol,
		}
		if err := stateManager.CompleteMainTask(task.ID, true, "端口映射创建成功（控制端转发）", taskResult); err != nil {
			global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
		}

		global.APP_LOG.Info("端口映射创建成功（控制端转发）",
			zap.Uint("taskId", task.ID),
			zap.Uint("portId", port.ID),
			zap.Int("hostPort", port.HostPort),
			zap.Int("guestPort", port.GuestPort))

		return nil
	}

	// 非 controller 模式：直接把现有数据库记录应用到节点，避免 Provider
	// 适配器额外插入一条临时端口记录。该入口也会完整展开端口段。
	s.updateTaskProgress(task.ID, 70, "step.configuringRemotePortMapping")
	applier := newPortMappingApplier(ctx, prov, &providerInfo)
	if err := applier.Apply(&instance, &port, false); err != nil {
		global.APP_LOG.Error("添加端口映射失败",
			zap.Uint("taskId", task.ID),
			zap.Uint("portId", port.ID),
			zap.Int("hostPort", port.HostPort),
			zap.Int("guestPort", port.GuestPort),
			zap.Error(err))

		// 更新端口状态为失败
		global.APP_DB.Model(&port).Update("status", "failed")

		return fmt.Errorf("添加端口映射失败: %v", err)
	}
	applier.Finish()
	s.updateTaskProgress(task.ID, 85, "step.applyingRemotePortMapping")

	// 更新进度 (92%)
	s.updateTaskProgress(task.ID, 92, "step.updatingPortStatus")

	// 更新端口状态为active
	mappingMethod := port.MappingMethod
	if mappingMethod == "" {
		mappingMethod = localIPv4PortMappingMethod
	}
	if err := global.APP_DB.Model(&port).Updates(map[string]interface{}{
		"status":         "active",
		"mapping_method": mappingMethod,
	}).Error; err != nil {
		global.APP_LOG.Error("更新端口状态失败", zap.Error(err))
		return fmt.Errorf("更新端口状态失败: %v", err)
	}

	// 标记任务完成
	stateManager := GetTaskStateManager()
	taskResult := map[string]interface{}{
		"portId":    port.ID,
		"hostPort":  port.HostPort,
		"guestPort": port.GuestPort,
		"protocol":  port.Protocol,
	}
	if err := stateManager.CompleteMainTask(task.ID, true, "端口映射创建成功", taskResult); err != nil {
		global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	global.APP_LOG.Info("端口映射创建成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("portId", port.ID),
		zap.Int("hostPort", port.HostPort),
		zap.Int("guestPort", port.GuestPort))

	return nil
}

// executeDeletePortMappingTask 执行删除端口映射任务
func (s *TaskService) executeDeletePortMappingTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度 (5%)
	s.updateTaskProgress(task.ID, 5, "step.parseTaskData")

	// 解析任务数据
	var taskReq adminModel.DeletePortMappingTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度 (15%)
	s.updateTaskProgress(task.ID, 15, "step.getPortMappingInfo")

	// 获取端口映射记录
	var port providerModel.Port
	if err := global.APP_DB.First(&port, taskReq.PortID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 端口已不存在，标记任务完成
			stateManager := GetTaskStateManager()
			if err := stateManager.CompleteMainTask(task.ID, true, "端口映射已不存在，删除任务完成", nil); err != nil {
				global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
			}
			return nil
		}
		return fmt.Errorf("获取端口映射记录失败: %v", err)
	}

	// 更新进度 (25%)
	s.updateTaskProgress(task.ID, 25, "step.getInstanceInfo")

	// 获取实例信息（可能实例已被删除）
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, port.InstanceID).Error; err != nil {
		global.APP_LOG.Warn("实例不存在，继续删除端口映射记录",
			zap.Uint("instanceId", port.InstanceID),
			zap.Error(err))
		instance.Name = "" // 清空实例名称
	}

	// 更新进度 (35%)
	s.updateTaskProgress(task.ID, 35, "step.getProviderConfig")

	// 控制端转发模式：直接停止 TCP 监听，跳过节点侧删除
	if port.MappingType == "controller" {
		agentSvc.StopControllerPortForward(port.ID)
		if err := global.APP_DB.Unscoped().Delete(&port).Error; err != nil {
			return fmt.Errorf("删除端口映射记录失败: %v", err)
		}
		stateManager := GetTaskStateManager()
		if err := stateManager.CompleteMainTask(task.ID, true, "控制端端口转发已停止并删除", nil); err != nil {
			global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
		}
		return nil
	}

	// 获取Provider信息
	var providerInfo providerModel.Provider
	providerDeleteSuccess := true
	if err := global.APP_DB.First(&providerInfo, port.ProviderID).Error; err != nil {
		global.APP_LOG.Warn("Provider不存在，仅删除端口映射数据库记录",
			zap.Uint("providerId", port.ProviderID),
			zap.Error(err))
		providerDeleteSuccess = false
	} else {
		// 只有Provider存在时才尝试从远程删除 (50%)
		s.updateTaskProgress(task.ID, 50, "step.deletingPortMappingInfo")
		providerApiService := &provider2.ProviderApiService{}
		prov, _, err := providerApiService.GetProviderByID(providerInfo.ID)
		if err != nil {
			global.APP_LOG.Warn("获取Provider实例失败，跳过远程删除",
				zap.Uint("providerId", providerInfo.ID),
				zap.Error(err))
			providerDeleteSuccess = false
		} else {
			applier := newPortMappingApplier(ctx, prov, &providerInfo)
			if deleteErr := applier.Remove(&instance, &port); deleteErr != nil {
				global.APP_LOG.Warn("从远程服务器删除端口映射失败",
					zap.Uint("portId", port.ID),
					zap.String("providerType", providerInfo.Type),
					zap.Error(deleteErr))
				providerDeleteSuccess = false
			}
			applier.Finish()
		}
	}

	// 更新进度 (85%)
	s.updateTaskProgress(task.ID, 85, "step.cleaningDatabaseRecords")

	// 删除数据库记录
	if err := global.APP_DB.Unscoped().Delete(&port).Error; err != nil {
		return fmt.Errorf("删除端口映射记录失败: %v", err)
	}

	// 标记任务完成
	completionMessage := "端口映射删除成功"
	if !providerDeleteSuccess {
		completionMessage = "端口映射删除完成，远程删除可能失败但数据已清理"
	}
	stateManager := GetTaskStateManager()
	if err := stateManager.CompleteMainTask(task.ID, true, completionMessage, nil); err != nil {
		global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	global.APP_LOG.Info("端口映射删除成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("portId", port.ID),
		zap.Int("hostPort", port.HostPort),
		zap.Bool("providerDeleteSuccess", providerDeleteSuccess))

	return nil
}
