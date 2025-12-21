package provider

import (
	"context"
	"errors"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/database"
	"oneclickvirt/service/pmacct"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/service/task"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// DeleteProvider 删除Provider（级联硬删除所有相关数据）
// 默认情况下会检查是否有运行中的实例，forceDelete=true时跳过检查（用于删除离线节点）
func (s *Service) DeleteProvider(providerID uint, forceDelete bool) error {
	global.APP_LOG.Info("开始删除Provider及其所有关联数据",
		zap.Uint("providerID", providerID),
		zap.Bool("forceDelete", forceDelete))

	// 如果不是强制删除，检查是否还有运行中的实例
	// 注意：deleting/resetting等中间状态不应阻止删除，因为这些可能是卡住的状态
	if !forceDelete {
		var runningInstanceCount int64
		global.APP_DB.Model(&providerModel.Instance{}).
			Where("provider_id = ? AND status NOT IN ?", providerID,
				[]string{"deleted", "deleting", "failed", "creating", "resetting"}).
			Count(&runningInstanceCount)

		if runningInstanceCount > 0 {
			global.APP_LOG.Warn("Provider删除失败：Provider还有运行中的实例",
				zap.Uint("providerID", providerID),
				zap.Int64("runningInstanceCount", runningInstanceCount))
			return errors.New("提供商还有运行中的实例，无法删除。请先停止或删除所有实例")
		}

		// 检查是否有卡住的实例，如果有则先尝试修复
		var stuckInstanceCount int64
		global.APP_DB.Model(&providerModel.Instance{}).
			Where("provider_id = ? AND status IN ?", providerID,
				[]string{"deleting", "creating", "resetting"}).
			Count(&stuckInstanceCount)

		if stuckInstanceCount > 0 {
			global.APP_LOG.Info("发现卡住的实例，尝试修复后再删除",
				zap.Uint("providerID", providerID),
				zap.Int64("stuckInstanceCount", stuckInstanceCount))

			// 修复卡住的实例状态
			if err := global.APP_DB.Model(&providerModel.Instance{}).
				Where("provider_id = ? AND status IN ?", providerID,
					[]string{"deleting", "creating", "resetting"}).
				Updates(map[string]interface{}{
					"status":     "failed",
					"updated_at": time.Now(),
				}).Error; err != nil {
				global.APP_LOG.Warn("修复卡住的实例状态失败",
					zap.Uint("providerID", providerID),
					zap.Error(err))
			} else {
				global.APP_LOG.Info("成功修复卡住的实例状态",
					zap.Uint("providerID", providerID),
					zap.Int64("count", stuckInstanceCount))
			}
		}
	} else {
		// 强制删除模式：记录被强制删除的实例数量
		var instanceCount int64
		global.APP_DB.Model(&providerModel.Instance{}).
			Where("provider_id = ?", providerID).
			Count(&instanceCount)
		if instanceCount > 0 {
			global.APP_LOG.Warn("强制删除Provider及其所有实例",
				zap.Uint("providerID", providerID),
				zap.Int64("instanceCount", instanceCount))
		}
	}

	// 获取所有关联的实例ID（包括软删除的）
	var instanceIDs []uint
	global.APP_DB.Unscoped().Model(&providerModel.Instance{}).
		Where("provider_id = ?", providerID).
		Pluck("id", &instanceIDs)

	dbService := database.GetDatabaseService()
	err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 1. 硬删除所有关联的端口映射（包括软删除的）
		portResult := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&providerModel.Port{})
		if portResult.Error != nil {
			global.APP_LOG.Error("删除Provider端口映射失败", zap.Error(portResult.Error))
			return portResult.Error
		}
		if portResult.RowsAffected > 0 {
			global.APP_LOG.Info("成功删除Provider端口映射",
				zap.Uint("providerID", providerID),
				zap.Int64("count", portResult.RowsAffected))
		}

		// 2. 硬删除所有关联的任务（包括软删除的）
		taskResult := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&admin.Task{})
		if taskResult.Error != nil {
			global.APP_LOG.Error("删除Provider任务失败", zap.Error(taskResult.Error))
			return taskResult.Error
		}
		if taskResult.RowsAffected > 0 {
			global.APP_LOG.Info("成功删除Provider任务",
				zap.Uint("providerID", providerID),
				zap.Int64("count", taskResult.RowsAffected))
		}

		// 3. 硬删除配置任务（包括软删除的）
		configTaskResult := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&admin.ConfigurationTask{})
		if configTaskResult.Error != nil {
			global.APP_LOG.Error("删除Provider配置任务失败", zap.Error(configTaskResult.Error))
			return configTaskResult.Error
		}
		if configTaskResult.RowsAffected > 0 {
			global.APP_LOG.Info("成功删除Provider配置任务",
				zap.Uint("providerID", providerID),
				zap.Int64("count", configTaskResult.RowsAffected))
		}

		// 4. 硬删除所有实例记录（包括软删除的）
		instanceResult := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&providerModel.Instance{})
		if instanceResult.Error != nil {
			global.APP_LOG.Error("删除Provider实例记录失败", zap.Error(instanceResult.Error))
			return instanceResult.Error
		}
		if instanceResult.RowsAffected > 0 {
			global.APP_LOG.Info("成功删除Provider实例记录",
				zap.Uint("providerID", providerID),
				zap.Int64("count", instanceResult.RowsAffected))
		}

		// 5. 硬删除Provider本身
		if err := tx.Unscoped().Delete(&providerModel.Provider{}, providerID).Error; err != nil {
			global.APP_LOG.Error("删除Provider记录失败", zap.Error(err))
			return err
		}

		return nil
	})

	if err != nil {
		global.APP_LOG.Error("Provider删除事务失败", zap.Uint("providerID", providerID), zap.Error(err))
		return err
	}

	// 6. 事务外批量删除流量相关数据（避免长时间锁表）
	s.batchCleanupProviderTrafficData(providerID, instanceIDs)

	// 7. 立即清理所有相关资源（防止内存泄漏）
	s.cleanupAllProviderResources(providerID)

	global.APP_LOG.Info("Provider及所有关联数据删除成功",
		zap.Uint("providerID", providerID),
		zap.Int("instanceCount", len(instanceIDs)))
	return nil
}

// cleanupAllProviderResources 清理Provider的所有相关资源（防止内存泄漏）
// 清理顺序：先断开连接 -> 清理缓存 -> 清理工作池 -> 清理状态 -> 清理Transport
func (s *Service) cleanupAllProviderResources(providerID uint) {
	global.APP_LOG.Info("开始清理Provider的所有内存资源", zap.Uint("providerID", providerID))

	// 1. 先清理SSH连接池（断开SSH连接，避免后续操作使用过期连接）
	if global.APP_SSH_POOL != nil {
		if pool, ok := global.APP_SSH_POOL.(interface {
			Remove(uint)
		}); ok {
			pool.Remove(providerID)
			global.APP_LOG.Debug("SSH连接池已清理", zap.Uint("providerID", providerID))
		}
	}

	// 2. 从 ProviderService 中移除 Provider
	providerService.GetProviderService().RemoveProvider(providerID)
	global.APP_LOG.Debug("Provider已移除", zap.Uint("providerID", providerID))

	// 3. 清理任务工作池及其所有相关的sync.Map（同步清理pools、lastAccess、createdAt）
	if taskService := task.GetTaskService(); taskService != nil {
		taskService.DeleteProviderPool(providerID)
		global.APP_LOG.Debug("任务工作池已清理", zap.Uint("providerID", providerID))
	}

	// 4. 清理监控状态（同步清理providerStateManager和lastResetTime）
	if global.APP_MONITORING_SCHEDULER != nil {
		if scheduler, ok := global.APP_MONITORING_SCHEDULER.(interface {
			DeleteProviderState(uint)
		}); ok {
			scheduler.DeleteProviderState(providerID)
			global.APP_LOG.Debug("监控状态已清理", zap.Uint("providerID", providerID))
		}
	}

	// 5. 清理HTTP Transport（释放连接池资源，同步清理transports和providerMap）
	provider.GetTransportCleanupManager().CleanupProvider(providerID)
	global.APP_LOG.Debug("HTTP Transport已清理", zap.Uint("providerID", providerID))

	global.APP_LOG.Info("所有Provider内存资源清理完成", zap.Uint("providerID", providerID))
}

// batchCleanupProviderTrafficData 批量清理Provider的流量相关数据
func (s *Service) batchCleanupProviderTrafficData(providerID uint, instanceIDs []uint) {
	// 1. TrafficRecord表已删除，跳过流量记录清理
	global.APP_LOG.Debug("跳过流量记录清理（TrafficRecord表已删除）",
		zap.Uint("providerID", providerID))

	// 2. 不删除Provider的pmacct流量记录，保留历史数据用于统计
	// 即使Provider被删除，历史流量数据仍然有价值
	global.APP_LOG.Info("保留Provider pmacct流量历史记录",
		zap.Uint("providerID", providerID))

	// 3. 删除Provider的pmacct监控记录（停止后续采集）
	monitorResult := global.APP_DB.Unscoped().Where("provider_id = ?", providerID).
		Delete(&monitoring.PmacctMonitor{})
	if monitorResult.Error != nil {
		global.APP_LOG.Error("删除Provider pmacct监控记录失败",
			zap.Uint("providerID", providerID),
			zap.Error(monitorResult.Error))
	} else if monitorResult.RowsAffected > 0 {
		global.APP_LOG.Info("成功删除Provider pmacct监控记录",
			zap.Uint("providerID", providerID),
			zap.Int64("count", monitorResult.RowsAffected))
	}
}

// cleanupPmacctDataOptimized 使用预加载的数据清理pmacct
func (s *Service) cleanupPmacctDataOptimized(
	pmacctService *pmacct.Service,
	instance *providerModel.Instance,
	providerInstance provider.Provider,
) error {
	// 调用原有的清理方法，它会处理宿主机清理和数据库清理
	return pmacctService.CleanupPmacctData(instance.ID)
}
