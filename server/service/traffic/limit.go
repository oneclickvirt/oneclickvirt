package traffic

import (
	"context"
	"fmt"
	"sort"
	"time"

	"oneclickvirt/global"
	dashboardModel "oneclickvirt/model/dashboard"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"
	"oneclickvirt/service/userquota"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// LimitService 流量统计查询服务
// 流量检查和限制功能在 three_tier_limit.go
// 本服务只负责流量数据的统计和查询
type LimitService struct {
	service *Service
}

// NewLimitService 创建流量统计查询服务
func NewLimitService() *LimitService {
	return &LimitService{
		service: NewService(),
	}
}

// ============ 流量统计查询方法 ============

// getUserMonthlyTrafficFromPmacct 从pmacct数据计算用户当月流量使用量
// 只统计启用了流量统计的Provider
// pmacct重启会导致累积值重置，需要检测并分段计算
func (s *LimitService) getUserMonthlyTrafficFromPmacct(userID uint) (float64, error) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 使用QueryService的方法来获取用户月度流量（已包含重启检测逻辑）
	queryService := NewQueryService()
	stats, err := queryService.GetUserMonthlyTraffic(userID, year, month)
	if err != nil {
		return 0, fmt.Errorf("获取用户月度流量失败: %w", err)
	}

	global.APP_LOG.Debug("计算用户pmacct月度流量",
		zap.Uint("userID", userID),
		zap.Int("year", year),
		zap.Int("month", month),
		zap.Float64("actualUsageMB", stats.ActualUsageMB))

	return stats.ActualUsageMB, nil
}

// getProviderMonthlyTrafficFromPmacct 通过 QueryService 查询 Provider 当月流量。
func (s *LimitService) getProviderMonthlyTrafficFromPmacct(providerID uint) (float64, error) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	stats, err := NewQueryService().GetProviderMonthlyTraffic(providerID, year, month)
	if err != nil {
		return 0, err
	}

	global.APP_LOG.Debug("计算Provider pmacct月度流量",
		zap.Uint("providerID", providerID),
		zap.Int("year", year),
		zap.Int("month", month),
		zap.Float64("totalTrafficMB", stats.ActualUsageMB))

	return stats.ActualUsageMB, nil
}

// GetUserTrafficUsageWithPmacct 获取用户流量使用情况（基于pmacct数据）
func (s *LimitService) GetUserTrafficUsageWithPmacct(userID uint) (map[string]interface{}, error) {
	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return nil, fmt.Errorf("获取用户信息失败: %w", err)
	}

	// 检查用户的所有实例所在的Provider是否都禁用了流量统计
	hasEnabledTrafficControl, err := s.hasAnyProviderWithTrafficControlEnabled(userID)
	if err != nil {
		global.APP_LOG.Warn("检查Provider流量统计状态失败", zap.Error(err))
	}
	dataSource := trafficDataSourceNone
	if hasEnabledTrafficControl {
		dataSource, err = trafficDataSourceForUser(userID)
		if err != nil {
			global.APP_LOG.Warn("查询用户流量采集来源失败", zap.Uint("userID", userID), zap.Error(err))
			dataSource = trafficDataSourceNone
		}
	}

	// 如果所有Provider都禁用了流量统计，返回无限制状态
	if !hasEnabledTrafficControl {
		return map[string]interface{}{
			"user_id":                 userID,
			"current_month_usage":     int64(0),
			"yearly_usage":            int64(0),
			"total_limit":             int64(0), // 0表示无限制
			"usage_percent":           float64(0),
			"is_limited":              false,
			"reset_time":              nil,
			"history":                 []map[string]interface{}{},
			"rx_bytes":                int64(0),
			"tx_bytes":                int64(0),
			"total_bytes":             int64(0),
			"traffic_control_enabled": false, // 标记流量统计已禁用
			"data_source":             dataSource,
			"formatted": map[string]string{
				"current_usage": "0 MB",
				"rx":            "0 B",
				"tx":            "0 B",
				"total":         "0 B",
				"total_limit":   "无限制",
			},
		}, nil
	}

	// 自动同步用户流量限额：如果TotalTraffic为0，从等级配置中获取
	if u.TotalTraffic == 0 {
		if levelLimits, err := userquota.ResolveLevelLimit(u.Level); err == nil && levelLimits.MaxTraffic > 0 {
			u.TotalTraffic = levelLimits.MaxTraffic
		}
	}

	// 获取当前节点重置周期内的流量使用量（MB 单位）
	queryService := NewQueryService()
	currentCycleStats, err := queryService.GetUserCurrentCycleTraffic(userID)
	if err != nil {
		return nil, fmt.Errorf("获取当前周期流量使用量失败: %w", err)
	}
	// Keep the fractional MB component. Truncating this value made every
	// sub-megabyte Agent/pmacct sample appear as 0 B in the detail API while
	// the ranking (which already preserves float64) showed the real usage.
	currentMonthUsageMB := currentCycleStats.ActualUsageMB
	resetAt, err := queryService.GetUserNextTrafficResetTime(userID)
	if err != nil {
		global.APP_LOG.Warn("获取用户下一次流量重置时间失败", zap.Uint("userID", userID), zap.Error(err))
		resetAt = u.TrafficResetAt
	}

	// 获取本年度总流量使用量
	yearlyUsage, err := s.getUserYearlyTrafficFromPmacct(userID)
	if err != nil {
		global.APP_LOG.Warn("获取年度流量使用量失败", zap.Error(err))
		yearlyUsage = 0
	}

	// 计算使用百分比
	var usagePercent float64
	if u.TotalTraffic > 0 {
		usagePercent = float64(currentMonthUsageMB) / float64(u.TotalTraffic) * 100
	}

	// 获取最近6个月的流量历史
	history, err := s.getUserTrafficHistoryFromPmacct(userID, 6)
	if err != nil {
		global.APP_LOG.Warn("获取流量历史失败", zap.Error(err))
		history = []map[string]interface{}{}
	}

	return map[string]interface{}{
		"user_id":                 userID,
		"current_month_usage":     currentMonthUsageMB, // 返回 MB 单位，保留小数精度
		"yearly_usage":            yearlyUsage,
		"total_limit":             u.TotalTraffic,
		"usage_percent":           usagePercent,
		"is_limited":              u.TrafficLimited,
		"reset_time":              resetAt,
		"history":                 history,
		"traffic_control_enabled": true, // 标记流量统计已启用
		"data_source":             dataSource,
		"rx_bytes":                currentCycleStats.RxBytes,
		"tx_bytes":                currentCycleStats.TxBytes,
		"total_bytes":             currentCycleStats.TotalBytes,
		"formatted": map[string]string{
			"current_usage": utils.FormatMB(currentMonthUsageMB),
			"total_limit":   utils.FormatMB(float64(u.TotalTraffic)),
			"rx":            utils.FormatBytes(currentCycleStats.RxBytes),
			"tx":            utils.FormatBytes(currentCycleStats.TxBytes),
			"total":         utils.FormatBytes(currentCycleStats.TotalBytes),
		},
	}, nil
}

// hasAnyProviderWithTrafficControlEnabled 检查用户的实例是否有任何Provider启用了流量统计
func (s *LimitService) hasAnyProviderWithTrafficControlEnabled(userID uint) (bool, error) {
	var count int64
	err := global.APP_DB.Table("instances").
		Joins("LEFT JOIN providers ON instances.provider_id = providers.id").
		Where("instances.user_id = ? AND instances.deleted_at IS NULL", userID).
		Where("providers.enable_traffic_control = ?", true).
		Count(&count).Error

	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// getUserYearlyTrafficFromPmacct 从pmacct数据获取用户年度流量使用量（O(n) 复杂度）
func (s *LimitService) getUserYearlyTrafficFromPmacct(userID uint) (float64, error) {
	currentYear := time.Now().Year()

	// 获取用户所有实例 ID（包含软删除），游标分页避免 Limit(1000) 截断
	var instanceIDs []uint
	if err := global.APP_DB.Unscoped().Table("instances").
		Where("user_id = ?", userID).
		Pluck("id", &instanceIDs).Error; err != nil {
		return 0, fmt.Errorf("获取用户实例列表失败: %w", err)
	}
	if len(instanceIDs) == 0 {
		return 0, nil
	}

	qs := NewQueryService()
	start := time.Date(currentYear, time.January, 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(1, 0, 0)
	statsMap, err := qs.computeBatchTrafficInWindow(instanceIDs, start, end)
	if err != nil {
		return 0, fmt.Errorf("计算年度流量失败: %w", err)
	}

	var totalMB float64
	for _, stats := range statsMap {
		totalMB += stats.ActualUsageMB
	}

	return totalMB, nil
}

// getUserTrafficHistoryFromPmacct 通过 QueryService 按月重新计算用户流量历史。
func (s *LimitService) getUserTrafficHistoryFromPmacct(userID uint, months int) ([]map[string]interface{}, error) {
	now := time.Now()
	history := make([]map[string]interface{}, 0, months)
	queryService := NewQueryService()

	// 获取最近N个月的数据
	for i := 0; i < months; i++ {
		targetTime := now.AddDate(0, -i, 0)
		year := targetTime.Year()
		month := int(targetTime.Month())

		stats, err := queryService.GetUserMonthlyTraffic(userID, year, month)
		if err != nil {
			global.APP_LOG.Warn("计算月度流量失败",
				zap.Int("year", year),
				zap.Int("month", month),
				zap.Error(err))
			stats = &TrafficStats{}
		}

		history = append(history, map[string]interface{}{
			"year":       year,
			"month":      month,
			"traffic_mb": stats.ActualUsageMB, // MB单位
			"date":       fmt.Sprintf("%d-%02d", year, month),
		})
	}

	return history, nil
}

// GetSystemTrafficStats 获取系统全局流量统计
func (s *LimitService) GetSystemTrafficStats(ownerAdminIDs ...uint) (map[string]interface{}, error) {
	// 获取当前时间
	now := time.Now()
	year, month, _ := now.Date()
	ownerAdminID := uint(0)
	if len(ownerAdminIDs) > 0 {
		ownerAdminID = ownerAdminIDs[0]
	}

	// 使用当前周期实时聚合，避免月度缓存未刷新导致概览和限额判断不一致。
	type systemTrafficProvider struct {
		ID                uint   `gorm:"column:id"`
		TrafficSyncMethod string `gorm:"column:traffic_sync_method"`
	}
	var providers []systemTrafficProvider
	providerQuery := global.APP_DB.Table("providers").
		Where("enable_traffic_control = ?", true)
	if ownerAdminID > 0 {
		providerQuery = providerQuery.Where("owner_admin_id = ?", ownerAdminID)
	}
	err := providerQuery.
		Select("id, traffic_sync_method").
		Find(&providers).Error
	if err != nil {
		return nil, fmt.Errorf("query traffic-enabled providers failed: %w", err)
	}
	providerIDs := make([]uint, 0, len(providers))
	trafficMethods := make([]string, 0, len(providers))
	for _, p := range providers {
		providerIDs = append(providerIDs, p.ID)
		trafficMethods = append(trafficMethods, p.TrafficSyncMethod)
	}

	queryService := NewQueryService()
	providerStats, err := queryService.BatchGetProvidersCurrentCycleTraffic(providerIDs)
	if err != nil {
		return nil, fmt.Errorf("query system traffic failed: %w", err)
	}
	var totalTraffic dashboardModel.TrafficStats
	for _, stats := range providerStats {
		totalTraffic.TotalRx += float64(stats.RxBytes)
		totalTraffic.TotalTx += float64(stats.TxBytes)
		totalTraffic.TotalBytes += float64(stats.TotalBytes)
	}

	// 获取用户数量和受限用户数量
	var userCounts dashboardModel.UserCountStats

	userCountQuery := global.APP_DB.Table("users").Where("users.deleted_at IS NULL")
	if ownerAdminID > 0 {
		userCountQuery = userCountQuery.
			Joins("INNER JOIN instances ON instances.user_id = users.id AND instances.deleted_at IS NULL").
			Joins("INNER JOIN providers ON providers.id = instances.provider_id AND providers.owner_admin_id = ?", ownerAdminID).
			Select("COUNT(DISTINCT users.id) as total_users, COUNT(DISTINCT CASE WHEN users.traffic_limited = true THEN users.id END) as limited_users")
	} else {
		userCountQuery = userCountQuery.
			Select("COUNT(*) as total_users, SUM(CASE WHEN traffic_limited = true THEN 1 ELSE 0 END) as limited_users")
	}
	err = userCountQuery.
		Scan(&userCounts).Error

	if err != nil {
		return nil, fmt.Errorf("获取用户统计失败: %w", err)
	}

	// 获取Provider数量和受限Provider数量
	var providerCounts dashboardModel.ProviderCountStats

	providerCountQuery := global.APP_DB.Table("providers")
	if ownerAdminID > 0 {
		providerCountQuery = providerCountQuery.Where("owner_admin_id = ?", ownerAdminID)
	}
	err = providerCountQuery.
		Select("COUNT(*) as total_providers, SUM(CASE WHEN traffic_limited = true THEN 1 ELSE 0 END) as limited_providers").
		Scan(&providerCounts).Error

	if err != nil {
		return nil, fmt.Errorf("获取Provider统计失败: %w", err)
	}

	// 获取实例数量（排除软删除的实例）
	var instanceCount int64
	instanceCountQuery := global.APP_DB.Model(&provider.Instance{})
	if ownerAdminID > 0 {
		instanceCountQuery = instanceCountQuery.
			Joins("INNER JOIN providers ON providers.id = instances.provider_id").
			Where("providers.owner_admin_id = ?", ownerAdminID)
	}
	err = instanceCountQuery.Count(&instanceCount).Error
	if err != nil {
		return nil, fmt.Errorf("获取实例数量失败: %w", err)
	}

	result := map[string]interface{}{
		"period":      fmt.Sprintf("%d-%02d", year, month),
		"period_type": "current_cycle",
		"traffic": map[string]interface{}{
			"total_rx":    totalTraffic.TotalRx,
			"total_tx":    totalTraffic.TotalTx,
			"total_bytes": totalTraffic.TotalBytes,
			"data_source": trafficDataSourceFromMethods(trafficMethods),
			"formatted": map[string]string{
				"total_rx":    utils.FormatBytesFloat(totalTraffic.TotalRx),
				"total_tx":    utils.FormatBytesFloat(totalTraffic.TotalTx),
				"total_bytes": utils.FormatBytesFloat(totalTraffic.TotalBytes),
			},
		},
		"users": map[string]interface{}{
			"total":   userCounts.TotalUsers,
			"limited": userCounts.LimitedUsers,
			"limited_percent": func() float64 {
				if userCounts.TotalUsers == 0 {
					return 0
				}
				return float64(userCounts.LimitedUsers) / float64(userCounts.TotalUsers) * 100
			}(),
		},
		"providers": map[string]interface{}{
			"total":   providerCounts.TotalProviders,
			"limited": providerCounts.LimitedProviders,
			"limited_percent": func() float64 {
				if providerCounts.TotalProviders == 0 {
					return 0
				}
				return float64(providerCounts.LimitedProviders) / float64(providerCounts.TotalProviders) * 100
			}(),
		},
		"instances": instanceCount,
	}

	return result, nil
}

// GetProviderTrafficUsageWithPmacct 获取Provider流量使用情况
func (s *LimitService) GetProviderTrafficUsageWithPmacct(providerID uint) (map[string]interface{}, error) {
	// 获取Provider信息
	var p provider.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	var monthlyTrafficMB float64
	// 如果未启用流量统计，流量使用量为0
	if !p.EnableTrafficControl {
		monthlyTrafficMB = 0
	} else {
		// 获取当前节点重置周期内的流量使用（MB 单位）
		stats, err := NewQueryService().GetProviderCurrentCycleTraffic(providerID)
		if err != nil {
			global.APP_LOG.Warn("获取Provider pmacct当前周期流量失败，使用默认值",
				zap.Uint("providerID", providerID),
				zap.Error(err))
			monthlyTrafficMB = 0
		} else {
			monthlyTrafficMB = stats.ActualUsageMB
		}
	}

	// 计算使用百分比
	var usagePercent float64 = 0
	if p.MaxTraffic > 0 {
		usagePercent = float64(monthlyTrafficMB) / float64(p.MaxTraffic) * 100
	}

	// 获取Provider下的实例数量（排除软删除的实例 - 用于显示活跃实例数）
	var instanceCount int64
	err := global.APP_DB.Model(&provider.Instance{}).Where("provider_id = ?", providerID).Count(&instanceCount).Error
	if err != nil {
		return nil, fmt.Errorf("获取Provider实例数量失败: %w", err)
	}

	// 获取受限实例数量（排除软删除的实例 - 用于显示活跃受限实例数）
	var limitedInstanceCount int64
	err = global.APP_DB.Model(&provider.Instance{}).
		Where("provider_id = ? AND traffic_limited = ?", providerID, true).
		Count(&limitedInstanceCount).Error
	if err != nil {
		return nil, fmt.Errorf("获取受限实例数量失败: %w", err)
	}

	return map[string]interface{}{
		"provider_id":            providerID,
		"provider_name":          p.Name,
		"enable_traffic_control": p.EnableTrafficControl, // 添加流量统计开关状态
		"current_month_usage":    monthlyTrafficMB,       // 返回 MB 单位，保留小数精度
		"total_limit":            p.MaxTraffic,
		"usage_percent":          usagePercent,
		"is_limited":             p.TrafficLimited,
		"reset_time":             p.TrafficResetAt,
		"instance_count":         instanceCount,
		"limited_instance_count": limitedInstanceCount,
		"data_source":            trafficDataSourceForProvider(p),
		"formatted": map[string]string{
			"current_usage": utils.FormatMB(monthlyTrafficMB),
			"total_limit":   utils.FormatMB(float64(p.MaxTraffic)),
		},
	}, nil
}

// GetUsersTrafficRanking 获取用户流量排行榜
func (s *LimitService) GetUsersTrafficRanking(page, pageSize int, username, nickname string, ownerAdminIDs ...uint) ([]map[string]interface{}, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}
	ownerAdminID := uint(0)
	if len(ownerAdminIDs) > 0 {
		ownerAdminID = ownerAdminIDs[0]
	}

	type userTrafficRow struct {
		UserID     uint   `gorm:"column:user_id"`
		Username   string `gorm:"column:username"`
		Nickname   string `gorm:"column:nickname"`
		TotalLimit int64  `gorm:"column:total_limit"`
		IsLimited  bool   `gorm:"column:is_limited"`
	}

	query := global.APP_DB.Table("users u").
		Select("u.id as user_id, u.username, u.nickname, u.total_traffic as total_limit, u.traffic_limited as is_limited").
		Where("u.deleted_at IS NULL")
	if ownerAdminID > 0 {
		query = query.Where(`EXISTS (
			SELECT 1
			FROM instances scoped_instances
			INNER JOIN providers scoped_providers ON scoped_providers.id = scoped_instances.provider_id
			WHERE scoped_instances.user_id = u.id
			  AND scoped_instances.deleted_at IS NULL
			  AND scoped_providers.owner_admin_id = ?
		)`, ownerAdminID)
	}
	if username != "" {
		query = query.Where("u.username LIKE ?", "%"+username+"%")
	}
	if nickname != "" {
		query = query.Where("u.nickname LIKE ?", "%"+nickname+"%")
	}

	var users []userTrafficRow
	if err := query.Find(&users).Error; err != nil {
		return nil, 0, fmt.Errorf("query traffic ranking users failed: %w", err)
	}
	total := int64(len(users))
	if len(users) == 0 {
		return []map[string]interface{}{}, 0, nil
	}

	userIDs := make([]uint, 0, len(users))
	for _, u := range users {
		userIDs = append(userIDs, u.UserID)
	}
	// Ranking must use each instance's provider-specific current cycle. A
	// natural-month query would rank users incorrectly when providers use a
	// custom traffic reset day.
	queryService := NewQueryService()
	statsMap, err := queryService.BatchGetUsersCurrentCycleTrafficForOwner(userIDs, ownerAdminID)
	if err != nil {
		return nil, 0, fmt.Errorf("query monthly traffic ranking failed: %w", err)
	}

	sort.SliceStable(users, func(i, j int) bool {
		left := 0.0
		if stats := statsMap[users[i].UserID]; stats != nil {
			left = stats.ActualUsageMB
		}
		right := 0.0
		if stats := statsMap[users[j].UserID]; stats != nil {
			right = stats.ActualUsageMB
		}
		return left > right
	})

	offset := (page - 1) * pageSize
	if offset >= len(users) {
		return []map[string]interface{}{}, total, nil
	}
	end := offset + pageSize
	if end > len(users) {
		end = len(users)
	}
	pageUserIDs := make([]uint, 0, end-offset)
	for _, row := range users[offset:end] {
		pageUserIDs = append(pageUserIDs, row.UserID)
	}
	resetTimes, err := queryService.BatchGetUsersNextTrafficResetTime(pageUserIDs, ownerAdminID)
	if err != nil {
		return nil, 0, fmt.Errorf("query traffic reset times failed: %w", err)
	}
	dataSources := defaultTrafficDataSources(pageUserIDs)
	resolvedDataSources, err := trafficDataSourcesForUsers(pageUserIDs, ownerAdminID)
	if err != nil {
		global.APP_LOG.Warn("查询用户流量采集来源失败", zap.Error(err), zap.Int("userCount", len(pageUserIDs)))
	} else {
		dataSources = resolvedDataSources
	}

	result := make([]map[string]interface{}, 0, end-offset)
	for i, row := range users[offset:end] {
		stats := statsMap[row.UserID]
		monthUsage := 0.0
		rxBytes, txBytes, totalBytes := int64(0), int64(0), int64(0)
		if stats != nil {
			monthUsage = stats.ActualUsageMB
			rxBytes = stats.RxBytes
			txBytes = stats.TxBytes
			totalBytes = stats.TotalBytes
		}

		var usagePercent float64
		if row.TotalLimit > 0 {
			usagePercent = monthUsage / float64(row.TotalLimit) * 100
		}

		result = append(result, map[string]interface{}{
			"rank":          offset + i + 1,
			"user_id":       row.UserID,
			"username":      row.Username,
			"nickname":      row.Nickname,
			"month_usage":   monthUsage,
			"total_limit":   row.TotalLimit,
			"usage_percent": usagePercent,
			"is_limited":    row.IsLimited,
			"reset_time":    resetTimes[row.UserID],
			"rx_bytes":      rxBytes,
			"tx_bytes":      txBytes,
			"total_bytes":   totalBytes,
			"data_source":   dataSources[row.UserID],
			"formatted": map[string]string{
				"month_usage": utils.FormatMB(monthUsage),
				"total_limit": utils.FormatMB(float64(row.TotalLimit)),
			},
		})
	}

	return result, total, nil
}

// SetUserTrafficLimit 设置用户流量限制
func (s *LimitService) SetUserTrafficLimit(userID uint, reason string) error {
	now := time.Now()
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user.User{}).
			Where("id = ?", userID).
			Updates(map[string]interface{}{
				"traffic_limited": true,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&provider.Instance{}).
			Where("user_id = ? AND deleted_at IS NULL AND status NOT IN ? AND traffic_limit_reason <> ?", userID, []string{"deleted", "deleting"}, "provider").
			Updates(map[string]interface{}{
				"traffic_limited":      true,
				"traffic_limit_reason": "user",
				"traffic_stopped":      false,
				"traffic_stopped_at":   nil,
				"updated_at":           now,
			}).Error
	})
}

// RemoveUserTrafficLimit 解除用户流量限制
func (s *LimitService) RemoveUserTrafficLimit(userID uint) error {
	now := time.Now()
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user.User{}).
			Where("id = ?", userID).
			Updates(map[string]interface{}{
				"traffic_limited": false,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&provider.Instance{}).
			Where("user_id = ? AND traffic_limit_reason = ?", userID, "user").
			Updates(map[string]interface{}{
				"traffic_limited":      false,
				"traffic_limit_reason": "",
				"updated_at":           now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&provider.Instance{}).
			Where("user_id = ? AND traffic_limit_reason = ? AND frozen_reason = ?", userID, "", "traffic_limit").
			Updates(map[string]interface{}{
				"is_frozen":     false,
				"frozen_reason": "",
				"frozen_at":     nil,
			}).Error
	}); err != nil {
		return err
	}
	return NewThreeTierLimitService().RecoverTrafficStoppedInstances(context.Background())
}

// SetProviderTrafficLimit 设置Provider流量限制
func (s *LimitService) SetProviderTrafficLimit(providerID uint, reason string) error {
	now := time.Now()
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&provider.Provider{}).
			Where("id = ?", providerID).
			Updates(map[string]interface{}{
				"traffic_limited": true,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND deleted_at IS NULL AND status NOT IN ?", providerID, []string{"deleted", "deleting"}).
			Updates(map[string]interface{}{
				"traffic_limited":      true,
				"traffic_limit_reason": "provider",
				"traffic_stopped":      false,
				"traffic_stopped_at":   nil,
				"updated_at":           now,
			}).Error
	})
}

// RemoveProviderTrafficLimit 解除Provider流量限制
func (s *LimitService) RemoveProviderTrafficLimit(providerID uint) error {
	now := time.Now()
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&provider.Provider{}).
			Where("id = ?", providerID).
			Updates(map[string]interface{}{
				"traffic_limited": false,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND traffic_limit_reason = ?", providerID, "provider").
			Updates(map[string]interface{}{
				"traffic_limited":      false,
				"traffic_limit_reason": "",
				"updated_at":           now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND traffic_limit_reason = ? AND frozen_reason = ?", providerID, "", "traffic_limit").
			Updates(map[string]interface{}{
				"is_frozen":     false,
				"frozen_reason": "",
				"frozen_at":     nil,
			}).Error
	}); err != nil {
		return err
	}
	return NewThreeTierLimitService().RecoverTrafficStoppedInstances(context.Background())
}

// FormatPmacctData 格式化pmacct数据显示（输入为字节）
