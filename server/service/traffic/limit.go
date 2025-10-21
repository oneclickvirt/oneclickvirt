package traffic

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	dashboardModel "oneclickvirt/model/dashboard"
	monitoringModel "oneclickvirt/model/monitoring"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"

	"go.uber.org/zap"
)

// LimitService 流量限制服务 - 集成vnStat数据进行流量控制
type LimitService struct {
	service *Service
}

// NewLimitService 创建流量限制服务
func NewLimitService() *LimitService {
	return &LimitService{
		service: NewService(),
	}
}

// CheckUserTrafficLimitWithVnStat 使用vnStat数据检查用户流量限制
func (s *LimitService) CheckUserTrafficLimitWithVnStat(userID uint) (bool, string, error) {
	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return false, "", fmt.Errorf("获取用户信息失败: %w", err)
	}

	// 自动同步用户流量限额：如果TotalTraffic为0，从等级配置中获取
	if u.TotalTraffic == 0 {
		levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[u.Level]
		if exists && levelLimits.MaxTraffic > 0 {
			// 更新数据库中的TotalTraffic字段
			if err := global.APP_DB.Model(&u).Update("total_traffic", levelLimits.MaxTraffic).Error; err != nil {
				global.APP_LOG.Warn("自动同步用户流量限额失败",
					zap.Uint("userID", userID),
					zap.Error(err))
			} else {
				u.TotalTraffic = levelLimits.MaxTraffic
				global.APP_LOG.Info("自动同步用户流量限额",
					zap.Uint("userID", userID),
					zap.Int("level", u.Level),
					zap.Int64("maxTraffic", levelLimits.MaxTraffic))
			}
		}
	}

	// 检查是否需要重置流量
	if err := s.service.checkAndResetMonthlyTraffic(userID); err != nil {
		global.APP_LOG.Error("检查月度流量重置失败",
			zap.Uint("userID", userID),
			zap.Error(err))
	}
	// 重新加载用户数据
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return false, "", fmt.Errorf("重新加载用户信息失败: %w", err)
	}
	// 使用vnStat数据计算当月总流量使用量
	totalUsed, err := s.getUserMonthlyTrafficFromVnStat(userID)
	if err != nil {
		global.APP_LOG.Error("从vnStat获取用户月度流量失败",
			zap.Uint("userID", userID),
			zap.Error(err))
		// 降级到旧方法
		totalUsed, err = s.service.getUserMonthlyTrafficUsage(userID)
		if err != nil {
			return false, "", fmt.Errorf("获取用户流量使用量失败: %w", err)
		}
	}
	// 更新用户已使用流量
	if err := global.APP_DB.Model(&u).Update("used_traffic", totalUsed).Error; err != nil {
		return false, "", fmt.Errorf("更新用户流量使用量失败: %w", err)
	}
	// 检查是否超限（仅在有有效的流量限制时进行检查）
	if u.TotalTraffic > 0 && totalUsed >= u.TotalTraffic {
		// 超限，标记用户为受限状态
		if err := global.APP_DB.Model(&u).Update("traffic_limited", true).Error; err != nil {
			return false, "", fmt.Errorf("标记用户流量受限失败: %w", err)
		}
		limitReason := fmt.Sprintf("用户流量已超限：使用 %dMB，限制 %dMB",
			totalUsed, u.TotalTraffic)
		global.APP_LOG.Info("用户流量超限",
			zap.Uint("userID", userID),
			zap.Int64("usedTraffic", totalUsed),
			zap.Int64("totalTraffic", u.TotalTraffic))

		return true, limitReason, nil
	}
	// 未超限，确保用户不处于受限状态
	if u.TrafficLimited {
		if err := global.APP_DB.Model(&u).Update("traffic_limited", false).Error; err != nil {
			return false, "", fmt.Errorf("取消用户流量限制状态失败: %w", err)
		}
		global.APP_LOG.Info("用户流量限制已解除",
			zap.Uint("userID", userID),
			zap.Int64("usedTraffic", totalUsed),
			zap.Int64("totalTraffic", u.TotalTraffic))
	}
	return false, "", nil
}

// getUserMonthlyTrafficFromVnStat 从vnStat数据计算用户当月流量使用量
func (s *LimitService) getUserMonthlyTrafficFromVnStat(userID uint) (int64, error) {
	// 获取用户所有实例（包含软删除的实例，因为需要统计本月已产生的流量）
	var instances []provider.Instance
	err := global.APP_DB.Unscoped().Where("user_id = ?", userID).Find(&instances).Error
	if err != nil {
		return 0, fmt.Errorf("获取用户实例列表失败: %w", err)
	}

	if len(instances) == 0 {
		return 0, nil // 用户没有实例
	}

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	var totalTraffic int64

	// 遍历每个实例，从vnStat记录中获取当月流量
	for _, instance := range instances {
		instanceTraffic, err := s.service.getInstanceMonthlyTrafficFromVnStat(instance.ID, year, month)
		if err != nil {
			global.APP_LOG.Warn("获取实例vnStat月度流量失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			continue
		}
		totalTraffic += instanceTraffic
	}

	global.APP_LOG.Debug("计算用户vnStat月度流量",
		zap.Uint("userID", userID),
		zap.Int("year", year),
		zap.Int("month", month),
		zap.Int("instancesCount", len(instances)),
		zap.Int64("totalTraffic", totalTraffic))

	return totalTraffic, nil
}

// CheckProviderTrafficLimitWithVnStat 使用vnStat数据检查Provider流量限制
func (s *LimitService) CheckProviderTrafficLimitWithVnStat(providerID uint) (bool, string, error) {
	var p provider.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return false, "", fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 检查是否需要重置流量
	if err := s.service.checkAndResetProviderMonthlyTraffic(providerID); err != nil {
		global.APP_LOG.Error("检查Provider月度流量重置失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
	}

	// 重新加载Provider数据
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return false, "", fmt.Errorf("重新加载Provider信息失败: %w", err)
	}

	// 使用vnStat数据计算Provider当月总流量使用量
	totalUsed, err := s.getProviderMonthlyTrafficFromVnStat(providerID)
	if err != nil {
		global.APP_LOG.Error("从vnStat获取Provider月度流量失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		// 降级到旧方法
		isLimited, err := s.service.CheckProviderTrafficLimit(providerID)
		if err != nil {
			return false, "", err
		}
		if isLimited {
			return true, "Provider流量超限（使用旧方法检测）", nil
		}
		return false, "", nil
	}

	// 更新Provider已使用流量
	if err := global.APP_DB.Model(&p).Update("used_traffic", totalUsed).Error; err != nil {
		return false, "", fmt.Errorf("更新Provider流量使用量失败: %w", err)
	}

	// 检查是否超限（仅在有有效的流量限制时进行检查）
	if p.MaxTraffic > 0 && totalUsed >= p.MaxTraffic {
		// 超限，标记Provider为受限状态
		if err := global.APP_DB.Model(&p).Update("traffic_limited", true).Error; err != nil {
			return false, "", fmt.Errorf("标记Provider流量受限失败: %w", err)
		}

		limitReason := fmt.Sprintf("Provider流量已超限：使用 %dMB，限制 %dMB",
			totalUsed, p.MaxTraffic)

		global.APP_LOG.Info("Provider流量超限",
			zap.Uint("providerID", providerID),
			zap.String("providerName", p.Name),
			zap.Int64("usedTraffic", totalUsed),
			zap.Int64("maxTraffic", p.MaxTraffic))

		return true, limitReason, nil
	}

	// 未超限，确保Provider不处于受限状态
	if p.TrafficLimited {
		if err := global.APP_DB.Model(&p).Update("traffic_limited", false).Error; err != nil {
			return false, "", fmt.Errorf("取消Provider流量限制状态失败: %w", err)
		}

		global.APP_LOG.Info("Provider流量限制已解除",
			zap.Uint("providerID", providerID),
			zap.String("providerName", p.Name),
			zap.Int64("usedTraffic", totalUsed),
			zap.Int64("maxTraffic", p.MaxTraffic))
	}

	return false, "", nil
}

// getProviderMonthlyTrafficFromVnStat 从vnStat数据计算Provider当月流量使用量
func (s *LimitService) getProviderMonthlyTrafficFromVnStat(providerID uint) (int64, error) {
	// 获取Provider下所有实例（包含软删除的实例，因为需要统计本月已产生的流量）
	var instances []provider.Instance
	err := global.APP_DB.Unscoped().Where("provider_id = ?", providerID).Find(&instances).Error
	if err != nil {
		return 0, fmt.Errorf("获取Provider实例列表失败: %w", err)
	}

	if len(instances) == 0 {
		return 0, nil // Provider下没有实例
	}

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	var totalTraffic int64

	// 遍历每个实例，从vnStat记录中获取当月流量
	for _, instance := range instances {
		instanceTraffic, err := s.service.getInstanceMonthlyTrafficFromVnStat(instance.ID, year, month)
		if err != nil {
			global.APP_LOG.Warn("获取实例vnStat月度流量失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			continue
		}
		totalTraffic += instanceTraffic
	}

	global.APP_LOG.Debug("计算Provider vnStat月度流量",
		zap.Uint("providerID", providerID),
		zap.Int("year", year),
		zap.Int("month", month),
		zap.Int("instancesCount", len(instances)),
		zap.Int64("totalTraffic", totalTraffic))

	return totalTraffic, nil
}

// SyncAllTrafficLimitsWithVnStat 同步所有流量限制状态（使用vnStat数据）
func (s *LimitService) SyncAllTrafficLimitsWithVnStat(ctx context.Context) error {
	global.APP_LOG.Info("开始同步所有流量限制状态（基于vnStat数据）")

	// 同步所有用户的流量限制
	var users []user.User
	if err := global.APP_DB.Find(&users).Error; err != nil {
		return fmt.Errorf("获取用户列表失败: %w", err)
	}

	userLimitCount := 0
	for _, u := range users {
		select {
		case <-ctx.Done():
			global.APP_LOG.Info("流量限制同步被取消")
			return ctx.Err()
		default:
		}

		isLimited, reason, err := s.CheckUserTrafficLimitWithVnStat(u.ID)
		if err != nil {
			global.APP_LOG.Error("检查用户流量限制失败",
				zap.Uint("userID", u.ID),
				zap.Error(err))
			continue
		}

		if isLimited {
			userLimitCount++
			global.APP_LOG.Info("用户流量受限",
				zap.Uint("userID", u.ID),
				zap.String("reason", reason))

			// 停止用户的实例
			if err := s.service.StopUserInstancesForTrafficLimit(u.ID); err != nil {
				global.APP_LOG.Error("停止用户受限实例失败",
					zap.Uint("userID", u.ID),
					zap.Error(err))
			}
		}
	}

	// 同步所有Provider的流量限制
	var providers []provider.Provider
	if err := global.APP_DB.Find(&providers).Error; err != nil {
		return fmt.Errorf("获取Provider列表失败: %w", err)
	}

	providerLimitCount := 0
	for _, p := range providers {
		select {
		case <-ctx.Done():
			global.APP_LOG.Info("流量限制同步被取消")
			return ctx.Err()
		default:
		}

		isLimited, reason, err := s.CheckProviderTrafficLimitWithVnStat(p.ID)
		if err != nil {
			global.APP_LOG.Error("检查Provider流量限制失败",
				zap.Uint("providerID", p.ID),
				zap.Error(err))
			continue
		}

		if isLimited {
			providerLimitCount++
			global.APP_LOG.Info("Provider流量受限",
				zap.Uint("providerID", p.ID),
				zap.String("providerName", p.Name),
				zap.String("reason", reason))

			// 停止Provider的实例
			if err := s.service.StopProviderInstancesForTrafficLimit(p.ID); err != nil {
				global.APP_LOG.Error("停止Provider受限实例失败",
					zap.Uint("providerID", p.ID),
					zap.Error(err))
			}
		}
	}

	global.APP_LOG.Info("流量限制同步完成",
		zap.Int("checkedUsers", len(users)),
		zap.Int("limitedUsers", userLimitCount),
		zap.Int("checkedProviders", len(providers)),
		zap.Int("limitedProviders", providerLimitCount))

	return nil
}

// GetUserTrafficUsageWithVnStat 获取用户流量使用情况（基于vnStat数据）
func (s *LimitService) GetUserTrafficUsageWithVnStat(userID uint) (map[string]interface{}, error) {
	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return nil, fmt.Errorf("获取用户信息失败: %w", err)
	}

	// 自动同步用户流量限额：如果TotalTraffic为0，从等级配置中获取
	if u.TotalTraffic == 0 {
		levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[u.Level]
		if exists && levelLimits.MaxTraffic > 0 {
			u.TotalTraffic = levelLimits.MaxTraffic
		}
	}

	// 获取当月流量使用量
	currentMonthUsage, err := s.getUserMonthlyTrafficFromVnStat(userID)
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
		usagePercent = float64(currentMonthUsage) / float64(u.TotalTraffic) * 100
	}

	// 获取最近6个月的流量历史
	history, err := s.getUserTrafficHistoryFromVnStat(userID, 6)
	if err != nil {
		global.APP_LOG.Warn("获取流量历史失败", zap.Error(err))
		history = []map[string]interface{}{}
	}

	return map[string]interface{}{
		"user_id":             userID,
		"current_month_usage": currentMonthUsage,
		"yearly_usage":        yearlyUsage,
		"total_limit":         u.TotalTraffic,
		"usage_percent":       usagePercent,
		"is_limited":          u.TrafficLimited,
		"reset_time":          u.TrafficResetAt,
		"history":             history,
		"formatted": map[string]string{
			"current_usage": FormatTrafficMB(currentMonthUsage),
			"total_limit":   FormatTrafficMB(int64(u.TotalTraffic)),
		},
	}, nil
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

	// 遍历每个实例，获取年度流量
	for _, instance := range instances {
		// 获取实例所有接口的年度总流量（total记录中year=0表示总计）
		var records []monitoringModel.VnStatTrafficRecord
		err := global.APP_DB.Where("instance_id = ? AND year = 0 AND month = 0 AND day = 0 AND hour = 0",
			instance.ID).Find(&records).Error
		if err != nil {
			continue
		}

		for _, record := range records {
			totalTraffic += record.TotalBytes / (1024 * 1024) // 转换为MB
		}
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

	// 获取系统总流量（所有实例本月流量总和）
	var totalTraffic dashboardModel.TrafficStats

	err := global.APP_DB.Table("vnstat_traffic_records").
		Select("SUM(rx_bytes) as total_rx, SUM(tx_bytes) as total_tx, SUM(total_bytes) as total_bytes").
		Where("year = ? AND month = ?", year, int(month)).
		Scan(&totalTraffic).Error

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

	// 获取当前月份的流量使用
	monthlyTraffic, err := s.getProviderMonthlyTrafficFromVnStat(providerID)
	if err != nil {
		global.APP_LOG.Warn("获取Provider vnStat月度流量失败，使用默认值",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		monthlyTraffic = 0
	}

	// 计算使用百分比
	var usagePercent float64 = 0
	if p.MaxTraffic > 0 {
		usagePercent = float64(monthlyTraffic) / float64(p.MaxTraffic) * 100
	}

	// 获取Provider下的实例数量（排除软删除的实例 - 用于显示活跃实例数）
	var instanceCount int64
	err = global.APP_DB.Model(&provider.Instance{}).Where("provider_id = ?", providerID).Count(&instanceCount).Error
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
		"current_month_usage":    monthlyTraffic,
		"total_limit":            p.MaxTraffic,
		"usage_percent":          usagePercent,
		"is_limited":             p.TrafficLimited,
		"reset_time":             p.TrafficResetAt,
		"instance_count":         instanceCount,
		"limited_instance_count": limitedInstanceCount,
		"data_source":            "vnstat",
		"formatted": map[string]string{
			"current_usage": FormatTrafficMB(monthlyTraffic),
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
		MonthUsage int64      `gorm:"column:month_usage"`
		TotalLimit uint64     `gorm:"column:total_limit"`
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
	offset := (page - 1) * pageSize
	query := `
		SELECT 
			u.id as user_id,
			u.username,
			u.nickname,
			COALESCE(SUM(vr.total_bytes), 0) as month_usage,
			u.total_traffic as total_limit,
			u.traffic_limited as is_limited,
			u.traffic_reset_at as reset_time
		FROM users u
		LEFT JOIN instances i ON u.id = i.user_id
		LEFT JOIN vnstat_traffic_records vr ON i.id = vr.instance_id 
			AND vr.year = ? AND vr.month = ?
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
			// MonthUsage 是字节数，TotalLimit 是 MB，需要统一单位
			// 将字节转换为 MB: bytes / (1024 * 1024)
			monthUsageMB := float64(rank.MonthUsage) / (1024 * 1024)
			usagePercent = (monthUsageMB / float64(rank.TotalLimit)) * 100
		}

		result = append(result, map[string]interface{}{
			"rank":          startRank + i + 1,
			"user_id":       rank.UserID,
			"username":      rank.Username,
			"nickname":      rank.Nickname,
			"month_usage":   rank.MonthUsage,
			"total_limit":   rank.TotalLimit,
			"usage_percent": usagePercent,
			"is_limited":    rank.IsLimited,
			"reset_time":    rank.ResetTime,
			"formatted": map[string]string{
				"month_usage": FormatVnStatData(rank.MonthUsage),
				"total_limit": FormatTrafficMB(int64(rank.TotalLimit)),
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
