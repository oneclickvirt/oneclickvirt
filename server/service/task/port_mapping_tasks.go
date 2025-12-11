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
	"oneclickvirt/provider/portmapping"
	"oneclickvirt/provider/proxmox"
	provider2 "oneclickvirt/service/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// executeCreatePortMappingTask 执行创建端口映射任务
func (s *TaskService) executeCreatePortMappingTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度 (5%)
	s.updateTaskProgress(task.ID, 5, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.CreatePortMappingTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度 (12%)
	s.updateTaskProgress(task.ID, 12, "正在获取端口映射信息...")

	// 获取端口映射记录
	var port providerModel.Port
	if err := global.APP_DB.First(&port, taskReq.PortID).Error; err != nil {
		return fmt.Errorf("端口映射记录不存在")
	}

	// 更新进度 (20%)
	s.updateTaskProgress(task.ID, 20, "正在获取实例信息...")

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, taskReq.InstanceID).Error; err != nil {
		// 更新端口状态为失败
		global.APP_DB.Model(&port).Update("status", "failed")
		return fmt.Errorf("实例不存在")
	}

	// 更新进度 (28%)
	s.updateTaskProgress(task.ID, 28, "正在获取Provider配置...")

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
	s.updateTaskProgress(task.ID, 35, "正在获取实例最新内网IP地址...")

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
				global.APP_LOG.Info("成功获取LXD实例最新内网IP",
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
				global.APP_LOG.Info("成功获取Incus实例最新内网IP",
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
				global.APP_LOG.Info("成功获取Proxmox实例最新内网IP",
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
	case "docker":
		// Docker通常不需要内网IP映射
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
			global.APP_LOG.Info("实例内网IP已更新到数据库",
				zap.Uint("instanceId", instance.ID),
				zap.String("oldPrivateIP", instance.PrivateIP),
				zap.String("newPrivateIP", currentPrivateIP))
			instance.PrivateIP = currentPrivateIP
		}
	}

	// 更新进度 (50%)
	s.updateTaskProgress(task.ID, 50, "正在配置端口映射...")

	// 使用 portmapping manager 添加端口映射
	manager := portmapping.NewManager(&portmapping.ManagerConfig{
		DefaultMappingMethod: localIPv4PortMappingMethod,
	})

	// 确定使用的 portmapping provider 类型
	portMappingType := localProviderType
	if portMappingType == "proxmox" {
		portMappingType = "iptables"
	}

	portReq := &portmapping.PortMappingRequest{
		InstanceID:    fmt.Sprintf("%d", instance.ID),
		ProviderID:    localProviderID,
		Protocol:      port.Protocol,
		HostPort:      port.HostPort,
		GuestPort:     port.GuestPort,
		Description:   port.Description,
		MappingMethod: localIPv4PortMappingMethod,
	}

	// 执行端口映射添加 (70%)
	s.updateTaskProgress(task.ID, 70, "正在远程服务器上配置端口映射...")

	result, err := manager.CreatePortMapping(ctx, portMappingType, portReq)
	if err != nil {
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

	// Provider 会创建一条新的数据库记录，我们需要删除它并更新我们原有的记录
	if result.ID != 0 && result.ID != port.ID {
		// 删除 provider 创建的重复记录
		global.APP_DB.Delete(&providerModel.Port{}, result.ID)
		global.APP_LOG.Info("删除 provider 创建的重复端口记录",
			zap.Uint("duplicatePortId", result.ID),
			zap.Uint("originalPortId", port.ID))
	}

	// 对于 LXD/Incus/Proxmox，还需要在远程服务器上实际创建端口映射 (85%)
	if localProviderType == "lxd" || localProviderType == "incus" || localProviderType == "proxmox" {
		s.updateTaskProgress(task.ID, 85, "正在应用端口映射到远程服务器...")

		// 调用 provider 层的方法在远程服务器上创建实际映射（使用最新获取的内网IP）
		switch localProviderType {
		case "lxd":
			lxdProv, ok := prov.(*lxd.LXDProvider)
			if !ok {
				return fmt.Errorf("Provider类型断言失败")
			}
			// 调用内部方法创建端口映射，使用最新的内网IP
			err = lxdProv.SetupPortMappingWithIP(ctx, instance.Name, port.HostPort, port.GuestPort, port.Protocol, localIPv4PortMappingMethod, currentPrivateIP)

		case "incus":
			incusProv, ok := prov.(*incus.IncusProvider)
			if !ok {
				return fmt.Errorf("Provider类型断言失败")
			}
			// 调用内部方法创建端口映射，使用最新的内网IP
			err = incusProv.SetupPortMappingWithIP(ctx, instance.Name, port.HostPort, port.GuestPort, port.Protocol, localIPv4PortMappingMethod, currentPrivateIP)

		case "proxmox":
			proxmoxProv, ok := prov.(*proxmox.ProxmoxProvider)
			if !ok {
				return fmt.Errorf("Provider类型断言失败")
			}
			// 调用内部方法创建端口映射，使用最新的内网IP
			err = proxmoxProv.SetupPortMappingWithIP(ctx, instance.Name, port.HostPort, port.GuestPort, port.Protocol, localIPv4PortMappingMethod, currentPrivateIP)
		}

		if err != nil {
			global.APP_LOG.Error("在远程服务器上创建端口映射失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("portId", port.ID),
				zap.Error(err))
			// 更新端口状态为失败
			global.APP_DB.Model(&port).Update("status", "failed")
			return fmt.Errorf("在远程服务器上创建端口映射失败: %v", err)
		}

		global.APP_LOG.Info("已在远程服务器上应用端口映射",
			zap.Uint("portId", port.ID),
			zap.String("providerType", localProviderType))
	}

	// 更新进度 (92%)
	s.updateTaskProgress(task.ID, 92, "正在更新端口状态...")

	// 更新端口状态为active
	if err := global.APP_DB.Model(&port).Updates(map[string]interface{}{
		"status":         "active",
		"mapping_method": result.MappingMethod,
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
	s.updateTaskProgress(task.ID, 5, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.DeletePortMappingTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度 (15%)
	s.updateTaskProgress(task.ID, 15, "正在获取端口映射信息...")

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
	s.updateTaskProgress(task.ID, 25, "正在获取实例信息...")

	// 获取实例信息（可能实例已被删除）
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, port.InstanceID).Error; err != nil {
		global.APP_LOG.Warn("实例不存在，继续删除端口映射记录",
			zap.Uint("instanceId", port.InstanceID),
			zap.Error(err))
		instance.Name = "" // 清空实例名称
	}

	// 更新进度 (35%)
	s.updateTaskProgress(task.ID, 35, "正在获取Provider配置...")

	// 获取Provider信息
	var providerInfo providerModel.Provider
	providerDeleteSuccess := true
	if err := global.APP_DB.First(&providerInfo, port.ProviderID).Error; err != nil {
		global.APP_LOG.Warn("Provider不存在，仅删除端口映射数据库记录",
			zap.Uint("providerId", port.ProviderID),
			zap.Error(err))
		providerDeleteSuccess = false
	} else {
		// 复制副本避免共享状态，立即创建Provider字段的本地副本
		localProviderID := providerInfo.ID
		localProviderType := providerInfo.Type
		localIPv4PortMappingMethod := providerInfo.IPv4PortMappingMethod

		// 只有Provider存在时才尝试从远程删除 (50%)
		s.updateTaskProgress(task.ID, 50, "正在从远程服务器删除端口映射...")

		// 使用 portmapping manager 删除端口映射
		manager := portmapping.NewManager(&portmapping.ManagerConfig{
			DefaultMappingMethod: localIPv4PortMappingMethod,
		})

		portMappingType := localProviderType
		if portMappingType == "proxmox" {
			portMappingType = "iptables"
		}

		deleteReq := &portmapping.DeletePortMappingRequest{
			ID:         port.ID,
			InstanceID: fmt.Sprintf("%d", instance.ID),
		}

		if err := manager.DeletePortMapping(ctx, portMappingType, deleteReq); err != nil {
			global.APP_LOG.Warn("从portmapping manager删除端口映射失败",
				zap.Uint("portId", port.ID),
				zap.Int("hostPort", port.HostPort),
				zap.Error(err))
			providerDeleteSuccess = false
			// 继续执行，不阻止数据库记录删除
		}

		// 对于 LXD/Incus，还需要在远程服务器上实际删除 proxy device (70%)
		if (localProviderType == "lxd" || localProviderType == "incus") && instance.Name != "" {
			s.updateTaskProgress(task.ID, 70, "正在从LXD/Incus服务器删除端口映射...")

			// 获取 Provider 实例
			providerApiService := &provider2.ProviderApiService{}
			prov, _, err := providerApiService.GetProviderByID(localProviderID)
			if err != nil {
				global.APP_LOG.Warn("获取Provider实例失败，跳过远程删除",
					zap.Uint("providerId", localProviderID),
					zap.Error(err))
				providerDeleteSuccess = false
			} else {
				// 调用 provider 层的方法在远程服务器上删除实际映射
				var deleteErr error
				switch localProviderType {
				case "lxd":
					if lxdProv, ok := prov.(*lxd.LXDProvider); ok {
						deleteErr = lxdProv.RemovePortMapping(instance.Name, port.HostPort, port.Protocol, localIPv4PortMappingMethod)
					} else {
						deleteErr = fmt.Errorf("Provider类型断言失败")
					}

				case "incus":
					if incusProv, ok := prov.(*incus.IncusProvider); ok {
						deleteErr = incusProv.RemovePortMapping(instance.Name, port.HostPort, port.Protocol, localIPv4PortMappingMethod)
					} else {
						deleteErr = fmt.Errorf("Provider类型断言失败")
					}
				}

				if deleteErr != nil {
					global.APP_LOG.Warn("从远程服务器删除端口映射失败",
						zap.Uint("portId", port.ID),
						zap.String("providerType", localProviderType),
						zap.Error(deleteErr))
					providerDeleteSuccess = false
				} else {
					global.APP_LOG.Info("已从远程服务器删除端口映射",
						zap.Uint("portId", port.ID),
						zap.String("providerType", localProviderType))
				}
			}
		}
	}

	// 更新进度 (85%)
	s.updateTaskProgress(task.ID, 85, "正在删除数据库记录...")

	// 删除数据库记录
	if err := global.APP_DB.Delete(&port).Error; err != nil {
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
