package traffic

import (
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	dashboardModel "oneclickvirt/model/dashboard"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"

	"go.uber.org/zap"
)

// LimitService 流量统计查询服务
// 注意：流量检查和限制功能已移至 three_tier_limit.go
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

// getUserMonthlyTrafficFromVnStat 从vnStat数据计算用户当月流量使用量
// 只统计启用了流量统计的Provider
func (s *LimitService) getUserMonthlyTrafficFromVnStat(userID uint) (int64, error) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 使用 SQL 批量查询，根据 Provider 的流量模式和倍率计算流量
	// 注意：使用子查询先对每个实例的多个接口流量进行聚合，避免重复统计
	// 只统计启用了流量统计的Provider
	var totalTrafficMB float64
	query := `
		SELECT COALESCE(SUM(
			CASE 
				WHEN p.traffic_count_mode = 'out' THEN agg.tx_bytes * COALESCE(p.traffic_multiplier, 1.0)
				WHEN p.traffic_count_mode = 'in' THEN agg.rx_bytes * COALESCE(p.traffic_multiplier, 1.0)
				ELSE (agg.rx_bytes + agg.tx_bytes) * COALESCE(p.traffic_multiplier, 1.0)
			END
		), 0) / 1048576
		FROM instances i
		LEFT JOIN providers p ON i.provider_id = p.id
		LEFT JOIN (
			SELECT instance_id, 
				   SUM(rx_bytes) as rx_bytes, 
				   SUM(tx_bytes) as tx_bytes
			FROM vnstat_traffic_records
			WHERE year = ? AND month = ? AND day = 0 AND hour = 0
			GROUP BY instance_id
		) agg ON i.id = agg.instance_id
		WHERE i.user_id = ?
		AND COALESCE(p.enable_traffic_control, true) = true
	`

	err := global.APP_DB.Raw(query, year, month, userID).Scan(&totalTrafficMB).Error
	if err != nil {
		return 0, fmt.Errorf("获取用户月度流量失败: %w", err)
	}

	global.APP_LOG.Debug("计算用户vnStat月度流量",
		zap.Uint("userID", userID),
		zap.Int("year", year),
		zap.Int("month", month),
		zap.Float64("totalTrafficMB", totalTrafficMB))

	return int64(totalTrafficMB), nil
}

// getProviderMonthlyTrafficFromVnStat 从vnStat数据计算Provider当月流量使用量
// 只统计启用了流量统计的Provider
func (s *LimitService) getProviderMonthlyTrafficFromVnStat(providerID uint) (int64, error) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 首先检查Provider是否启用了流量统计
	var p provider.Provider
	if err := global.APP_DB.Select("enable_traffic_control").First(&p, providerID).Error; err != nil {
		return 0, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 如果未启用流量统计，返回0
	if !p.EnableTrafficControl {
		return 0, nil
	}

	// 使用 SQL 批量查询，根据 Provider 的流量模式和倍率计算流量
	// 注意：使用子查询先对每个实例的多个接口流量进行聚合，避免重复统计
	var totalTrafficMB float64
	query := `
		SELECT COALESCE(SUM(
			CASE 
				WHEN p.traffic_count_mode = 'out' THEN agg.tx_bytes * COALESCE(p.traffic_multiplier, 1.0)
				WHEN p.traffic_count_mode = 'in' THEN agg.rx_bytes * COALESCE(p.traffic_multiplier, 1.0)
				ELSE (agg.rx_bytes + agg.tx_bytes) * COALESCE(p.traffic_multiplier, 1.0)
			END
		), 0) / 1048576
		FROM instances i
		LEFT JOIN providers p ON i.provider_id = p.id
		LEFT JOIN (
			SELECT instance_id, 
				   SUM(rx_bytes) as rx_bytes, 
				   SUM(tx_bytes) as tx_bytes
			FROM vnstat_traffic_records
			WHERE year = ? AND month = ? AND day = 0 AND hour = 0
			GROUP BY instance_id
		) agg ON i.id = agg.instance_id
		WHERE i.provider_id = ?
	`

	err := global.APP_DB.Raw(query, year, month, providerID).Scan(&totalTrafficMB).Error
	if err != nil {
		return 0, fmt.Errorf("获取Provider月度流量失败: %w", err)
	}

	global.APP_LOG.Debug("计算Provider vnStat月度流量",
		zap.Uint("providerID", providerID),
		zap.Int("year", year),
		zap.Int("month", month),
		zap.Float64("totalTrafficMB", totalTrafficMB))

	return int64(totalTrafficMB), nil
}

// GetUserTrafficUsageWithVnStat 获取用户流量使用情况（基于vnStat数据）
func (s *LimitService) GetUserTrafficUsageWithVnStat(userID uint) (map[string]interface{}, error) {
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
	currentMonthUsageMB, err := s.getUserMonthlyTrafficFromVnStat(userID)
	if err != nil {
		return nil, fmt.Errorf("获取当月流量使用量失败: %w", err)
	}

	// 获取本年度总流量使用量
	yearlyUsage, err := s.getUserYearlyTrafficFromVnStat(userID)
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
	history, err := s.getUserTrafficHistoryFromVnStat(userID, 6)
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
			"current_usage": FormatTrafficMB(currentMonthUsageMB),
			"total_limit":   FormatTrafficMB(int64(u.TotalTraffic)),
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

// getUserYearlyTrafficFromVnStat 从vnStat数据获取用户年度流量使用量
func (s *LimitService) getUserYearlyTrafficFromVnStat(userID uint) (int64, error) {
	// 获取用户所有实例（包含软删除的实例，因为需要统计本年已产生的流量）
	var instances []provider.Instance
	err := global.APP_DB.Unscoped().Where("user_id = ?", userID).Find(&instances).Error
	if err != nil {
		return 0, fmt.Errorf("获取用户实例列表失败: %w", err)
	}

	if len(instances) == 0 {
		return 0, nil
	}

	var totalTraffic int64

	// 遍历每个实例，获取年度流量（聚合所有接口，避免重复统计）
	for _, instance := range instances {
		// 获取实例所有接口的年度总流量聚合（使用子查询避免重复统计多个接口）
		var instanceTotalMB float64
		err := global.APP_DB.Raw(`
			SELECT COALESCE(SUM(rx_bytes + tx_bytes), 0) / 1048576.0
			FROM (
				SELECT instance_id, SUM(rx_bytes) as rx_bytes, SUM(tx_bytes) as tx_bytes
				FROM vnstat_traffic_records
				WHERE instance_id = ? AND year = 0 AND month = 0 AND day = 0 AND hour = 0
				GROUP BY instance_id
			) agg
		`, instance.ID).Scan(&instanceTotalMB).Error

		if err != nil {
			global.APP_LOG.Warn("获取实例年度流量失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			continue
		}

		totalTraffic += int64(instanceTotalMB)
	}

	return totalTraffic, nil
}

// getUserTrafficHistoryFromVnStat 从vnStat数据获取用户流量历史
func (s *LimitService) getUserTrafficHistoryFromVnStat(userID uint, months int) ([]map[string]interface{}, error) {
	// 获取用户所有实例（包含软删除的实例，因为需要统计历史流量）
	var instances []provider.Instance
	err := global.APP_DB.Unscoped().Where("user_id = ?", userID).Find(&instances).Error
	if err != nil {
		return nil, fmt.Errorf("获取用户实例列表失败: %w", err)
	}

	if len(instances) == 0 {
		return []map[string]interface{}{}, nil
	}

	now := time.Now()
	history := make([]map[string]interface{}, 0, months)

	// 获取最近N个月的数据
	for i := 0; i < months; i++ {
		targetTime := now.AddDate(0, -i, 0)
		year := targetTime.Year()
		month := int(targetTime.Month())

		var monthlyTraffic int64

		// 计算该月所有实例的流量总和
		for _, instance := range instances {
			instanceTraffic, err := s.service.getInstanceMonthlyTrafficFromVnStat(instance.ID, year, month)
			if err != nil {
				continue
			}
			monthlyTraffic += instanceTraffic
		}

		history = append(history, map[string]interface{}{
			"year":    year,
			"month":   month,
			"traffic": monthlyTraffic,
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

	// 获取系统总流量（所有实例本月流量总和，避免重复统计多个接口）
	var totalTraffic dashboardModel.TrafficStats

	err := global.APP_DB.Raw(`
		SELECT 
			COALESCE(SUM(rx_bytes), 0) as total_rx, 
			COALESCE(SUM(tx_bytes), 0) as total_tx, 
			COALESCE(SUM(rx_bytes + tx_bytes), 0) as total_bytes
		FROM (
			SELECT instance_id, SUM(rx_bytes) as rx_bytes, SUM(tx_bytes) as tx_bytes
			FROM vnstat_traffic_records
			WHERE year = ? AND month = ? AND day = 0 AND hour = 0
			GROUP BY instance_id
		) agg
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
				"total_rx":    FormatVnStatData(totalTraffic.TotalRx),
				"total_tx":    FormatVnStatData(totalTraffic.TotalTx),
				"total_bytes": FormatVnStatData(totalTraffic.TotalBytes),
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

// GetProviderTrafficUsageWithVnStat 获取Provider流量使用情况
func (s *LimitService) GetProviderTrafficUsageWithVnStat(providerID uint) (map[string]interface{}, error) {
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
		monthlyTrafficMB, err = s.getProviderMonthlyTrafficFromVnStat(providerID)
		if err != nil {
			global.APP_LOG.Warn("获取Provider vnStat月度流量失败，使用默认值",
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
		"data_source":            "vnstat",
		"formatted": map[string]string{
			"current_usage": FormatTrafficMB(monthlyTrafficMB),
			"total_limit":   FormatTrafficMB(p.MaxTraffic),
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

	// 先获取总数
	countQuery := `
		SELECT COUNT(DISTINCT u.id)
		FROM users u
		LEFT JOIN instances i ON u.id = i.user_id
		LEFT JOIN vnstat_traffic_records vr ON i.id = vr.instance_id 
			AND vr.year = ? AND vr.month = ?
		WHERE 1=1` + whereClause

	countArgs := append([]interface{}{year, int(month)}, whereArgs...)
	err := global.APP_DB.Raw(countQuery, countArgs...).Scan(&total).Error
	if err != nil {
		return nil, 0, fmt.Errorf("获取用户流量总数失败: %w", err)
	}

	// 构建分页查询
	// 根据 Provider 的流量模式计算流量：
	// - both: rx_bytes + tx_bytes（乘以倍率）
	// - out: tx_bytes（乘以倍率）
	// - in: rx_bytes（乘以倍率）
	offset := (page - 1) * pageSize
	query := `
		SELECT 
			u.id as user_id,
			u.username,
			u.nickname,
			COALESCE(SUM(
				CASE 
					WHEN p.traffic_count_mode = 'out' THEN vr.tx_bytes * COALESCE(p.traffic_multiplier, 1.0)
					WHEN p.traffic_count_mode = 'in' THEN vr.rx_bytes * COALESCE(p.traffic_multiplier, 1.0)
					ELSE (vr.rx_bytes + vr.tx_bytes) * COALESCE(p.traffic_multiplier, 1.0)
				END
			), 0) / 1048576 as month_usage,
			u.total_traffic as total_limit,
			u.traffic_limited as is_limited,
			u.traffic_reset_at as reset_time
		FROM users u
		LEFT JOIN instances i ON u.id = i.user_id
		LEFT JOIN providers p ON i.provider_id = p.id
		LEFT JOIN vnstat_traffic_records vr ON i.id = vr.instance_id 
			AND vr.year = ? AND vr.month = ? AND vr.day = 0 AND vr.hour = 0
		WHERE 1=1` + whereClause + `
		GROUP BY u.id, u.username, u.nickname, u.total_traffic, u.traffic_limited, u.traffic_reset_at
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
				"month_usage": FormatTrafficMB(int64(rank.MonthUsage)),
				"total_limit": FormatTrafficMB(rank.TotalLimit),
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

// FormatVnStatData 格式化vnStat数据显示（输入为字节）
func FormatVnStatData(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)

	if bytes >= TB {
		return fmt.Sprintf("%.2f TB", float64(bytes)/TB)
	} else if bytes >= GB {
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	} else if bytes >= MB {
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	} else if bytes >= KB {
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	}
	return fmt.Sprintf("%d B", bytes)
}

// FormatTrafficMB 格式化流量数据显示（输入为MB）
func FormatTrafficMB(mb int64) string {
	const (
		KB_IN_MB = float64(1) / 1024 // 1 MB = 1024 KB
		GB_IN_MB = 1024              // 1 GB = 1024 MB
		TB_IN_MB = 1024 * 1024       // 1 TB = 1024 * 1024 MB
	)

	if mb >= TB_IN_MB {
		return fmt.Sprintf("%.2f TB", float64(mb)/TB_IN_MB)
	} else if mb >= GB_IN_MB {
		return fmt.Sprintf("%.2f GB", float64(mb)/GB_IN_MB)
	} else if mb >= 1 {
		return fmt.Sprintf("%.2f MB", float64(mb))
	} else if mb > 0 {
		return fmt.Sprintf("%.2f KB", float64(mb)/KB_IN_MB)
	}
	return "0 B"
}
