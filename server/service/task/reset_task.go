package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/provider/portmapping"
	traffic_monitor "oneclickvirt/service/admin/traffic_monitor"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// PortMappingRequest 端口映射创建请求
type PortMappingRequest struct {
	InstanceID    uint
	ProviderID    uint
	HostPort      int
	GuestPort     int
	Protocol      string
	Description   string
	IsSSH         bool
	IsAutomatic   bool
	PortType      string
	MappingMethod string
	IPv6Enabled   bool
}

// ResetTaskContext 重置任务上下文
type ResetTaskContext struct {
	Instance           providerModel.Instance
	Provider           providerModel.Provider
	SystemImage        systemModel.SystemImage
	OldPortMappings    []providerModel.Port
	OldInstanceID      uint
	OldInstanceName    string
	OriginalUserID     uint
	OriginalExpiresAt  *time.Time
	OriginalMaxTraffic uint64
	NewInstanceID      uint
	NewPassword        string
	NewPrivateIP       string
}

// executeResetTask 执行实例重置任务
// 直接复用删除和创建逻辑，避免代码重复和资源管理错误
func (s *TaskService) executeResetTask(ctx context.Context, task *adminModel.Task) error {
	// 解析任务数据
	var taskReq adminModel.InstanceOperationTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	var resetCtx ResetTaskContext

	// 阶段1: 准备阶段 - 收集必要信息
	if err := s.resetTask_Prepare(ctx, task, &taskReq, &resetCtx); err != nil {
		return err
	}

	// 阶段2: 执行Provider删除（复用删除逻辑）
	if err := s.resetTask_DeleteOldInstance(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段3: 清理旧实例数据库记录和资源
	if err := s.resetTask_CleanupOldInstance(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段4: 创建新实例（复用创建逻辑）
	if err := s.resetTask_CreateNewInstance(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段5: 设置密码
	if err := s.resetTask_SetPassword(ctx, task, &resetCtx); err != nil {
		// 密码设置失败不影响重置流程
		global.APP_LOG.Warn("重置系统：密码设置失败，使用默认密码", zap.Error(err))
	}

	// 阶段6: 更新实例信息
	if err := s.resetTask_UpdateInstanceInfo(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段7: 恢复端口映射（使用端口映射服务）
	if err := s.resetTask_RestorePortMappings(ctx, task, &resetCtx); err != nil {
		// 端口映射失败不影响重置流程
		global.APP_LOG.Warn("重置系统：端口映射恢复部分失败", zap.Error(err))
	}

	// 阶段8: 重新初始化监控
	if err := s.resetTask_ReinitializeMonitoring(ctx, task, &resetCtx); err != nil {
		// 监控初始化失败不影响重置流程
		global.APP_LOG.Warn("重置系统：监控初始化失败", zap.Error(err))
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

		// 4. 查询端口映射（包含status='active'的）
		if err := global.APP_DB.Where("instance_id = ? AND status = ?", resetCtx.Instance.ID, "active").
			Find(&resetCtx.OldPortMappings).Error; err != nil {
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
	resetCtx.OriginalUserID = resetCtx.Instance.UserID
	resetCtx.OriginalExpiresAt = resetCtx.Instance.ExpiresAt
	resetCtx.OriginalMaxTraffic = uint64(resetCtx.Instance.MaxTraffic)

	global.APP_LOG.Info("准备阶段完成",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", resetCtx.OldInstanceID),
		zap.String("instanceName", resetCtx.OldInstanceName),
		zap.Int("portMappings", len(resetCtx.OldPortMappings)))

	return nil
}

// resetTask_DeleteOldInstance 阶段2: 删除Provider上的旧实例（复用删除逻辑）
func (s *TaskService) resetTask_DeleteOldInstance(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 15, "正在删除旧实例...")

	providerApiService := &provider2.ProviderApiService{}

	// 直接调用Provider删除API
	if err := providerApiService.DeleteInstanceByProviderID(ctx, resetCtx.Provider.ID, resetCtx.OldInstanceName); err != nil {
		// 如果实例不存在，继续流程
		errStr := err.Error()
		if contains(errStr, "not found") || contains(errStr, "no such") {
			global.APP_LOG.Info("实例已不存在，继续重置流程",
				zap.String("instanceName", resetCtx.OldInstanceName))
		} else {
			return fmt.Errorf("删除旧实例失败: %v", err)
		}
	}

	// 等待删除完成
	time.Sleep(10 * time.Second)

	global.APP_LOG.Info("旧实例删除完成",
		zap.String("instanceName", resetCtx.OldInstanceName))

	return nil
}

// resetTask_CleanupOldInstance 阶段3: 清理旧实例数据库记录和资源（复用删除逻辑）
func (s *TaskService) resetTask_CleanupOldInstance(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 25, "正在清理旧实例数据...")

	// 清理pmacct监控（事务外操作）
	trafficMonitorManager := traffic_monitor.GetManager()
	cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cleanupCancel()

	if err := trafficMonitorManager.DetachMonitor(cleanupCtx, resetCtx.OldInstanceID); err != nil {
		global.APP_LOG.Warn("清理pmacct监控失败", zap.Error(err))
	}

	// 在单个事务中清理数据库记录和释放资源
	err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// 1. 删除端口映射
		portMappingService := resources.PortMappingService{}
		if err := portMappingService.DeleteInstancePortMappingsInTx(tx, resetCtx.OldInstanceID); err != nil {
			global.APP_LOG.Warn("删除端口映射失败", zap.Error(err))
		}

		// 2. 释放Provider资源
		resourceService := &resources.ResourceService{}
		if err := resourceService.ReleaseResourcesInTx(tx, resetCtx.Provider.ID, resetCtx.Instance.InstanceType,
			resetCtx.Instance.CPU, resetCtx.Instance.Memory, resetCtx.Instance.Disk); err != nil {
			global.APP_LOG.Warn("释放Provider资源失败", zap.Error(err))
		}

		// 3. 释放用户配额（根据实例状态）
		quotaService := resources.NewQuotaService()
		resourceUsage := resources.ResourceUsage{
			CPU:       resetCtx.Instance.CPU,
			Memory:    resetCtx.Instance.Memory,
			Disk:      resetCtx.Instance.Disk,
			Bandwidth: resetCtx.Instance.Bandwidth,
		}

		// 根据实例状态释放对应的配额
		isPendingState := resetCtx.Instance.Status == "creating" || resetCtx.Instance.Status == "resetting"
		if isPendingState {
			if err := quotaService.ReleasePendingQuota(tx, resetCtx.OriginalUserID, resourceUsage); err != nil {
				global.APP_LOG.Warn("释放待确认配额失败", zap.Error(err))
			}
		} else {
			if err := quotaService.ReleaseUsedQuota(tx, resetCtx.OriginalUserID, resourceUsage); err != nil {
				global.APP_LOG.Warn("释放已使用配额失败", zap.Error(err))
			}
		}

		// 4. 软删除实例记录
		if err := tx.Delete(&resetCtx.Instance).Error; err != nil {
			return fmt.Errorf("删除实例记录失败: %v", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	global.APP_LOG.Info("旧实例清理完成",
		zap.Uint("instanceId", resetCtx.OldInstanceID))

	return nil
}

// resetTask_CreateNewInstance 阶段4: 创建新实例（复用创建逻辑）
func (s *TaskService) resetTask_CreateNewInstance(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 40, "正在创建新实例...")

	// 获取用户信息
	var user userModel.User
	if err := global.APP_DB.First(&user, task.UserID).Error; err != nil {
		return fmt.Errorf("获取用户信息失败: %v", err)
	}

	// 在事务中创建新实例记录并分配配额
	err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// 创建新实例记录
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
			UserID:       resetCtx.OriginalUserID,
			Status:       "creating",
			OSType:       resetCtx.Instance.OSType,
			ExpiresAt:    resetCtx.OriginalExpiresAt,
			PublicIP:     resetCtx.Provider.Endpoint,
			MaxTraffic:   int64(resetCtx.OriginalMaxTraffic),
		}

		if err := tx.Create(&newInstance).Error; err != nil {
			return fmt.Errorf("创建新实例记录失败: %v", err)
		}

		resetCtx.NewInstanceID = newInstance.ID

		// 分配待确认配额
		quotaService := resources.NewQuotaService()
		resourceUsage := resources.ResourceUsage{
			CPU:       resetCtx.Instance.CPU,
			Memory:    resetCtx.Instance.Memory,
			Disk:      resetCtx.Instance.Disk,
			Bandwidth: resetCtx.Instance.Bandwidth,
		}

		if err := quotaService.AllocatePendingQuota(tx, resetCtx.OriginalUserID, resourceUsage); err != nil {
			return fmt.Errorf("分配待确认配额失败: %v", err)
		}

		// 分配Provider资源
		resourceService := &resources.ResourceService{}
		if err := resourceService.AllocateResourcesInTx(tx, resetCtx.Provider.ID, resetCtx.Instance.InstanceType,
			resetCtx.Instance.CPU, resetCtx.Instance.Memory, resetCtx.Instance.Disk); err != nil {
			return fmt.Errorf("分配Provider资源失败: %v", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	global.APP_LOG.Info("新实例记录创建完成",
		zap.Uint("newInstanceId", resetCtx.NewInstanceID),
		zap.String("instanceName", resetCtx.OldInstanceName))

	s.updateTaskProgress(task.ID, 50, "正在调用Provider创建实例...")

	// 准备创建请求（使用与正常创建完全相同的逻辑）
	createReq := provider2.CreateInstanceRequest{
		InstanceConfig: providerModel.ProviderInstanceConfig{
			Name:         resetCtx.OldInstanceName,
			Image:        resetCtx.Instance.Image,
			InstanceType: resetCtx.Instance.InstanceType,
			CPU:          fmt.Sprintf("%d", resetCtx.Instance.CPU),
			Memory:       fmt.Sprintf("%dm", resetCtx.Instance.Memory),
			Disk:         fmt.Sprintf("%dm", resetCtx.Instance.Disk),
			Env:          map[string]string{"RESET_OPERATION": "true"},
			Metadata: map[string]string{
				"user_level":               fmt.Sprintf("%d", user.Level),
				"bandwidth_spec":           fmt.Sprintf("%d", resetCtx.Instance.Bandwidth),
				"ipv4_port_mapping_method": resetCtx.Provider.IPv4PortMappingMethod,
				"ipv6_port_mapping_method": resetCtx.Provider.IPv6PortMappingMethod,
				"network_type":             resetCtx.Provider.NetworkType,
				"instance_id":              fmt.Sprintf("%d", resetCtx.NewInstanceID),
				"provider_id":              fmt.Sprintf("%d", resetCtx.Provider.ID),
				"reset_from_instance_id":   fmt.Sprintf("%d", resetCtx.OldInstanceID),
			},
			Privileged:   boolPtr(resetCtx.Provider.ContainerPrivileged),
			AllowNesting: boolPtr(resetCtx.Provider.ContainerAllowNesting),
			EnableLXCFS:  boolPtr(resetCtx.Provider.ContainerEnableLXCFS),
			CPUAllowance: stringPtr(resetCtx.Provider.ContainerCPUAllowance),
			MemorySwap:   boolPtr(resetCtx.Provider.ContainerMemorySwap),
			MaxProcesses: intPtr(resetCtx.Provider.ContainerMaxProcesses),
			DiskIOLimit:  stringPtr(resetCtx.Provider.ContainerDiskIOLimit),
		},
		SystemImageID: resetCtx.SystemImage.ID,
	}

	// Docker端口映射特殊处理
	if resetCtx.Provider.Type == "docker" && len(resetCtx.OldPortMappings) > 0 {
		var ports []string
		for _, oldPort := range resetCtx.OldPortMappings {
			if oldPort.Protocol == "both" {
				ports = append(ports,
					fmt.Sprintf("0.0.0.0:%d:%d/tcp", oldPort.HostPort, oldPort.GuestPort),
					fmt.Sprintf("0.0.0.0:%d:%d/udp", oldPort.HostPort, oldPort.GuestPort))
			} else {
				ports = append(ports,
					fmt.Sprintf("0.0.0.0:%d:%d/%s", oldPort.HostPort, oldPort.GuestPort, oldPort.Protocol))
			}
		}
		createReq.InstanceConfig.Ports = ports
	}

	// 调用Provider API创建实例
	providerApiService := &provider2.ProviderApiService{}
	if err := providerApiService.CreateInstanceByProviderID(ctx, resetCtx.Provider.ID, createReq); err != nil {
		// 创建失败，更新实例状态为failed，但不回滚数据库（保留记录供排查）
		s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
			return tx.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).
				Update("status", "failed").Error
		})
		return fmt.Errorf("Provider创建实例失败: %v", err)
	}

	// 等待实例启动
	time.Sleep(15 * time.Second)

	// 确保实例运行
	if prov, _, err := providerApiService.GetProviderByID(resetCtx.Provider.ID); err == nil {
		if instance, err := prov.GetInstance(ctx, resetCtx.OldInstanceName); err == nil {
			if instance.Status != "running" {
				global.APP_LOG.Info("实例未运行，尝试启动",
					zap.String("instanceName", resetCtx.OldInstanceName),
					zap.String("status", instance.Status))
				if err := prov.StartInstance(ctx, resetCtx.OldInstanceName); err != nil {
					global.APP_LOG.Warn("启动实例失败", zap.Error(err))
				} else {
					time.Sleep(10 * time.Second)
				}
			}
		}
	}

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

	// 获取内网IP
	providerApiService := &provider2.ProviderApiService{}
	prov, _, err := providerApiService.GetProviderByID(resetCtx.Provider.ID)
	if err == nil {
		resetCtx.NewPrivateIP = getInstancePrivateIP(ctx, prov, resetCtx.Provider.Type, resetCtx.OldInstanceName)
	}

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
			global.APP_LOG.Warn("设置密码失败，准备重试",
				zap.Int("attempt", attempt),
				zap.Error(err))
			continue
		}

		global.APP_LOG.Info("密码设置成功",
			zap.Uint("instanceId", resetCtx.NewInstanceID),
			zap.Int("attempt", attempt))
		return nil
	}

	// 所有重试失败，使用默认密码
	global.APP_LOG.Warn("设置密码失败，使用默认密码",
		zap.Error(lastErr))
	resetCtx.NewPassword = "root"

	return nil
}

// resetTask_UpdateInstanceInfo 阶段6: 更新实例信息并确认配额
func (s *TaskService) resetTask_UpdateInstanceInfo(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 80, "正在更新实例信息...")

	// 使用短事务更新实例信息和确认配额
	err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"status":   "running",
			"username": "root",
			"password": resetCtx.NewPassword,
		}

		if resetCtx.NewPrivateIP != "" {
			updates["private_ip"] = resetCtx.NewPrivateIP
		}

		if err := tx.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("更新实例信息失败: %v", err)
		}

		// 确认待确认配额（将 pending_quota 转为 used_quota）
		quotaService := resources.NewQuotaService()
		resourceUsage := resources.ResourceUsage{
			CPU:       resetCtx.Instance.CPU,
			Memory:    resetCtx.Instance.Memory,
			Disk:      resetCtx.Instance.Disk,
			Bandwidth: resetCtx.Instance.Bandwidth,
		}

		if err := quotaService.ConfirmPendingQuota(tx, resetCtx.OriginalUserID, resourceUsage); err != nil {
			return fmt.Errorf("确认配额失败: %v", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	global.APP_LOG.Info("实例信息已更新并确认配额",
		zap.Uint("instanceId", resetCtx.NewInstanceID))

	return nil
}

// resetTask_RestorePortMappings 阶段7: 恢复端口映射（直接创建，不使用任务系统）
func (s *TaskService) resetTask_RestorePortMappings(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 88, "正在恢复端口映射...")

	// 对于LXD/Incus，等待实例获取IP地址
	if resetCtx.Provider.Type == "lxd" || resetCtx.Provider.Type == "incus" {
		if resetCtx.NewPrivateIP == "" {
			providerApiService := &provider2.ProviderApiService{}
			prov, _, err := providerApiService.GetProviderByID(resetCtx.Provider.ID)
			if err == nil {
				// 尝试获取IP，最多等待30秒
				for attempt := 1; attempt <= 10; attempt++ {
					ip := getInstancePrivateIP(ctx, prov, resetCtx.Provider.Type, resetCtx.OldInstanceName)
					if ip != "" {
						resetCtx.NewPrivateIP = ip
						global.APP_LOG.Info("实例IP获取成功",
							zap.String("instanceName", resetCtx.OldInstanceName),
							zap.String("ip", ip),
							zap.Int("attempt", attempt))
						break
					}
					if attempt < 10 {
						time.Sleep(3 * time.Second)
					}
				}
			}

			if resetCtx.NewPrivateIP == "" {
				global.APP_LOG.Warn("无法获取实例IP地址，端口映射可能失败",
					zap.String("instanceName", resetCtx.OldInstanceName))
			}
		}

		// 更新实例的内网IP到数据库
		if resetCtx.NewPrivateIP != "" {
			s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
				return tx.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).
					Update("private_ip", resetCtx.NewPrivateIP).Error
			})
		}
	}

	// 如果没有旧端口映射，创建默认端口
	if len(resetCtx.OldPortMappings) == 0 {
		portMappingService := &resources.PortMappingService{}
		if err := portMappingService.CreateDefaultPortMappings(resetCtx.NewInstanceID, resetCtx.Provider.ID); err != nil {
			global.APP_LOG.Warn("创建默认端口映射失败", zap.Error(err))
		}
		return nil
	}

	// 恢复端口映射
	successCount := 0
	failCount := 0

	// Docker类型：端口映射已在创建时设置，只需创建数据库记录
	if resetCtx.Provider.Type == "docker" {
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
				global.APP_LOG.Warn("创建端口映射数据库记录失败",
					zap.Int("hostPort", oldPort.HostPort),
					zap.Error(err))
				failCount++
			} else {
				successCount++
			}
		}
	} else {
		// LXD/Incus/Proxmox：需要在远程服务器上创建端口映射
		for _, oldPort := range resetCtx.OldPortMappings {
			if err := s.createPortMappingDirect(ctx, resetCtx, oldPort); err != nil {
				global.APP_LOG.Warn("恢复端口映射失败",
					zap.Int("hostPort", oldPort.HostPort),
					zap.Int("guestPort", oldPort.GuestPort),
					zap.String("protocol", oldPort.Protocol),
					zap.Error(err))
				failCount++
			} else {
				successCount++
			}
		}
	}

	// 更新SSH端口
	s.dbService.ExecuteQuery(ctx, func() error {
		var sshPort providerModel.Port
		if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'",
			resetCtx.NewInstanceID).First(&sshPort).Error; err == nil {
			global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).
				Update("ssh_port", sshPort.HostPort)
		} else {
			global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).
				Update("ssh_port", 22)
		}
		return nil
	})

	global.APP_LOG.Info("端口映射恢复完成",
		zap.Int("成功", successCount),
		zap.Int("失败", failCount))

	return nil
}

// createPortMappingDirect 直接创建端口映射（绕过任务系统）
func (s *TaskService) createPortMappingDirect(ctx context.Context, resetCtx *ResetTaskContext, oldPort providerModel.Port) error {
	// 获取Provider实例（暂时不需要直接使用prov）
	// portmapping.Manager会自动处理provider连接

	// 确定端口映射类型
	portMappingType := resetCtx.Provider.Type
	if portMappingType == "proxmox" {
		portMappingType = "iptables"
	}

	// 使用portmapping管理器创建端口映射
	manager := portmapping.NewManager(&portmapping.ManagerConfig{
		DefaultMappingMethod: resetCtx.Provider.IPv4PortMappingMethod,
	})

	portReq := &portmapping.PortMappingRequest{
		InstanceID:    fmt.Sprintf("%d", resetCtx.NewInstanceID),
		ProviderID:    resetCtx.Provider.ID,
		Protocol:      oldPort.Protocol,
		HostPort:      oldPort.HostPort,
		GuestPort:     oldPort.GuestPort,
		Description:   oldPort.Description,
		MappingMethod: resetCtx.Provider.IPv4PortMappingMethod,
		IsSSH:         &oldPort.IsSSH,
	}

	// 创建端口映射（在远程服务器上）
	result, err := manager.CreatePortMapping(ctx, portMappingType, portReq)
	if err != nil {
		// 即使远程创建失败，也尝试创建数据库记录（状态为failed）
		s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
			newPort := providerModel.Port{
				InstanceID:    resetCtx.NewInstanceID,
				ProviderID:    resetCtx.Provider.ID,
				HostPort:      oldPort.HostPort,
				GuestPort:     oldPort.GuestPort,
				Protocol:      oldPort.Protocol,
				Description:   oldPort.Description,
				Status:        "failed",
				IsSSH:         oldPort.IsSSH,
				IsAutomatic:   oldPort.IsAutomatic,
				PortType:      oldPort.PortType,
				MappingMethod: oldPort.MappingMethod,
				IPv6Enabled:   oldPort.IPv6Enabled,
			}
			return tx.Create(&newPort).Error
		})
		return fmt.Errorf("在远程服务器上创建端口映射失败: %v", err)
	}

	global.APP_LOG.Debug("端口映射已应用到远程服务器",
		zap.Uint("portId", result.ID),
		zap.Int("hostPort", result.HostPort),
		zap.Int("guestPort", result.GuestPort))

	return nil
}

// resetTask_ReinitializeMonitoring 阶段8: 重新初始化监控
func (s *TaskService) resetTask_ReinitializeMonitoring(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 96, "正在重新初始化监控...")

	// 检查是否启用流量控制
	var providerTrafficEnabled bool
	err := s.dbService.ExecuteQuery(ctx, func() error {
		var dbProvider providerModel.Provider
		if err := global.APP_DB.Select("enable_traffic_control").Where("id = ?", resetCtx.Provider.ID).
			First(&dbProvider).Error; err != nil {
			return err
		}
		providerTrafficEnabled = dbProvider.EnableTrafficControl
		return nil
	})

	if err != nil || !providerTrafficEnabled {
		return nil
	}

	// 使用统一的流量监控管理器重新初始化pmacct
	trafficMonitorManager := traffic_monitor.GetManager()
	if err := trafficMonitorManager.AttachMonitor(ctx, resetCtx.NewInstanceID); err != nil {
		global.APP_LOG.Warn("重新初始化流量监控失败", zap.Error(err))
	} else {
		global.APP_LOG.Info("流量监控重新初始化成功",
			zap.Uint("instanceId", resetCtx.NewInstanceID))
	}

	return nil
}

// 辅助函数：创建指针类型
func boolPtr(b bool) *bool {
	return &b
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func intPtr(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}

// 辅助函数：字符串包含检查（不区分大小写）
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(substr) == 0 ||
			findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	sLower := toLower(s)
	substrLower := toLower(substr)
	for i := 0; i <= len(sLower)-len(substrLower); i++ {
		if sLower[i:i+len(substrLower)] == substrLower {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}

// 辅助函数：获取实例内网IP
func getInstancePrivateIP(ctx context.Context, prov interface{}, providerType, instanceName string) string {
	switch providerType {
	case "lxd":
		if p, ok := prov.(interface {
			GetInstanceIPv4(context.Context, string) (string, error)
		}); ok {
			if ip, err := p.GetInstanceIPv4(ctx, instanceName); err == nil {
				return ip
			}
		}
	case "incus":
		if p, ok := prov.(interface {
			GetInstanceIPv4(context.Context, string) (string, error)
		}); ok {
			if ip, err := p.GetInstanceIPv4(ctx, instanceName); err == nil {
				return ip
			}
		}
	case "proxmox":
		if p, ok := prov.(interface {
			GetInstanceIPv4(context.Context, string) (string, error)
		}); ok {
			if ip, err := p.GetInstanceIPv4(ctx, instanceName); err == nil {
				return ip
			}
		}
	}
	return ""
}
