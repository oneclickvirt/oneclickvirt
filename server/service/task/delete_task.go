package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/database"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/traffic"
	"oneclickvirt/service/vnstat"
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

	// 更新进度 (12%)
	s.updateTaskProgress(task.ID, 12, "正在获取实例信息...")

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

	// 更新进度 (20%)
	s.updateTaskProgress(task.ID, 20, "正在获取Provider配置...")

	// 获取Provider配置
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("获取Provider配置失败: %v", err)
	}

	// 复制副本避免共享状态，立即创建Provider字段的本地副本
	localProviderID := provider.ID
	localProviderName := provider.Name

	// 更新进度 (28%)
	s.updateTaskProgress(task.ID, 28, "正在同步流量数据...")

	// 删除前进行最后一次流量同步
	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerInstanceTrafficSync(instance.ID, "实例删除前最终同步")

	// 使用可取消的等待
	select {
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
		return fmt.Errorf("任务已取消")
	}

	// 更新进度 (40%)
	s.updateTaskProgress(task.ID, 40, "正在删除实例...")

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
			// 每次重试增加进度 (40% -> 50% -> 60% -> 70%)
			progressIncrement := 40 + (attempt-1)*10
			if progressIncrement > 75 {
				progressIncrement = 75
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
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retryDelay):
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

	// 更新进度 (85%)
	s.updateTaskProgress(task.ID, 85, "正在清理数据库记录...")

	// 在事务中删除实例记录并释放资源配额
	dbService := database.GetDatabaseService()
	quotaService := resources.NewQuotaService()

	err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// 更新进度 (92%)
		s.updateTaskProgress(task.ID, 92, "正在清理vnStat监控数据...")

		// 清理实例vnStat数据
		vnstatService := vnstat.NewService()
		if err := vnstatService.CleanupVnStatData(instance.ID); err != nil {
			global.APP_LOG.Warn("清理实例vnStat数据失败",
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		}

		// 更新进度 (95%)
		s.updateTaskProgress(task.ID, 95, "正在清理端口映射...")

		// 删除实例的端口映射
		portMappingService := resources.PortMappingService{}
		if err := portMappingService.DeleteInstancePortMappings(instance.ID); err != nil {
			global.APP_LOG.Warn("删除实例端口映射失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		}

		// 释放Provider资源
		resourceService := &resources.ResourceService{}
		if err := resourceService.ReleaseResourcesInTx(tx, instance.ProviderID, instance.InstanceType,
			instance.CPU, instance.Memory, instance.Disk); err != nil {
			global.APP_LOG.Error("释放Provider资源失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		}

		// 删除实例记录
		if err := tx.Delete(&instance).Error; err != nil {
			return fmt.Errorf("删除实例记录失败: %v", err)
		}

		// 释放用户配额
		resourceUsage := resources.ResourceUsage{
			CPU:       instance.CPU,
			Memory:    instance.Memory,
			Disk:      instance.Disk,
			Bandwidth: instance.Bandwidth,
		}

		if err := quotaService.UpdateUserQuotaAfterDeletionWithTx(tx, instance.UserID, resourceUsage); err != nil {
			return fmt.Errorf("释放用户配额失败: %v", err)
		}

		return nil
	})

	if err != nil {
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
