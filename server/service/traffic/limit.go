package traffic

import (
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	dashboardModel "oneclickvirt/model/dashboard"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"
	"oneclickvirt/utils"

	"go.uber.org/zap"
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
func (s *LimitService) getUserMonthlyTrafficFromPmacct(userID uint) (int64, error) {
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

	return int64(stats.ActualUsageMB), nil
}

// getProviderMonthlyTrafficFromPmacct 从聚合表获取Provider当月流量使用量
// 使用provider_traffic_histories聚合表，大幅提升性能
func (s *LimitService) getProviderMonthlyTrafficFromPmacct(providerID uint) (int64, error) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 首先检查Provider是否启用了流量统计
	var p provider.Provider
	if err := global.APP_DB.Select("enable_traffic_control, traffic_count_mode, traffic_multiplier").First(&p, providerID).Error; err != nil {
		return 0, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 如果未启用流量统计，返回0
	if !p.EnableTrafficControl {
		return 0, nil
	}

	// 使用聚合表查询，性能大幅提升
	// day=0,hour=0 表示月度汇总数据
	var totalTrafficMB float64
	query := `
		SELECT 
			CASE 
				WHEN ? = 'out' THEN pth.traffic_out * COALESCE(?, 1.0)
				WHEN ? = 'in' THEN pth.traffic_in * COALESCE(?, 1.0)
				ELSE pth.total_used * COALESCE(?, 1.0)
			END as total_traffic_mb
		FROM provider_traffic_histories pth
		WHERE pth.provider_id = ?
		  AND pth.year = ?
		  AND pth.month = ?
		  AND pth.day = 0
		  AND pth.hour = 0
		  AND pth.deleted_at IS NULL
	`

	err := global.APP_DB.Raw(query,
		p.TrafficCountMode, p.TrafficMultiplier,
		p.TrafficCountMode, p.TrafficMultiplier,
		p.TrafficMultiplier,
		providerID, year, month).Scan(&totalTrafficMB).Error
	if err != nil {
		return 0, fmt.Errorf("获取Provider月度流量失败: %w", err)
	}

	global.APP_LOG.Debug("计算Provider pmacct月度流量",
		zap.Uint("providerID", providerID),
		zap.Int("year", year),
		zap.Int("month", month),
		zap.Float64("totalTrafficMB", totalTrafficMB))

	return int64(totalTrafficMB), nil
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
			"traffic_control_enabled": false, // 标记流量统计已禁用
			"formatted": map[string]string{
				"current_usage": "0 MB",
				"total_limit":   "无限制",
			},
		}, nil
	}

	// 自动同步用户流量限额：如果TotalTraffic为0，从等级配置中获取
	if u.TotalTraffic == 0 {
		levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[u.Level]
		if exists && levelLimits.MaxTraffic > 0 {
			u.TotalTraffic = levelLimits.MaxTraffic
		}
	}

	// 获取当月流量使用量（MB 单位）
	currentMonthUsageMB, err := s.getUserMonthlyTrafficFromPmacct(userID)
	if err != nil {
		return nil, fmt.Errorf("获取当月流量使用量失败: %w", err)
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
		"current_month_usage":     currentMonthUsageMB, // 返回 MB 单位
		"yearly_usage":            yearlyUsage,
		"total_limit":             u.TotalTraffic,
		"usage_percent":           usagePercent,
		"is_limited":              u.TrafficLimited,
		"reset_time":              u.TrafficResetAt,
		"history":                 history,
		"traffic_control_enabled": true, // 标记流量统计已启用
		"formatted": map[string]string{
			"current_usage": utils.FormatMB(float64(currentMonthUsageMB)),
			"total_limit":   utils.FormatMB(float64(u.TotalTraffic)),
		},
	}, nil
}

// hasAnyProviderWithTrafficControlEnabled 检查用户的实例是否有任何Provider启用了流量统计
func (s *LimitService) hasAnyProviderWithTrafficControlEnabled(userID uint) (bool, error) {
	var count int64
	err := global.APP_DB.Table("instances").
		Joins("LEFT JOIN providers ON instances.provider_id = providers.id").
		Where("instances.user_id = ?", userID).
		Where("providers.enable_traffic_control = ?", true).
		Count(&count).Error

	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// getUserYearlyTrafficFromPmacct 从pmacct数据获取用户年度流量使用量
func (s *LimitService) getUserYearlyTrafficFromPmacct(userID uint) (int64, error) {
	// 获取用户所有实例（包含软删除的实例）
	var instances []provider.Instance
	err := global.APP_DB.Unscoped().
		Where("user_id = ?", userID).
		Limit(1000). // 限制最多1000个实例
		Find(&instances).Error
	if err != nil {
		return 0, fmt.Errorf("获取用户实例列表失败: %w", err)
	}

	if len(instances) == 0 {
		return 0, nil
	}

	// 收集所有实例ID
	instanceIDs := make([]uint, 0, len(instances))
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}

	// 一次性批量查询所有实例的年度流量（当前年度）
	// 处理pmacct重启导致的累积值重置问题
	var totalTrafficMB float64
	currentYear := time.Now().Year()
	err = global.APP_DB.Raw(`
		SELECT COALESCE(SUM(segment_total), 0) / 1048576.0
		FROM (
			-- 对每个instance按segment求和（处理pmacct重启）
			SELECT 
				instance_id,
				SUM(max_rx + max_tx) as segment_total
			FROM (
				-- 检测重启并分段，每段取MAX
				SELECT 
					instance_id,
					segment_id,
					MAX(rx_bytes) as max_rx,
					MAX(tx_bytes) as max_tx
				FROM (
					-- 计算每条记录的segment_id（累积重启次数）
					SELECT 
						t1.instance_id,
						t1.rx_bytes,
						t1.tx_bytes,
						(
							SELECT COUNT(*)
							FROM pmacct_traffic_records t2
							LEFT JOIN pmacct_traffic_records t3 ON t2.instance_id = t3.instance_id 
								AND t3.timestamp = (
									SELECT MAX(timestamp) 
									FROM pmacct_traffic_records 
									WHERE instance_id = t2.instance_id 
										AND timestamp < t2.timestamp
										AND year = ?
								)
							WHERE t2.instance_id = t1.instance_id
								AND t2.year = ?
								AND t2.timestamp <= t1.timestamp
								AND (
									(t3.rx_bytes IS NOT NULL AND t2.rx_bytes < t3.rx_bytes)
									OR
									(t3.tx_bytes IS NOT NULL AND t2.tx_bytes < t3.tx_bytes)
								)
						) as segment_id
					FROM pmacct_traffic_records t1
					WHERE t1.instance_id IN ? AND t1.year = ?
				) AS segments
				GROUP BY instance_id, segment_id
			) AS instance_segments
			GROUP BY instance_id
		) AS instance_totals
	`, currentYear, currentYear, instanceIDs, currentYear).Scan(&totalTrafficMB).Error

	if err != nil {
		return 0, fmt.Errorf("获取用户年度流量失败: %w", err)
	}

	return int64(totalTrafficMB), nil
}

// getUserTrafficHistoryFromPmacct 从聚合表获取用户流量历史
// 使用user_traffic_histories聚合表，大幅提升性能
func (s *LimitService) getUserTrafficHistoryFromPmacct(userID uint, months int) ([]map[string]interface{}, error) {
	now := time.Now()
	history := make([]map[string]interface{}, 0, months)

	// 获取最近N个月的数据
	for i := 0; i < months; i++ {
		targetTime := now.AddDate(0, -i, 0)
		year := targetTime.Year()
		month := int(targetTime.Month())

		// 使用聚合表查询用户月度流量，考虑不同provider的流量模式
		var monthlyTraffic float64
		err := global.APP_DB.Raw(`
			SELECT COALESCE(SUM(
				CASE 
					WHEN p.traffic_count_mode = 'out' THEN ith.traffic_out * COALESCE(p.traffic_multiplier, 1.0)
					WHEN p.traffic_count_mode = 'in' THEN ith.traffic_in * COALESCE(p.traffic_multiplier, 1.0)
					ELSE ith.total_used * COALESCE(p.traffic_multiplier, 1.0)
				END
			), 0)
			FROM instance_traffic_histories ith
			INNER JOIN instances i ON ith.instance_id = i.id AND i.deleted_at IS NULL
			INNER JOIN providers p ON ith.provider_id = p.id AND p.deleted_at IS NULL
			WHERE ith.user_id = ?
			  AND ith.year = ?
			  AND ith.month = ?
			  AND ith.day = 0
			  AND ith.hour = 0
			  AND p.enable_traffic_control = true
			  AND ith.deleted_at IS NULL
		`, userID, year, month).Scan(&monthlyTraffic).Error

		if err != nil {
			global.APP_LOG.Warn("查询月度流量失败",
				zap.Int("year", year),
				zap.Int("month", month),
				zap.Error(err))
			monthlyTraffic = 0
		}

		history = append(history, map[string]interface{}{
			"year":    year,
			"month":   month,
			"traffic": int64(monthlyTraffic * 1024 * 1024), // 转换为字节保持兼容性
			"date":    fmt.Sprintf("%d-%02d", year, month),
		})
	}

	return history, nil
}

// GetSystemTrafficStats 获取系统全局流量统计
func (s *LimitService) GetSystemTrafficStats() (map[string]interface{}, error) {
	// 获取当前时间
	now := time.Now()
	year, month, _ := now.Date()

	// 获取系统总流量（所有实例本月流量总和）
	// 兼容MySQL 5.x：直接取MAX累积值
	var totalTraffic dashboardModel.TrafficStats

	err := global.APP_DB.Raw(`
		SELECT 
			COALESCE(SUM(max_rx), 0) as total_rx, 
			COALESCE(SUM(max_tx), 0) as total_tx, 
			COALESCE(SUM(max_rx + max_tx), 0) as total_bytes
		FROM (
			SELECT 
				instance_id,
				MAX(rx_bytes) as max_rx,
				MAX(tx_bytes) as max_tx
			FROM pmacct_traffic_records
			WHERE year = ? AND month = ?
			GROUP BY instance_id
		) AS instance_max
	`, year, int(month)).Scan(&totalTraffic).Error

	if err != nil {
		return nil, fmt.Errorf("获取系统总流量失败: %w", err)
	}

	// 获取用户数量和受限用户数量
	var userCounts dashboardModel.UserCountStats

	err = global.APP_DB.Table("users").
		Select("COUNT(*) as total_users, SUM(CASE WHEN traffic_limited = true THEN 1 ELSE 0 END) as limited_users").
		Scan(&userCounts).Error

	if err != nil {
		return nil, fmt.Errorf("获取用户统计失败: %w", err)
	}

	// 获取Provider数量和受限Provider数量
	var providerCounts dashboardModel.ProviderCountStats

	err = global.APP_DB.Table("providers").
		Select("COUNT(*) as total_providers, SUM(CASE WHEN traffic_limited = true THEN 1 ELSE 0 END) as limited_providers").
		Scan(&providerCounts).Error

	if err != nil {
		return nil, fmt.Errorf("获取Provider统计失败: %w", err)
	}

	// 获取实例数量（排除软删除的实例）
	var instanceCount int64
	err = global.APP_DB.Model(&provider.Instance{}).Count(&instanceCount).Error
	if err != nil {
		return nil, fmt.Errorf("获取实例数量失败: %w", err)
	}

	result := map[string]interface{}{
		"period": fmt.Sprintf("%d-%02d", year, month),
		"traffic": map[string]interface{}{
			"total_rx":    totalTraffic.TotalRx,
			"total_tx":    totalTraffic.TotalTx,
			"total_bytes": totalTraffic.TotalBytes,
			"formatted": map[string]string{
				"total_rx":    utils.FormatBytes(totalTraffic.TotalRx),
				"total_tx":    utils.FormatBytes(totalTraffic.TotalTx),
				"total_bytes": utils.FormatBytes(totalTraffic.TotalBytes),
			},
		},
		"users": map[string]interface{}{
			"total":           userCounts.TotalUsers,
			"limited":         userCounts.LimitedUsers,
			"limited_percent": float64(userCounts.LimitedUsers) / float64(userCounts.TotalUsers) * 100,
		},
		"providers": map[string]interface{}{
			"total":           providerCounts.TotalProviders,
			"limited":         providerCounts.LimitedProviders,
			"limited_percent": float64(providerCounts.LimitedProviders) / float64(providerCounts.TotalProviders) * 100,
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

	var monthlyTrafficMB int64
	// 如果未启用流量统计，流量使用量为0
	if !p.EnableTrafficControl {
		monthlyTrafficMB = 0
	} else {
		// 获取当前月份的流量使用（MB 单位）
		var err error
		monthlyTrafficMB, err = s.getProviderMonthlyTrafficFromPmacct(providerID)
		if err != nil {
			global.APP_LOG.Warn("获取Provider pmacct月度流量失败，使用默认值",
				zap.Uint("providerID", providerID),
				zap.Error(err))
			monthlyTrafficMB = 0
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
		"current_month_usage":    monthlyTrafficMB,       // 返回 MB 单位
		"total_limit":            p.MaxTraffic,
		"usage_percent":          usagePercent,
		"is_limited":             p.TrafficLimited,
		"reset_time":             p.TrafficResetAt,
		"instance_count":         instanceCount,
		"limited_instance_count": limitedInstanceCount,
		"data_source":            "pmacct",
		"formatted": map[string]string{
			"current_usage": utils.FormatMB(float64(monthlyTrafficMB)),
			"total_limit":   utils.FormatMB(float64(p.MaxTraffic)),
		},
	}, nil
}

// GetUsersTrafficRanking 获取用户流量排行榜
func (s *LimitService) GetUsersTrafficRanking(page, pageSize int, username, nickname string) ([]map[string]interface{}, int64, error) {
	// 获取当前月份
	now := time.Now()
	year, month, _ := now.Date()

	// 查询用户本月流量使用排行
	type UserTrafficRank struct {
		UserID     uint       `gorm:"column:user_id"`
		Username   string     `gorm:"column:username"`
		Nickname   string     `gorm:"column:nickname"`
		MonthUsage float64    `gorm:"column:month_usage"`
		TotalLimit int64      `gorm:"column:total_limit"`
		IsLimited  bool       `gorm:"column:is_limited"`
		ResetTime  *time.Time `gorm:"column:reset_time"`
	}

	var rankings []UserTrafficRank
	var total int64

	// 构建查询条件
	whereConditions := []string{}
	whereArgs := []interface{}{}

	if username != "" {
		whereConditions = append(whereConditions, "u.username LIKE ?")
		whereArgs = append(whereArgs, "%"+username+"%")
	}
	if nickname != "" {
		whereConditions = append(whereConditions, "u.nickname LIKE ?")
		whereArgs = append(whereArgs, "%"+nickname+"%")
	}

	whereClause := ""
	if len(whereConditions) > 0 {
		whereClause = " AND " + strings.Join(whereConditions, " AND ")
	}

	// 先获取总数 - 简化查询，只统计用户表
	countQuery := `
		SELECT COUNT(*)
		FROM users u
		WHERE 1=1` + whereClause

	err := global.APP_DB.Raw(countQuery, whereArgs...).Scan(&total).Error
	if err != nil {
		return nil, 0, fmt.Errorf("获取用户流量总数失败: %w", err)
	}

	// 构建分页查询
	// 使用instance_traffic_histories聚合表，避免复杂的实时计算
	// day=0,hour=0 表示月度汇总数据
	offset := (page - 1) * pageSize

	// 查询用户月度流量汇总，关联providers表获取流量模式和倍率
	// 直接使用instance_traffic_histories表按user_id聚合
	query := `
		SELECT 
			u.id as user_id,
			u.username,
			u.nickname,
			COALESCE(traffic_data.month_usage, 0) as month_usage,
			u.total_traffic as total_limit,
			u.traffic_limited as is_limited,
			u.traffic_reset_at as reset_time
		FROM users u
		LEFT JOIN (
			SELECT 
				ith.user_id,
				SUM(
					CASE 
						WHEN p.traffic_count_mode = 'out' THEN ith.traffic_out * COALESCE(p.traffic_multiplier, 1.0)
						WHEN p.traffic_count_mode = 'in' THEN ith.traffic_in * COALESCE(p.traffic_multiplier, 1.0)
						ELSE ith.total_used * COALESCE(p.traffic_multiplier, 1.0)
					END
				) as month_usage
			FROM instance_traffic_histories ith
			INNER JOIN instances i ON ith.instance_id = i.id AND i.deleted_at IS NULL
			INNER JOIN providers p ON ith.provider_id = p.id AND p.deleted_at IS NULL
			WHERE ith.year = ?
			  AND ith.month = ?
			  AND ith.day = 0
			  AND ith.hour = 0
			  AND p.enable_traffic_control = true
			  AND ith.deleted_at IS NULL
			GROUP BY ith.user_id
		) traffic_data ON u.id = traffic_data.user_id
		WHERE 1=1` + whereClause + `
		ORDER BY month_usage DESC
		LIMIT ? OFFSET ?
	`

	queryArgs := append([]interface{}{year, int(month)}, whereArgs...)
	queryArgs = append(queryArgs, pageSize, offset)

	err = global.APP_DB.Raw(query, queryArgs...).Scan(&rankings).Error
	if err != nil {
		return nil, 0, fmt.Errorf("获取用户流量排行失败: %w", err)
	}

	// 格式化结果
	result := make([]map[string]interface{}, 0, len(rankings))
	// 计算起始排名
	startRank := (page - 1) * pageSize
	for i, rank := range rankings {
		var usagePercent float64 = 0
		if rank.TotalLimit > 0 {
			// rank.MonthUsage 和 rank.TotalLimit 都是 MB 单位，直接计算百分比
			usagePercent = (rank.MonthUsage / float64(rank.TotalLimit)) * 100
		}

		result = append(result, map[string]interface{}{
			"rank":          startRank + i + 1,
			"user_id":       rank.UserID,
			"username":      rank.Username,
			"nickname":      rank.Nickname,
			"month_usage":   rank.MonthUsage * 1024 * 1024, // 转换为字节以保持前端兼容性
			"total_limit":   rank.TotalLimit,
			"usage_percent": usagePercent,
			"is_limited":    rank.IsLimited,
			"reset_time":    rank.ResetTime,
			"formatted": map[string]string{
				"month_usage": utils.FormatMB(float64(rank.MonthUsage)),
				"total_limit": utils.FormatMB(float64(rank.TotalLimit)),
			},
		})
	}

	return result, total, nil
}

// SetUserTrafficLimit 设置用户流量限制
func (s *LimitService) SetUserTrafficLimit(userID uint, reason string) error {
	return global.APP_DB.Model(&user.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"traffic_limited": true,
			"updated_at":      time.Now(),
		}).Error
}

// RemoveUserTrafficLimit 解除用户流量限制
func (s *LimitService) RemoveUserTrafficLimit(userID uint) error {
	return global.APP_DB.Model(&user.User{}).
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"traffic_limited": false,
			"updated_at":      time.Now(),
		}).Error
}

// SetProviderTrafficLimit 设置Provider流量限制
func (s *LimitService) SetProviderTrafficLimit(providerID uint, reason string) error {
	return global.APP_DB.Model(&provider.Provider{}).
		Where("id = ?", providerID).
		Updates(map[string]interface{}{
			"traffic_limited": true,
			"updated_at":      time.Now(),
		}).Error
}

// RemoveProviderTrafficLimit 解除Provider流量限制
func (s *LimitService) RemoveProviderTrafficLimit(providerID uint) error {
	return global.APP_DB.Model(&provider.Provider{}).
		Where("id = ?", providerID).
		Updates(map[string]interface{}{
			"traffic_limited": false,
			"updated_at":      time.Now(),
		}).Error
}

// FormatPmacctData 格式化pmacct数据显示（输入为字节）
