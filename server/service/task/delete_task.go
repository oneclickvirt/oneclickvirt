package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	traffic_monitor "oneclickvirt/service/admin/traffic_monitor"
	"oneclickvirt/service/database"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/traffic"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// executeDeleteInstanceTask 执行删除实例任务
func (s *TaskService) executeDeleteInstanceTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度 (5%)
	s.updateTaskProgress(task.ID, 5, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.DeleteInstanceTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度 (10%)
	s.updateTaskProgress(task.ID, 10, "正在获取实例信息...")

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, taskReq.InstanceId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 实例已不存在，标记任务完成
			stateManager := GetTaskStateManager()
			if err := stateManager.CompleteMainTask(task.ID, true, "实例已不存在，删除任务完成", nil); err != nil {
				global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
			}
			return nil
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}

	// 验证实例所有权 - 管理员操作跳过权限验证
	if !taskReq.AdminOperation && instance.UserID != task.UserID {
		return fmt.Errorf("无权限删除此实例")
	}

	// 更新进度 (15%)
	s.updateTaskProgress(task.ID, 15, "正在获取Provider配置...")

	// 获取Provider配置
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("获取Provider配置失败: %v", err)
	}

	// 复制副本避免共享状态，立即创建Provider字段的本地副本
	localProviderID := provider.ID
	localProviderName := provider.Name

	// 更新进度 (20%)
	s.updateTaskProgress(task.ID, 20, "正在同步流量数据...")

	// 删除前进行最后一次流量同步
	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerInstanceTrafficSync(instance.ID, "实例删除前最终同步")

	// 使用可取消的等待
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-ctx.Done():
		return fmt.Errorf("任务已取消")
	}

	// 更新进度 (25%)
	s.updateTaskProgress(task.ID, 25, "正在删除实例...")

	// 调用Provider删除实例，重试机制
	providerApiService := &provider2.ProviderApiService{}
	maxRetries := global.APP_CONFIG.Task.DeleteRetryCount
	if maxRetries <= 0 {
		maxRetries = 3
	}
	retryDelay := time.Duration(global.APP_CONFIG.Task.DeleteRetryDelay) * time.Second
	if retryDelay <= 0 {
		retryDelay = 2 * time.Second
	}
	var lastErr error

	providerDeleteSuccess := false
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			// 每次重试增加进度 (25% -> 40% -> 55% -> 70%)
			progressIncrement := 25 + (attempt-1)*15
			if progressIncrement > 70 {
				progressIncrement = 70
			}
			s.updateTaskProgress(task.ID, progressIncrement, fmt.Sprintf("正在删除实例（第%d次尝试）...", attempt))
		}

		if err := providerApiService.DeleteInstanceByProviderID(ctx, localProviderID, instance.Name); err != nil {
			lastErr = err
			global.APP_LOG.Warn("Provider删除实例失败，准备重试",
				zap.Uint("taskId", task.ID),
				zap.String("instanceName", instance.Name),
				zap.String("provider", localProviderName),
				zap.Int("attempt", attempt),
				zap.Int("maxRetries", maxRetries),
				zap.Error(err))

			if attempt < maxRetries {
				timer := time.NewTimer(retryDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
				retryDelay *= 2 // 指数退避
			}
		} else {
			providerDeleteSuccess = true
			global.APP_LOG.Info("Provider删除实例成功",
				zap.Uint("taskId", task.ID),
				zap.String("instanceName", instance.Name),
				zap.String("provider", provider.Name),
				zap.Int("attempt", attempt))
			break
		}
	}

	if !providerDeleteSuccess {
		global.APP_LOG.Error("Provider删除实例最终失败，已重试最大次数",
			zap.Uint("taskId", task.ID),
			zap.String("instanceName", instance.Name),
			zap.String("provider", provider.Name),
			zap.Int("maxRetries", maxRetries),
			zap.Error(lastErr))
	}

	// 更新进度 (80%)
	s.updateTaskProgress(task.ID, 80, "正在清理pmacct监控数据...")

	// 第一步：事务外清理pmacct（可能包含SSH操作）
	trafficMonitorManager := traffic_monitor.GetManager()
	deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer deleteCancel()
	if err := trafficMonitorManager.DetachMonitor(deleteCtx, instance.ID); err != nil {
		global.APP_LOG.Warn("清理实例pmacct数据失败",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
	}

	// 更新进度 (90%)
	s.updateTaskProgress(task.ID, 90, "正在清理数据库记录...")

	// 第三步：在短事务中批量处理数据库操作
	dbService := database.GetDatabaseService()
	quotaService := resources.NewQuotaService()

	// 在事务前保存需要使用的字段
	instanceID := instance.ID
	instanceCPU := instance.CPU
	instanceMemory := instance.Memory
	instanceDisk := instance.Disk
	instanceBandwidth := instance.Bandwidth
	instanceProviderID := instance.ProviderID
	instanceType := instance.InstanceType
	instanceUserID := instance.UserID

	// 分离事务操作
	err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// 1. 删除端口映射（在独立的事务中）
		portMappingService := resources.PortMappingService{}
		if err := portMappingService.DeleteInstancePortMappingsInTx(tx, instanceID); err != nil {
			global.APP_LOG.Warn("删除实例端口映射失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instanceID),
				zap.Error(err))
			// 端口映射删除失败不阻止整个流程
		}

		// 2. 释放Provider资源
		resourceService := &resources.ResourceService{}
		if err := resourceService.ReleaseResourcesInTx(tx, instanceProviderID, instanceType,
			instanceCPU, instanceMemory, instanceDisk); err != nil {
			global.APP_LOG.Warn("释放Provider资源失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instanceID),
				zap.Error(err))
			// Provider资源释放失败不阻止整个流程
		}

		// 3. 释放用户配额（根据实例状态决定释放哪种配额）
		// 如果实例处于 creating/resetting 状态，释放 pending_quota
		// 如果实例处于其他稳定状态，释放 used_quota
		resourceUsage := resources.ResourceUsage{
			CPU:       instanceCPU,
			Memory:    instanceMemory,
			Disk:      instanceDisk,
			Bandwidth: instanceBandwidth,
		}

		isPendingState := instance.Status == "creating" || instance.Status == "resetting"
		if isPendingState {
			if err := quotaService.ReleasePendingQuota(tx, instanceUserID, resourceUsage); err != nil {
				global.APP_LOG.Warn("释放待确认配额失败",
					zap.Uint("taskId", task.ID),
					zap.Uint("instanceId", instanceID),
					zap.String("status", instance.Status),
					zap.Error(err))
				// 配额释放失败不阻止整个流程
			}
		} else {
			if err := quotaService.ReleaseUsedQuota(tx, instanceUserID, resourceUsage); err != nil {
				global.APP_LOG.Warn("释放已使用配额失败",
					zap.Uint("taskId", task.ID),
					zap.Uint("instanceId", instanceID),
					zap.String("status", instance.Status),
					zap.Error(err))
				// 配额释放失败不阻止整个流程
			}
		}

		// 4. 软删除当前实例记录（保留流量数据以供统计）- 这是最关键的操作
		if err := tx.Delete(&instance).Error; err != nil {
			return fmt.Errorf("删除实例记录失败: %v", err)
		}

		return nil
	})

	if err != nil {
		// 即使删除失败，也尝试恢复实例状态
		global.APP_LOG.Error("数据库清理失败，尝试恢复实例状态",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instanceID),
			zap.Error(err))

		// 恢复实例状态为stopped，避免卡在deleting状态
		if recoverErr := global.APP_DB.Model(&providerModel.Instance{}).
			Where("id = ?", instanceID).
			Update("status", "stopped").Error; recoverErr != nil {
			global.APP_LOG.Error("恢复实例状态失败",
				zap.Uint("instanceId", instanceID),
				zap.Error(recoverErr))
		}

		return err
	}

	// 标记任务完成
	operationType := "用户"
	if taskReq.AdminOperation {
		operationType = "管理员"
	}
	completionMessage := fmt.Sprintf("实例删除成功（%s操作）", operationType)
	if !providerDeleteSuccess {
		completionMessage = fmt.Sprintf("实例删除完成（%s操作），Provider删除可能失败但数据已清理", operationType)
	}
	stateManager := GetTaskStateManager()
	if err := stateManager.CompleteMainTask(task.ID, true, completionMessage, nil); err != nil {
		global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	global.APP_LOG.Info("实例删除成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name),
		zap.Uint("userId", instance.UserID),
		zap.String("operationType", operationType),
		zap.Bool("adminOperation", taskReq.AdminOperation),
		zap.Bool("providerDeleteSuccess", providerDeleteSuccess))

	return nil
}
