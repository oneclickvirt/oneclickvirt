package traffic

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"

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

		for _, u := range users {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			isLimited, err := s.CheckUserTrafficLimit(u.ID)
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

	// 检查用户的所有实例所在的Provider是否都禁用了流量统计
	var enabledProviderCount int64
	err := global.APP_DB.Table("instances").
		Joins("LEFT JOIN providers ON instances.provider_id = providers.id").
		Where("instances.user_id = ?", userID).
		Where("providers.enable_traffic_control = ?", true).
		Count(&enabledProviderCount).Error

	if err != nil {
		global.APP_LOG.Warn("检查Provider流量统计状态失败", zap.Error(err))
	}

	// 如果所有Provider都禁用了流量统计，解除用户层级限制
	if enabledProviderCount == 0 {
		if u.TrafficLimited {
			return s.unlimitUserInstances(userID, "所有Provider已禁用流量统计")
		}
		return false, nil
	}

	// checkAndResetMonthlyTraffic方法已删除，流量重置由单独的调度器处理

	// 自动同步用户流量限额
	if u.TotalTraffic == 0 {
		levelLimits, exists := global.GetAppConfig().Quota.LevelLimits[u.Level]
		if exists && levelLimits.MaxTraffic > 0 {
			u.TotalTraffic = levelLimits.MaxTraffic
			if err := global.APP_DB.Model(&u).Update("total_traffic", u.TotalTraffic).Error; err != nil {
				global.APP_LOG.Warn("同步用户流量限额失败", zap.Error(err))
			}
		}
	}

	// 如果用户没有流量限制，解除可能存在的用户级限制
	if u.TotalTraffic <= 0 {
		if u.TrafficLimited {
			return s.unlimitUserInstances(userID, "用户无流量限制")
		}
		return false, nil
	}

	// 从pmacct_traffic_records实时汇总用户当月总流量（已包含流量模式和倍率计算）
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 使用统一的流量查询服务（会自动包含软删除实例的流量统计）
	queryService := NewQueryService()
	monthlyStats, err := queryService.GetUserMonthlyTraffic(userID, year, month)
	if err != nil {
		return false, fmt.Errorf("获取用户流量失败: %w", err)
	}

	totalUsedMB := int64(monthlyStats.ActualUsageMB)

	// 检查是否超限
	if totalUsedMB >= u.TotalTraffic {
		// 用户超限，根据Provider设置决定停止或限速
		global.APP_LOG.Info("用户流量超限",
			zap.Uint("userID", userID),
			zap.String("username", u.Username),
			zap.Int64("usedTraffic", totalUsedMB),
			zap.Int64("totalTraffic", u.TotalTraffic))

		return s.limitUserInstances(userID, fmt.Sprintf("用户流量超限: %dMB/%dMB", totalUsedMB, u.TotalTraffic))
	}

	// 未超限，解除用户级限制
	if u.TrafficLimited {
		return s.unlimitUserInstances(userID, "用户流量恢复正常")
	}

	return false, nil
}

// limitUserInstances 限制用户的所有实例（原子性：用户状态与实例状态在同一事务中更新）
// 对于启用speed_limit的Provider下的实例只做限速标记，不停机
func (s *ThreeTierLimitService) limitUserInstances(userID uint, message string) (bool, error) {
	// 获取用户所有运行中实例及其Provider限流配置
	type InstanceWithAction struct {
		ID                     uint
		ProviderID             uint
		TrafficOverLimitAction string
	}
	var instances []InstanceWithAction
	if err := global.APP_DB.Table("instances").
		Select("instances.id, instances.provider_id, COALESCE(providers.traffic_over_limit_action, 'stop') as traffic_over_limit_action").
		Joins("LEFT JOIN providers ON instances.provider_id = providers.id").
		Where("instances.user_id = ? AND instances.status = ?", userID, "running").
		Find(&instances).Error; err != nil {
		return false, fmt.Errorf("获取用户运行实例失败: %w", err)
	}

	// 分为停机实例和限速实例
	var stopInstanceIDs []uint
	var speedLimitInstanceIDs []uint
	for _, inst := range instances {
		if inst.TrafficOverLimitAction == "speed_limit" {
			speedLimitInstanceIDs = append(speedLimitInstanceIDs, inst.ID)
		} else {
			stopInstanceIDs = append(stopInstanceIDs, inst.ID)
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
				Where("id IN ?", speedLimitInstanceIDs).
				Updates(map[string]interface{}{
					"traffic_limited":      true,
					"traffic_limit_reason": "user",
				}).Error; err != nil {
				return fmt.Errorf("批量标记限速实例失败: %w", err)
			}
		}

		// 停机实例：标记受限并停机
		if len(stopInstanceIDs) > 0 {
			if err := tx.Model(&provider.Instance{}).
				Where("id IN ?", stopInstanceIDs).
				Updates(map[string]interface{}{
					"traffic_limited":      true,
					"traffic_limit_reason": "user",
					"status":               "stopped",
				}).Error; err != nil {
				return fmt.Errorf("批量标记停机实例失败: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		global.APP_LOG.Error("限制用户实例失败", zap.Uint("userID", userID), zap.Error(err))
		return false, err
	}

	// 事务提交后批量创建停止任务（事务外执行，避免长事务）
	if len(stopInstanceIDs) > 0 {
		// 利用已有数据构建 provider.Instance 切片，避免重复查询
		var stopInstances []provider.Instance
		for _, inst := range instances {
			if inst.TrafficOverLimitAction != "speed_limit" {
				si := provider.Instance{ProviderID: inst.ProviderID}
				si.ID = inst.ID
				stopInstances = append(stopInstances, si)
			}
		}
		if err := s.batchCreateStopTasks(userID, stopInstances, message); err != nil {
			global.APP_LOG.Warn("批量创建实例停止任务失败",
				zap.Uint("userID", userID),
				zap.Int("instanceCount", len(stopInstances)),
				zap.Error(err))
		}
	}

	// 事务提交后触发调度器
	if global.APP_SCHEDULER != nil && len(stopInstanceIDs) > 0 {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	global.APP_LOG.Info("已限制用户所有实例",
		zap.Uint("userID", userID),
		zap.Int("停机实例数", len(stopInstanceIDs)),
		zap.Int("限速实例数", len(speedLimitInstanceIDs)))

	return true, nil
}

// unlimitUserInstances 解除用户所有实例的限制
func (s *ThreeTierLimitService) unlimitUserInstances(userID uint, reason string) (bool, error) {
	// 标记用户为非受限状态
	if err := global.APP_DB.Model(&user.User{}).Where("id = ?", userID).Update("traffic_limited", false).Error; err != nil {
		return false, fmt.Errorf("解除用户限制失败: %w", err)
	}

	// 解除所有因用户层级限制的实例
	updates := map[string]interface{}{
		"traffic_limited":      false,
		"traffic_limit_reason": "",
	}

	if err := global.APP_DB.Model(&provider.Instance{}).
		Where("user_id = ? AND traffic_limit_reason = ?", userID, "user").
		Updates(updates).Error; err != nil {
		return false, fmt.Errorf("解除用户实例限制失败: %w", err)
	}

	global.APP_LOG.Info("解除用户流量限制",
		zap.Uint("userID", userID),
		zap.String("reason", reason))

	return false, nil
}

