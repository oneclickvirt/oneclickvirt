package traffic

import (
	"fmt"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/model/system"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// HistoryService 流量历史记录服务
type HistoryService struct{}

// NewHistoryService 创建流量历史记录服务实例
func NewHistoryService() *HistoryService {
	return &HistoryService{}
}

// RecordInstanceTrafficHistory 记录实例流量历史数据（小时级）
// 在每次流量同步时调用使用批量插入
func (h *HistoryService) RecordInstanceTrafficHistory(tx *gorm.DB, instanceID, providerID, userID uint, data *system.PmacctData) error {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	day := now.Day()
	hour := now.Hour()

	// 使用upsert避免重复记录
	history := monitoringModel.InstanceTrafficHistory{
		InstanceID: instanceID,
		ProviderID: providerID,
		UserID:     userID,
		TrafficIn:  data.RxMB,
		TrafficOut: data.TxMB,
		TotalUsed:  data.RxMB + data.TxMB,
		Year:       year,
		Month:      month,
		Day:        day,
		Hour:       hour,
		RecordTime: now,
	}

	// 使用ON DUPLICATE KEY UPDATE确保幂等性（兼容MySQL 5.x/9.x 和 MariaDB）
	return tx.Exec(`
		INSERT INTO instance_traffic_histories 
			(instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used),
			record_time = VALUES(record_time),
			updated_at = VALUES(updated_at)
	`, history.InstanceID, history.ProviderID, history.UserID, history.TrafficIn, history.TrafficOut,
		history.TotalUsed, history.Year, history.Month, history.Day, history.Hour,
		history.RecordTime, now, now).Error
}

// AggregateDailyInstanceTraffic 聚合实例每日流量（从小时数据）
// 通常在每日凌晨或定时任务中调用
func (h *HistoryService) AggregateDailyInstanceTraffic(date time.Time) error {
	year := date.Year()
	month := int(date.Month())
	day := date.Day()

	// 从小时级数据聚合到日级
	// hour=0表示日级聚合数据
	return global.APP_DB.Exec(`
		INSERT INTO instance_traffic_histories 
			(instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at)
		SELECT 
			instance_id,
			provider_id,
			user_id,
			SUM(traffic_in) as traffic_in,
			SUM(traffic_out) as traffic_out,
			SUM(total_used) as total_used,
			year,
			month,
			day,
			0 as hour,
			? as record_time,
			? as created_at,
			? as updated_at
		FROM instance_traffic_histories
		WHERE year = ? AND month = ? AND day = ? AND hour > 0 AND deleted_at IS NULL
		GROUP BY instance_id, provider_id, user_id, year, month, day
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used),
			record_time = VALUES(record_time),
			updated_at = VALUES(updated_at)
	`, date, time.Now(), time.Now(), year, month, day).Error
}

// AggregateProviderTrafficHistory 聚合Provider流量历史（小时级）
// 从所有实例的小时级数据聚合
func (h *HistoryService) AggregateProviderTrafficHistory(providerID uint) error {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	day := now.Day()
	hour := now.Hour()

	// 聚合该Provider所有实例的当前小时流量
	return global.APP_DB.Exec(`
		INSERT INTO provider_traffic_histories 
			(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT 
			provider_id,
			SUM(traffic_in) as traffic_in,
			SUM(traffic_out) as traffic_out,
			SUM(total_used) as total_used,
			COUNT(DISTINCT instance_id) as instance_count,
			year,
			month,
			day,
			hour,
			? as record_time,
			? as created_at,
			? as updated_at
		FROM instance_traffic_histories
		WHERE provider_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
		GROUP BY provider_id, year, month, day, hour
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used),
			instance_count = VALUES(instance_count),
			record_time = VALUES(record_time),
			updated_at = VALUES(updated_at)
	`, now, time.Now(), time.Now(), providerID, year, month, day, hour).Error
}

// AggregateDailyProviderTraffic 聚合Provider每日流量
func (h *HistoryService) AggregateDailyProviderTraffic(providerID uint, date time.Time) error {
	year := date.Year()
	month := int(date.Month())
	day := date.Day()

	// 从小时级数据聚合到日级
	return global.APP_DB.Exec(`
		INSERT INTO provider_traffic_histories 
			(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT 
			provider_id,
			SUM(traffic_in) as traffic_in,
			SUM(traffic_out) as traffic_out,
			SUM(total_used) as total_used,
			MAX(instance_count) as instance_count,
			year,
			month,
			day,
			0 as hour,
			? as record_time,
			? as created_at,
			? as updated_at
		FROM provider_traffic_histories
		WHERE provider_id = ? AND year = ? AND month = ? AND day = ? AND hour > 0 AND deleted_at IS NULL
		GROUP BY provider_id, year, month, day
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used),
			instance_count = VALUES(instance_count),
			record_time = VALUES(record_time),
			updated_at = VALUES(updated_at)
	`, date, time.Now(), time.Now(), providerID, year, month, day).Error
}

// AggregateUserTrafficHistory 聚合用户流量历史（小时级）
// 从所有实例的小时级数据聚合
func (h *HistoryService) AggregateUserTrafficHistory(userID uint) error {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	day := now.Day()
	hour := now.Hour()

	// 聚合该用户所有实例的当前小时流量
	return global.APP_DB.Exec(`
		INSERT INTO user_traffic_histories 
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT 
			user_id,
			SUM(traffic_in) as traffic_in,
			SUM(traffic_out) as traffic_out,
			SUM(total_used) as total_used,
			COUNT(DISTINCT instance_id) as instance_count,
			year,
			month,
			day,
			hour,
			? as record_time,
			? as created_at,
			? as updated_at
		FROM instance_traffic_histories
		WHERE user_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
		GROUP BY user_id, year, month, day, hour
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used),
			instance_count = VALUES(instance_count),
			record_time = VALUES(record_time),
			updated_at = VALUES(updated_at)
	`, now, time.Now(), time.Now(), userID, year, month, day, hour).Error
}

// GetInstanceTrafficHistory 获取实例流量历史（用于图表展示）
// period: 时间范围，支持 "5m", "10m", "15m", "30m", "45m", "1h", "6h", "12h", "24h"
// interval: 数据点间隔（分钟），0表示自动选择最佳间隔
// includeArchived: 是否包含已归档的数据（重置前的历史数据），默认false
func (h *HistoryService) GetInstanceTrafficHistory(instanceID uint, period string, interval int, includeArchived bool) ([]monitoringModel.InstanceTrafficHistory, error) {
	now := time.Now()

	// 解析时间范围并计算起始时间
	var startTime time.Time
	var autoInterval int // 自动选择的间隔（分钟）

	switch period {
	case "5m":
		startTime = now.Add(-5 * time.Minute)
		autoInterval = 5 // 5分钟查看，每5分钟一个点
	case "10m":
		startTime = now.Add(-10 * time.Minute)
		autoInterval = 5
	case "15m":
		startTime = now.Add(-15 * time.Minute)
		autoInterval = 5
	case "30m":
		startTime = now.Add(-30 * time.Minute)
		autoInterval = 5
	case "45m":
		startTime = now.Add(-45 * time.Minute)
		autoInterval = 5
	case "1h":
		startTime = now.Add(-1 * time.Hour)
		autoInterval = 5
	case "6h":
		startTime = now.Add(-6 * time.Hour)
		autoInterval = 15 // 6小时查看，每15分钟一个点
	case "12h":
		startTime = now.Add(-12 * time.Hour)
		autoInterval = 30 // 12小时查看，每30分钟一个点
	case "24h":
		startTime = now.Add(-24 * time.Hour)
		autoInterval = 60 // 24小时查看，每60分钟一个点
	default:
		startTime = now.Add(-24 * time.Hour)
		autoInterval = 60
	}

	// 如果没有指定interval，使用自动选择的间隔
	if interval == 0 {
		interval = autoInterval
	}

	// 从主表查询数据并计算增量（pmacct_traffic_records是累积值）
	// 兼容MySQL 5.x：使用自连接计算相邻时间点之间的差值
	var histories []monitoringModel.InstanceTrafficHistory

	// 构建间隔过滤条件
	intervalCondition := ""
	if interval > 5 {
		intervalCondition = fmt.Sprintf("AND t1.minute %% %d = 0", interval)
	}

	query := fmt.Sprintf(`
		SELECT 
			t1.instance_id,
			t1.provider_id,
			t1.user_id,
			t1.timestamp as record_time,
			t1.year, t1.month, t1.day, t1.hour,
			-- 计算增量：当前值 - 前一个值（处理重启情况）
			CASE 
				WHEN t2.rx_bytes IS NULL THEN t1.rx_bytes
				WHEN t1.rx_bytes < t2.rx_bytes THEN t1.rx_bytes
				ELSE t1.rx_bytes - t2.rx_bytes
			END as traffic_in,
			CASE 
				WHEN t2.tx_bytes IS NULL THEN t1.tx_bytes
				WHEN t1.tx_bytes < t2.tx_bytes THEN t1.tx_bytes
				ELSE t1.tx_bytes - t2.tx_bytes
			END as traffic_out,
			CASE 
				WHEN t2.total_bytes IS NULL THEN t1.total_bytes
				WHEN t1.total_bytes < t2.total_bytes THEN t1.total_bytes
				ELSE t1.total_bytes - t2.total_bytes
			END as total_used
		FROM pmacct_traffic_records t1
		LEFT JOIN pmacct_traffic_records t2 ON t1.instance_id = t2.instance_id
			AND t2.timestamp = (
				SELECT MAX(timestamp)
				FROM pmacct_traffic_records
				WHERE instance_id = t1.instance_id
					AND timestamp < t1.timestamp
					AND timestamp >= ?
			)
		WHERE t1.instance_id = ? AND t1.timestamp >= ? %s
		ORDER BY t1.timestamp ASC
		LIMIT 500
	`, intervalCondition)

	err := global.APP_DB.Raw(query, startTime, instanceID, startTime).Scan(&histories).Error
	if err != nil {
		return nil, err
	}

	// 填充缺失的时间点，确保折线图连续显示
	histories = fillMissingInstanceTimePoints(histories, startTime, now, interval, instanceID, 0, 0)

	return histories, nil
}

// GetProviderTrafficHistory 获取Provider流量历史
// period: "5m", "10m", "15m", "30m", "45m", "1h", "6h", "12h", "24h"
// interval: 数据点间隔（分钟），0表示自动选择
func (h *HistoryService) GetProviderTrafficHistory(providerID uint, period string, interval int) ([]monitoringModel.ProviderTrafficHistory, error) {
	now := time.Now()

	// 解析时间范围
	var startTime time.Time
	var autoInterval int

	switch period {
	case "5m":
		startTime = now.Add(-5 * time.Minute)
		autoInterval = 5
	case "10m":
		startTime = now.Add(-10 * time.Minute)
		autoInterval = 5
	case "15m":
		startTime = now.Add(-15 * time.Minute)
		autoInterval = 5
	case "30m":
		startTime = now.Add(-30 * time.Minute)
		autoInterval = 5
	case "45m":
		startTime = now.Add(-45 * time.Minute)
		autoInterval = 5
	case "1h":
		startTime = now.Add(-1 * time.Hour)
		autoInterval = 5
	case "6h":
		startTime = now.Add(-6 * time.Hour)
		autoInterval = 15
	case "12h":
		startTime = now.Add(-12 * time.Hour)
		autoInterval = 30
	case "24h":
		startTime = now.Add(-24 * time.Hour)
		autoInterval = 60
	default:
		startTime = now.Add(-24 * time.Hour)
		autoInterval = 60
	}

	if interval == 0 {
		interval = autoInterval
	}

	// 从主表聚合Provider的所有实例数据，并计算增量
	// 处理pmacct重启导致的累积值重置问题
	var histories []monitoringModel.ProviderTrafficHistory

	// 构建间隔过滤条件
	intervalCondition := ""
	if interval > 5 {
		intervalCondition = fmt.Sprintf("AND minute %% %d = 0", interval)
	}

	// 先对每个实例进行重启检测和分段处理，然后按时间聚合，最后计算增量
	query := fmt.Sprintf(`
		SELECT 
			t1.timestamp as record_time,
			t1.year, t1.month, t1.day, t1.hour,
			t1.instance_cnt,
			CASE 
				WHEN t2.total_rx IS NULL THEN t1.total_rx
				WHEN t1.total_rx < t2.total_rx THEN t1.total_rx
				ELSE t1.total_rx - t2.total_rx
			END as traffic_in,
			CASE 
				WHEN t2.total_tx IS NULL THEN t1.total_tx
				WHEN t1.total_tx < t2.total_tx THEN t1.total_tx
				ELSE t1.total_tx - t2.total_tx
			END as traffic_out,
			CASE 
				WHEN t2.total_bytes IS NULL THEN t1.total_bytes
				WHEN t1.total_bytes < t2.total_bytes THEN t1.total_bytes
				ELSE t1.total_bytes - t2.total_bytes
			END as total_used
		FROM (
			-- 按时间戳聚合所有实例（每个实例已处理重启）
			SELECT 
				timestamp,
				year, month, day, hour, minute,
				SUM(segment_rx) as total_rx,
				SUM(segment_tx) as total_tx,
				SUM(segment_rx + segment_tx) as total_bytes,
				COUNT(DISTINCT instance_id) as instance_cnt
			FROM (
				-- 对每个实例按时间戳求和各段的流量
				SELECT 
					instance_id,
					timestamp,
					year, month, day, hour, minute,
					SUM(max_rx) as segment_rx,
					SUM(max_tx) as segment_tx
				FROM (
					-- 检测每个实例的重启并分段，每段取MAX
					SELECT 
						instance_id,
						timestamp,
						year, month, day, hour, minute,
						segment_id,
						MAX(rx_bytes) as max_rx,
						MAX(tx_bytes) as max_tx
					FROM (
						-- 计算每条记录的segment_id（累积重启次数）
						SELECT 
							t1.instance_id,
							t1.timestamp,
							t1.year, t1.month, t1.day, t1.hour, t1.minute,
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
											AND timestamp >= ?
									)
								WHERE t2.instance_id = t1.instance_id
									AND t2.provider_id = ?
									AND t2.timestamp >= ?
									AND t2.timestamp <= t1.timestamp
									AND (
										(t3.rx_bytes IS NOT NULL AND t2.rx_bytes < t3.rx_bytes)
										OR
										(t3.tx_bytes IS NOT NULL AND t2.tx_bytes < t3.tx_bytes)
									)
							) as segment_id
						FROM pmacct_traffic_records t1
						WHERE t1.provider_id = ? AND t1.timestamp >= ? %s
					) AS segments
					GROUP BY instance_id, timestamp, year, month, day, hour, minute, segment_id
				) AS instance_segments
				GROUP BY instance_id, timestamp, year, month, day, hour, minute
			) AS instance_totals
			GROUP BY timestamp, year, month, day, hour, minute
		) t1
		LEFT JOIN (
			-- 获取前一个时间点的累积值（用于计算增量）
			SELECT 
				timestamp,
				SUM(segment_rx) as total_rx,
				SUM(segment_tx) as total_tx,
				SUM(segment_rx + segment_tx) as total_bytes
			FROM (
				SELECT 
					instance_id,
					timestamp,
					SUM(max_rx) as segment_rx,
					SUM(max_tx) as segment_tx
				FROM (
					SELECT 
						instance_id,
						timestamp,
						segment_id,
						MAX(rx_bytes) as max_rx,
						MAX(tx_bytes) as max_tx
					FROM (
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
											AND timestamp >= ?
									)
								WHERE t2.instance_id = t1.instance_id
									AND t2.provider_id = ?
									AND t2.timestamp >= ?
									AND t2.timestamp <= t1.timestamp
									AND (
										(t3.rx_bytes IS NOT NULL AND t2.rx_bytes < t3.rx_bytes)
										OR
										(t3.tx_bytes IS NOT NULL AND t2.tx_bytes < t3.tx_bytes)
									)
							) as segment_id
						FROM pmacct_traffic_records t1
						WHERE t1.provider_id = ? AND t1.timestamp >= ?
					) AS segments
					GROUP BY instance_id, timestamp, segment_id
				) AS instance_segments
				GROUP BY instance_id, timestamp
			) AS instance_totals
			GROUP BY timestamp
		) t2 ON t2.timestamp = (
			SELECT MAX(timestamp)
			FROM pmacct_traffic_records
			WHERE provider_id = ? AND timestamp < t1.timestamp AND timestamp >= ?
		)
		ORDER BY t1.timestamp ASC
		LIMIT 500
	`, intervalCondition)

	err := global.APP_DB.Raw(query,
		startTime, providerID, startTime, providerID, startTime,
		startTime, providerID, startTime, providerID, startTime,
		providerID, startTime).Scan(&histories).Error
	if err != nil {
		return nil, err
	}

	// 填充ProviderID
	for i := range histories {
		histories[i].ProviderID = providerID
	}

	// 填充缺失的时间点，确保折线图连续显示
	histories = fillMissingProviderTimePoints(histories, startTime, now, interval, providerID)

	return histories, nil
}

// GetUserTrafficHistory 获取用户流量历史
// period: "5m", "10m", "15m", "30m", "45m", "1h", "6h", "12h", "24h"
// interval: 数据点间隔（分钟），0表示自动选择
func (h *HistoryService) GetUserTrafficHistory(userID uint, period string, interval int) ([]monitoringModel.UserTrafficHistory, error) {
	now := time.Now()

	// 解析时间范围
	var startTime time.Time
	var autoInterval int

	switch period {
	case "5m":
		startTime = now.Add(-5 * time.Minute)
		autoInterval = 5
	case "10m":
		startTime = now.Add(-10 * time.Minute)
		autoInterval = 5
	case "15m":
		startTime = now.Add(-15 * time.Minute)
		autoInterval = 5
	case "30m":
		startTime = now.Add(-30 * time.Minute)
		autoInterval = 5
	case "45m":
		startTime = now.Add(-45 * time.Minute)
		autoInterval = 5
	case "1h":
		startTime = now.Add(-1 * time.Hour)
		autoInterval = 5
	case "6h":
		startTime = now.Add(-6 * time.Hour)
		autoInterval = 15
	case "12h":
		startTime = now.Add(-12 * time.Hour)
		autoInterval = 30
	case "24h":
		startTime = now.Add(-24 * time.Hour)
		autoInterval = 60
	default:
		startTime = now.Add(-24 * time.Hour)
		autoInterval = 60
	}

	if interval == 0 {
		interval = autoInterval
	}

	// 从主表聚合用户的所有实例数据，并计算增量
	// 处理pmacct重启导致的累积值重置问题
	var histories []monitoringModel.UserTrafficHistory

	// 构建间隔过滤条件
	intervalCondition := ""
	if interval > 5 {
		intervalCondition = fmt.Sprintf("AND minute %% %d = 0", interval)
	}

	// 先对每个实例进行重启检测和分段处理，然后按时间聚合，最后计算增量
	query := fmt.Sprintf(`
		SELECT 
			t1.timestamp as record_time,
			t1.year, t1.month, t1.day, t1.hour,
			t1.instance_cnt,
			CASE 
				WHEN t2.total_rx IS NULL THEN t1.total_rx
				WHEN t1.total_rx < t2.total_rx THEN t1.total_rx
				ELSE t1.total_rx - t2.total_rx
			END as traffic_in,
			CASE 
				WHEN t2.total_tx IS NULL THEN t1.total_tx
				WHEN t1.total_tx < t2.total_tx THEN t1.total_tx
				ELSE t1.total_tx - t2.total_tx
			END as traffic_out,
			CASE 
				WHEN t2.total_bytes IS NULL THEN t1.total_bytes
				WHEN t1.total_bytes < t2.total_bytes THEN t1.total_bytes
				ELSE t1.total_bytes - t2.total_bytes
			END as total_used
		FROM (
			-- 按时间戳聚合所有实例（每个实例已处理重启）
			SELECT 
				timestamp,
				year, month, day, hour, minute,
				SUM(segment_rx) as total_rx,
				SUM(segment_tx) as total_tx,
				SUM(segment_rx + segment_tx) as total_bytes,
				COUNT(DISTINCT instance_id) as instance_cnt
			FROM (
				-- 对每个实例按时间戳求和各段的流量
				SELECT 
					instance_id,
					timestamp,
					year, month, day, hour, minute,
					SUM(max_rx) as segment_rx,
					SUM(max_tx) as segment_tx
				FROM (
					-- 检测每个实例的重启并分段，每段取MAX
					SELECT 
						instance_id,
						timestamp,
						year, month, day, hour, minute,
						segment_id,
						MAX(rx_bytes) as max_rx,
						MAX(tx_bytes) as max_tx
					FROM (
						-- 计算每条记录的segment_id（累积重启次数）
						SELECT 
							t1.instance_id,
							t1.timestamp,
							t1.year, t1.month, t1.day, t1.hour, t1.minute,
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
											AND timestamp >= ?
									)
								WHERE t2.instance_id = t1.instance_id
									AND t2.user_id = ?
									AND t2.timestamp >= ?
									AND t2.timestamp <= t1.timestamp
									AND (
										(t3.rx_bytes IS NOT NULL AND t2.rx_bytes < t3.rx_bytes)
										OR
										(t3.tx_bytes IS NOT NULL AND t2.tx_bytes < t3.tx_bytes)
									)
							) as segment_id
						FROM pmacct_traffic_records t1
						WHERE t1.user_id = ? AND t1.timestamp >= ? %s
					) AS segments
					GROUP BY instance_id, timestamp, year, month, day, hour, minute, segment_id
				) AS instance_segments
				GROUP BY instance_id, timestamp, year, month, day, hour, minute
			) AS instance_totals
			GROUP BY timestamp, year, month, day, hour, minute
		) t1
		LEFT JOIN (
			-- 获取前一个时间点的累积值（用于计算增量）
			SELECT 
				timestamp,
				SUM(segment_rx) as total_rx,
				SUM(segment_tx) as total_tx,
				SUM(segment_rx + segment_tx) as total_bytes
			FROM (
				SELECT 
					instance_id,
					timestamp,
					SUM(max_rx) as segment_rx,
					SUM(max_tx) as segment_tx
				FROM (
					SELECT 
						instance_id,
						timestamp,
						segment_id,
						MAX(rx_bytes) as max_rx,
						MAX(tx_bytes) as max_tx
					FROM (
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
											AND timestamp >= ?
									)
								WHERE t2.instance_id = t1.instance_id
									AND t2.user_id = ?
									AND t2.timestamp >= ?
									AND t2.timestamp <= t1.timestamp
									AND (
										(t3.rx_bytes IS NOT NULL AND t2.rx_bytes < t3.rx_bytes)
										OR
										(t3.tx_bytes IS NOT NULL AND t2.tx_bytes < t3.tx_bytes)
									)
							) as segment_id
						FROM pmacct_traffic_records t1
						WHERE t1.user_id = ? AND t1.timestamp >= ?
					) AS segments
					GROUP BY instance_id, timestamp, segment_id
				) AS instance_segments
				GROUP BY instance_id, timestamp
			) AS instance_totals
			GROUP BY timestamp
		) t2 ON t2.timestamp = (
			SELECT MAX(timestamp)
			FROM pmacct_traffic_records
			WHERE user_id = ? AND timestamp < t1.timestamp AND timestamp >= ?
		)
		ORDER BY t1.timestamp ASC
		LIMIT 500
	`, intervalCondition)

	err := global.APP_DB.Raw(query,
		startTime, userID, startTime, userID, startTime,
		startTime, userID, startTime, userID, startTime,
		userID, startTime).Scan(&histories).Error
	if err != nil {
		return nil, err
	}

	// 填充UserID
	for i := range histories {
		histories[i].UserID = userID
	}

	// 填充缺失的时间点，确保折线图连续显示
	histories = fillMissingUserTimePoints(histories, startTime, now, interval, userID)

	return histories, nil
}

// CleanupOldHistory 清理过期的历史数据
// 默认保留72小时数据，自动清理更早的数据
func (h *HistoryService) CleanupOldHistory() error {
	// 固定保留72小时
	cutoffTime := time.Now().Add(-72 * time.Hour)

	// 清理实例历史
	if err := global.APP_DB.Where("record_time < ?", cutoffTime).
		Delete(&monitoringModel.InstanceTrafficHistory{}).Error; err != nil {
		global.APP_LOG.Error("清理实例流量历史失败", zap.Error(err))
		return err
	}

	// 清理Provider历史
	if err := global.APP_DB.Where("record_time < ?", cutoffTime).
		Delete(&monitoringModel.ProviderTrafficHistory{}).Error; err != nil {
		global.APP_LOG.Error("清理Provider流量历史失败", zap.Error(err))
		return err
	}

	// 清理用户历史
	if err := global.APP_DB.Where("record_time < ?", cutoffTime).
		Delete(&monitoringModel.UserTrafficHistory{}).Error; err != nil {
		global.APP_LOG.Error("清理用户流量历史失败", zap.Error(err))
		return err
	}

	global.APP_LOG.Info("清理历史流量数据完成", zap.String("保留时长", "72小时"))
	return nil
}

// BatchRecordInstanceHistory 批量记录实例流量历史
func (h *HistoryService) BatchRecordInstanceHistory(instances []providerModel.Instance, trafficDataMap map[uint]*system.PmacctData) error {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	day := now.Day()
	hour := now.Hour()

	// 批量插入
	var histories []monitoringModel.InstanceTrafficHistory
	for _, instance := range instances {
		data, exists := trafficDataMap[instance.ID]
		if !exists {
			continue
		}

		histories = append(histories, monitoringModel.InstanceTrafficHistory{
			InstanceID: instance.ID,
			ProviderID: instance.ProviderID,
			UserID:     instance.UserID,
			TrafficIn:  data.RxMB,
			TrafficOut: data.TxMB,
			TotalUsed:  data.RxMB + data.TxMB,
			Year:       year,
			Month:      month,
			Day:        day,
			Hour:       hour,
			RecordTime: now,
		})
	}

	if len(histories) == 0 {
		return nil
	}

	// 使用批量插入，提高性能
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		for _, history := range histories {
			if err := tx.Exec(`
				INSERT INTO instance_traffic_histories 
					(instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON DUPLICATE KEY UPDATE
					traffic_in = VALUES(traffic_in),
					traffic_out = VALUES(traffic_out),
					total_used = VALUES(total_used),
					record_time = VALUES(record_time),
					updated_at = VALUES(updated_at)
			`, history.InstanceID, history.ProviderID, history.UserID, history.TrafficIn, history.TrafficOut,
				history.TotalUsed, history.Year, history.Month, history.Day, history.Hour,
				history.RecordTime, now, now).Error; err != nil {
				return fmt.Errorf("批量记录实例流量历史失败: %w", err)
			}
		}
		return nil
	})
}

// fillMissingInstanceTimePoints 填充缺失的实例流量时间点
// 在展示层自动构造缺失的时间点，流量值设为0，确保折线图连续显示
func fillMissingInstanceTimePoints(histories []monitoringModel.InstanceTrafficHistory, startTime, endTime time.Time, intervalMinutes int, instanceID, providerID, userID uint) []monitoringModel.InstanceTrafficHistory {
	if len(histories) == 0 {
		return histories
	}

	// 创建时间点映射，快速查找已有数据
	existingMap := make(map[time.Time]monitoringModel.InstanceTrafficHistory)
	for _, h := range histories {
		existingMap[h.RecordTime] = h
	}

	// 生成完整的时间点序列
	result := make([]monitoringModel.InstanceTrafficHistory, 0)
	interval := time.Duration(intervalMinutes) * time.Minute

	// 对齐起始时间到间隔边界
	alignedStart := startTime.Truncate(interval)
	if alignedStart.Before(startTime) {
		alignedStart = alignedStart.Add(interval)
	}

	for currentTime := alignedStart; currentTime.Before(endTime) || currentTime.Equal(endTime); currentTime = currentTime.Add(interval) {
		if existing, found := existingMap[currentTime]; found {
			// 已有数据，直接使用
			result = append(result, existing)
		} else {
			// 缺失数据，填充0值
			result = append(result, monitoringModel.InstanceTrafficHistory{
				InstanceID: instanceID,
				ProviderID: providerID,
				UserID:     userID,
				TrafficIn:  0,
				TrafficOut: 0,
				TotalUsed:  0,
				Year:       currentTime.Year(),
				Month:      int(currentTime.Month()),
				Day:        currentTime.Day(),
				Hour:       currentTime.Hour(),
				RecordTime: currentTime,
			})
		}
	}

	return result
}

// fillMissingProviderTimePoints 填充缺失的Provider流量时间点
func fillMissingProviderTimePoints(histories []monitoringModel.ProviderTrafficHistory, startTime, endTime time.Time, intervalMinutes int, providerID uint) []monitoringModel.ProviderTrafficHistory {
	if len(histories) == 0 {
		return histories
	}

	existingMap := make(map[time.Time]monitoringModel.ProviderTrafficHistory)
	for _, h := range histories {
		existingMap[h.RecordTime] = h
	}

	result := make([]monitoringModel.ProviderTrafficHistory, 0)
	interval := time.Duration(intervalMinutes) * time.Minute

	alignedStart := startTime.Truncate(interval)
	if alignedStart.Before(startTime) {
		alignedStart = alignedStart.Add(interval)
	}

	for currentTime := alignedStart; currentTime.Before(endTime) || currentTime.Equal(endTime); currentTime = currentTime.Add(interval) {
		if existing, found := existingMap[currentTime]; found {
			result = append(result, existing)
		} else {
			result = append(result, monitoringModel.ProviderTrafficHistory{
				ProviderID:    providerID,
				TrafficIn:     0,
				TrafficOut:    0,
				TotalUsed:     0,
				InstanceCount: 0,
				Year:          currentTime.Year(),
				Month:         int(currentTime.Month()),
				Day:           currentTime.Day(),
				Hour:          currentTime.Hour(),
				RecordTime:    currentTime,
			})
		}
	}

	return result
}

// fillMissingUserTimePoints 填充缺失的用户流量时间点
func fillMissingUserTimePoints(histories []monitoringModel.UserTrafficHistory, startTime, endTime time.Time, intervalMinutes int, userID uint) []monitoringModel.UserTrafficHistory {
	if len(histories) == 0 {
		return histories
	}

	existingMap := make(map[time.Time]monitoringModel.UserTrafficHistory)
	for _, h := range histories {
		existingMap[h.RecordTime] = h
	}

	result := make([]monitoringModel.UserTrafficHistory, 0)
	interval := time.Duration(intervalMinutes) * time.Minute

	alignedStart := startTime.Truncate(interval)
	if alignedStart.Before(startTime) {
		alignedStart = alignedStart.Add(interval)
	}

	for currentTime := alignedStart; currentTime.Before(endTime) || currentTime.Equal(endTime); currentTime = currentTime.Add(interval) {
		if existing, found := existingMap[currentTime]; found {
			result = append(result, existing)
		} else {
			result = append(result, monitoringModel.UserTrafficHistory{
				UserID:        userID,
				TrafficIn:     0,
				TrafficOut:    0,
				TotalUsed:     0,
				InstanceCount: 0,
				Year:          currentTime.Year(),
				Month:         int(currentTime.Month()),
				Day:           currentTime.Day(),
				Hour:          currentTime.Hour(),
				RecordTime:    currentTime,
			})
		}
	}

	return result
}
