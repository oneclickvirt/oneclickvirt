package pmacct

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
	"oneclickvirt/utils/dbcompat"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// lastMaxTraffic 存储历史最大流量值
type lastMaxTraffic struct {
	MaxRxBytes    int64
	MaxTxBytes    int64
	MaxTotalBytes int64
	LastTimestamp *time.Time
}

// fillGapRecords 填补连接异常恢复后的空白期数据
func (s *Service) fillGapRecords(instanceID uint, instance *providerModel.Instance, monitor *monitoringModel.PmacctMonitor,
	lastMax lastMaxTraffic, firstNewTimestamp time.Time, recordTimeStr string) {

	if lastMax.LastTimestamp == nil || lastMax.LastTimestamp.IsZero() {
		return
	}

	fillStart := lastMax.LastTimestamp.Add(time.Minute)
	fillEnd := firstNewTimestamp.Add(-time.Minute)

	if !fillStart.Before(fillEnd) && !fillStart.Equal(fillEnd) {
		return
	}

	var fillRecords []monitoringModel.PmacctTrafficRecord
	for current := fillStart; current.Before(firstNewTimestamp); current = current.Add(time.Minute) {
		fillRecords = append(fillRecords, monitoringModel.PmacctTrafficRecord{
			InstanceID:   instanceID,
			UserID:       instance.UserID,
			ProviderID:   instance.ProviderID,
			ProviderType: instance.Provider,
			MappedIP:     monitor.MappedIP,
			RxBytes:      lastMax.MaxRxBytes,
			TxBytes:      lastMax.MaxTxBytes,
			TotalBytes:   lastMax.MaxTotalBytes,
			Timestamp:    current,
			Year:         current.Year(),
			Month:        int(current.Month()),
			Day:          current.Day(),
			Hour:         current.Hour(),
			Minute:       current.Minute(),
		})
	}

	if len(fillRecords) == 0 {
		return
	}

	sort.Slice(fillRecords, func(i, j int) bool {
		return fillRecords[i].Timestamp.Before(fillRecords[j].Timestamp)
	})

	fillBatchSize := 20
	for i := 0; i < len(fillRecords); i += fillBatchSize {
		end := i + fillBatchSize
		if end > len(fillRecords) {
			end = len(fillRecords)
		}
		batch := fillRecords[i:end]

		err := s.retryDBOperation(func() error {
			return global.APP_DB.Transaction(func(tx *gorm.DB) error {
				return s.execBatchInsertIgnore(tx, batch, recordTimeStr)
			})
		})
		if err != nil {
			global.APP_LOG.Warn("填补空白期数据失败（继续执行）",
				zap.Uint("instanceID", instanceID),
				zap.Int("count", len(batch)),
				zap.Error(err))
		}
	}

	global.APP_LOG.Debug("已填补空白期数据",
		zap.Uint("instanceID", instanceID),
		zap.Int("fillCount", len(fillRecords)),
		zap.Time("fillStart", fillStart),
		zap.Time("fillEnd", fillEnd.Add(time.Minute)))
}

// batchUpsertRecords 批量插入新采集的流量记录
func (s *Service) batchUpsertRecords(instanceID uint, records []monitoringModel.PmacctTrafficRecord, recordTimeStr string) (int, error) {
	var imported int
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.Before(records[j].Timestamp)
	})
	batchSize := 20

	for i := 0; i < len(records); i += batchSize {
		end := i + batchSize
		if end > len(records) {
			end = len(records)
		}
		batch := records[i:end]

		err := s.retryDBOperation(func() error {
			return global.APP_DB.Transaction(func(tx *gorm.DB) error {
				return s.execBatchUpsert(tx, batch, recordTimeStr)
			})
		})
		if err != nil {
			global.APP_LOG.Error("批量创建流量记录失败",
				zap.Uint("instanceID", instanceID),
				zap.Int("count", len(batch)),
				zap.Error(err))
			return imported, fmt.Errorf("failed to batch create records: %w", err)
		}
		imported += len(batch)
	}
	return imported, nil
}

// execBatchInsertIgnore 执行 INSERT IGNORE 批量插入（用于填补数据）
func (s *Service) execBatchInsertIgnore(tx *gorm.DB, batch []monitoringModel.PmacctTrafficRecord, recordTimeStr string) error {
	values := make([]string, 0, len(batch))
	args := make([]interface{}, 0, len(batch)*15)

	for _, record := range batch {
		values = append(values, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args,
			record.InstanceID, record.UserID, record.ProviderID, record.ProviderType,
			record.MappedIP, record.RxBytes, record.TxBytes, record.TotalBytes,
			record.Timestamp, record.Year, record.Month, record.Day, record.Hour, record.Minute,
			recordTimeStr,
		)
	}

	insertSQL := fmt.Sprintf(`
		INSERT IGNORE INTO pmacct_traffic_records 
		(instance_id, user_id, provider_id, provider_type, mapped_ip, 
		 rx_bytes, tx_bytes, total_bytes, timestamp, 
		 year, month, day, hour, minute, record_time)
		VALUES %s
	`, strings.Join(values, ","))

	return tx.Exec(insertSQL, args...).Error
}

func (s *Service) retryDBOperation(operation func() error) error {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return utils.RetryableDBOperation(ctx, operation, 8)
}

// execBatchUpsert 执行 ON DUPLICATE KEY UPDATE 批量插入（用于新数据）
func (s *Service) execBatchUpsert(tx *gorm.DB, batch []monitoringModel.PmacctTrafficRecord, recordTimeStr string) error {
	values := make([]string, 0, len(batch))
	args := make([]interface{}, 0, len(batch)*15)

	for _, record := range batch {
		values = append(values, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args,
			record.InstanceID, record.UserID, record.ProviderID, record.ProviderType,
			record.MappedIP, record.RxBytes, record.TxBytes, record.TotalBytes,
			record.Timestamp, record.Year, record.Month, record.Day, record.Hour, record.Minute,
			recordTimeStr,
		)
	}

	var insertSQL string
	if dbcompat.UseRowAlias() {
		insertSQL = fmt.Sprintf(`
			INSERT INTO pmacct_traffic_records 
			(instance_id, user_id, provider_id, provider_type, mapped_ip, 
			 rx_bytes, tx_bytes, total_bytes, timestamp, 
			 year, month, day, hour, minute, record_time)
			VALUES %s AS _new_row
			ON DUPLICATE KEY UPDATE
				rx_bytes = IF(
					TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR _new_row.rx_bytes > pmacct_traffic_records.rx_bytes,
					_new_row.rx_bytes,
					pmacct_traffic_records.rx_bytes
				),
				tx_bytes = IF(
					TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR _new_row.tx_bytes > pmacct_traffic_records.tx_bytes,
					_new_row.tx_bytes,
					pmacct_traffic_records.tx_bytes
				),
				total_bytes = IF(
					TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR _new_row.total_bytes > pmacct_traffic_records.total_bytes,
					_new_row.total_bytes,
					pmacct_traffic_records.total_bytes
				),
				record_time = IF(
					TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR _new_row.total_bytes > pmacct_traffic_records.total_bytes,
					_new_row.record_time,
					pmacct_traffic_records.record_time
				)
		`, strings.Join(values, ","))
	} else {
		insertSQL = fmt.Sprintf(`
			INSERT INTO pmacct_traffic_records 
			(instance_id, user_id, provider_id, provider_type, mapped_ip, 
			 rx_bytes, tx_bytes, total_bytes, timestamp, 
			 year, month, day, hour, minute, record_time)
			VALUES %s
			ON DUPLICATE KEY UPDATE
				rx_bytes = IF(
					TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR VALUES(rx_bytes) > pmacct_traffic_records.rx_bytes,
					VALUES(rx_bytes),
					pmacct_traffic_records.rx_bytes
				),
				tx_bytes = IF(
					TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR VALUES(tx_bytes) > pmacct_traffic_records.tx_bytes,
					VALUES(tx_bytes),
					pmacct_traffic_records.tx_bytes
				),
				total_bytes = IF(
					TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR VALUES(total_bytes) > pmacct_traffic_records.total_bytes,
					VALUES(total_bytes),
					pmacct_traffic_records.total_bytes
				),
				record_time = IF(
					TIMESTAMPDIFF(MINUTE, pmacct_traffic_records.timestamp, NOW()) <= 5 OR VALUES(total_bytes) > pmacct_traffic_records.total_bytes,
					VALUES(record_time),
					pmacct_traffic_records.record_time
				)
		`, strings.Join(values, ","))
	}

	return tx.Exec(insertSQL, args...).Error
}
