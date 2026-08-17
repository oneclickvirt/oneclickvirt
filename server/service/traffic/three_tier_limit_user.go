package traffic

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"
	"oneclickvirt/service/taskgate"
	"oneclickvirt/service/userquota"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ============ 用户层级流量限制 ============

// CheckAllUsersTrafficLimit 检查所有用户的流量限制
// 使用游标分页避免一次性加载所有用户导致内存溢出
func (s *ThreeTierLimitService) CheckAllUsersTrafficLimit(ctx context.Context) error {
	const batchSize = 200
	var lastID uint = 0
	limitedCount := 0
	totalCount := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var users []user.User
		if err := global.APP_DB.
			Where("id > ? AND status = ?", lastID, 1).
			Order("id ASC").
			Limit(batchSize).
			Find(&users).Error; err != nil {
			return fmt.Errorf("获取用户列表失败: %w", err)
		}

		if len(users) == 0 {
			break
		}

		userIDs := make([]uint, 0, len(users))
		for _, u := range users {
			userIDs = append(userIDs, u.ID)
		}

		enabledProviderCounts, err := s.batchGetEnabledTrafficProviderCounts(userIDs)
		if err != nil {
			global.APP_LOG.Warn("批量检查用户Provider流量统计状态失败，跳过本批次用户检查",
				zap.Error(err), zap.Int("batchSize", len(users)))
			lastID = users[len(users)-1].ID
			totalCount += len(users)
			if len(users) < batchSize {
				break
			}
			continue
		}

		statsMap, err := NewQueryService().BatchGetUsersCurrentCycleTraffic(userIDs)
		if err != nil {
			global.APP_LOG.Warn("批量获取用户当前流量周期失败，跳过本批次用户检查",
				zap.Error(err), zap.Int("batchSize", len(users)))
			lastID = users[len(users)-1].ID
			totalCount += len(users)
			if len(users) < batchSize {
				break
			}
			continue
		}

		for _, u := range users {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			isLimited, err := s.checkUserTrafficLimitWithStats(u, enabledProviderCounts[u.ID], statsMap[u.ID])
			if err != nil {
				global.APP_LOG.Warn("检查用户流量限制失败",
					zap.Uint("userID", u.ID),
					zap.Error(err))
				continue
			}

			if isLimited {
				limitedCount++
			}
		}

		totalCount += len(users)
		lastID = users[len(users)-1].ID

		if len(users) < batchSize {
			break
		}
	}

	global.APP_LOG.Debug("用户层级流量检查完成",
		zap.Int("总用户数", totalCount),
		zap.Int("超限用户数", limitedCount))
	return nil
}

// CheckUserTrafficLimit 检查单个用户的流量限制
// 返回是否被限制
func (s *ThreeTierLimitService) CheckUserTrafficLimit(userID uint) (bool, error) {
	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return false, fmt.Errorf("获取用户信息失败: %w", err)
	}

	enabledProviderCounts, err := s.batchGetEnabledTrafficProviderCounts([]uint{userID})
	if err != nil {
		return false, fmt.Errorf("检查Provider流量统计状态失败: %w", err)
	}

	// 使用统一的流量查询服务，按各节点的当前重置周期汇总用户流量
	queryService := NewQueryService()
	monthlyStats, err := queryService.GetUserCurrentCycleTraffic(userID)
	if err != nil {
		return false, fmt.Errorf("获取用户流量失败: %w", err)
	}

	return s.checkUserTrafficLimitWithStats(u, enabledProviderCounts[userID], monthlyStats)
}

// CheckUsersTrafficLimit evaluates a selected user set with batched reads.
// It is used by explicit multi-user traffic refreshes so a shared node pull is
// followed by one ownership/count/statistics pass instead of N per-user reads.
// Any state-changing limit operation still owns its own short transaction.
func (s *ThreeTierLimitService) CheckUsersTrafficLimit(ctx context.Context, userIDs []uint) error {
	userIDs = normalizeTrafficUserIDs(userIDs)
	if len(userIDs) == 0 {
		return nil
	}

	var users []user.User
	if err := global.APP_DB.Where("id IN ? AND status = ?", userIDs, 1).Find(&users).Error; err != nil {
		return fmt.Errorf("获取批量用户信息失败: %w", err)
	}
	if len(users) == 0 {
		return nil
	}

	enabledProviderCounts, err := s.batchGetEnabledTrafficProviderCounts(userIDs)
	if err != nil {
		return fmt.Errorf("批量检查用户Provider流量统计状态失败: %w", err)
	}
	statsMap, err := NewQueryService().BatchGetUsersCurrentCycleTraffic(userIDs)
	if err != nil {
		return fmt.Errorf("批量获取用户当前流量周期失败: %w", err)
	}

	var result error
	for _, u := range users {
		select {
		case <-ctx.Done():
			return errors.Join(result, ctx.Err())
		default:
		}

		if _, err := s.checkUserTrafficLimitWithStats(u, enabledProviderCounts[u.ID], statsMap[u.ID]); err != nil {
			result = errors.Join(result, fmt.Errorf("检查用户 %d 流量限制: %w", u.ID, err))
			global.APP_LOG.Warn("批量检查用户流量限制失败", zap.Uint("userID", u.ID), zap.Error(err))
		}
	}
	return result
}

func normalizeTrafficUserIDs(values []uint) []uint {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[uint]struct{}, len(values))
	result := make([]uint, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (s *ThreeTierLimitService) batchGetEnabledTrafficProviderCounts(userIDs []uint) (map[uint]int64, error) {
	counts := make(map[uint]int64, len(userIDs))
	if len(userIDs) == 0 {
		return counts, nil
	}
	for _, userID := range userIDs {
		counts[userID] = 0
	}

	var rows []struct {
		UserID uint
		Count  int64
	}
	err := global.APP_DB.Table("instances").
		Select("instances.user_id, COUNT(DISTINCT instances.provider_id) as count").
		Joins("LEFT JOIN providers ON instances.provider_id = providers.id").
		Where("instances.user_id IN ?", userIDs).
		Where("providers.enable_traffic_control = ?", true).
		Group("instances.user_id").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		counts[row.UserID] = row.Count
	}
	return counts, nil
}

func (s *ThreeTierLimitService) checkUserTrafficLimitWithStats(u user.User, enabledProviderCount int64, monthlyStats *TrafficStats) (bool, error) {
	// 如果所有Provider都禁用了流量统计，解除用户层级限制
	if enabledProviderCount == 0 {
		if u.TrafficLimited {
			return s.unlimitUserInstances(u.ID, "所有Provider已禁用流量统计")
		}
		return false, nil
	}

	// checkAndResetMonthlyTraffic方法已删除，流量重置由单独的调度器处理

	// 自动同步用户流量限额
	if u.TotalTraffic == 0 {
		if levelLimits, err := userquota.ResolveLevelLimit(u.Level); err == nil && levelLimits.MaxTraffic > 0 {
			u.TotalTraffic = levelLimits.MaxTraffic
			if err := global.APP_DB.Model(&u).Update("total_traffic", u.TotalTraffic).Error; err != nil {
				global.APP_LOG.Warn("同步用户流量限额失败", zap.Error(err))
			}
		}
	}

	// 如果用户没有流量限制，解除可能存在的用户级限制
	if u.TotalTraffic <= 0 {
		if u.TrafficLimited {
			return s.unlimitUserInstances(u.ID, "用户无流量限制")
		}
		return false, nil
	}

	if monthlyStats == nil {
		monthlyStats = &TrafficStats{}
	}

	totalUsedMB := int64(monthlyStats.ActualUsageMB)

	// 检查是否超限
	if totalUsedMB >= u.TotalTraffic {
		// 用户超限，根据Provider设置决定停止或限速
		global.APP_LOG.Info("用户流量超限",
			zap.Uint("userID", u.ID),
			zap.String("username", u.Username),
			zap.Int64("usedTraffic", totalUsedMB),
			zap.Int64("totalTraffic", u.TotalTraffic))

		return s.limitUserInstances(u.ID, fmt.Sprintf("用户流量超限: %dMB/%dMB", totalUsedMB, u.TotalTraffic))
	}

	// 未超限，解除用户级限制
	if u.TrafficLimited {
		return s.unlimitUserInstances(u.ID, "用户流量恢复正常")
	}

	return false, nil
}

// limitUserInstances 限制用户的所有实例（原子性：用户状态与实例状态在同一事务中更新）
// 对于启用speed_limit的Provider下的实例只做限速标记，不停机
func (s *ThreeTierLimitService) limitUserInstances(userID uint, message string) (bool, error) {
	// 获取用户所有有效实例及其Provider限流配置。非运行实例也必须进入操作锁定状态。
	type InstanceWithAction struct {
		ID                     uint
		ProviderID             uint
		Status                 string
		TrafficOverLimitAction string
	}
	var instances []InstanceWithAction
	if err := global.APP_DB.Table("instances").
		Select("instances.id, instances.provider_id, instances.status, COALESCE(providers.traffic_over_limit_action, 'stop') as traffic_over_limit_action").
		Joins("LEFT JOIN providers ON instances.provider_id = providers.id").
		Where("instances.user_id = ? AND instances.deleted_at IS NULL AND instances.status NOT IN ?", userID, []string{"deleted", "deleting"}).
		Find(&instances).Error; err != nil {
		return false, fmt.Errorf("获取用户实例失败: %w", err)
	}

	// 分为停机、限速、冻结、仅标记实例
	var stopInstanceIDs []uint
	var stopRunningInstanceIDs []uint
	var speedLimitInstanceIDs []uint
	var freezeInstanceIDs []uint
	var markOnlyInstanceIDs []uint
	var stopInstances []provider.Instance
	for _, inst := range instances {
		switch inst.TrafficOverLimitAction {
		case provider.TrafficOverLimitActionSpeedLimit:
			speedLimitInstanceIDs = append(speedLimitInstanceIDs, inst.ID)
		case provider.TrafficOverLimitActionFreeze:
			freezeInstanceIDs = append(freezeInstanceIDs, inst.ID)
		case provider.TrafficOverLimitActionMarkOnly:
			markOnlyInstanceIDs = append(markOnlyInstanceIDs, inst.ID)
		default:
			stopInstanceIDs = append(stopInstanceIDs, inst.ID)
			if inst.Status == "running" {
				stopRunningInstanceIDs = append(stopRunningInstanceIDs, inst.ID)
				si := provider.Instance{ProviderID: inst.ProviderID, UserID: userID}
				si.ID = inst.ID
				stopInstances = append(stopInstances, si)
			}
		}
	}
	if len(stopRunningInstanceIDs) > 0 {
		if err := taskgate.EnsureAccepting(); err != nil {
			global.APP_LOG.Warn("任务池暂不接受任务，用户级流量限制仅锁定实例，稍后重试停机",
				zap.Uint("userID", userID),
				zap.Int("instanceCount", len(stopRunningInstanceIDs)),
				zap.Error(err))
			stopRunningInstanceIDs = nil
			stopInstances = nil
		}
	}

	// 在事务中原子性更新：用户状态 + 实例状态
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 标记用户为受限状态
		if err := tx.Model(&user.User{}).Where("id = ?", userID).Update("traffic_limited", true).Error; err != nil {
			return fmt.Errorf("标记用户为受限状态失败: %w", err)
		}

		// 限速实例：仅标记受限，不停机
		if len(speedLimitInstanceIDs) > 0 {
			if err := tx.Model(&provider.Instance{}).
				Where("id IN ? AND traffic_limit_reason <> ?", speedLimitInstanceIDs, "provider").
				Updates(map[string]interface{}{
					"traffic_limited":      true,
					"traffic_limit_reason": "user",
					"traffic_stopped":      false,
					"traffic_stopped_at":   nil,
				}).Error; err != nil {
				return fmt.Errorf("批量标记限速实例失败: %w", err)
			}
		}

		if len(freezeInstanceIDs) > 0 {
			now := time.Now()
			if err := tx.Model(&provider.Instance{}).
				Where("id IN ? AND traffic_limit_reason <> ?", freezeInstanceIDs, "provider").
				Updates(map[string]interface{}{
					"traffic_limited":      true,
					"traffic_limit_reason": "user",
					"traffic_stopped":      false,
					"traffic_stopped_at":   nil,
					"is_frozen":            true,
					"frozen_reason":        "traffic_limit",
					"frozen_at":            now,
				}).Error; err != nil {
				return fmt.Errorf("批量冻结超流量实例失败: %w", err)
			}
		}

		if len(markOnlyInstanceIDs) > 0 {
			if err := tx.Model(&provider.Instance{}).
				Where("id IN ? AND traffic_limit_reason <> ?", markOnlyInstanceIDs, "provider").
				Updates(map[string]interface{}{
					"traffic_limited":      true,
					"traffic_limit_reason": "user",
					"traffic_stopped":      false,
					"traffic_stopped_at":   nil,
				}).Error; err != nil {
				return fmt.Errorf("批量标记超流量实例失败: %w", err)
			}
		}

		// 停机实例：标记受限并停机
		if len(stopInstanceIDs) > 0 {
			if err := tx.Model(&provider.Instance{}).
				Where("id IN ? AND traffic_limit_reason <> ?", stopInstanceIDs, "provider").
				Updates(map[string]interface{}{
					"traffic_limited":      true,
					"traffic_limit_reason": "user",
					"traffic_stopped":      false,
					"traffic_stopped_at":   nil,
				}).Error; err != nil {
				return fmt.Errorf("批量标记停机实例失败: %w", err)
			}
		}
		if len(stopRunningInstanceIDs) > 0 {
			now := time.Now()
			if err := tx.Model(&provider.Instance{}).
				Where("id IN ? AND traffic_limit_reason <> ?", stopRunningInstanceIDs, "provider").
				Updates(map[string]interface{}{
					"status":             "stopped",
					"traffic_stopped":    true,
					"traffic_stopped_at": now,
				}).Error; err != nil {
				return fmt.Errorf("批量标记运行实例为流量停机失败: %w", err)
			}
		}
		if err := s.batchCreateStopTasksTx(tx, stopInstances, message); err != nil {
			return fmt.Errorf("批量创建实例停止任务失败: %w", err)
		}

		return nil
	})
	if err != nil {
		global.APP_LOG.Error("限制用户实例失败", zap.Uint("userID", userID), zap.Error(err))
		return false, err
	}

	// 事务提交后触发调度器
	if global.APP_SCHEDULER != nil && len(stopInstances) > 0 {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	global.APP_LOG.Info("已限制用户所有实例",
		zap.Uint("userID", userID),
		zap.Int("锁定停机策略实例数", len(stopInstanceIDs)),
		zap.Int("自动停机实例数", len(stopRunningInstanceIDs)),
		zap.Int("限速实例数", len(speedLimitInstanceIDs)),
		zap.Int("冻结实例数", len(freezeInstanceIDs)),
		zap.Int("仅标记实例数", len(markOnlyInstanceIDs)))

	return true, nil
}

// unlimitUserInstances 解除用户所有实例的限制
func (s *ThreeTierLimitService) unlimitUserInstances(userID uint, reason string) (bool, error) {
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user.User{}).Where("id = ?", userID).Update("traffic_limited", false).Error; err != nil {
			return fmt.Errorf("解除用户限制失败: %w", err)
		}
		if err := tx.Model(&provider.Instance{}).
			Where("user_id = ? AND traffic_limit_reason = ?", userID, "user").
			Updates(map[string]interface{}{
				"traffic_limited":      false,
				"traffic_limit_reason": "",
			}).Error; err != nil {
			return fmt.Errorf("解除用户实例限制失败: %w", err)
		}
		if err := tx.Model(&provider.Instance{}).
			Where("user_id = ? AND traffic_limit_reason = ? AND frozen_reason = ?", userID, "", "traffic_limit").
			Updates(map[string]interface{}{
				"is_frozen":     false,
				"frozen_reason": "",
				"frozen_at":     nil,
			}).Error; err != nil {
			return fmt.Errorf("解除用户实例流量冻结失败: %w", err)
		}
		return nil
	}); err != nil {
		return false, err
	}

	global.APP_LOG.Info("解除用户流量限制",
		zap.Uint("userID", userID),
		zap.String("reason", reason))

	return false, nil
}
