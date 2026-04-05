package pmacct

import (
	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils/dbcompat"
	"time"

	"go.uber.org/zap"
)

// syncTrafficHistories 同步更新历史表（在主事务成功后执行，失败不影响采集）
// pmacct_traffic_records存储的是累积值快照，历史表应存储时间段内的最大累积值
// 前端/API查询时通过相邻时间点的差值计算实际使用量
func (s *Service) syncTrafficHistories(instanceID uint, instance *providerModel.Instance) {
	now := time.Now()
	year, month := now.Year(), int(now.Month())
	day, hour := now.Day(), now.Hour()

	s.syncInstanceHourlyHistory(instanceID, instance, year, month, day, hour, now)
	s.syncInstanceMonthlyHistory(instanceID, instance, year, month, now)
	s.syncProviderHourlyHistory(instance, year, month, day, hour, now)
	s.syncProviderMonthlyHistory(instance, year, month, now)
	s.syncUserHourlyHistory(instance, year, month, day, hour, now)
	s.syncUserMonthlyHistory(instance, year, month, now)
}

// syncInstanceHourlyHistory 更新实例流量历史表（小时级）
func (s *Service) syncInstanceHourlyHistory(instanceID uint, instance *providerModel.Instance, year, month, day, hour int, now time.Time) {
	var hourlyData struct {
		InstanceID uint
		ProviderID uint
		UserID     uint
		TrafficIn  float64
		TrafficOut float64
		TotalUsed  float64
	}

	err := global.APP_DB.Table("pmacct_traffic_records").
		Select("instance_id, provider_id, user_id, MAX(rx_bytes) DIV 1048576 as traffic_in, MAX(tx_bytes) DIV 1048576 as traffic_out, MAX(total_bytes) DIV 1048576 as total_used").
		Where("instance_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL", instanceID, year, month, day, hour).
		Group("instance_id, provider_id, user_id, year, month, day, hour").
		Scan(&hourlyData).Error

	if err != nil || hourlyData.InstanceID == 0 {
		return
	}

	var existing monitoringModel.InstanceTrafficHistory
	err = global.APP_DB.Where(
		"instance_id = ? AND year = ? AND month = ? AND day = ? AND hour = ?",
		instanceID, year, month, day, hour,
	).First(&existing).Error

	if err == nil {
		existing.ProviderID = hourlyData.ProviderID
		existing.UserID = hourlyData.UserID
		existing.TrafficIn = hourlyData.TrafficIn
		existing.TrafficOut = hourlyData.TrafficOut
		existing.TotalUsed = hourlyData.TotalUsed
		existing.RecordTime = now
		if err := global.APP_DB.Save(&existing).Error; err != nil {
			global.APP_LOG.Warn("更新实例流量历史失败",
				zap.Uint("instanceID", instanceID),
				zap.Error(err))
		}
	} else {
		newRecord := monitoringModel.InstanceTrafficHistory{
			InstanceID: hourlyData.InstanceID,
			ProviderID: hourlyData.ProviderID,
			UserID:     hourlyData.UserID,
			TrafficIn:  hourlyData.TrafficIn,
			TrafficOut: hourlyData.TrafficOut,
			TotalUsed:  hourlyData.TotalUsed,
			Year:       year,
			Month:      month,
			Day:        day,
			Hour:       hour,
			RecordTime: now,
		}
		if err := global.APP_DB.Create(&newRecord).Error; err != nil {
			global.APP_LOG.Warn("插入实例流量历史失败",
				zap.Uint("instanceID", instanceID),
				zap.Error(err))
		}
	}
}

// syncInstanceMonthlyHistory 更新实例月度汇总（day=0, hour=0）
func (s *Service) syncInstanceMonthlyHistory(instanceID uint, instance *providerModel.Instance, year, month int, now time.Time) {
	var monthlyData struct {
		InstanceID uint
		ProviderID uint
		UserID     uint
		TrafficIn  float64
		TrafficOut float64
		TotalUsed  float64
	}

	err := global.APP_DB.Raw(`
		SELECT 
			instance_id,
			provider_id,
			user_id,
			COALESCE(SUM(segment_max_rx), 0) DIV 1048576 as traffic_in,
			COALESCE(SUM(segment_max_tx), 0) DIV 1048576 as traffic_out,
			COALESCE(SUM(segment_max_total), 0) DIV 1048576 as total_used
		FROM (
			SELECT 
				instance_id, provider_id, user_id,
				segment_id,
				MAX(rx_bytes) as segment_max_rx,
				MAX(tx_bytes) as segment_max_tx,
				MAX(total_bytes) as segment_max_total
			FROM (
				SELECT 
					t1.instance_id,
					t1.provider_id,
					t1.user_id,
					t1.rx_bytes,
					t1.tx_bytes,
					t1.total_bytes,
					(
						SELECT COUNT(DISTINCT t2.id)
						FROM pmacct_traffic_records t2
						LEFT JOIN pmacct_traffic_records t3 ON t2.instance_id = t3.instance_id 
							AND t3.timestamp = (
								SELECT MAX(timestamp) 
								FROM pmacct_traffic_records 
								WHERE instance_id = t2.instance_id 
									AND timestamp < t2.timestamp
									AND year = t1.year AND month = t1.month
							)
						WHERE t2.instance_id = t1.instance_id
							AND t2.year = t1.year AND t2.month = t1.month
							AND t2.timestamp <= t1.timestamp
							AND t3.id IS NOT NULL
							AND (t2.rx_bytes < t3.rx_bytes OR t2.tx_bytes < t3.tx_bytes)
					) as segment_id
				FROM pmacct_traffic_records t1
				WHERE t1.instance_id = ? AND t1.year = ? AND t1.month = ? AND t1.deleted_at IS NULL
			) AS segments
			GROUP BY instance_id, provider_id, user_id, segment_id
		) AS segment_totals
		GROUP BY instance_id, provider_id, user_id
	`, instanceID, year, month).Scan(&monthlyData).Error

	if err != nil {
		global.APP_LOG.Warn("查询月度汇总数据失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return
	}
	if monthlyData.InstanceID == 0 {
		return
	}

	var existing monitoringModel.InstanceTrafficHistory
	err = global.APP_DB.Where(
		"instance_id = ? AND year = ? AND month = ? AND day = ? AND hour = ?",
		instanceID, year, month, 0, 0,
	).First(&existing).Error

	if err == nil {
		existing.ProviderID = monthlyData.ProviderID
		existing.UserID = monthlyData.UserID
		existing.TrafficIn = monthlyData.TrafficIn
		existing.TrafficOut = monthlyData.TrafficOut
		existing.TotalUsed = monthlyData.TotalUsed
		existing.RecordTime = now
		if err := global.APP_DB.Save(&existing).Error; err != nil {
			global.APP_LOG.Warn("更新实例月度汇总失败",
				zap.Uint("instanceID", instanceID),
				zap.Error(err))
		}
	} else {
		newRecord := monitoringModel.InstanceTrafficHistory{
			InstanceID: monthlyData.InstanceID,
			ProviderID: monthlyData.ProviderID,
			UserID:     monthlyData.UserID,
			TrafficIn:  monthlyData.TrafficIn,
			TrafficOut: monthlyData.TrafficOut,
			TotalUsed:  monthlyData.TotalUsed,
			Year:       year,
			Month:      month,
			Day:        0,
			Hour:       0,
			RecordTime: now,
		}
		if err := global.APP_DB.Create(&newRecord).Error; err != nil {
			global.APP_LOG.Warn("插入实例月度汇总失败",
				zap.Uint("instanceID", instanceID),
				zap.Error(err))
		}
	}
}

// syncProviderHourlyHistory 更新Provider流量历史表（小时级）
func (s *Service) syncProviderHourlyHistory(instance *providerModel.Instance, year, month, day, hour int, now time.Time) {
	if err := dbcompat.Exec(global.APP_DB,
		`INSERT INTO provider_traffic_histories 
			(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT 
			provider_id,
			SUM(traffic_in) as traffic_in,
			SUM(traffic_out) as traffic_out,
			SUM(total_used) as total_used,
			COUNT(DISTINCT instance_id) as instance_count,
			year, month, day, hour,
			? as record_time, ? as created_at, ? as updated_at
		FROM instance_traffic_histories
		WHERE provider_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
		GROUP BY provider_id, year, month, day, hour
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used),
			instance_count = VALUES(instance_count),
			record_time = VALUES(record_time),
			updated_at = VALUES(updated_at)`,
		`INSERT INTO provider_traffic_histories 
			(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at
		FROM (
			SELECT 
				provider_id,
				SUM(traffic_in) as traffic_in,
				SUM(traffic_out) as traffic_out,
				SUM(total_used) as total_used,
				COUNT(DISTINCT instance_id) as instance_count,
				year, month, day, hour,
				? as record_time, ? as created_at, ? as updated_at
			FROM instance_traffic_histories
			WHERE provider_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
			GROUP BY provider_id, year, month, day, hour
		) AS _src
		ON DUPLICATE KEY UPDATE
			traffic_in = _src.traffic_in,
			traffic_out = _src.traffic_out,
			total_used = _src.total_used,
			instance_count = _src.instance_count,
			record_time = _src.record_time,
			updated_at = _src.updated_at`,
		now, now, now, instance.ProviderID, year, month, day, hour).Error; err != nil {
		global.APP_LOG.Warn("更新Provider流量历史失败",
			zap.Uint("providerID", instance.ProviderID),
			zap.Error(err))
	}
}

// syncProviderMonthlyHistory 更新Provider月度汇总（day=0, hour=0）
func (s *Service) syncProviderMonthlyHistory(instance *providerModel.Instance, year, month int, now time.Time) {
	if err := dbcompat.Exec(global.APP_DB,
		`INSERT INTO provider_traffic_histories 
			(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT 
			provider_id,
			SUM(traffic_in) as traffic_in,
			SUM(traffic_out) as traffic_out,
			SUM(total_used) as total_used,
			COUNT(DISTINCT instance_id) as instance_count,
			year, month, 0 as day, 0 as hour,
			? as record_time, ? as created_at, ? as updated_at
		FROM instance_traffic_histories
		WHERE provider_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
		GROUP BY provider_id, year, month
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used),
			instance_count = VALUES(instance_count),
			record_time = VALUES(record_time),
			updated_at = VALUES(updated_at)`,
		`INSERT INTO provider_traffic_histories 
			(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at
		FROM (
			SELECT 
				provider_id,
				SUM(traffic_in) as traffic_in,
				SUM(traffic_out) as traffic_out,
				SUM(total_used) as total_used,
				COUNT(DISTINCT instance_id) as instance_count,
				year, month, 0 as day, 0 as hour,
				? as record_time, ? as created_at, ? as updated_at
			FROM instance_traffic_histories
			WHERE provider_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
			GROUP BY provider_id, year, month
		) AS _src
		ON DUPLICATE KEY UPDATE
			traffic_in = _src.traffic_in,
			traffic_out = _src.traffic_out,
			total_used = _src.total_used,
			instance_count = _src.instance_count,
			record_time = _src.record_time,
			updated_at = _src.updated_at`,
		now, now, now, instance.ProviderID, year, month).Error; err != nil {
		global.APP_LOG.Warn("更新Provider月度汇总失败",
			zap.Uint("providerID", instance.ProviderID),
			zap.Error(err))
	}
}

// syncUserHourlyHistory 更新用户流量历史表（小时级）
func (s *Service) syncUserHourlyHistory(instance *providerModel.Instance, year, month, day, hour int, now time.Time) {
	if err := dbcompat.Exec(global.APP_DB,
		`INSERT INTO user_traffic_histories 
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT 
			user_id,
			SUM(traffic_in) as traffic_in,
			SUM(traffic_out) as traffic_out,
			SUM(total_used) as total_used,
			COUNT(DISTINCT instance_id) as instance_count,
			year, month, day, hour,
			? as record_time, ? as created_at, ? as updated_at
		FROM instance_traffic_histories
		WHERE user_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
		GROUP BY user_id, year, month, day, hour
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used),
			instance_count = VALUES(instance_count),
			record_time = VALUES(record_time),
			updated_at = VALUES(updated_at)`,
		`INSERT INTO user_traffic_histories 
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at
		FROM (
			SELECT 
				user_id,
				SUM(traffic_in) as traffic_in,
				SUM(traffic_out) as traffic_out,
				SUM(total_used) as total_used,
				COUNT(DISTINCT instance_id) as instance_count,
				year, month, day, hour,
				? as record_time, ? as created_at, ? as updated_at
			FROM instance_traffic_histories
			WHERE user_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
			GROUP BY user_id, year, month, day, hour
		) AS _src
		ON DUPLICATE KEY UPDATE
			traffic_in = _src.traffic_in,
			traffic_out = _src.traffic_out,
			total_used = _src.total_used,
			instance_count = _src.instance_count,
			record_time = _src.record_time,
			updated_at = _src.updated_at`,
		now, now, now, instance.UserID, year, month, day, hour).Error; err != nil {
		global.APP_LOG.Warn("更新用户流量历史失败",
			zap.Uint("userID", instance.UserID),
			zap.Error(err))
	}
}

// syncUserMonthlyHistory 更新用户月度汇总（day=0, hour=0）
func (s *Service) syncUserMonthlyHistory(instance *providerModel.Instance, year, month int, now time.Time) {
	if err := dbcompat.Exec(global.APP_DB,
		`INSERT INTO user_traffic_histories 
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT 
			user_id,
			SUM(traffic_in) as traffic_in,
			SUM(traffic_out) as traffic_out,
			SUM(total_used) as total_used,
			COUNT(DISTINCT instance_id) as instance_count,
			year, month, 0 as day, 0 as hour,
			? as record_time, ? as created_at, ? as updated_at
		FROM instance_traffic_histories
		WHERE user_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
		GROUP BY user_id, year, month
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used),
			instance_count = VALUES(instance_count),
			record_time = VALUES(record_time),
			updated_at = VALUES(updated_at)`,
		`INSERT INTO user_traffic_histories 
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at
		FROM (
			SELECT 
				user_id,
				SUM(traffic_in) as traffic_in,
				SUM(traffic_out) as traffic_out,
				SUM(total_used) as total_used,
				COUNT(DISTINCT instance_id) as instance_count,
				year, month, 0 as day, 0 as hour,
				? as record_time, ? as created_at, ? as updated_at
			FROM instance_traffic_histories
			WHERE user_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
			GROUP BY user_id, year, month
		) AS _src
		ON DUPLICATE KEY UPDATE
			traffic_in = _src.traffic_in,
			traffic_out = _src.traffic_out,
			total_used = _src.total_used,
			instance_count = _src.instance_count,
			record_time = _src.record_time,
			updated_at = _src.updated_at`,
		now, now, now, instance.UserID, year, month).Error; err != nil {
		global.APP_LOG.Warn("更新用户月度汇总失败",
			zap.Uint("userID", instance.UserID),
			zap.Error(err))
	}
}
