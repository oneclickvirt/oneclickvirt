package traffic

import (
	"fmt"
	"sort"
	"time"

	"oneclickvirt/global"

	"gorm.io/gorm"
)

// QueryService 流量查询服务 - 统一的流量数据查询入口
// 所有流量数据从 pmacct_traffic_records 实时聚合计算，确保数据一致性
type QueryService struct{}

// NewQueryService 创建流量查询服务
func NewQueryService() *QueryService {
	return &QueryService{}
}

// TrafficStats 流量统计结果
type TrafficStats struct {
	RxBytes       int64   `json:"rx_bytes"`        // 接收字节数
	TxBytes       int64   `json:"tx_bytes"`        // 发送字节数
	TotalBytes    int64   `json:"total_bytes"`     // 总字节数
	ActualUsageMB float64 `json:"actual_usage_mb"` // 实际使用量（MB，已应用流量计算模式）
}

// GetInstanceMonthlyTraffic 获取实例当月流量统计
// 返回原始流量和应用Provider流量计算模式后的实际使用量
func (s *QueryService) GetInstanceMonthlyTraffic(instanceID uint, year, month int) (*TrafficStats, error) {
	query := `
		SELECT 
			COALESCE(SUM(max_rx), 0) as rx_bytes,
			COALESCE(SUM(max_tx), 0) as tx_bytes
		FROM (
			-- 检测重启并分段
			SELECT 
				segment_id,
				MAX(rx_bytes) as max_rx,
				MAX(tx_bytes) as max_tx
			FROM (
				-- 计算累积重启次数作为segment_id
				SELECT 
					t1.timestamp,
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
								AND year = ? AND month = ?
						)
					WHERE t2.instance_id = ?
						AND t2.year = ? AND t2.month = ?
						AND t2.timestamp <= t1.timestamp
						AND (
								(t3.rx_bytes IS NOT NULL AND t2.rx_bytes < t3.rx_bytes)
								OR
								(t3.tx_bytes IS NOT NULL AND t2.tx_bytes < t3.tx_bytes)
							)
					) as segment_id
			FROM pmacct_traffic_records t1
			WHERE t1.instance_id = ? AND t1.year = ? AND t1.month = ?
			) AS segments
			GROUP BY segment_id
		) AS segment_max
	`

	var result struct {
		RxBytes int64
		TxBytes int64
	}

	err := global.APP_DB.Raw(query, year, month, instanceID, year, month, instanceID, year, month).Scan(&result).Error
	if err != nil {
		return nil, fmt.Errorf("查询实例月度流量失败: %w", err)
	}

	// 获取Provider配置用于计算实际使用量
	var providerConfig struct {
		TrafficCountMode  string
		TrafficMultiplier float64
	}

	err = global.APP_DB.Table("instances i").
		Joins("INNER JOIN providers p ON i.provider_id = p.id").
		Select("COALESCE(p.traffic_count_mode, 'both') as traffic_count_mode, COALESCE(p.traffic_multiplier, 1.0) as traffic_multiplier").
		Where("i.id = ?", instanceID).
		Scan(&providerConfig).Error
	if err != nil {
		return nil, fmt.Errorf("查询Provider配置失败: %w", err)
	}

	stats := &TrafficStats{
		RxBytes:    result.RxBytes,
		TxBytes:    result.TxBytes,
		TotalBytes: result.RxBytes + result.TxBytes,
	}

	// 应用流量计算模式
	stats.ActualUsageMB = s.calculateActualUsage(
		result.RxBytes,
		result.TxBytes,
		providerConfig.TrafficCountMode,
		providerConfig.TrafficMultiplier,
	)

	return stats, nil
}

// GetUserMonthlyTraffic 获取用户当月所有实例的流量统计
// 只统计启用了流量控制的Provider
// 处理pmacct重启导致的累积值重置问题
func (s *QueryService) GetUserMonthlyTraffic(userID uint, year, month int) (*TrafficStats, error) {
	// 获取用户所有实例列表（包含软删除的实例，以统计历史流量）
	var instanceIDs []uint
	err := global.APP_DB.Unscoped().Table("instances").
		Where("user_id = ?", userID).
		Pluck("id", &instanceIDs).Error
	if err != nil {
		return nil, fmt.Errorf("获取用户实例列表失败: %w", err)
	}

	if len(instanceIDs) == 0 {
		return &TrafficStats{}, nil
	}

	// 使用批量查询（已包含重启检测逻辑）
	instanceStats, err := s.BatchGetInstancesMonthlyTraffic(instanceIDs, year, month)
	if err != nil {
		return nil, err
	}

	// 汇总所有实例的流量（只统计启用了流量控制的Provider）
	var totalRxBytes int64
	var totalTxBytes int64
	var totalActualUsageMB float64

	for _, stats := range instanceStats {
		totalRxBytes += stats.RxBytes
		totalTxBytes += stats.TxBytes
		totalActualUsageMB += stats.ActualUsageMB
	}

	return &TrafficStats{
		RxBytes:       totalRxBytes,
		TxBytes:       totalTxBytes,
		TotalBytes:    totalRxBytes + totalTxBytes,
		ActualUsageMB: totalActualUsageMB,
	}, nil
}

// GetProviderMonthlyTraffic 获取Provider当月所有实例的流量统计
// 使用provider_traffic_histories聚合表，大幅提升性能
func (s *QueryService) GetProviderMonthlyTraffic(providerID uint, year, month int) (*TrafficStats, error) {
	// 首先检查Provider是否启用了流量控制
	var p struct {
		EnableTrafficControl bool
		TrafficCountMode     string
		TrafficMultiplier    float64
	}

	err := global.APP_DB.Table("providers").
		Select("enable_traffic_control, COALESCE(traffic_count_mode, 'both') as traffic_count_mode, COALESCE(traffic_multiplier, 1.0) as traffic_multiplier").
		Where("id = ?", providerID).
		Scan(&p).Error
	if err != nil {
		return nil, fmt.Errorf("查询Provider配置失败: %w", err)
	}

	if !p.EnableTrafficControl {
		// 未启用流量控制，返回0
		return &TrafficStats{}, nil
	}

	// 使用聚合表查询，性能大幅提升
	// day=0,hour=0 表示月度汇总数据
	var result struct {
		TrafficIn  int64
		TrafficOut int64
		TotalUsed  int64
	}

	err = global.APP_DB.Table("provider_traffic_histories").
		Select("traffic_in, traffic_out, total_used").
		Where("provider_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL",
			providerID, year, month).
		Scan(&result).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("查询Provider流量失败: %w", err)
	}

	// 聚合表中存储的traffic_in/traffic_out/total_used都是MB单位
	// 根据流量模式计算实际使用量（MB）
	var actualUsageMB float64
	switch p.TrafficCountMode {
	case "out":
		actualUsageMB = float64(result.TrafficOut) * p.TrafficMultiplier
	case "in":
		actualUsageMB = float64(result.TrafficIn) * p.TrafficMultiplier
	default: // "both"
		actualUsageMB = float64(result.TotalUsed) * p.TrafficMultiplier
	}

	// 聚合表存储的是MB，转换为字节用于统一返回格式
	rxBytes := result.TrafficIn * 1048576 // MB转字节：* 1024 * 1024
	txBytes := result.TrafficOut * 1048576

	return &TrafficStats{
		RxBytes:       rxBytes,
		TxBytes:       txBytes,
		TotalBytes:    rxBytes + txBytes,
		ActualUsageMB: actualUsageMB,
	}, nil
}

// BatchGetInstancesMonthlyTraffic 批量获取多个实例的月度流量
// 1. 优先使用缓存表（instance_traffic_histories）快速查询
// 2. 缓存未命中时，使用正确的分段计算逻辑
// 3. 支持增量更新缓存
func (s *QueryService) BatchGetInstancesMonthlyTraffic(instanceIDs []uint, year, month int) (map[uint]*TrafficStats, error) {
	if len(instanceIDs) == 0 {
		return make(map[uint]*TrafficStats), nil
	}

	// 策略1: 尝试从缓存表获取（日度汇总 hour=0, day=0 表示月度汇总）
	cachedStats := s.getBatchFromCache(instanceIDs, year, month)

	// 策略2: 识别缓存未命中的实例
	var uncachedIDs []uint
	for _, id := range instanceIDs {
		if _, ok := cachedStats[id]; !ok {
			uncachedIDs = append(uncachedIDs, id)
		}
	}

	// 策略3: 对未缓存的实例执行实时计算
	if len(uncachedIDs) > 0 {
		computedStats, err := s.computeBatchMonthlyTraffic(uncachedIDs, year, month)
		if err != nil {
			return nil, err
		}
		// 合并结果
		for id, stats := range computedStats {
			cachedStats[id] = stats
		}
	}

	// 确保所有实例都有结果（即使是空值）
	for _, id := range instanceIDs {
		if _, ok := cachedStats[id]; !ok {
			cachedStats[id] = &TrafficStats{}
		}
	}

	return cachedStats, nil
}

// getBatchFromCache 从缓存表批量获取流量数据
func (s *QueryService) getBatchFromCache(instanceIDs []uint, year, month int) map[uint]*TrafficStats {
	type CacheResult struct {
		InstanceID uint
		TrafficIn  int64
		TrafficOut int64
		TotalUsed  int64
	}

	var results []CacheResult
	// 查询月度汇总记录 (day=0, hour=0)
	err := global.APP_DB.Table("instance_traffic_histories").
		Select("instance_id, traffic_in, traffic_out, total_used").
		Where("instance_id IN ? AND year = ? AND month = ? AND day = 0 AND hour = 0", instanceIDs, year, month).
		Find(&results).Error

	if err != nil {
		return make(map[uint]*TrafficStats)
	}

	statsMap := make(map[uint]*TrafficStats)
	for _, r := range results {
		// 缓存表存储的是MB，转换为字节用于统一返回格式
		// RxBytes/TxBytes/TotalBytes: 字节单位
		// ActualUsageMB: MB单位（已应用流量计算模式）
		statsMap[r.InstanceID] = &TrafficStats{
			RxBytes:       r.TrafficIn * 1048576,  // MB -> Bytes
			TxBytes:       r.TrafficOut * 1048576, // MB -> Bytes
			TotalBytes:    (r.TrafficIn + r.TrafficOut) * 1048576,
			ActualUsageMB: float64(r.TotalUsed),
		}
	}

	return statsMap
}

// computeBatchMonthlyTraffic 实时计算多个实例的月度流量（正确处理pmacct重启）
// 使用与GetInstanceMonthlyTraffic相同的正确分段逻辑
func (s *QueryService) computeBatchMonthlyTraffic(instanceIDs []uint, year, month int) (map[uint]*TrafficStats, error) {
	if len(instanceIDs) == 0 {
		return make(map[uint]*TrafficStats), nil
	}

	// 完整的分段计算逻辑：检测pmacct重启并正确累加各段流量
	// 与GetInstanceMonthlyTraffic保持一致，确保计算准确性
	query := `
		SELECT 
			segment_max.instance_id,
			COALESCE(SUM(max_rx), 0) as rx_bytes,
			COALESCE(SUM(max_tx), 0) as tx_bytes
		FROM (
			-- 检测重启并分段：每个segment_id代表一个连续累积段
			SELECT 
				instance_id,
				segment_id,
				MAX(rx_bytes) as max_rx,
				MAX(tx_bytes) as max_tx
			FROM (
				-- 计算累积重启次数作为segment_id
				-- 当检测到计数器重置（当前值 < 之前最大值）时，segment_id递增
				SELECT 
					t1.instance_id,
					t1.timestamp,
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
									AND year = ? AND month = ?
							)
						WHERE t2.instance_id = t1.instance_id
							AND t2.year = ? AND t2.month = ?
							AND t2.timestamp <= t1.timestamp
							AND (
								(t3.rx_bytes IS NOT NULL AND t2.rx_bytes < t3.rx_bytes)
								OR
								(t3.tx_bytes IS NOT NULL AND t2.tx_bytes < t3.tx_bytes)
							)
					) as segment_id
				FROM pmacct_traffic_records t1
				WHERE t1.instance_id IN (?) AND t1.year = ? AND t1.month = ?
			) AS segments
			GROUP BY instance_id, segment_id
		) AS segment_max
		GROUP BY segment_max.instance_id
	`

	type RawResult struct {
		InstanceID uint
		RxBytes    int64
		TxBytes    int64
	}

	var rawResults []RawResult
	err := global.APP_DB.Raw(query, year, month, year, month, instanceIDs, year, month).Scan(&rawResults).Error
	if err != nil {
		return nil, fmt.Errorf("批量计算实例月度流量失败: %w", err)
	}

	// 如果没有流量数据，直接返回空结果
	if len(rawResults) == 0 {
		statsMap := make(map[uint]*TrafficStats)
		for _, id := range instanceIDs {
			statsMap[id] = &TrafficStats{}
		}
		return statsMap, nil
	}

	// 批量获取Provider配置
	var providerConfigs []struct {
		InstanceID        uint
		TrafficCountMode  string
		TrafficMultiplier float64
	}

	err = global.APP_DB.Table("instances i").
		Joins("INNER JOIN providers p ON i.provider_id = p.id").
		Select("i.id as instance_id, COALESCE(p.traffic_count_mode, 'both') as traffic_count_mode, COALESCE(p.traffic_multiplier, 1.0) as traffic_multiplier").
		Where("i.id IN ?", instanceIDs).
		Find(&providerConfigs).Error
	if err != nil {
		return nil, fmt.Errorf("批量查询Provider配置失败: %w", err)
	}

	// 构建配置映射
	configMap := make(map[uint]struct {
		CountMode  string
		Multiplier float64
	})
	for _, cfg := range providerConfigs {
		configMap[cfg.InstanceID] = struct {
			CountMode  string
			Multiplier float64
		}{
			CountMode:  cfg.TrafficCountMode,
			Multiplier: cfg.TrafficMultiplier,
		}
	}

	// 计算实际使用量并构建结果
	statsMap := make(map[uint]*TrafficStats)
	for _, raw := range rawResults {
		stats := &TrafficStats{
			RxBytes:    raw.RxBytes,
			TxBytes:    raw.TxBytes,
			TotalBytes: raw.RxBytes + raw.TxBytes,
		}

		// 应用流量计算模式
		if config, ok := configMap[raw.InstanceID]; ok {
			stats.ActualUsageMB = s.calculateActualUsage(
				raw.RxBytes,
				raw.TxBytes,
				config.CountMode,
				config.Multiplier,
			)
		}

		statsMap[raw.InstanceID] = stats
	}

	// 确保所有请求的实例都有结果
	for _, id := range instanceIDs {
		if _, ok := statsMap[id]; !ok {
			statsMap[id] = &TrafficStats{}
		}
	}

	return statsMap, nil
}

// GetInstanceTrafficHistory 获取实例的流量历史（按天聚合）
// 实时从 pmacct_traffic_records 聚合生成历史数据
func (s *QueryService) GetInstanceTrafficHistory(instanceID uint, days int) ([]*HistoryPoint, error) {
	// 获取实例和Provider配置（用于计算实际用量）
	var config struct {
		TrafficCountMode  string
		TrafficMultiplier float64
	}
	if err := global.APP_DB.Table("instances i").
		Joins("INNER JOIN providers p ON i.provider_id = p.id").
		Select("p.traffic_count_mode, p.traffic_multiplier").
		Where("i.id = ?", instanceID).
		Scan(&config).Error; err != nil {
		return nil, fmt.Errorf("查询实例配置失败: %w", err)
	}

	// 计算起始日期
	startDate := time.Now().AddDate(0, 0, -days).Truncate(24 * time.Hour)

	// 按天聚合查询，处理pmacct重启问题
	var results []struct {
		Date    time.Time
		RxBytes int64
		TxBytes int64
	}

	// 兼容 MySQL 5.x - 不使用 CTE (WITH AS) 和窗口函数
	// MySQL 5.x 不支持 CTE，改用派生表（子查询）实现相同逻辑
	query := `
		SELECT 
			date,
			SUM(max_rx) as rx_bytes,
			SUM(max_tx) as tx_bytes
		FROM (
			-- 每天的每个段取MAX
			SELECT 
				date,
				segment_id,
				MAX(rx_bytes) as max_rx,
				MAX(tx_bytes) as max_tx
			FROM (
				-- 检测累积值重置点（使用相关子查询，兼容MySQL 5.x）
				SELECT 
					DATE(t1.timestamp) as date,
					t1.timestamp,
					t1.rx_bytes,
					t1.tx_bytes,
					(SELECT COUNT(*)
					 FROM pmacct_traffic_records t2
					 WHERE t2.instance_id = ? 
					   AND DATE(t2.timestamp) = DATE(t1.timestamp)
					   AND t2.timestamp <= t1.timestamp
					   AND (
						 (t2.rx_bytes < (SELECT COALESCE(MAX(t3.rx_bytes), 0)
										 FROM pmacct_traffic_records t3
										 WHERE t3.instance_id = ?
										   AND DATE(t3.timestamp) = DATE(t1.timestamp)
										   AND t3.timestamp < t2.timestamp))
						 OR
						 (t2.tx_bytes < (SELECT COALESCE(MAX(t3.tx_bytes), 0)
										 FROM pmacct_traffic_records t3
										 WHERE t3.instance_id = ?
										   AND DATE(t3.timestamp) = DATE(t1.timestamp)
										   AND t3.timestamp < t2.timestamp))
					   )
					) as segment_id
				FROM pmacct_traffic_records t1
				WHERE t1.instance_id = ? AND t1.timestamp >= ?
			) AS daily_segments
			GROUP BY date, segment_id
		) AS daily_segment_max
		GROUP BY date
		ORDER BY date ASC
	`

	if err := global.APP_DB.Raw(query, instanceID, instanceID, instanceID, instanceID, startDate).Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("查询实例流量历史失败: %w", err)
	}

	// 转换为历史点
	history := make([]*HistoryPoint, 0, len(results))
	for _, r := range results {
		actualUsageMB := s.calculateActualUsage(r.RxBytes, r.TxBytes, config.TrafficCountMode, config.TrafficMultiplier)
		history = append(history, &HistoryPoint{
			Date:          r.Date,
			Year:          r.Date.Year(),
			Month:         int(r.Date.Month()),
			Day:           r.Date.Day(),
			RxBytes:       r.RxBytes,
			TxBytes:       r.TxBytes,
			TotalBytes:    r.RxBytes + r.TxBytes,
			ActualUsageMB: actualUsageMB,
		})
	}

	return history, nil
}

// GetUserTrafficHistory 获取用户的流量历史（按天聚合）
// 实时从 pmacct_traffic_records 聚合所有实例的流量
func (s *QueryService) GetUserTrafficHistory(userID uint, days int) ([]*HistoryPoint, error) {
	startDate := time.Now().AddDate(0, 0, -days).Truncate(24 * time.Hour)

	// 查询用户所有实例的配置（用于计算实际用量）（包含软删除的实例）
	var instanceConfigs []struct {
		InstanceID        uint
		TrafficCountMode  string
		TrafficMultiplier float64
	}
	if err := global.APP_DB.Unscoped().Table("instances").
		Select("id as instance_id, traffic_count_mode, traffic_multiplier").
		Where("user_id = ?", userID).
		Find(&instanceConfigs).Error; err != nil {
		return nil, fmt.Errorf("查询用户实例配置失败: %w", err)
	}

	// 构建实例ID->配置的映射
	configMap := make(map[uint]struct {
		CountMode  string
		Multiplier float64
	})
	for _, cfg := range instanceConfigs {
		configMap[cfg.InstanceID] = struct {
			CountMode  string
			Multiplier float64
		}{
			CountMode:  cfg.TrafficCountMode,
			Multiplier: cfg.TrafficMultiplier,
		}
	}

	// 从 pmacct_traffic_records 按天聚合查询（包含 instance_id 用于计算实际用量）
	// 处理pmacct重启导致的累积值重置问题
	var rawResults []struct {
		Date       time.Time
		InstanceID uint
		RxBytes    int64
		TxBytes    int64
	}

	query := `
		SELECT 
			DATE(t1.timestamp) as date,
			instance_id,
			SUM(max_rx) as rx_bytes,
			SUM(max_tx) as tx_bytes
		FROM (
			-- 检测重启并分段，每段取MAX
			SELECT 
				instance_id,
				timestamp,
				segment_id,
				MAX(rx_bytes) as max_rx,
				MAX(tx_bytes) as max_tx
			FROM (
				-- 计算每条记录的segment_id（累积重启次数）
				SELECT 
					t1.instance_id,
					t1.timestamp,
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
									AND DATE(timestamp) = DATE(t2.timestamp)
							)
						WHERE t2.instance_id = t1.instance_id
							AND t2.user_id = ?
							AND t2.timestamp >= ?
							AND t2.timestamp <= t1.timestamp
							AND DATE(t2.timestamp) = DATE(t1.timestamp)
							AND (
								(t3.rx_bytes IS NOT NULL AND t2.rx_bytes < t3.rx_bytes)
								OR
								(t3.tx_bytes IS NOT NULL AND t2.tx_bytes < t3.tx_bytes)
							)
					) as segment_id
				FROM pmacct_traffic_records t1
				WHERE t1.user_id = ? AND t1.timestamp >= ?
			) AS segments
			GROUP BY instance_id, DATE(timestamp), segment_id, timestamp
		) AS daily_segments
		GROUP BY DATE(timestamp), instance_id
		ORDER BY date ASC, instance_id
	`

	if err := global.APP_DB.Raw(query, userID, startDate, userID, startDate).Scan(&rawResults).Error; err != nil {
		return nil, fmt.Errorf("查询用户流量历史失败: %w", err)
	}

	// 按天汇总所有实例
	dayMap := make(map[string]*HistoryPoint)
	for _, r := range rawResults {
		dateKey := r.Date.Format("2006-01-02")

		if _, exists := dayMap[dateKey]; !exists {
			dayMap[dateKey] = &HistoryPoint{
				Date:          r.Date,
				Year:          r.Date.Year(),
				Month:         int(r.Date.Month()),
				Day:           r.Date.Day(),
				RxBytes:       0,
				TxBytes:       0,
				TotalBytes:    0,
				ActualUsageMB: 0,
			}
		}

		// 累加原始字节
		dayMap[dateKey].RxBytes += r.RxBytes
		dayMap[dateKey].TxBytes += r.TxBytes
		dayMap[dateKey].TotalBytes += r.RxBytes + r.TxBytes

		// 根据实例配置计算实际用量
		if config, ok := configMap[r.InstanceID]; ok {
			actualMB := s.calculateActualUsage(r.RxBytes, r.TxBytes, config.CountMode, config.Multiplier)
			dayMap[dateKey].ActualUsageMB += actualMB
		}
	}

	// 转换为有序数组
	history := make([]*HistoryPoint, 0, len(dayMap))
	for _, point := range dayMap {
		history = append(history, point)
	}

	// 按日期排序
	sort.Slice(history, func(i, j int) bool {
		return history[i].Date.Before(history[j].Date)
	})

	return history, nil
}

// HistoryPoint 流量历史数据点
type HistoryPoint struct {
	Date          time.Time `json:"date"`
	Year          int       `json:"year"`
	Month         int       `json:"month"`
	Day           int       `json:"day"`
	RxBytes       int64     `json:"rx_bytes"`
	TxBytes       int64     `json:"tx_bytes"`
	TotalBytes    int64     `json:"total_bytes"`
	ActualUsageMB float64   `json:"actual_usage_mb"`
}

// calculateActualUsage 根据流量计算模式计算实际使用量（MB）
func (s *QueryService) calculateActualUsage(rxBytes, txBytes int64, countMode string, multiplier float64) float64 {
	var bytes float64
	switch countMode {
	case "out":
		bytes = float64(txBytes)
	case "in":
		bytes = float64(rxBytes)
	default: // "both"
		bytes = float64(rxBytes + txBytes)
	}
	return (bytes * multiplier) / 1048576.0 // 转换为MB
}
