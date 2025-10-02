package system

import (
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
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

	for _, instance := range failedInstances {
		if err := s.cleanupSingleFailedInstance(&instance); err != nil {
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
func (s *InstanceCleanupService) cleanupSingleFailedInstance(instance *providerModel.Instance) error {
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

		// 3. 释放资源配额（实例数量）
		global.APP_LOG.Debug("释放失败实例资源配额",
			zap.Uint("instanceId", instance.ID))

		// 获取Provider信息并更新使用配额
		var provider providerModel.Provider
		if err := tx.First(&provider, instance.ProviderID).Error; err == nil {
			if provider.UsedQuota > 0 {
				newUsedQuota := provider.UsedQuota - 1
				if err := tx.Model(&provider).Update("used_quota", newUsedQuota).Error; err != nil {
					global.APP_LOG.Error("更新Provider配额失败", zap.Error(err))
				}
			}
		}

		// 4. 删除实例记录
		if err := tx.Delete(instance).Error; err != nil {
			return err
		}

		global.APP_LOG.Info("成功清理失败实例",
			zap.Uint("instanceId", instance.ID),
			zap.String("instanceName", instance.Name))

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

	for _, instance := range expiredInstances {
		if err := s.cleanupSingleExpiredInstance(&instance); err != nil {
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
func (s *InstanceCleanupService) cleanupSingleExpiredInstance(instance *providerModel.Instance) error {
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

		// 获取Provider信息并更新使用配额
		var provider providerModel.Provider
		if err := tx.First(&provider, instance.ProviderID).Error; err == nil {
			if provider.UsedQuota > 0 {
				newUsedQuota := provider.UsedQuota - 1
				if err := tx.Model(&provider).Update("used_quota", newUsedQuota).Error; err != nil {
					global.APP_LOG.Error("更新Provider配额失败", zap.Error(err))
				}
			}
		}

		// 3. 软删除实例记录（使用GORM的软删除）
		if err := tx.Delete(instance).Error; err != nil {
			return err
		}

		global.APP_LOG.Info("成功清理过期实例",
			zap.Uint("instanceId", instance.ID),
			zap.String("instanceName", instance.Name),
			zap.Time("expiredAt", instance.ExpiredAt))

		return nil
	})
}

// GetInstanceCleanupService 获取实例清理服务实例
func GetInstanceCleanupService() *InstanceCleanupService {
	return &InstanceCleanupService{}
}
