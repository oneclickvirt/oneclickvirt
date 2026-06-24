package traffic

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/service/taskgate"

	"go.uber.org/zap"
)

// ============ Provider层级流量限制 ============

// CheckAllProvidersTrafficLimit 检查所有Provider的流量限制
func (s *ThreeTierLimitService) CheckAllProvidersTrafficLimit(ctx context.Context) error {
	// 可用性口径：标准节点看 active/partial，agent 节点仅看在线状态
	var providers []provider.Provider
	if err := global.APP_DB.Where("(connection_type <> ? AND status IN (?, ?)) OR (connection_type = ? AND agent_status = ?)", "agent", "active", "partial", "agent", "online").Find(&providers).Error; err != nil {
		return fmt.Errorf("获取Provider列表失败: %w", err)
	}

	limitedCount := 0
	for _, p := range providers {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		isLimited, err := s.CheckProviderTrafficLimit(p.ID)
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

	global.APP_LOG.Debug("Provider层级流量检查完成",
		zap.Int("总Provider数", len(providers)),
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

	// 如果Provider未启用流量统计和限制，直接跳过检查
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

	// 使用统一的流量查询服务获取Provider当月流量
	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	queryService := NewQueryService()
	monthlyStats, err := queryService.GetProviderMonthlyTraffic(providerID, year, month)
	if err != nil {
		global.APP_LOG.Error("获取Provider流量失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return false, fmt.Errorf("获取Provider流量失败: %w", err)
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

		return s.limitProviderInstances(providerID, fmt.Sprintf("Provider流量超限: %dMB/%dMB", totalUsedMB, p.MaxTraffic))
	}

	// 未超限，解除Provider级限制
	if p.TrafficLimited {
		return s.unlimitProviderInstances(providerID, "Provider流量恢复正常")
	}

	return false, nil
}

// limitProviderInstances 限制Provider的所有实例
// 支持stop（停机）和speed_limit（限速）两种模式
func (s *ThreeTierLimitService) limitProviderInstances(providerID uint, message string) (bool, error) {
	// 查询Provider的流量超限动作配置
	var p provider.Provider
	if err := global.APP_DB.Select("traffic_over_limit_action, traffic_speed_limit_kbps").
		First(&p, providerID).Error; err != nil {
		global.APP_LOG.Warn("获取Provider限流配置失败，使用默认停机", zap.Error(err))
		p.TrafficOverLimitAction = provider.TrafficOverLimitActionStop
	}

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

	// 标记Provider为受限状态
	if err := global.APP_DB.Model(&provider.Provider{}).Where("id = ?", providerID).
		Update("traffic_limited", true).Error; err != nil {
		return false, fmt.Errorf("标记Provider为受限状态失败: %w", err)
	}

	if p.TrafficOverLimitAction == provider.TrafficOverLimitActionSpeedLimit {
		// 限速模式：标记受限但不停机
		result := global.APP_DB.Model(&provider.Instance{}).
			Where("id IN ?", instanceIDs).
			Updates(map[string]interface{}{
				"traffic_limited":      true,
				"traffic_limit_reason": "provider",
				"traffic_stopped":      false,
				"traffic_stopped_at":   nil,
			})
		if result.Error != nil {
			return false, fmt.Errorf("批量标记Provider限速实例失败: %w", result.Error)
		}

		global.APP_LOG.Info("已对Provider所有实例限速",
			zap.Uint("providerID", providerID),
			zap.Int64("影响实例数", result.RowsAffected))

		return true, nil
	}
	if p.TrafficOverLimitAction == provider.TrafficOverLimitActionFreeze {
		now := time.Now()
		result := global.APP_DB.Model(&provider.Instance{}).
			Where("id IN ?", instanceIDs).
			Updates(map[string]interface{}{
				"traffic_limited":      true,
				"traffic_limit_reason": "provider",
				"traffic_stopped":      false,
				"traffic_stopped_at":   nil,
				"is_frozen":            true,
				"frozen_reason":        "traffic_limit",
				"frozen_at":            now,
			})
		if result.Error != nil {
			return false, fmt.Errorf("批量冻结Provider实例失败: %w", result.Error)
		}
		global.APP_LOG.Info("已冻结Provider所有超流量实例",
			zap.Uint("providerID", providerID),
			zap.Int64("影响实例数", result.RowsAffected))
		return true, nil
	}
	if p.TrafficOverLimitAction == provider.TrafficOverLimitActionMarkOnly {
		result := global.APP_DB.Model(&provider.Instance{}).
			Where("id IN ?", instanceIDs).
			Updates(map[string]interface{}{
				"traffic_limited":      true,
				"traffic_limit_reason": "provider",
				"traffic_stopped":      false,
				"traffic_stopped_at":   nil,
			})
		if result.Error != nil {
			return false, fmt.Errorf("批量标记Provider实例失败: %w", result.Error)
		}
		global.APP_LOG.Info("已标记Provider所有超流量实例",
			zap.Uint("providerID", providerID),
			zap.Int64("影响实例数", result.RowsAffected))
		return true, nil
	}

	// 停机模式（默认）。所有有效实例进入操作锁；仅原本运行的实例进入自动恢复队列。
	updates := map[string]interface{}{
		"traffic_limited":      true,
		"traffic_limit_reason": "provider",
		"traffic_stopped":      false,
		"traffic_stopped_at":   nil,
	}

	result := global.APP_DB.Model(&provider.Instance{}).
		Where("id IN ?", instanceIDs).
		Updates(updates)

	if result.Error != nil {
		return false, fmt.Errorf("批量标记实例为受限状态失败: %w", result.Error)
	}

	if len(stopRunningIDs) > 0 {
		now := time.Now()
		if err := global.APP_DB.Model(&provider.Instance{}).
			Where("id IN ?", stopRunningIDs).
			Updates(map[string]interface{}{
				"status":             "stopped",
				"traffic_stopped":    true,
				"traffic_stopped_at": now,
			}).Error; err != nil {
			return false, fmt.Errorf("批量标记Provider运行实例为流量停机失败: %w", err)
		}
		if err := s.batchCreateStopTasksForProvider(providerID, stopInstances, message); err != nil {
			global.APP_LOG.Warn("批量创建实例停止任务失败",
				zap.Uint("providerID", providerID),
				zap.Int("instanceCount", len(stopInstances)),
				zap.Error(err))
		}
	}

	global.APP_LOG.Info("已批量限制Provider所有实例",
		zap.Uint("providerID", providerID),
		zap.Int64("影响实例数", result.RowsAffected),
		zap.Int("自动停机实例数", len(stopRunningIDs)))

	return true, nil
}

// unlimitProviderInstances 解除Provider所有实例的限制
func (s *ThreeTierLimitService) unlimitProviderInstances(providerID uint, reason string) (bool, error) {
	// 标记Provider为非受限状态
	if err := global.APP_DB.Model(&provider.Provider{}).Where("id = ?", providerID).
		Update("traffic_limited", false).Error; err != nil {
		return false, fmt.Errorf("解除Provider限制失败: %w", err)
	}

	// 解除所有因Provider层级限制的实例
	updates := map[string]interface{}{
		"traffic_limited":      false,
		"traffic_limit_reason": "",
	}

	if err := global.APP_DB.Model(&provider.Instance{}).
		Where("provider_id = ? AND traffic_limit_reason = ?", providerID, "provider").
		Updates(updates).Error; err != nil {
		return false, fmt.Errorf("解除Provider实例限制失败: %w", err)
	}
	if err := global.APP_DB.Model(&provider.Instance{}).
		Where("provider_id = ? AND traffic_limit_reason = ? AND frozen_reason = ?", providerID, "", "traffic_limit").
		Updates(map[string]interface{}{
			"is_frozen":     false,
			"frozen_reason": "",
			"frozen_at":     nil,
		}).Error; err != nil {
		return false, fmt.Errorf("解除Provider实例流量冻结失败: %w", err)
	}

	global.APP_LOG.Info("解除Provider流量限制",
		zap.Uint("providerID", providerID),
		zap.String("reason", reason))

	return false, nil
}
