package traffic

import (
	"fmt"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
)

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
