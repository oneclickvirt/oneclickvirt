package task

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
	userModel "oneclickvirt/model/user"
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
	Instance               providerModel.Instance
	Provider               providerModel.Provider
	SystemImage            systemModel.SystemImage
	OldPortMappings        []providerModel.Port
	OldInstanceID          uint
	OldInstanceName        string
	OriginalUserID         uint
	OriginalStatus         string // 实例重置前的原始状态（用于正确释放配额）
	OriginalExpiresAt      *time.Time
	OriginalIsManualExpiry bool // 实例重置前的手动过期时间设置
	OriginalMaxTraffic     uint64
	NewInstanceID          uint
	NewPassword            string
	NewPrivateIP           string
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

	// 当任务context被取消时（超时/强制停止），确保新实例不会卡在creating状态
	// 使用独立的background context执行清理，避免被取消的ctx影响
	defer func() {
		if ctx.Err() != nil && resetCtx.NewInstanceID != 0 {
			bgCtx := context.Background()
			result := global.APP_DB.WithContext(bgCtx).
				Model(&providerModel.Instance{}).
				Where("id = ? AND status = ?", resetCtx.NewInstanceID, "creating").
				Updates(map[string]interface{}{
					"status":     "stopped",
					"updated_at": time.Now(),
				})
			if result.Error != nil {
				global.APP_LOG.Error("重置任务context取消后清理新实例状态失败",
					zap.Uint("taskId", task.ID),
					zap.Uint("newInstanceId", resetCtx.NewInstanceID),
					zap.Error(result.Error))
			} else if result.RowsAffected > 0 {
				global.APP_LOG.Warn("重置任务因context取消而中断，已将新实例状态从creating恢复为stopped",
					zap.Uint("taskId", task.ID),
					zap.Uint("newInstanceId", resetCtx.NewInstanceID))
			}
		}
	}()

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

	// 解析taskData获取originalStatus（实例重置前的原始状态）
	var taskData map[string]interface{}
	if err := json.Unmarshal([]byte(task.TaskData), &taskData); err == nil {
		if originalStatus, ok := taskData["originalStatus"].(string); ok {
			resetCtx.OriginalStatus = originalStatus
			global.APP_LOG.Debug("从任务数据中解析到原始状态",
				zap.String("originalStatus", originalStatus))
		}
	}

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

	// 如果无法从taskData解析originalStatus，则使用当前状态作为兜底
	if resetCtx.OriginalStatus == "" {
		resetCtx.OriginalStatus = resetCtx.Instance.Status
		global.APP_LOG.Warn("无法从任务数据解析原始状态，使用当前状态作为兜底",
			zap.String("currentStatus", resetCtx.Instance.Status))
	}

	// 保存必要信息
	resetCtx.OldInstanceID = resetCtx.Instance.ID
	resetCtx.OldInstanceName = resetCtx.Instance.Name
	resetCtx.OriginalUserID = resetCtx.Instance.UserID
	resetCtx.OriginalExpiresAt = resetCtx.Instance.ExpiresAt
	resetCtx.OriginalIsManualExpiry = resetCtx.Instance.IsManualExpiry
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

		// 3. 释放用户配额（根据实例的原始状态，而非当前状态）
		// 实例在重置前可能是running/stopped等稳定状态，但触发重置后状态被更新为resetting
		// 应该根据原始状态判断配额类型，而不是当前的resetting状态
		quotaService := resources.NewQuotaService()
		resourceUsage := resources.ResourceUsage{
			CPU:       resetCtx.Instance.CPU,
			Memory:    resetCtx.Instance.Memory,
			Disk:      resetCtx.Instance.Disk,
			Bandwidth: resetCtx.Instance.Bandwidth,
		}

		// 根据实例的原始状态（重置前的状态）释放对应的配额
		isPendingState := constant.IsTransitionalStatus(resetCtx.OriginalStatus)
		if isPendingState {
			if err := quotaService.ReleasePendingQuota(tx, resetCtx.OriginalUserID, resourceUsage); err != nil {
				global.APP_LOG.Warn("释放待确认配额失败", zap.Error(err))
			}
			global.APP_LOG.Debug("已释放待确认配额",
				zap.String("originalStatus", resetCtx.OriginalStatus),
				zap.Uint("userId", resetCtx.OriginalUserID))
		} else {
			if err := quotaService.ReleaseUsedQuota(tx, resetCtx.OriginalUserID, resourceUsage); err != nil {
				global.APP_LOG.Warn("释放已使用配额失败", zap.Error(err))
			}
			global.APP_LOG.Debug("已释放已使用配额",
				zap.String("originalStatus", resetCtx.OriginalStatus),
				zap.Uint("userId", resetCtx.OriginalUserID))
		}

		// 4. 重命名并软删除实例记录（避免唯一索引冲突，同时保留流量统计）
		// 在旧实例名后添加时间戳，释放 name+provider_id 的唯一索引
		deletedName := fmt.Sprintf("%s_deleted_%d", resetCtx.Instance.Name, time.Now().Unix())
		if err := tx.Model(&resetCtx.Instance).Update("name", deletedName).Error; err != nil {
			return fmt.Errorf("重命名实例失败: %v", err)
		}

		// 软删除实例记录，保留流量统计数据
		if err := tx.Delete(&resetCtx.Instance).Error; err != nil {
			return fmt.Errorf("删除实例记录失败: %v", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	global.APP_LOG.Info("旧实例清理完成（重命名后软删除）",
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
			Name:           resetCtx.OldInstanceName,
			Provider:       resetCtx.Provider.Name,
			ProviderID:     resetCtx.Provider.ID,
			Image:          resetCtx.Instance.Image,
			InstanceType:   resetCtx.Instance.InstanceType,
			CPU:            resetCtx.Instance.CPU,
			Memory:         resetCtx.Instance.Memory,
			Disk:           resetCtx.Instance.Disk,
			Bandwidth:      resetCtx.Instance.Bandwidth,
			UserID:         resetCtx.OriginalUserID,
			Status:         "creating",
			OSType:         resetCtx.Instance.OSType,
			ExpiresAt:      resetCtx.OriginalExpiresAt,
			IsManualExpiry: resetCtx.OriginalIsManualExpiry, // 继承原实例的手动过期时间设置
			PublicIP:       resetCtx.Provider.Endpoint,
			MaxTraffic:     int64(resetCtx.OriginalMaxTraffic),
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

	// 调用Provider创建实例（根据Provider的ExecutionRule配置自动选择API或SSH）
	providerApiService := &provider2.ProviderApiService{}
	if err := providerApiService.CreateInstanceByProviderID(ctx, resetCtx.Provider.ID, createReq); err != nil {
		// 创建失败，更新实例状态为failed，但不回滚数据库（保留记录供排查）
		// 使用独立的background context，避免ctx已被取消时无法更新状态
		s.dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
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
				global.APP_LOG.Debug("实例未运行，尝试启动",
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

		global.APP_LOG.Debug("密码设置成功",
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
