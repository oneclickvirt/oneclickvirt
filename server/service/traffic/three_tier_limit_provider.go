package traffic

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/service/taskgate"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ============ Provider层级流量限制 ============

// CheckAllProvidersTrafficLimit 检查所有Provider的流量限制
func (s *ThreeTierLimitService) CheckAllProvidersTrafficLimit(ctx context.Context) error {
	const batchSize = 200
	var lastID uint
	limitedCount := 0
	totalCount := 0
	queryService := NewQueryService()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var providers []provider.Provider
		if err := global.APP_DB.
			Where("id > ? AND ((connection_type <> ? AND status IN (?, ?)) OR (connection_type = ? AND agent_status = ?))",
				lastID, "agent", "active", "partial", "agent", "online").
			Order("id ASC").
			Limit(batchSize).
			Find(&providers).Error; err != nil {
			return fmt.Errorf("获取Provider列表失败: %w", err)
		}
		if len(providers) == 0 {
			break
		}

		providerIDs := make([]uint, 0, len(providers))
		for _, p := range providers {
			if p.EnableTrafficControl && p.MaxTraffic > 0 {
				providerIDs = append(providerIDs, p.ID)
			}
		}
		statsMap := make(map[uint]*TrafficStats, len(providerIDs))
		var err error
		if len(providerIDs) > 0 {
			statsMap, err = queryService.BatchGetProvidersCurrentCycleTraffic(providerIDs)
		}
		if err != nil {
			global.APP_LOG.Warn("批量获取Provider当前流量周期失败，跳过本批次检查",
				zap.Int("batchSize", len(providers)), zap.Error(err))
			lastID = providers[len(providers)-1].ID
			totalCount += len(providers)
			if len(providers) < batchSize {
				break
			}
			continue
		}

		for _, p := range providers {
			isLimited, err := s.checkProviderTrafficLimitWithStats(p, statsMap[p.ID])
			if err != nil {
				global.APP_LOG.Warn("检查Provider流量限制失败",
					zap.Uint("providerID", p.ID),
					zap.Error(err))
				continue
			}
			if isLimited {
				limitedCount++
			}
		}

		totalCount += len(providers)
		lastID = providers[len(providers)-1].ID
		if len(providers) < batchSize {
			break
		}
	}

	global.APP_LOG.Debug("Provider层级流量检查完成",
		zap.Int("总Provider数", totalCount),
		zap.Int("超限Provider数", limitedCount))
	return nil
}

// CheckProviderTrafficLimit 检查单个Provider的流量限制
// 返回是否被限制
// 该方法假设Provider的流量数据已经通过SyncProviderInstancesTraffic更新
// 如果需要确保数据最新，调用方应先调用SyncProviderInstancesTraffic
func (s *ThreeTierLimitService) CheckProviderTrafficLimit(providerID uint) (bool, error) {
	var p provider.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return false, fmt.Errorf("获取Provider信息失败: %w", err)
	}
	if !p.EnableTrafficControl || p.MaxTraffic <= 0 {
		return s.checkProviderTrafficLimitWithStats(p, nil)
	}

	queryService := NewQueryService()
	monthlyStats, err := queryService.GetProviderCurrentCycleTraffic(providerID)
	if err != nil {
		global.APP_LOG.Error("获取Provider流量失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return false, fmt.Errorf("获取Provider流量失败: %w", err)
	}

	return s.checkProviderTrafficLimitWithStats(p, monthlyStats)
}

func (s *ThreeTierLimitService) checkProviderTrafficLimitWithStats(p provider.Provider, monthlyStats *TrafficStats) (bool, error) {
	providerID := p.ID

	if !p.EnableTrafficControl {
		// 如果之前被限制过，解除限制
		if p.TrafficLimited {
			return s.unlimitProviderInstances(providerID, "Provider已禁用流量统计和限制")
		}
		return false, nil
	}

	// checkAndResetProviderMonthlyTraffic方法已删除，流量重置由单独的调度器处理

	// 如果Provider没有流量限制，解除可能存在的限制
	if p.MaxTraffic <= 0 {
		if p.TrafficLimited {
			return s.unlimitProviderInstances(providerID, "Provider无流量限制")
		}
		return false, nil
	}

	if monthlyStats == nil {
		monthlyStats = &TrafficStats{}
	}
	totalUsedMB := int64(monthlyStats.ActualUsageMB)

	global.APP_LOG.Debug("检查Provider流量限制",
		zap.Uint("providerID", providerID),
		zap.String("providerName", p.Name),
		zap.Int64("usedTraffic", totalUsedMB),
		zap.Int64("maxTraffic", p.MaxTraffic))

	// 检查是否超限
	if totalUsedMB >= p.MaxTraffic {
		// Provider超限，停止Provider所有实例，禁止申请
		global.APP_LOG.Info("Provider流量超限",
			zap.Uint("providerID", providerID),
			zap.String("providerName", p.Name),
			zap.Int64("usedTraffic", totalUsedMB),
			zap.Int64("maxTraffic", p.MaxTraffic))

		return s.limitProviderInstances(p, fmt.Sprintf("Provider流量超限: %dMB/%dMB", totalUsedMB, p.MaxTraffic))
	}

	// 未超限，解除Provider级限制
	if p.TrafficLimited {
		return s.unlimitProviderInstances(providerID, "Provider流量恢复正常")
	}

	return false, nil
}

// limitProviderInstances 限制Provider的所有实例
// 支持stop（停机）和speed_limit（限速）两种模式
func (s *ThreeTierLimitService) limitProviderInstances(p provider.Provider, message string) (bool, error) {
	providerID := p.ID

	type providerInstance struct {
		ID     uint
		UserID uint
		Status string
	}
	var allInstances []providerInstance
	if err := global.APP_DB.Table("instances").
		Select("id, user_id, status").
		Where("provider_id = ? AND deleted_at IS NULL AND status NOT IN ?", providerID, []string{"deleted", "deleting"}).
		Find(&allInstances).Error; err != nil {
		return false, fmt.Errorf("获取Provider实例失败: %w", err)
	}

	instanceIDs := make([]uint, 0, len(allInstances))
	stopRunningIDs := make([]uint, 0)
	stopInstances := make([]provider.Instance, 0)
	for _, inst := range allInstances {
		instanceIDs = append(instanceIDs, inst.ID)
		if inst.Status == "running" {
			stopRunningIDs = append(stopRunningIDs, inst.ID)
			si := provider.Instance{UserID: inst.UserID, ProviderID: providerID}
			si.ID = inst.ID
			stopInstances = append(stopInstances, si)
		}
	}
	if len(stopRunningIDs) > 0 {
		if err := taskgate.EnsureAccepting(); err != nil {
			global.APP_LOG.Warn("任务池暂不接受任务，Provider级流量限制仅锁定实例，稍后重试停机",
				zap.Uint("providerID", providerID),
				zap.Int("instanceCount", len(stopRunningIDs)),
				zap.Error(err))
			stopRunningIDs = nil
			stopInstances = nil
		}
	}

	applyProviderAndInstanceUpdates := func(updates map[string]interface{}) (int64, error) {
		var affected int64
		err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&provider.Provider{}).Where("id = ?", providerID).
				Update("traffic_limited", true).Error; err != nil {
				return fmt.Errorf("标记Provider为受限状态失败: %w", err)
			}
			if len(instanceIDs) == 0 {
				return nil
			}
			result := tx.Model(&provider.Instance{}).Where("id IN ?", instanceIDs).Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			affected = result.RowsAffected
			return nil
		})
		return affected, err
	}

	if p.TrafficOverLimitAction == provider.TrafficOverLimitActionSpeedLimit {
		// 限速模式：标记受限但不停机
		affected, err := applyProviderAndInstanceUpdates(map[string]interface{}{
			"traffic_limited":      true,
			"traffic_limit_reason": "provider",
			"traffic_stopped":      false,
			"traffic_stopped_at":   nil,
		})
		if err != nil {
			return false, fmt.Errorf("批量标记Provider限速实例失败: %w", err)
		}

		global.APP_LOG.Info("已对Provider所有实例限速",
			zap.Uint("providerID", providerID),
			zap.Int64("影响实例数", affected))

		return true, nil
	}
	if p.TrafficOverLimitAction == provider.TrafficOverLimitActionFreeze {
		now := time.Now()
		affected, err := applyProviderAndInstanceUpdates(map[string]interface{}{
			"traffic_limited":      true,
			"traffic_limit_reason": "provider",
			"traffic_stopped":      false,
			"traffic_stopped_at":   nil,
			"is_frozen":            true,
			"frozen_reason":        "traffic_limit",
			"frozen_at":            now,
		})
		if err != nil {
			return false, fmt.Errorf("批量冻结Provider实例失败: %w", err)
		}
		global.APP_LOG.Info("已冻结Provider所有超流量实例",
			zap.Uint("providerID", providerID),
			zap.Int64("影响实例数", affected))
		return true, nil
	}
	if p.TrafficOverLimitAction == provider.TrafficOverLimitActionMarkOnly {
		affected, err := applyProviderAndInstanceUpdates(map[string]interface{}{
			"traffic_limited":      true,
			"traffic_limit_reason": "provider",
			"traffic_stopped":      false,
			"traffic_stopped_at":   nil,
		})
		if err != nil {
			return false, fmt.Errorf("批量标记Provider实例失败: %w", err)
		}
		global.APP_LOG.Info("已标记Provider所有超流量实例",
			zap.Uint("providerID", providerID),
			zap.Int64("影响实例数", affected))
		return true, nil
	}

	// 停机模式（默认）。所有有效实例进入操作锁；仅原本运行的实例进入自动恢复队列。
	updates := map[string]interface{}{
		"traffic_limited":      true,
		"traffic_limit_reason": "provider",
		"traffic_stopped":      false,
		"traffic_stopped_at":   nil,
	}

	var affected int64
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&provider.Provider{}).Where("id = ?", providerID).
			Update("traffic_limited", true).Error; err != nil {
			return fmt.Errorf("标记Provider为受限状态失败: %w", err)
		}
		if len(instanceIDs) > 0 {
			result := tx.Model(&provider.Instance{}).Where("id IN ?", instanceIDs).Updates(updates)
			if result.Error != nil {
				return fmt.Errorf("批量标记实例为受限状态失败: %w", result.Error)
			}
			affected = result.RowsAffected
		}
		if len(stopRunningIDs) > 0 {
			now := time.Now()
			if err := tx.Model(&provider.Instance{}).
				Where("id IN ?", stopRunningIDs).
				Updates(map[string]interface{}{
					"status":             "stopped",
					"traffic_stopped":    true,
					"traffic_stopped_at": now,
				}).Error; err != nil {
				return fmt.Errorf("批量标记Provider运行实例为流量停机失败: %w", err)
			}
		}
		if err := s.batchCreateStopTasksTx(tx, stopInstances, message); err != nil {
			return fmt.Errorf("批量创建实例停止任务失败: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if global.APP_SCHEDULER != nil && len(stopInstances) > 0 {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	global.APP_LOG.Info("已批量限制Provider所有实例",
		zap.Uint("providerID", providerID),
		zap.Int64("影响实例数", affected),
		zap.Int("自动停机实例数", len(stopRunningIDs)))

	return true, nil
}

// unlimitProviderInstances 解除Provider所有实例的限制
func (s *ThreeTierLimitService) unlimitProviderInstances(providerID uint, reason string) (bool, error) {
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&provider.Provider{}).Where("id = ?", providerID).
			Update("traffic_limited", false).Error; err != nil {
			return fmt.Errorf("解除Provider限制失败: %w", err)
		}
		if err := tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND traffic_limit_reason = ?", providerID, "provider").
			Updates(map[string]interface{}{
				"traffic_limited":      false,
				"traffic_limit_reason": "",
			}).Error; err != nil {
			return fmt.Errorf("解除Provider实例限制失败: %w", err)
		}
		if err := tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND traffic_limit_reason = ? AND frozen_reason = ?", providerID, "", "traffic_limit").
			Updates(map[string]interface{}{
				"is_frozen":     false,
				"frozen_reason": "",
				"frozen_at":     nil,
			}).Error; err != nil {
			return fmt.Errorf("解除Provider实例流量冻结失败: %w", err)
		}
		return nil
	}); err != nil {
		return false, err
	}

	global.APP_LOG.Info("解除Provider流量限制",
		zap.Uint("providerID", providerID),
		zap.String("reason", reason))

	return false, nil
}
