package system

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"

	traffic_monitor "oneclickvirt/service/admin/traffic_monitor"
	"oneclickvirt/service/cache"
	domainService "oneclickvirt/service/domain"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/taskgate"

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

// RepairStuckInstances 确认卡住的实例状态
// 主要确认 deleting/resetting/starting 等中间状态超时的实例
func (s *InstanceCleanupService) RepairStuckInstances() error {
	// 确认超过30分钟仍在中间状态的实例
	cutoffTime := time.Now().Add(-30 * time.Minute)
	stuckStatuses := []string{
		constant.InstanceStatusDeleting,
		constant.InstanceStatusResetting,
		constant.InstanceStatusCreating,
		constant.InstanceStatusStarting,
		constant.InstanceStatusStopping,
		constant.InstanceStatusRestarting,
		constant.InstanceStatusRebuilding,
	}

	var stuckInstances []providerModel.Instance
	if err := global.APP_DB.Where("status IN (?) AND updated_at < ?",
		stuckStatuses, cutoffTime).Find(&stuckInstances).Error; err != nil {
		global.APP_LOG.Error("查询卡住的实例失败", zap.Error(err))
		return err
	}

	if len(stuckInstances) == 0 {
		return nil
	}

	global.APP_LOG.Warn("发现卡住的实例，尝试确认",
		zap.Int("count", len(stuckInstances)))

	for _, instance := range stuckInstances {
		var newStatus string
		switch instance.Status {
		case constant.InstanceStatusDeleting:
			// deleting状态超时，恢复为stopped
			newStatus = constant.InstanceStatusStopped
		case constant.InstanceStatusResetting, constant.InstanceStatusRebuilding:
			// reset/rebuild状态超时，尽量恢复为任务记录的原始状态
			newStatus = getLatestInstanceOriginalStatus(instance.ID, []string{"reset", "rebuild"}, constant.InstanceStatusStopped)
		case constant.InstanceStatusStarting:
			newStatus = constant.InstanceStatusStopped
		case constant.InstanceStatusStopping, constant.InstanceStatusRestarting:
			newStatus = constant.InstanceStatusRunning
		case constant.InstanceStatusCreating:
			// creating状态超时：需判断是否是重置操作中创建的实例
			// 重置操作会将旧实例重命名为 "<name>_deleted_<timestamp>" 后软删除，再创建同名新实例
			// 若存在对应的软删除旧实例，说明这是重置中途中断遗留的新实例，应恢复为stopped
			var deletedCount int64
			global.APP_DB.Unscoped().Model(&providerModel.Instance{}).
				Where("name LIKE ? AND deleted_at IS NOT NULL", instance.Name+"_deleted_%").
				Count(&deletedCount)
			if deletedCount > 0 {
				// 重置操作中断，新实例实际上已在Provider侧创建（或部分创建），恢复为stopped
				newStatus = constant.InstanceStatusStopped
				global.APP_LOG.Debug("检测到重置操作遗留的creating实例，恢复为stopped",
					zap.Uint("instanceId", instance.ID),
					zap.String("instanceName", instance.Name),
					zap.Int64("deletedPredecessors", deletedCount))
			} else {
				// 普通创建超时，标记为failed
				newStatus = constant.InstanceStatusFailed
			}
		}
		if newStatus == "" {
			continue
		}

		if err := global.APP_DB.Model(&instance).Updates(map[string]interface{}{
			"status":     newStatus,
			"updated_at": time.Now(),
		}).Error; err != nil {
			global.APP_LOG.Error("确认实例状态失败",
				zap.Uint("instanceId", instance.ID),
				zap.String("oldStatus", instance.Status),
				zap.String("newStatus", newStatus),
				zap.Error(err))
			continue
		}
		cacheService := cache.GetUserCacheService()
		cacheService.InvalidateUserCache(instance.UserID)
		cacheService.InvalidateInstanceCache(instance.ID)

		global.APP_LOG.Debug("成功确认卡住的实例",
			zap.Uint("instanceId", instance.ID),
			zap.String("instanceName", instance.Name),
			zap.String("oldStatus", instance.Status),
			zap.String("newStatus", newStatus),
			zap.Time("stuckSince", instance.UpdatedAt))
	}

	return nil
}

func getLatestInstanceOriginalStatus(instanceID uint, taskTypes []string, fallback string) string {
	var task adminModel.Task
	if err := global.APP_DB.Where("instance_id = ? AND task_type IN ?", instanceID, taskTypes).
		Order("id DESC").
		First(&task).Error; err != nil {
		return fallback
	}

	var taskData map[string]interface{}
	if err := json.Unmarshal([]byte(task.TaskData), &taskData); err != nil {
		return fallback
	}
	if originalStatus, ok := taskData["originalStatus"].(string); ok && originalStatus != "" {
		return originalStatus
	}
	return fallback
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
			global.APP_LOG.Warn("清理旧失败实例时发生错误",
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
	domainSvc := &domainService.Service{}
	instanceDomains, domainErr := domainSvc.GetInstanceDomains(instance.ID)
	if domainErr != nil {
		global.APP_LOG.Warn("清理失败实例时查询域名绑定失败，继续清理实例",
			zap.Uint("instanceId", instance.ID),
			zap.Error(domainErr))
	}

	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
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
			global.APP_LOG.Debug("清理失败实例端口映射成功",
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
			global.APP_LOG.Debug("释放失败实例物理资源成功",
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

		// 4. 删除实例域名绑定记录
		if err := domainSvc.DeleteInstanceDomainsInTx(tx, instanceID); err != nil {
			return err
		}

		// 5. 删除实例记录
		if err := tx.Delete(instance).Error; err != nil {
			return err
		}

		global.APP_LOG.Info("成功清理失败实例",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instanceName))

		return nil
	}); err != nil {
		return err
	}

	domainSvc.RemoveDomainProxies(instanceDomains)
	return nil
}

// CleanupExpiredInstances 清理过期实例
func (s *InstanceCleanupService) CleanupExpiredInstances() error {
	now := time.Now()

	var expiredInstances []providerModel.Instance
	if err := global.APP_DB.Where("expires_at < ? AND status NOT IN ?",
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
			global.APP_LOG.Warn("清理过期实例时发生错误",
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
	if provider, ok := providerMap[instance.ProviderID]; ok {
		switch provider.InstanceExpiryAction {
		case providerModel.InstanceExpiryActionFreeze:
			return s.freezeExpiredInstance(instance)
		case providerModel.InstanceExpiryActionStop:
			return s.stopExpiredInstance(instance)
		case providerModel.InstanceExpiryActionExtend:
			return s.extendExpiredInstance(instance, provider.InstanceExpiryExtendDays)
		}
	}

	domainSvc := &domainService.Service{}
	instanceDomains, domainErr := domainSvc.GetInstanceDomains(instance.ID)
	if domainErr != nil {
		global.APP_LOG.Warn("清理过期实例时查询域名绑定失败，继续清理实例",
			zap.Uint("instanceId", instance.ID),
			zap.Error(domainErr))
	}

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
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
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
			global.APP_LOG.Debug("成功删除过期实例端口映射",
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

		// 3. 删除实例域名绑定记录
		if err := domainSvc.DeleteInstanceDomainsInTx(tx, instanceID); err != nil {
			return err
		}

		// 4. 软删除实例记录（使用GORM的软删除）
		if err := tx.Delete(instance).Error; err != nil {
			return err
		}

		global.APP_LOG.Debug("成功清理过期实例",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instanceName),
			zap.Timep("expiredAt", instanceExpiresAt))

		return nil
	}); err != nil {
		return err
	}

	domainSvc.RemoveDomainProxies(instanceDomains)
	return nil
}

func (s *InstanceCleanupService) freezeExpiredInstance(instance *providerModel.Instance) error {
	now := time.Now()
	return global.APP_DB.Model(&providerModel.Instance{}).
		Where("id = ?", instance.ID).
		Updates(map[string]interface{}{
			"is_frozen":     true,
			"frozen_at":     now,
			"frozen_reason": "expired",
			"updated_at":    now,
		}).Error
}

func (s *InstanceCleanupService) stopExpiredInstance(instance *providerModel.Instance) error {
	now := time.Now()
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		nextStatus := constant.InstanceStatusStopped
		needsStopTask := instance.Status == constant.InstanceStatusRunning
		if needsStopTask {
			nextStatus = constant.InstanceStatusStopping
		}

		if err := tx.Model(&providerModel.Instance{}).
			Where("id = ?", instance.ID).
			Updates(map[string]interface{}{
				"status":        nextStatus,
				"is_frozen":     true,
				"frozen_at":     now,
				"frozen_reason": "expired",
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}

		if !needsStopTask {
			return nil
		}

		var existing adminModel.Task
		if err := taskgate.EnsureAccepting(); err != nil {
			return err
		}

		err := tx.Where("instance_id = ? AND task_type = ? AND status IN ?",
			instance.ID, "stop", []string{"pending", "running", "processing"}).
			First(&existing).Error
		if err == nil {
			return nil
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}

		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d,"expiredPolicy":"stop"}`, instance.ID, instance.ProviderID)
		task := &adminModel.Task{
			TaskType:         "stop",
			Status:           "pending",
			Progress:         0,
			StatusMessage:    "实例已到期，按节点策略自动关机",
			TaskData:         taskData,
			UserID:           instance.UserID,
			ProviderID:       &instance.ProviderID,
			InstanceID:       &instance.ID,
			TimeoutDuration:  600,
			IsForceStoppable: true,
			CanForceStop:     false,
		}
		return tx.Create(task).Error
	})
	if err == nil && global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return err
}

func (s *InstanceCleanupService) extendExpiredInstance(instance *providerModel.Instance, extendDays int) error {
	if extendDays <= 0 {
		extendDays = 1
	}
	nextExpiry := time.Now().AddDate(0, 0, extendDays)
	return global.APP_DB.Model(&providerModel.Instance{}).
		Where("id = ?", instance.ID).
		Updates(map[string]interface{}{
			"expires_at":    nextExpiry,
			"is_frozen":     false,
			"frozen_at":     nil,
			"frozen_reason": "",
			"updated_at":    time.Now(),
		}).Error
}

// RepairUserQuotas 确认所有用户的配额（定期运行，批量处理，避免N+1和竞态）
// 重新计算每个用户的实际资源占用，确认因异常、删除等操作导致的配额不准确问题
func (s *InstanceCleanupService) RepairUserQuotas() error {
	global.APP_LOG.Debug("开始批量确认用户配额...")

	// 1. 获取所有用户ID（只查询ID，避免加载大量数据）
	var userIDs []uint
	if err := global.APP_DB.Model(&userModel.User{}).
		Pluck("id", &userIDs).Error; err != nil {
		global.APP_LOG.Error("查询用户ID列表失败", zap.Error(err))
		return err
	}

	if len(userIDs) == 0 {
		global.APP_LOG.Debug("没有用户需要确认配额")
		return nil
	}

	quotaService := resources.NewQuotaService()
	repairedCount := 0
	errorCount := 0

	// 2. 批量处理，每批20个用户，避免长时间锁定
	batchSize := 20
	for i := 0; i < len(userIDs); i += batchSize {
		end := i + batchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}
		batch := userIDs[i:end]

		// 3. 对每个用户单独处理（使用短事务）
		for _, userID := range batch {
			// 使用独立的短事务，避免长时间锁表
			if err := quotaService.RecalculateUserQuota(userID); err != nil {
				global.APP_LOG.Warn("确认用户配额失败",
					zap.Uint("userId", userID),
					zap.Error(err))
				errorCount++
			} else {
				repairedCount++
			}
		}

		// 4. 批次间休眠，避免对数据库造成过大压力
		if end < len(userIDs) {
			time.Sleep(100 * time.Millisecond)
		}
	}

	global.APP_LOG.Info("用户配额批量确认完成",
		zap.Int("totalUsers", len(userIDs)),
		zap.Int("repaired", repairedCount),
		zap.Int("errors", errorCount))

	return nil
}

// CleanupExpiredInstanceShareLinks removes expired or revoked temporary instance grants.
func (s *InstanceCleanupService) CleanupExpiredInstanceShareLinks() error {
	if global.APP_DB == nil {
		return nil
	}
	now := time.Now()
	result := global.APP_DB.
		Where("expires_at < ? OR revoked_at IS NOT NULL", now).
		Delete(&providerModel.InstanceShareLink{})
	if result.Error != nil {
		global.APP_LOG.Warn("清理过期实例分享链接失败", zap.Error(result.Error))
		return result.Error
	}
	if result.RowsAffected > 0 {
		global.APP_LOG.Info("已清理过期实例分享链接", zap.Int64("count", result.RowsAffected))
	}
	return nil
}

// GetInstanceCleanupService 获取实例清理服务实例
func GetInstanceCleanupService() *InstanceCleanupService {
	return &InstanceCleanupService{}
}
