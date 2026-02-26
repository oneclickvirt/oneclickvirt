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

	// 使用 GORM 确保幂等性（兼容MySQL 5.x/9.x 和 MariaDB）
	var existing monitoringModel.InstanceTrafficHistory
	err := tx.Where(
		"instance_id = ? AND year = ? AND month = ? AND day = ? AND hour = ?",
		history.InstanceID, history.Year, history.Month, history.Day, history.Hour,
	).First(&existing).Error

	if err == nil {
		// 更新现有记录
		existing.ProviderID = history.ProviderID
		existing.UserID = history.UserID
		existing.TrafficIn = history.TrafficIn
		existing.TrafficOut = history.TrafficOut
		existing.TotalUsed = history.TotalUsed
		existing.RecordTime = history.RecordTime
		return tx.Save(&existing).Error
	}

	// 插入新记录
	return tx.Create(&history).Error
}

// AggregateDailyInstanceTraffic 聚合实例每日流量（从小时数据）
// 通常在每日凌晨或定时任务中调用
func (h *HistoryService) AggregateDailyInstanceTraffic(date time.Time) error {
	year := date.Year()
	month := int(date.Month())
	day := date.Day()

	// 从小时级数据聚合到日级 - 先查询聚合结果
	type DailyAggregate struct {
		InstanceID uint
		ProviderID uint
		UserID     uint
		TrafficIn  int64
		TrafficOut int64
		TotalUsed  int64
	}

	var aggregates []DailyAggregate
	err := global.APP_DB.Table("instance_traffic_histories").
		Select("instance_id, provider_id, user_id, SUM(traffic_in) as traffic_in, SUM(traffic_out) as traffic_out, SUM(total_used) as total_used").
		Where("year = ? AND month = ? AND day = ? AND hour > 0 AND deleted_at IS NULL", year, month, day).
		Group("instance_id, provider_id, user_id, year, month, day").
		Scan(&aggregates).Error

	if err != nil {
		return err
	}

	// 使用GORM保存或更新每个聚合结果
	now := time.Now()
	for _, agg := range aggregates {
		var existing monitoringModel.InstanceTrafficHistory
		err := global.APP_DB.Where(
			"instance_id = ? AND year = ? AND month = ? AND day = ? AND hour = ?",
			agg.InstanceID, year, month, day, 0,
		).First(&existing).Error

		if err == nil {
			// 更新现有记录
			existing.ProviderID = agg.ProviderID
			existing.UserID = agg.UserID
			existing.TrafficIn = agg.TrafficIn
			existing.TrafficOut = agg.TrafficOut
			existing.TotalUsed = agg.TotalUsed
			existing.RecordTime = now
			if err := global.APP_DB.Save(&existing).Error; err != nil {
				return err
			}
		} else {
			// 插入新记录
			newRecord := monitoringModel.InstanceTrafficHistory{
				InstanceID: agg.InstanceID,
				ProviderID: agg.ProviderID,
				UserID:     agg.UserID,
				TrafficIn:  agg.TrafficIn,
				TrafficOut: agg.TrafficOut,
				TotalUsed:  agg.TotalUsed,
				Year:       year,
				Month:      month,
				Day:        day,
				Hour:       0,
				RecordTime: now,
			}
			if err := global.APP_DB.Create(&newRecord).Error; err != nil {
				return err
			}
		}
	}

	return nil
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
			// 检查是否存在
			var existing monitoringModel.InstanceTrafficHistory
			err := tx.Where(
				"instance_id = ? AND year = ? AND month = ? AND day = ? AND hour = ?",
				history.InstanceID, history.Year, history.Month, history.Day, history.Hour,
			).First(&existing).Error

			if err == nil {
				// 更新现有记录
				existing.ProviderID = history.ProviderID
				existing.UserID = history.UserID
				existing.TrafficIn = history.TrafficIn
				existing.TrafficOut = history.TrafficOut
				existing.TotalUsed = history.TotalUsed
				existing.RecordTime = history.RecordTime
				if err := tx.Save(&existing).Error; err != nil {
					return fmt.Errorf("批量更新实例流量历史失败: %w", err)
				}
			} else {
				// 插入新记录
				if err := tx.Create(&history).Error; err != nil {
					return fmt.Errorf("批量插入实例流量历史失败: %w", err)
				}
			}
		}
		return nil
	})
}
