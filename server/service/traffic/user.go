package traffic

import (
	"fmt"
	"time"

	"oneclickvirt/global"
	dashboardModel "oneclickvirt/model/dashboard"
	monitoringModel "oneclickvirt/model/monitoring"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"
	"oneclickvirt/service/cache"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// UserTrafficService 用户流量服务 - 提供基于 pmacct 的流量查询
type UserTrafficService struct {
	queryService *QueryService
	limitService *LimitService
}

// NewUserTrafficService 创建用户流量服务
func NewUserTrafficService() *UserTrafficService {
	return &UserTrafficService{
		queryService: NewQueryService(),
		limitService: NewLimitService(),
	}
}

// GetUserTrafficOverview 获取用户流量概览（带缓存）
func (s *UserTrafficService) GetUserTrafficOverview(userID uint) (map[string]interface{}, error) {
	cacheService := cache.GetUserCacheService()
	cacheKey := cache.MakeUserTrafficOverviewKey(userID)

	// 尝试从缓存获取
	if cachedData, ok := cacheService.Get(cacheKey); ok {
		if overview, ok := cachedData.(map[string]interface{}); ok {
			return overview, nil
		}
	}

	// 缓存未命中，查询数据
	overview, err := s.fetchUserTrafficOverview(userID)
	if err != nil {
		return nil, err
	}

	// 缓存结果
	cacheService.Set(cacheKey, overview, cache.TTLUserTrafficOverview)
	return overview, nil
}

// fetchUserTrafficOverview 从数据库获取用户流量概览
func (s *UserTrafficService) fetchUserTrafficOverview(userID uint) (map[string]interface{}, error) {
	// 获取用户信息
	var u user.User
	if err := global.APP_DB.Select("id, level, total_traffic, traffic_reset_at, traffic_limited").
		First(&u, userID).Error; err != nil {
		return nil, fmt.Errorf("获取用户信息失败: %w", err)
	}

	// 检查用户的所有实例所在的Provider是否都禁用了流量统计
	hasEnabledTrafficControl, err := s.limitService.hasAnyProviderWithTrafficControlEnabled(userID)
	if err != nil {
		global.APP_LOG.Warn("检查Provider流量统计状态失败", zap.Error(err))
	}

	// 如果所有Provider都禁用了流量统计，返回无限制状态
	if !hasEnabledTrafficControl {
		return map[string]interface{}{
			"user_id":                 userID,
			"current_month_usage_mb":  float64(0),
			"total_limit_mb":          int64(0), // 0表示无限制
			"usage_percent":           float64(0),
			"is_limited":              false,
			"traffic_control_enabled": false,
			"data_source":             "none",
			"formatted": map[string]string{
				"current_usage": "0 MB",
				"total_limit":   "无限制",
			},
		}, nil
	}

	// 自动设置TotalTraffic（如TotalTraffic为0时）
	if u.TotalTraffic == 0 {
		levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[u.Level]
		if exists && levelLimits.MaxTraffic > 0 {
			u.TotalTraffic = levelLimits.MaxTraffic
		}
	}

	// 从QueryService获取当月流量统计
	now := time.Now()
	stats, err := s.queryService.GetUserMonthlyTraffic(userID, now.Year(), int(now.Month()))
	if err != nil {
		return nil, fmt.Errorf("查询用户月度流量失败: %w", err)
	}

	// 计算使用百分比
	var usagePercent float64
	if u.TotalTraffic > 0 {
		usagePercent = (stats.ActualUsageMB / float64(u.TotalTraffic)) * 100
	}

	return map[string]interface{}{
		"user_id":                 userID,
		"current_month_usage_mb":  stats.ActualUsageMB,
		"total_limit_mb":          u.TotalTraffic,
		"usage_percent":           usagePercent,
		"is_limited":              u.TrafficLimited,
		"reset_time":              u.TrafficResetAt,
		"traffic_control_enabled": true,
		"data_source":             "pmacct_realtime",
		"rx_bytes":                stats.RxBytes,
		"tx_bytes":                stats.TxBytes,
		"total_bytes":             stats.TotalBytes,
		"formatted": map[string]string{
			"current_usage": utils.FormatMB(stats.ActualUsageMB),
			"total_limit":   utils.FormatMB(float64(u.TotalTraffic)),
			"rx":            utils.FormatBytes(stats.RxBytes),
			"tx":            utils.FormatBytes(stats.TxBytes),
			"total":         utils.FormatBytes(stats.TotalBytes),
		},
	}, nil
}

// GetInstanceTrafficDetail 获取实例流量详情（带缓存）
func (s *UserTrafficService) GetInstanceTrafficDetail(userID, instanceID uint) (map[string]interface{}, error) {
	cacheService := cache.GetUserCacheService()
	cacheKey := cache.MakeInstanceTrafficDetailKey(instanceID)

	// 尝试从缓存获取
	if cachedData, ok := cacheService.Get(cacheKey); ok {
		if detail, ok := cachedData.(map[string]interface{}); ok {
			// 验证用户权限（即使是缓存数据也要验证）
			if !s.hasInstanceAccess(userID, instanceID) {
				return nil, fmt.Errorf("用户无权限访问该实例")
			}
			return detail, nil
		}
	}

	// 缓存未命中，查询数据
	detail, err := s.fetchInstanceTrafficDetail(userID, instanceID)
	if err != nil {
		return nil, err
	}

	// 缓存结果
	cacheService.Set(cacheKey, detail, cache.TTLInstanceTrafficDetail)
	return detail, nil
}

// fetchInstanceTrafficDetail 从数据库获取实例流量详情
func (s *UserTrafficService) fetchInstanceTrafficDetail(userID, instanceID uint) (map[string]interface{}, error) {
	// 验证用户权限
	if !s.hasInstanceAccess(userID, instanceID) {
		return nil, fmt.Errorf("用户无权限访问该实例")
	}

	// 获取实例基本信息
	var instance provider.Instance
	if err := global.APP_DB.Select("id, name, provider_id, public_ip, traffic_limited").
		First(&instance, instanceID).Error; err != nil {
		return nil, fmt.Errorf("实例不存在: %w", err)
	}

	// 获取实例的pmacct监控信息
	var monitor monitoringModel.PmacctMonitor
	err := global.APP_DB.Where("instance_id = ?", instanceID).First(&monitor).Error
	if err != nil {
		global.APP_LOG.Warn("获取实例pmacct监控信息失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		// 继续执行，返回基本信息
	}

	// 获取Provider配置
	var prov provider.Provider
	if err := global.APP_DB.Select("id, enable_traffic_control, traffic_count_mode, traffic_multiplier").
		First(&prov, instance.ProviderID).Error; err != nil {
		return nil, fmt.Errorf("查询Provider配置失败: %w", err)
	}

	// 如果未启用流量控制，返回基本信息
	if !prov.EnableTrafficControl {
		return map[string]interface{}{
			"instance_id":             instanceID,
			"instance_name":           instance.Name,
			"mapped_ip":               monitor.MappedIP,
			"traffic_control_enabled": false,
			"current_month_usage_mb":  float64(0),
			"formatted": map[string]string{
				"current_usage": "0 MB",
			},
		}, nil
	}

	// 从QueryService获取当月流量数据
	now := time.Now()
	stats, err := s.queryService.GetInstanceMonthlyTraffic(instanceID, now.Year(), int(now.Month()))
	if err != nil {
		return nil, fmt.Errorf("查询实例流量失败: %w", err)
	}

	// 获取流量历史（最近30天）
	history, err := s.queryService.GetInstanceTrafficHistory(instanceID, 30)
	if err != nil {
		global.APP_LOG.Warn("获取实例流量历史失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		history = []*HistoryPoint{}
	}

	return map[string]interface{}{
		"instance_id":             instanceID,
		"instance_name":           instance.Name,
		"mapped_ip":               monitor.MappedIP,
		"mapped_ipv6":             monitor.MappedIPv6,
		"is_enabled":              monitor.IsEnabled,
		"last_sync":               monitor.LastSync,
		"traffic_control_enabled": true,
		"traffic_limited":         instance.TrafficLimited,
		"current_month_usage_mb":  stats.ActualUsageMB,
		"rx_bytes":                stats.RxBytes,
		"tx_bytes":                stats.TxBytes,
		"total_bytes":             stats.TotalBytes,
		"traffic_count_mode":      prov.TrafficCountMode,
		"traffic_multiplier":      prov.TrafficMultiplier,
		"year":                    now.Year(),
		"month":                   int(now.Month()),
		"history":                 history,
		"formatted": map[string]string{
			"current_usage": utils.FormatMB(stats.ActualUsageMB),
			"rx":            utils.FormatBytes(stats.RxBytes),
			"tx":            utils.FormatBytes(stats.TxBytes),
			"total":         utils.FormatBytes(stats.TotalBytes),
		},
	}, nil
}

// GetUserInstancesTrafficSummary 获取用户所有实例的流量汇总（带缓存）
func (s *UserTrafficService) GetUserInstancesTrafficSummary(userID uint) (map[string]interface{}, error) {
	now := time.Now()
	cacheService := cache.GetUserCacheService()
	cacheKey := cache.MakeUserTrafficSummaryKey(userID, now.Year(), int(now.Month()))

	// 尝试从缓存获取
	if cachedData, ok := cacheService.Get(cacheKey); ok {
		if summary, ok := cachedData.(map[string]interface{}); ok {
			return summary, nil
		}
	}

	// 缓存未命中，查询数据
	summary, err := s.fetchUserInstancesTrafficSummary(userID)
	if err != nil {
		return nil, err
	}

	// 缓存结果
	cacheService.Set(cacheKey, summary, cache.TTLUserTrafficSummary)
	return summary, nil
}

// fetchUserInstancesTrafficSummary 从数据库获取用户所有实例的流量汇总
func (s *UserTrafficService) fetchUserInstancesTrafficSummary(userID uint) (map[string]interface{}, error) {
	// 获取用户所有实例
	var instances []dashboardModel.InstanceSummary
	err := global.APP_DB.Table("instances").
		Select("id, name, status").
		Where("user_id = ?", userID).
		Find(&instances).Error
	if err != nil {
		return nil, fmt.Errorf("获取用户实例列表失败: %w", err)
	}

	result := map[string]interface{}{
		"user_id":        userID,
		"instance_count": len(instances),
		"instances":      []map[string]interface{}{},
	}

	if len(instances) == 0 {
		result["total_traffic_mb"] = float64(0)
		result["formatted_total"] = "0 MB"
		return result, nil
	}

	// 提取实例ID列表
	instanceIDs := make([]uint, 0, len(instances))
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}

	// 批量查询流量数据
	now := time.Now()
	statsMap, err := s.queryService.BatchGetInstancesMonthlyTraffic(instanceIDs, now.Year(), int(now.Month()))
	if err != nil {
		return nil, fmt.Errorf("批量查询实例流量失败: %w", err)
	}

	// 构建响应数据
	instanceDetails := make([]map[string]interface{}, 0, len(instances))
	var totalTrafficMB float64

	for _, instance := range instances {
		stats := statsMap[instance.ID]

		instanceDetail := map[string]interface{}{
			"id":                 instance.ID,
			"name":               instance.Name,
			"status":             instance.Status,
			"monthly_traffic_mb": stats.ActualUsageMB,
			"rx_bytes":           stats.RxBytes,
			"tx_bytes":           stats.TxBytes,
			"total_bytes":        stats.TotalBytes,
			"formatted_monthly":  utils.FormatMB(stats.ActualUsageMB),
		}

		totalTrafficMB += stats.ActualUsageMB
		instanceDetails = append(instanceDetails, instanceDetail)
	}

	result["instances"] = instanceDetails
	result["total_traffic_mb"] = totalTrafficMB
	result["formatted_total"] = utils.FormatMB(totalTrafficMB)

	return result, nil
}

// hasInstanceAccess 检查用户是否有实例访问权限
func (s *UserTrafficService) hasInstanceAccess(userID, instanceID uint) bool {
	var count int64
	err := global.APP_DB.Table("instances").
		Where("id = ? AND user_id = ?", instanceID, userID).
		Count(&count).Error
	if err != nil {
		return false
	}
	return count > 0
}

// GetTrafficLimitStatus 获取流量限制状态
func (s *UserTrafficService) GetTrafficLimitStatus(userID uint) (map[string]interface{}, error) {
	// 使用三层级流量限制服务检查用户流量限制状态
	threeTierService := NewThreeTierLimitService()
	isUserLimited, err := threeTierService.CheckUserTrafficLimit(userID)
	if err != nil {
		return nil, fmt.Errorf("检查用户流量限制失败: %w", err)
	}

	// 获取用户流量概览
	trafficOverview, err := s.GetUserTrafficOverview(userID)
	if err != nil {
		return nil, fmt.Errorf("获取流量概览失败: %w", err)
	}

	result := map[string]interface{}{
		"user_id":           userID,
		"is_user_limited":   isUserLimited,
		"traffic_overview":  trafficOverview,
		"limited_instances": []map[string]interface{}{},
	}

	// 获取受限实例列表
	var limitedInstances []dashboardModel.LimitedInstanceSummary

	err = global.APP_DB.Table("instances").
		Where("user_id = ? AND traffic_limited = ?", userID, true).
		Find(&limitedInstances).Error

	if err != nil {
		global.APP_LOG.Warn("获取受限实例列表失败", zap.Error(err))
	} else {
		// 批量检查所有受限实例的Provider状态
		// 先收集所有唯一的providerID
		providerIDSet := make(map[uint]bool)
		for _, instance := range limitedInstances {
			providerIDSet[instance.ProviderID] = true
		}

		// 批量检查所有Provider的流量限制状态
		providerLimitMap := make(map[uint]bool)
		for providerID := range providerIDSet {
			isProviderLimited, providerErr := threeTierService.CheckProviderTrafficLimit(providerID)
			if providerErr != nil {
				global.APP_LOG.Warn("检查Provider流量限制失败",
					zap.Uint("providerID", providerID),
					zap.Error(providerErr))
			}
			providerLimitMap[providerID] = isProviderLimited
		}

		// 构建实例详情列表
		instanceDetails := []map[string]interface{}{}
		for _, instance := range limitedInstances {
			instanceDetail := map[string]interface{}{
				"id":                  instance.ID,
				"name":                instance.Name,
				"status":              instance.Status,
				"is_provider_limited": providerLimitMap[instance.ProviderID],
			}
			instanceDetails = append(instanceDetails, instanceDetail)
		}
		result["limited_instances"] = instanceDetails
	}

	return result, nil
}
