package traffic

import (
	"fmt"
	"sort"
	"time"

	"oneclickvirt/global"

	"gorm.io/gorm"
)

type trafficSeriesScope struct {
	instanceID uint
	providerID uint
	userID     uint
}

type trafficRawPoint struct {
	InstanceID uint
	ProviderID uint
	UserID     uint
	Timestamp  time.Time
	Year       int
	Month      int
	Day        int
	Hour       int
	Minute     int
	RxBytes    int64
	TxBytes    int64
}

type trafficDeltaPoint struct {
	trafficRawPoint
	RxDelta int64
	TxDelta int64
}

func (s trafficSeriesScope) apply(db *gorm.DB) *gorm.DB {
	if s.instanceID > 0 {
		db = db.Where("instance_id = ?", s.instanceID)
	}
	if s.providerID > 0 {
		db = db.Where("provider_id = ?", s.providerID)
	}
	if s.userID > 0 {
		db = db.Where("user_id = ?", s.userID)
	}
	return db
}

func loadTrafficSeriesRecords(scope trafficSeriesScope, start, end time.Time) ([]trafficRawPoint, error) {
	if end.Before(start) {
		end = start
	}

	var records []trafficRawPoint
	query := global.APP_DB.Table("pmacct_traffic_records").
		Select("instance_id, provider_id, user_id, timestamp, year, month, day, hour, minute, rx_bytes, tx_bytes").
		Where("timestamp >= ? AND timestamp <= ? AND deleted_at IS NULL", start, end)
	if err := scope.apply(query).
		Order("instance_id ASC, timestamp ASC").
		Find(&records).Error; err != nil {
		return nil, fmt.Errorf("加载流量原始记录失败: %w", err)
	}
	if len(records) == 0 {
		return records, nil
	}

	baselines, err := loadTrafficSeriesBaselines(uniqueTrafficInstanceIDs(records), start)
	if err != nil {
		return nil, err
	}
	records = append(baselines, records...)
	sortTrafficRawPoints(records)
	return records, nil
}

func loadTrafficSeriesBaselines(instanceIDs []uint, start time.Time) ([]trafficRawPoint, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}

	subQuery := global.APP_DB.Table("pmacct_traffic_records").
		Select("instance_id, MAX(timestamp) AS max_timestamp").
		Where("instance_id IN ? AND timestamp < ? AND deleted_at IS NULL", instanceIDs, start).
		Group("instance_id")

	var baselines []trafficRawPoint
	if err := global.APP_DB.Table("pmacct_traffic_records r").
		Select("r.instance_id, r.provider_id, r.user_id, r.timestamp, r.year, r.month, r.day, r.hour, r.minute, r.rx_bytes, r.tx_bytes").
		Joins("INNER JOIN (?) latest ON latest.instance_id = r.instance_id AND latest.max_timestamp = r.timestamp", subQuery).
		Where("r.deleted_at IS NULL").
		Find(&baselines).Error; err != nil {
		return nil, fmt.Errorf("加载流量窗口基线失败: %w", err)
	}
	return baselines, nil
}

func uniqueTrafficInstanceIDs(records []trafficRawPoint) []uint {
	seen := make(map[uint]struct{}, len(records))
	ids := make([]uint, 0, len(records))
	for _, record := range records {
		if record.InstanceID == 0 {
			continue
		}
		if _, ok := seen[record.InstanceID]; ok {
			continue
		}
		seen[record.InstanceID] = struct{}{}
		ids = append(ids, record.InstanceID)
	}
	return ids
}

func sortTrafficRawPoints(records []trafficRawPoint) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].InstanceID != records[j].InstanceID {
			return records[i].InstanceID < records[j].InstanceID
		}
		return records[i].Timestamp.Before(records[j].Timestamp)
	})
}

func computeTrafficDeltas(records []trafficRawPoint, start time.Time) []trafficDeltaPoint {
	sortTrafficRawPoints(records)
	prevByInstance := make(map[uint]trafficRawPoint)
	deltas := make([]trafficDeltaPoint, 0, len(records))

	for _, record := range records {
		prev, hasPrev := prevByInstance[record.InstanceID]
		rxDelta, txDelta := record.RxBytes, record.TxBytes
		if hasPrev {
			rxDelta = trafficCounterDelta(prev.RxBytes, record.RxBytes)
			txDelta = trafficCounterDelta(prev.TxBytes, record.TxBytes)
		}
		prevByInstance[record.InstanceID] = record

		if record.Timestamp.Before(start) {
			continue
		}
		deltas = append(deltas, trafficDeltaPoint{
			trafficRawPoint: record,
			RxDelta:         rxDelta,
			TxDelta:         txDelta,
		})
	}

	return deltas
}

func trafficCounterDelta(prev, current int64) int64 {
	if current >= prev {
		return current - prev
	}
	return current
}

func shouldKeepTrafficInterval(ts time.Time, intervalMinutes int) bool {
	if intervalMinutes <= 5 {
		return true
	}
	return ts.Minute()%intervalMinutes == 0
}

func trafficBytesToMB(bytes int64) float64 {
	return float64(bytes) / 1048576.0
}

func trafficDayStart(ts time.Time) time.Time {
	return time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, ts.Location())
}

func resolveRecentTrafficWindow(period string, interval int, now time.Time) (time.Time, int) {
	var duration time.Duration
	autoInterval := 60
	switch period {
	case "5m":
		duration = 5 * time.Minute
		autoInterval = 5
	case "10m":
		duration = 10 * time.Minute
		autoInterval = 5
	case "15m":
		duration = 15 * time.Minute
		autoInterval = 5
	case "30m":
		duration = 30 * time.Minute
		autoInterval = 5
	case "45m":
		duration = 45 * time.Minute
		autoInterval = 5
	case "1h":
		duration = time.Hour
		autoInterval = 5
	case "6h":
		duration = 6 * time.Hour
		autoInterval = 15
	case "12h":
		duration = 12 * time.Hour
		autoInterval = 30
	case "24h":
		duration = 24 * time.Hour
		autoInterval = 60
	default:
		duration = 24 * time.Hour
		autoInterval = 60
	}
	if interval <= 0 {
		interval = autoInterval
	}
	return now.Add(-duration), interval
}
