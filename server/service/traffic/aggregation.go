package traffic

import (
	"fmt"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"

	"go.uber.org/zap"
)

// AggregationService 流量聚合服务 - 定期将pmacct原始数据聚合到缓存表
type AggregationService struct {
	queryService *QueryService
}

// NewAggregationService 创建流量聚合服务
func NewAggregationService() *AggregationService {
	return &AggregationService{
		queryService: NewQueryService(),
	}
}

// AggregateMonthlyTraffic 聚合指定月份的流量数据到缓存表
// 用于加速查询，避免每次都执行复杂的分段计算
func (s *AggregationService) AggregateMonthlyTraffic(year, month int) error {
	global.APP_LOG.Info("开始聚合流量数据",
		zap.Int("year", year),
		zap.Int("month", month))

	// 获取所有有流量记录的实例ID
	var instanceIDs []uint
	err := global.APP_DB.Table("pmacct_traffic_records").
		Select("DISTINCT instance_id").
		Where("year = ? AND month = ?", year, month).
		Pluck("instance_id", &instanceIDs).Error

	if err != nil {
		return fmt.Errorf("获取实例ID列表失败: %w", err)
	}

	if len(instanceIDs) == 0 {
		global.APP_LOG.Info("没有需要聚合的流量数据")
		return nil
	}

	global.APP_LOG.Info("找到需要聚合的实例",
		zap.Int("count", len(instanceIDs)))

	// 预加载所有实例的provider_id和user_id
	type InstanceInfo struct {
		ID         uint
		ProviderID uint
		UserID     uint
	}
	var instanceInfos []InstanceInfo
	err = global.APP_DB.Table("instances").
		Select("id, provider_id, user_id").
		Where("id IN ?", instanceIDs).
		Find(&instanceInfos).Error
	if err != nil {
		return fmt.Errorf("预加载实例信息失败: %w", err)
	}

	// 创建实例信息映射
	instanceInfoMap := make(map[uint]InstanceInfo)
	for _, info := range instanceInfos {
		instanceInfoMap[info.ID] = info
	}

	// 分批处理，避免一次性处理太多数据
	batchSize := 50
	successCount := 0
	errorCount := 0

	for i := 0; i < len(instanceIDs); i += batchSize {
		end := i + batchSize
		if end > len(instanceIDs) {
			end = len(instanceIDs)
		}
		batch := instanceIDs[i:end]

		// 计算这批实例的流量
		statsMap, err := s.queryService.computeBatchMonthlyTraffic(batch, year, month)
		if err != nil {
			global.APP_LOG.Error("计算流量失败",
				zap.Error(err),
				zap.Int("batch_start", i),
				zap.Int("batch_end", end))
			errorCount += len(batch)
			continue
		}

		// 保存到缓存表
		for instanceID, stats := range statsMap {
			instanceInfo, exists := instanceInfoMap[instanceID]
			if !exists {
				global.APP_LOG.Warn("实例信息不存在",
					zap.Uint("instance_id", instanceID))
				errorCount++
				continue
			}
			err = s.saveToCacheWithInfo(instanceID, instanceInfo.ProviderID, instanceInfo.UserID, year, month, stats)
			if err != nil {
				global.APP_LOG.Error("保存流量缓存失败",
					zap.Error(err),
					zap.Uint("instance_id", instanceID))
				errorCount++
			} else {
				successCount++
			}
		}
	}

	global.APP_LOG.Info("流量聚合完成",
		zap.Int("success", successCount),
		zap.Int("error", errorCount))

	return nil
}

// saveToCacheWithInfo 保存流量统计到缓存表（使用预加载的实例信息）
func (s *AggregationService) saveToCacheWithInfo(instanceID, providerID, userID uint, year, month int, stats *TrafficStats) error {
	// 使用UPSERT逻辑（ON DUPLICATE KEY UPDATE）
	record := monitoringModel.InstanceTrafficHistory{
		InstanceID: instanceID,
		ProviderID: providerID,
		UserID:     userID,
		TrafficIn:  stats.RxBytes / 1048576,    // 转换为MB
		TrafficOut: stats.TxBytes / 1048576,    // 转换为MB
		TotalUsed:  int64(stats.ActualUsageMB), // 已经是MB
		Year:       year,
		Month:      month,
		Day:        0, // 0表示月度汇总
		Hour:       0, // 0表示月度汇总
		RecordTime: time.Now(),
	}

	// MySQL: 使用ON DUPLICATE KEY UPDATE
	// 先尝试创建，如果唯一键冲突则更新
	err := global.APP_DB.Exec(`
		INSERT INTO instance_traffic_histories 
			(instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, 
			 year, month, day, hour, record_time, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used),
			record_time = VALUES(record_time),
			updated_at = NOW()
	`, record.InstanceID, record.ProviderID, record.UserID,
		record.TrafficIn, record.TrafficOut, record.TotalUsed,
		record.Year, record.Month, record.Day, record.Hour, record.RecordTime).Error

	return err
}

// saveToCache 保存流量统计到缓存表（保留用于单独调用）
func (s *AggregationService) saveToCache(instanceID uint, year, month int, stats *TrafficStats) error {
	// 获取instance的provider_id和user_id
	var instance struct {
		ProviderID uint
		UserID     uint
	}
	err := global.APP_DB.Table("instances").
		Select("provider_id, user_id").
		Where("id = ?", instanceID).
		First(&instance).Error

	if err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	return s.saveToCacheWithInfo(instanceID, instance.ProviderID, instance.UserID, year, month, stats)
}

// AggregateCurrentMonth 聚合当月流量数据（定时任务调用）
func (s *AggregationService) AggregateCurrentMonth() error {
	now := time.Now()
	return s.AggregateMonthlyTraffic(now.Year(), int(now.Month()))
}

// AggregateDailyTraffic 聚合每日流量数据（可选，用于更细粒度的缓存）
// 每日聚合也需要使用完整的分段逻辑处理pmacct重启
func (s *AggregationService) AggregateDailyTraffic(year, month, day int) error {
	global.APP_LOG.Info("开始聚合每日流量数据",
		zap.Int("year", year),
		zap.Int("month", month),
		zap.Int("day", day))

	// 获取当天有流量记录的实例ID
	var instanceIDs []uint
	err := global.APP_DB.Table("pmacct_traffic_records").
		Select("DISTINCT instance_id").
		Where("year = ? AND month = ? AND day = ?", year, month, day).
		Pluck("instance_id", &instanceIDs).Error

	if err != nil {
		return fmt.Errorf("获取实例ID列表失败: %w", err)
	}

	if len(instanceIDs) == 0 {
		return nil
	}

	// 预加载所有实例信息
	type InstanceInfo struct {
		ID         uint
		ProviderID uint
		UserID     uint
	}
	var instanceInfos []InstanceInfo
	err = global.APP_DB.Table("instances").
		Select("id, provider_id, user_id").
		Where("id IN ?", instanceIDs).
		Find(&instanceInfos).Error
	if err != nil {
		return fmt.Errorf("预加载实例信息失败: %w", err)
	}

	instanceInfoMap := make(map[uint]InstanceInfo)
	for _, info := range instanceInfos {
		instanceInfoMap[info.ID] = info
	}

	// 对每个实例计算当天的流量（使用完整分段逻辑）
	for _, instanceID := range instanceIDs {
		instanceInfo, exists := instanceInfoMap[instanceID]
		if !exists {
			global.APP_LOG.Warn("实例信息不存在",
				zap.Uint("instance_id", instanceID))
			continue
		}

		dailyStats, err := s.computeDailyTraffic(instanceID, year, month, day)
		if err != nil {
			global.APP_LOG.Error("计算每日流量失败",
				zap.Uint("instance_id", instanceID),
				zap.Error(err))
			continue
		}

		// 保存到缓存表（day!=0, hour=0表示按天缓存）
		err = s.saveDailyCacheWithInfo(instanceID, instanceInfo.ProviderID, instanceInfo.UserID, year, month, day, dailyStats)
		if err != nil {
			global.APP_LOG.Error("保存每日缓存失败",
				zap.Uint("instance_id", instanceID),
				zap.Error(err))
		}
	}

	global.APP_LOG.Info("每日流量聚合完成",
		zap.Int("instance_count", len(instanceIDs)))

	return nil
}

// computeDailyTraffic 计算实例的每日流量（处理pmacct重启）
func (s *AggregationService) computeDailyTraffic(instanceID uint, year, month, day int) (*TrafficStats, error) {
	// 使用与月度计算相同的分段逻辑
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
									AND year = ? AND month = ? AND day = ?
							)
						WHERE t2.instance_id = ?
							AND t2.year = ? AND t2.month = ? AND t2.day = ?
							AND t2.timestamp <= t1.timestamp
							AND (
								(t3.rx_bytes IS NOT NULL AND t2.rx_bytes < t3.rx_bytes)
								OR
								(t3.tx_bytes IS NOT NULL AND t2.tx_bytes < t3.tx_bytes)
							)
					) as segment_id
				FROM pmacct_traffic_records t1
				WHERE t1.instance_id = ? AND t1.year = ? AND t1.month = ? AND t1.day = ?
			) AS segments
			GROUP BY segment_id
		) AS segment_max
	`

	var result struct {
		RxBytes int64
		TxBytes int64
	}

	err := global.APP_DB.Raw(query,
		year, month, day, instanceID, year, month, day, instanceID, year, month, day).
		Scan(&result).Error
	if err != nil {
		return nil, fmt.Errorf("查询每日流量失败: %w", err)
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
	stats.ActualUsageMB = s.queryService.calculateActualUsage(
		result.RxBytes,
		result.TxBytes,
		providerConfig.TrafficCountMode,
		providerConfig.TrafficMultiplier,
	)

	return stats, nil
}

// saveDailyCacheWithInfo 保存每日缓存数据（使用预加载的实例信息）
func (s *AggregationService) saveDailyCacheWithInfo(instanceID, providerID, userID uint, year, month, day int, stats *TrafficStats) error {
	// 转换为MB
	trafficInMB := stats.RxBytes / 1048576
	trafficOutMB := stats.TxBytes / 1048576
	totalUsedMB := int64(stats.ActualUsageMB)

	// UPSERT：存在则更新，不存在则插入（day!=0, hour=0表示按天缓存）
	query := `
		INSERT INTO instance_traffic_histories 
			(instance_id, provider_id, user_id, year, month, day, hour, 
			 traffic_in, traffic_out, total_used, record_time, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, NOW(), NOW(), NOW())
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used),
			record_time = NOW(),
			updated_at = NOW()
	`

	return global.APP_DB.Exec(query,
		instanceID, providerID, userID,
		year, month, day,
		trafficInMB, trafficOutMB, totalUsedMB,
	).Error
}

// saveDailyCache 保存每日缓存数据（保留用于单独调用）
func (s *AggregationService) saveDailyCache(instanceID uint, year, month, day int, stats *TrafficStats) error {
	// 获取实例关联信息
	var instance struct {
		ProviderID uint
		UserID     uint
	}
	err := global.APP_DB.Table("instances").
		Select("provider_id, user_id").
		Where("id = ?", instanceID).
		Scan(&instance).Error
	if err != nil {
		return fmt.Errorf("查询实例信息失败: %w", err)
	}

	return s.saveDailyCacheWithInfo(instanceID, instance.ProviderID, instance.UserID, year, month, day, stats)
}

// CleanOldCache 清理过期的缓存数据
func (s *AggregationService) CleanOldCache(retentionMonths int) error {
	cutoffDate := time.Now().AddDate(0, -retentionMonths, 0)
	cutoffYear := cutoffDate.Year()
	cutoffMonth := int(cutoffDate.Month())

	result := global.APP_DB.
		Where("year < ? OR (year = ? AND month < ?)", cutoffYear, cutoffYear, cutoffMonth).
		Delete(&monitoringModel.InstanceTrafficHistory{})

	if result.Error != nil {
		return result.Error
	}

	global.APP_LOG.Info("清理旧缓存完成",
		zap.Int64("deleted_rows", result.RowsAffected))

	return nil
}
