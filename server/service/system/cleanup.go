package system

import (
	"context"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"

	traffic_monitor "oneclickvirt/service/admin/traffic_monitor"
	"oneclickvirt/service/resources"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// InstanceCleanupService 实例清理服务
type InstanceCleanupService struct{}

// ResourceServiceInterface 资源服务接口
type ResourceServiceInterface interface {
	ReleaseResourcesInTx(tx *gorm.DB, providerID uint, instanceType string, cpu, memory, disk int) error
}

// PortMappingServiceInterface 端口映射服务接口
type PortMappingServiceInterface interface {
	DeleteInstancePortMappingsInTx(tx *gorm.DB, instanceID uint) error
}

// AdminServiceInterface 管理员服务接口
type AdminServiceInterface interface {
	DeleteInstance(instanceID uint) error
}

// RepairStuckInstances 修复卡住的实例状态
// 主要修复 deleting/resetting 等中间状态超时的实例
func (s *InstanceCleanupService) RepairStuckInstances() error {
	// 修复超过30分钟仍在deleting状态的实例
	cutoffTime := time.Now().Add(-30 * time.Minute)

	var stuckInstances []providerModel.Instance
	if err := global.APP_DB.Where("status IN (?) AND updated_at < ?",
		[]string{"deleting", "resetting", "creating"}, cutoffTime).Find(&stuckInstances).Error; err != nil {
		global.APP_LOG.Error("查询卡住的实例失败", zap.Error(err))
		return err
	}

	if len(stuckInstances) == 0 {
		return nil
	}

	global.APP_LOG.Warn("发现卡住的实例，尝试修复",
		zap.Int("count", len(stuckInstances)))

	for _, instance := range stuckInstances {
		var newStatus string
		switch instance.Status {
		case "deleting":
			// deleting状态超时，恢复为stopped
			newStatus = "stopped"
		case "resetting":
			// resetting状态超时，恢复为stopped
			newStatus = "stopped"
		case "creating":
			// creating状态超时，标记为failed
			newStatus = "failed"
		}

		if err := global.APP_DB.Model(&instance).Updates(map[string]interface{}{
			"status":     newStatus,
			"updated_at": time.Now(),
		}).Error; err != nil {
			global.APP_LOG.Error("修复实例状态失败",
				zap.Uint("instanceId", instance.ID),
				zap.String("oldStatus", instance.Status),
				zap.String("newStatus", newStatus),
				zap.Error(err))
			continue
		}

		global.APP_LOG.Info("成功修复卡住的实例",
			zap.Uint("instanceId", instance.ID),
			zap.String("instanceName", instance.Name),
			zap.String("oldStatus", instance.Status),
			zap.String("newStatus", newStatus),
			zap.Time("stuckSince", instance.UpdatedAt))
	}

	return nil
}

// CleanupOldFailedInstances 清理旧的失败实例（兜底机制）
// 清理超过24小时的失败实例，作为即时清理机制的兜底
func (s *InstanceCleanupService) CleanupOldFailedInstances() error {
	// 清理超过24小时的失败实例作为兜底
	cutoffTime := time.Now().Add(-24 * time.Hour)

	var failedInstances []providerModel.Instance
	if err := global.APP_DB.Where("status = ? AND created_at < ?", "failed", cutoffTime).Find(&failedInstances).Error; err != nil {
		global.APP_LOG.Error("查询旧失败实例失败", zap.Error(err))
		return err
	}

	if len(failedInstances) == 0 {
		global.APP_LOG.Debug("没有需要清理的旧失败实例")
		return nil
	}

	global.APP_LOG.Warn("发现旧的失败实例，可能即时清理机制未生效",
		zap.Int("count", len(failedInstances)))

	// 批量预加载provider信息
	var providerIDs []uint
	providerIDSet := make(map[uint]bool)
	for _, instance := range failedInstances {
		if instance.ProviderID > 0 && !providerIDSet[instance.ProviderID] {
			providerIDs = append(providerIDs, instance.ProviderID)
			providerIDSet[instance.ProviderID] = true
		}
	}

	providerMap := make(map[uint]providerModel.Provider)
	if len(providerIDs) > 0 {
		var providers []providerModel.Provider
		if err := global.APP_DB.Where("id IN ?", providerIDs).Find(&providers).Error; err == nil {
			for _, provider := range providers {
				providerMap[provider.ID] = provider
			}
		}
	}

	// 逐个清理失败实例
	for _, instance := range failedInstances {
		if err := s.cleanupSingleFailedInstance(&instance, providerMap); err != nil {
			global.APP_LOG.Error("清理旧失败实例时发生错误",
				zap.Uint("instanceId", instance.ID),
				zap.String("instanceName", instance.Name),
				zap.Error(err))
			// 继续清理其他实例
		}
	}

	global.APP_LOG.Info("旧失败实例清理完成", zap.Int("processedCount", len(failedInstances)))
	return nil
}

// cleanupSingleFailedInstance 清理单个失败实例
func (s *InstanceCleanupService) cleanupSingleFailedInstance(instance *providerModel.Instance, providerMap map[uint]providerModel.Provider) error {
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 1. 清理实例相关的端口映射等资源
		global.APP_LOG.Debug("清理失败实例端口映射",
			zap.Uint("instanceId", instance.ID))

		// 清理端口映射记录 - 使用实际的端口映射服务
		portMappingService := &resources.PortMappingService{}
		if err := portMappingService.DeleteInstancePortMappingsInTx(tx, instance.ID); err != nil {
			global.APP_LOG.Error("删除失败实例端口映射失败",
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
			// 不返回错误，继续其他清理操作
		} else {
			global.APP_LOG.Info("清理失败实例端口映射成功",
				zap.Uint("instanceId", instance.ID),
				zap.String("instanceName", instance.Name))
		}

		// 2. 释放物理资源（CPU/Memory/Disk）
		global.APP_LOG.Debug("释放失败实例物理资源",
			zap.Uint("instanceId", instance.ID),
			zap.Int("cpu", instance.CPU),
			zap.Int64("memory", instance.Memory),
			zap.Int64("disk", instance.Disk))

		resourceService := &resources.ResourceService{}
		if err := resourceService.ReleaseResourcesInTx(tx, instance.ProviderID, instance.InstanceType,
			instance.CPU, instance.Memory, instance.Disk); err != nil {
			global.APP_LOG.Error("释放失败实例物理资源失败",
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
			// 不返回错误，继续其他清理操作
		} else {
			global.APP_LOG.Info("释放失败实例物理资源成功",
				zap.Uint("instanceId", instance.ID))
		}

		// 保存需要用于日志的字段
		instanceID := instance.ID
		instanceName := instance.Name
		instanceProviderID := instance.ProviderID

		// 3. 释放资源配额（实例数量）- 使用预加载的provider数据
		global.APP_LOG.Debug("释放失败实例资源配额",
			zap.Uint("instanceId", instanceID))

		// 从预加载的map获取Provider信息
		if provider, ok := providerMap[instanceProviderID]; ok {
			if provider.UsedQuota > 0 {
				newUsedQuota := provider.UsedQuota - 1
				if err := tx.Model(&providerModel.Provider{}).
					Where("id = ?", instanceProviderID).
					Update("used_quota", newUsedQuota).Error; err != nil {
					global.APP_LOG.Error("更新Provider配额失败", zap.Error(err))
				}
			}
		}

		// 4. 删除实例记录
		if err := tx.Delete(instance).Error; err != nil {
			return err
		}

		global.APP_LOG.Info("成功清理失败实例",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instanceName))

		return nil
	})
}

// CleanupExpiredInstances 清理过期实例
func (s *InstanceCleanupService) CleanupExpiredInstances() error {
	now := time.Now()

	var expiredInstances []providerModel.Instance
	if err := global.APP_DB.Where("expired_at < ? AND status NOT IN ?",
		now, []string{"deleted", "deleting"}).Find(&expiredInstances).Error; err != nil {
		global.APP_LOG.Error("查询过期实例失败", zap.Error(err))
		return err
	}

	if len(expiredInstances) == 0 {
		global.APP_LOG.Debug("没有需要清理的过期实例")
		return nil
	}

	global.APP_LOG.Info("开始清理过期实例", zap.Int("count", len(expiredInstances)))

	// 批量预加载provider信息
	var providerIDs []uint
	providerIDSet := make(map[uint]bool)
	for _, instance := range expiredInstances {
		if instance.ProviderID > 0 && !providerIDSet[instance.ProviderID] {
			providerIDs = append(providerIDs, instance.ProviderID)
			providerIDSet[instance.ProviderID] = true
		}
	}

	providerMap := make(map[uint]providerModel.Provider)
	if len(providerIDs) > 0 {
		var providers []providerModel.Provider
		if err := global.APP_DB.Where("id IN ?", providerIDs).Find(&providers).Error; err == nil {
			for _, provider := range providers {
				providerMap[provider.ID] = provider
			}
		}
	}

	// 逐个清理过期实例
	for _, instance := range expiredInstances {
		if err := s.cleanupSingleExpiredInstance(&instance, providerMap); err != nil {
			global.APP_LOG.Error("清理过期实例时发生错误",
				zap.Uint("instanceId", instance.ID),
				zap.String("instanceName", instance.Name),
				zap.Error(err))
			// 继续清理其他实例
		}
	}

	global.APP_LOG.Info("过期实例清理完成", zap.Int("processedCount", len(expiredInstances)))
	return nil
}

// cleanupSingleExpiredInstance 清理单个过期实例
func (s *InstanceCleanupService) cleanupSingleExpiredInstance(instance *providerModel.Instance, providerMap map[uint]providerModel.Provider) error {
	// 第一步：在事务外清理 pmacct 数据（可能包含SSH命令，不应在事务内）
	trafficMonitorManager := traffic_monitor.GetManager()
	deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer deleteCancel()
	if err := trafficMonitorManager.DetachMonitor(deleteCtx, instance.ID); err != nil {
		global.APP_LOG.Warn("清理过期实例pmacct数据失败",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
		// 不返回错误，继续清理数据库记录
	}

	// 第二步：在短事务内完成数据库操作
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 1. 标记实例为删除中
		if err := tx.Model(instance).Updates(map[string]interface{}{
			"status":     "deleting",
			"updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}

		// 2. 清理实例相关资源
		global.APP_LOG.Debug("清理过期实例资源",
			zap.Uint("instanceId", instance.ID))

		// 删除实例的端口映射（在事务内）
		portMappingService := resources.PortMappingService{}
		if err := portMappingService.DeleteInstancePortMappingsInTx(tx, instance.ID); err != nil {
			global.APP_LOG.Warn("删除过期实例端口映射失败",
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
			// 不返回错误，继续其他清理操作
		} else {
			global.APP_LOG.Info("成功删除过期实例端口映射",
				zap.Uint("instanceId", instance.ID))
		}

		// 保存需要用于日志的字段
		instanceID := instance.ID
		instanceName := instance.Name
		instanceExpiresAt := instance.ExpiresAt
		instanceProviderID := instance.ProviderID

		// 从预加载的map获取Provider信息并更新使用配额
		if provider, ok := providerMap[instanceProviderID]; ok {
			if provider.UsedQuota > 0 {
				newUsedQuota := provider.UsedQuota - 1
				if err := tx.Model(&providerModel.Provider{}).
					Where("id = ?", instanceProviderID).
					Update("used_quota", newUsedQuota).Error; err != nil {
					global.APP_LOG.Error("更新Provider配额失败", zap.Error(err))
				}
			}
		}

		// 3. 软删除实例记录（使用GORM的软删除）
		if err := tx.Delete(instance).Error; err != nil {
			return err
		}

		global.APP_LOG.Info("成功清理过期实例",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instanceName),
			zap.Timep("expiredAt", instanceExpiresAt))

		return nil
	})
}

// RepairUserQuotas 修复所有用户的配额（定期运行）
// 重新计算每个用户的实际资源占用，修复因重置、删除等操作导致的配额不准确问题
func (s *InstanceCleanupService) RepairUserQuotas() error {
	global.APP_LOG.Info("开始修复用户配额...")

	var users []userModel.User
	if err := global.APP_DB.Find(&users).Error; err != nil {
		global.APP_LOG.Error("查询用户列表失败", zap.Error(err))
		return err
	}

	quotaService := resources.NewQuotaService()
	repairedCount := 0
	errorCount := 0

	for _, user := range users {
		if err := quotaService.RecalculateUserQuota(user.ID); err != nil {
			global.APP_LOG.Error("修复用户配额失败",
				zap.Uint("userId", user.ID),
				zap.String("username", user.Username),
				zap.Error(err))
			errorCount++
		} else {
			repairedCount++
		}
	}

	global.APP_LOG.Info("用户配额修复完成",
		zap.Int("totalUsers", len(users)),
		zap.Int("repaired", repairedCount),
		zap.Int("errors", errorCount))

	return nil
}

// GetInstanceCleanupService 获取实例清理服务实例
func GetInstanceCleanupService() *InstanceCleanupService {
	return &InstanceCleanupService{}
}
