package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	"oneclickvirt/provider/incus"
	"oneclickvirt/provider/lxd"
	"oneclickvirt/provider/portmapping"
	"oneclickvirt/provider/proxmox"
	traffic_monitor "oneclickvirt/service/admin/traffic_monitor"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ResetTaskContext 重置任务上下文
type ResetTaskContext struct {
	Instance        providerModel.Instance
	Provider        providerModel.Provider
	SystemImage     systemModel.SystemImage
	OldPortMappings []providerModel.Port
	OldInstanceID   uint
	OldInstanceName string
	NewInstanceID   uint
	NewOldName      string
	NewPassword     string
	NewPrivateIP    string
}

// executeResetTask 执行实例重置任务
func (s *TaskService) executeResetTask(ctx context.Context, task *adminModel.Task) error {
	// 解析任务数据
	var taskReq adminModel.InstanceOperationTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	var resetCtx ResetTaskContext

	// 阶段1: 准备阶段
	if err := s.resetTask_Prepare(ctx, task, &taskReq, &resetCtx); err != nil {
		return err
	}

	// 阶段2: 数据库操作 - 重命名旧实例并创建新实例记录（短事务）
	if err := s.resetTask_RenameAndCreateNew(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段3: Provider操作 - 删除旧实例（无事务）
	if err := s.resetTask_DeleteOldInstance(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段4: Provider操作 - 创建新实例（无事务）
	if err := s.resetTask_CreateNewInstance(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段5: 设置密码（无事务）
	if err := s.resetTask_SetPassword(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段6: 更新实例信息（短事务）
	if err := s.resetTask_UpdateInstanceInfo(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段7: 恢复端口映射（批量短事务）
	if err := s.resetTask_RestorePortMappings(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段8: 重新初始化监控（短事务）
	if err := s.resetTask_ReinitializeMonitoring(ctx, task, &resetCtx); err != nil {
		return err
	}

	s.updateTaskProgress(task.ID, 100, "重置完成")

	global.APP_LOG.Info("用户实例重置成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("oldInstanceId", resetCtx.OldInstanceID),
		zap.Uint("newInstanceId", resetCtx.NewInstanceID),
		zap.String("instanceName", resetCtx.OldInstanceName),
		zap.Uint("userId", task.UserID))

	return nil
}

// resetTask_Prepare 阶段1: 准备阶段 - 查询必要信息
func (s *TaskService) resetTask_Prepare(ctx context.Context, task *adminModel.Task, taskReq *adminModel.InstanceOperationTaskRequest, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 5, "正在准备重置...")

	// 使用单个短事务查询所有需要的数据
	err := s.dbService.ExecuteQuery(ctx, func() error {
		// 1. 查询实例
		if err := global.APP_DB.First(&resetCtx.Instance, taskReq.InstanceId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("实例不存在")
			}
			return fmt.Errorf("获取实例信息失败: %v", err)
		}

		// 验证实例所有权
		if resetCtx.Instance.UserID != task.UserID {
			return fmt.Errorf("无权限操作此实例")
		}

		// 2. 查询Provider
		if err := global.APP_DB.First(&resetCtx.Provider, resetCtx.Instance.ProviderID).Error; err != nil {
			return fmt.Errorf("获取Provider配置失败: %v", err)
		}

		// 3. 查询系统镜像
		if err := global.APP_DB.Where("name = ? AND provider_type = ? AND instance_type = ? AND architecture = ?",
			resetCtx.Instance.Image, resetCtx.Provider.Type, resetCtx.Instance.InstanceType, resetCtx.Provider.Architecture).
			First(&resetCtx.SystemImage).Error; err != nil {
			return fmt.Errorf("获取系统镜像信息失败: %v", err)
		}

		// 4. 查询端口映射
		if err := global.APP_DB.Where("instance_id = ?", resetCtx.Instance.ID).Find(&resetCtx.OldPortMappings).Error; err != nil {
			global.APP_LOG.Warn("获取旧端口映射失败", zap.Error(err))
		}

		return nil
	})

	if err != nil {
		return err
	}

	// 保存必要信息
	resetCtx.OldInstanceID = resetCtx.Instance.ID
	resetCtx.OldInstanceName = resetCtx.Instance.Name
	resetCtx.NewOldName = fmt.Sprintf("%s-old-%d", resetCtx.OldInstanceName, time.Now().Unix())

	global.APP_LOG.Info("准备阶段完成",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", resetCtx.OldInstanceID),
		zap.Int("portMappings", len(resetCtx.OldPortMappings)))

	return nil
}

// resetTask_RenameAndCreateNew 阶段2: 重命名旧实例并创建新实例记录
func (s *TaskService) resetTask_RenameAndCreateNew(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 15, "正在重命名旧实例并创建新记录...")

	// 使用一个事务完成重命名和创建
	err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// 1. 重命名旧实例
		if err := tx.Model(&resetCtx.Instance).Updates(map[string]interface{}{
			"name": resetCtx.NewOldName,
		}).Error; err != nil {
			return fmt.Errorf("重命名旧实例失败: %v", err)
		}

		// 2. 软删除旧实例
		if err := tx.Delete(&resetCtx.Instance).Error; err != nil {
			return fmt.Errorf("软删除旧实例失败: %v", err)
		}

		// 3. 创建新实例记录
		newInstance := providerModel.Instance{
			Name:         resetCtx.OldInstanceName,
			Provider:     resetCtx.Provider.Name,
			ProviderID:   resetCtx.Provider.ID,
			Image:        resetCtx.Instance.Image,
			InstanceType: resetCtx.Instance.InstanceType,
			CPU:          resetCtx.Instance.CPU,
			Memory:       resetCtx.Instance.Memory,
			Disk:         resetCtx.Instance.Disk,
			Bandwidth:    resetCtx.Instance.Bandwidth,
			UserID:       task.UserID,
			Status:       "creating",
			OSType:       resetCtx.Instance.OSType,
			ExpiredAt:    resetCtx.Instance.ExpiredAt,
			PublicIP:     resetCtx.Provider.Endpoint,
			MaxTraffic:   resetCtx.Instance.MaxTraffic,
		}

		if err := tx.Create(&newInstance).Error; err != nil {
			return fmt.Errorf("创建新实例记录失败: %v", err)
		}

		resetCtx.NewInstanceID = newInstance.ID
		return nil
	})

	if err != nil {
		return err
	}

	global.APP_LOG.Info("数据库操作完成",
		zap.Uint("oldInstanceId", resetCtx.OldInstanceID),
		zap.Uint("newInstanceId", resetCtx.NewInstanceID),
		zap.String("oldName", resetCtx.NewOldName),
		zap.String("newName", resetCtx.OldInstanceName))

	return nil
}

// resetTask_DeleteOldInstance 阶段3: 删除Provider上的旧实例
func (s *TaskService) resetTask_DeleteOldInstance(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 30, "正在删除Provider上的旧实例...")

	providerApiService := &provider2.ProviderApiService{}

	// Provider操作，不在事务中
	deleteErr := providerApiService.DeleteInstanceByProviderID(ctx, resetCtx.Provider.ID, resetCtx.NewOldName)
	if deleteErr != nil {
		errorStr := strings.ToLower(deleteErr.Error())
		isNotFoundError := strings.Contains(errorStr, "no such container") ||
			strings.Contains(errorStr, "not found") ||
			strings.Contains(errorStr, "already removed")

		if !isNotFoundError {
			return fmt.Errorf("删除旧实例失败: %v", deleteErr)
		}

		global.APP_LOG.Info("实例已不存在，继续重置流程")
	}

	// 简单等待删除完成
	time.Sleep(10 * time.Second)

	global.APP_LOG.Info("旧实例删除完成",
		zap.String("instanceName", resetCtx.NewOldName))

	return nil
}

// resetTask_CreateNewInstance 阶段4: 在Provider上创建新实例
func (s *TaskService) resetTask_CreateNewInstance(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 50, "正在创建新实例...")

	providerApiService := &provider2.ProviderApiService{}

	// 准备创建请求
	createReq := provider2.CreateInstanceRequest{
		InstanceConfig: providerModel.ProviderInstanceConfig{
			Name:         resetCtx.OldInstanceName,
			Image:        resetCtx.Instance.Image,
			InstanceType: resetCtx.Instance.InstanceType,
			CPU:          fmt.Sprintf("%d", resetCtx.Instance.CPU),
			Memory:       fmt.Sprintf("%dMB", resetCtx.Instance.Memory),
			Disk:         fmt.Sprintf("%dMB", resetCtx.Instance.Disk),
			Env:          map[string]string{"RESET_OPERATION": "true"},
			Metadata:     make(map[string]string),
		},
		SystemImageID: resetCtx.SystemImage.ID,
	}

	// Docker特殊处理：端口映射
	if resetCtx.Provider.Type == "docker" && len(resetCtx.OldPortMappings) > 0 {
		var ports []string
		for _, oldPort := range resetCtx.OldPortMappings {
			portMapping := fmt.Sprintf("0.0.0.0:%d:%d/%s", oldPort.HostPort, oldPort.GuestPort, oldPort.Protocol)
			ports = append(ports, portMapping)
		}
		createReq.InstanceConfig.Ports = ports
	}

	// 调用Provider API创建
	if err := providerApiService.CreateInstanceByProviderID(ctx, resetCtx.Provider.ID, createReq); err != nil {
		// 创建失败，更新数据库状态
		s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
			return tx.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).Update("status", "failed").Error
		})
		return fmt.Errorf("重置实例失败（重建阶段）: %v", err)
	}

	// 等待实例启动
	time.Sleep(15 * time.Second)

	global.APP_LOG.Info("新实例创建完成",
		zap.Uint("newInstanceId", resetCtx.NewInstanceID),
		zap.String("instanceName", resetCtx.OldInstanceName))

	return nil
}

// resetTask_SetPassword 阶段5: 设置新密码
func (s *TaskService) resetTask_SetPassword(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 70, "正在设置新密码...")

	// 生成新密码
	resetCtx.NewPassword = utils.GenerateStrongPassword(12)

	// 获取内网IP（如果需要）
	s.resetTask_GetPrivateIP(ctx, resetCtx)

	// 设置密码（带重试）
	providerService := provider2.GetProviderService()
	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}

		err := providerService.SetInstancePassword(ctx, resetCtx.Provider.ID, resetCtx.OldInstanceName, resetCtx.NewPassword)
		if err != nil {
			lastErr = err
			continue
		}

		global.APP_LOG.Info("密码设置成功",
			zap.Uint("instanceId", resetCtx.NewInstanceID),
			zap.Int("attempt", attempt))
		return nil
	}

	global.APP_LOG.Warn("设置密码失败，使用默认密码",
		zap.Error(lastErr))
	resetCtx.NewPassword = "root"

	return nil
}

// resetTask_GetPrivateIP 获取实例内网IP
func (s *TaskService) resetTask_GetPrivateIP(ctx context.Context, resetCtx *ResetTaskContext) {
	providerApiService := &provider2.ProviderApiService{}
	prov, _, err := providerApiService.GetProviderByID(resetCtx.Provider.ID)
	if err != nil {
		return
	}

	switch resetCtx.Provider.Type {
	case "lxd":
		if lxdProv, ok := prov.(*lxd.LXDProvider); ok {
			if ip, err := lxdProv.GetInstanceIPv4(resetCtx.OldInstanceName); err == nil {
				resetCtx.NewPrivateIP = ip
			}
		}
	case "incus":
		if incusProv, ok := prov.(*incus.IncusProvider); ok {
			if ip, err := incusProv.GetInstanceIPv4(ctx, resetCtx.OldInstanceName); err == nil {
				resetCtx.NewPrivateIP = ip
			}
		}
	case "proxmox":
		if proxmoxProv, ok := prov.(*proxmox.ProxmoxProvider); ok {
			if ip, err := proxmoxProv.GetInstanceIPv4(ctx, resetCtx.OldInstanceName); err == nil {
				resetCtx.NewPrivateIP = ip
			}
		}
	}
}

// resetTask_UpdateInstanceInfo 阶段6: 更新实例信息
func (s *TaskService) resetTask_UpdateInstanceInfo(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 80, "正在更新实例信息...")

	// 使用短事务更新
	err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"status":   "running",
			"username": "root",
			"password": resetCtx.NewPassword,
		}

		if resetCtx.NewPrivateIP != "" {
			updates["private_ip"] = resetCtx.NewPrivateIP
		}

		return tx.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).Updates(updates).Error
	})

	if err != nil {
		return fmt.Errorf("更新实例信息失败: %v", err)
	}

	global.APP_LOG.Info("实例信息已更新",
		zap.Uint("instanceId", resetCtx.NewInstanceID))

	return nil
}

// resetTask_RestorePortMappings 阶段7: 恢复端口映射
func (s *TaskService) resetTask_RestorePortMappings(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 88, "正在恢复端口映射...")

	if len(resetCtx.OldPortMappings) == 0 {
		// 创建默认端口映射
		portMappingService := &resources.PortMappingService{}
		if err := portMappingService.CreateDefaultPortMappings(resetCtx.NewInstanceID, resetCtx.Provider.ID); err != nil {
			global.APP_LOG.Warn("创建默认端口映射失败", zap.Error(err))
		}
		return nil
	}

	successCount := 0
	failCount := 0

	if resetCtx.Provider.Type == "docker" {
		// Docker: 只需恢复数据库记录
		for _, oldPort := range resetCtx.OldPortMappings {
			err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
				newPort := providerModel.Port{
					InstanceID:    resetCtx.NewInstanceID,
					ProviderID:    resetCtx.Provider.ID,
					HostPort:      oldPort.HostPort,
					GuestPort:     oldPort.GuestPort,
					Protocol:      oldPort.Protocol,
					Description:   oldPort.Description,
					Status:        "active",
					IsSSH:         oldPort.IsSSH,
					IsAutomatic:   oldPort.IsAutomatic,
					PortType:      oldPort.PortType,
					MappingMethod: oldPort.MappingMethod,
					IPv6Enabled:   oldPort.IPv6Enabled,
				}
				return tx.Create(&newPort).Error
			})

			if err != nil {
				failCount++
			} else {
				successCount++
			}
		}
	} else {
		// LXD/Incus/Proxmox: 需要应用到远程服务器
		manager := portmapping.NewManager(&portmapping.ManagerConfig{
			DefaultMappingMethod: resetCtx.Provider.IPv4PortMappingMethod,
		})

		portMappingType := resetCtx.Provider.Type
		if portMappingType == "proxmox" {
			portMappingType = "iptables"
		}

		// 按协议分组
		tcpPorts := []providerModel.Port{}
		udpPorts := []providerModel.Port{}
		bothPorts := []providerModel.Port{}

		for _, oldPort := range resetCtx.OldPortMappings {
			switch oldPort.Protocol {
			case "tcp":
				tcpPorts = append(tcpPorts, oldPort)
			case "udp":
				udpPorts = append(udpPorts, oldPort)
			case "both":
				bothPorts = append(bothPorts, oldPort)
			}
		}

		// 分别处理
		if len(tcpPorts) > 0 {
			processed, failed := s.restorePortMappingsOptimized(ctx, tcpPorts, resetCtx.Instance, resetCtx.Provider, manager, portMappingType)
			successCount += processed
			failCount += failed
		}
		if len(udpPorts) > 0 {
			processed, failed := s.restorePortMappingsOptimized(ctx, udpPorts, resetCtx.Instance, resetCtx.Provider, manager, portMappingType)
			successCount += processed
			failCount += failed
		}
		if len(bothPorts) > 0 {
			processed, failed := s.restorePortMappingsOptimized(ctx, bothPorts, resetCtx.Instance, resetCtx.Provider, manager, portMappingType)
			successCount += processed
			failCount += failed
		}
	}

	// 更新SSH端口
	s.dbService.ExecuteQuery(ctx, func() error {
		var sshPort providerModel.Port
		if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'", resetCtx.NewInstanceID).First(&sshPort).Error; err == nil {
			global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).Update("ssh_port", sshPort.HostPort)
		} else {
			global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).Update("ssh_port", 22)
		}
		return nil
	})

	global.APP_LOG.Info("端口映射恢复完成",
		zap.Int("成功", successCount),
		zap.Int("失败", failCount))

	return nil
}

// resetTask_ReinitializeMonitoring 阶段8: 重新初始化监控
func (s *TaskService) resetTask_ReinitializeMonitoring(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 96, "正在重新初始化监控...")

	// 检查是否启用流量控制
	var providerTrafficEnabled bool
	err := s.dbService.ExecuteQuery(ctx, func() error {
		var dbProvider providerModel.Provider
		if err := global.APP_DB.Select("enable_traffic_control").Where("id = ?", resetCtx.Provider.ID).First(&dbProvider).Error; err != nil {
			return err
		}
		providerTrafficEnabled = dbProvider.EnableTrafficControl
		return nil
	})

	if err != nil || !providerTrafficEnabled {
		return nil
	}

	// 使用统一的流量监控管理器重新初始化pmacct（无事务）
	trafficMonitorManager := traffic_monitor.GetManager()
	if err := trafficMonitorManager.AttachMonitor(ctx, resetCtx.NewInstanceID); err != nil {
		global.APP_LOG.Warn("重新初始化流量监控失败", zap.Error(err))
	} else {
		global.APP_LOG.Info("pmacct监控重新初始化成功",
			zap.Uint("instanceId", resetCtx.NewInstanceID))
	}

	return nil
}
