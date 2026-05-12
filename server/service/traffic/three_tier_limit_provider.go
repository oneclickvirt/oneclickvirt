package traffic

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"

	"go.uber.org/zap"
)

// ============ Provider层级流量限制 ============

// CheckAllProvidersTrafficLimit 检查所有Provider的流量限制
func (s *ThreeTierLimitService) CheckAllProvidersTrafficLimit(ctx context.Context) error {
	// 获取所有活跃Provider
	var providers []provider.Provider
	if err := global.APP_DB.Where("status IN (?)", []string{"active", "partial"}).Find(&providers).Error; err != nil {
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
		p.TrafficOverLimitAction = "stop"
	}

	// 标记Provider为受限状态
	if err := global.APP_DB.Model(&provider.Provider{}).Where("id = ?", providerID).
		Update("traffic_limited", true).Error; err != nil {
		return false, fmt.Errorf("标记Provider为受限状态失败: %w", err)
	}

	if p.TrafficOverLimitAction == "speed_limit" {
		// 限速模式：标记受限但不停机
		result := global.APP_DB.Model(&provider.Instance{}).
			Where("provider_id = ? AND status = ?", providerID, "running").
			Updates(map[string]interface{}{
				"traffic_limited":      true,
				"traffic_limit_reason": "provider",
			})

		global.APP_LOG.Info("已对Provider所有实例限速",
			zap.Uint("providerID", providerID),
			zap.Int64("影响实例数", result.RowsAffected))

		return true, nil
	}

	// 停机模式（默认）
	// 批量更新实例状态，避免逐个UPDATE
	updates := map[string]interface{}{
		"traffic_limited":      true,
		"traffic_limit_reason": "provider",
		"status":               "stopped",
	}

	result := global.APP_DB.Model(&provider.Instance{}).
		Where("provider_id = ? AND status = ?", providerID, "running").
		Updates(updates)

	if result.Error != nil {
		return false, fmt.Errorf("批量标记实例为受限状态失败: %w", result.Error)
	}

	// 获取被停止的实例ID列表用于创建任务
	var instances []provider.Instance
	if err := global.APP_DB.Select("id, user_id").
		Where("provider_id = ? AND traffic_limited = ? AND traffic_limit_reason = ?",
			providerID, true, "provider").
		Find(&instances).Error; err != nil {
		global.APP_LOG.Warn("获取受限实例列表失败", zap.Error(err))
		// 不返回错误，状态已更新，任务创建是次要的
	} else if len(instances) > 0 {
		// 批量创建停止任务
		// 这里的userID来自instance，需要特殊处理
		if err := s.batchCreateStopTasksForProvider(providerID, instances, message); err != nil {
			global.APP_LOG.Warn("批量创建实例停止任务失败",
				zap.Uint("providerID", providerID),
				zap.Int("instanceCount", len(instances)),
				zap.Error(err))
		}
	}

	global.APP_LOG.Info("已批量限制Provider所有实例",
		zap.Uint("providerID", providerID),
		zap.Int64("影响实例数", result.RowsAffected))

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

	global.APP_LOG.Info("解除Provider流量限制",
		zap.Uint("providerID", providerID),
		zap.String("reason", reason))

	return false, nil
}
