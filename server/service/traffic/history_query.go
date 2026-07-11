package traffic

import (
	"sort"
	"time"

	monitoringModel "oneclickvirt/model/monitoring"
)

// GetInstanceTrafficHistory returns recent instance traffic deltas for chart display.
func (h *HistoryService) GetInstanceTrafficHistory(instanceID uint, period string, interval int, includeArchived bool) ([]monitoringModel.InstanceTrafficHistory, error) {
	now := time.Now()
	startTime, interval := resolveRecentTrafficWindow(period, interval, now)

	records, err := loadTrafficSeriesRecords(trafficSeriesScope{instanceID: instanceID}, startTime, now)
	if err != nil {
		return nil, err
	}

	deltas := computeTrafficDeltas(records, startTime)
	histories := make([]monitoringModel.InstanceTrafficHistory, 0, len(deltas))
	for _, delta := range deltas {
		if !shouldKeepTrafficInterval(delta.Timestamp, interval) {
			continue
		}
		histories = append(histories, monitoringModel.InstanceTrafficHistory{
			InstanceID: delta.InstanceID,
			ProviderID: delta.ProviderID,
			UserID:     delta.UserID,
			TrafficIn:  trafficBytesToMB(delta.RxDelta),
			TrafficOut: trafficBytesToMB(delta.TxDelta),
			TotalUsed:  trafficBytesToMB(delta.RxDelta + delta.TxDelta),
			Year:       delta.Timestamp.Year(),
			Month:      int(delta.Timestamp.Month()),
			Day:        delta.Timestamp.Day(),
			Hour:       delta.Timestamp.Hour(),
			RecordTime: delta.Timestamp,
		})
	}

	sort.Slice(histories, func(i, j int) bool {
		return histories[i].RecordTime.Before(histories[j].RecordTime)
	})
	return fillMissingInstanceTimePoints(histories, startTime, now, interval, instanceID, 0, 0), nil
}

// GetProviderTrafficHistory returns recent provider traffic deltas for chart display.
func (h *HistoryService) GetProviderTrafficHistory(providerID uint, period string, interval int) ([]monitoringModel.ProviderTrafficHistory, error) {
	now := time.Now()
	startTime, interval := resolveRecentTrafficWindow(period, interval, now)

	records, err := loadTrafficSeriesRecords(trafficSeriesScope{providerID: providerID}, startTime, now)
	if err != nil {
		return nil, err
	}

	type providerPoint struct {
		history   monitoringModel.ProviderTrafficHistory
		instances map[uint]struct{}
	}
	points := make(map[time.Time]*providerPoint)
	for _, delta := range computeTrafficDeltas(records, startTime) {
		if !shouldKeepTrafficInterval(delta.Timestamp, interval) {
			continue
		}
		point := points[delta.Timestamp]
		if point == nil {
			point = &providerPoint{
				history: monitoringModel.ProviderTrafficHistory{
					ProviderID: providerID,
					Year:       delta.Timestamp.Year(),
					Month:      int(delta.Timestamp.Month()),
					Day:        delta.Timestamp.Day(),
					Hour:       delta.Timestamp.Hour(),
					RecordTime: delta.Timestamp,
				},
				instances: make(map[uint]struct{}),
			}
			points[delta.Timestamp] = point
		}
		point.history.TrafficIn += trafficBytesToMB(delta.RxDelta)
		point.history.TrafficOut += trafficBytesToMB(delta.TxDelta)
		point.history.TotalUsed += trafficBytesToMB(delta.RxDelta + delta.TxDelta)
		if delta.InstanceID > 0 {
			point.instances[delta.InstanceID] = struct{}{}
		}
	}

	histories := make([]monitoringModel.ProviderTrafficHistory, 0, len(points))
	for _, point := range points {
		point.history.InstanceCount = len(point.instances)
		histories = append(histories, point.history)
	}
	sort.Slice(histories, func(i, j int) bool {
		return histories[i].RecordTime.Before(histories[j].RecordTime)
	})

	return fillMissingProviderTimePoints(histories, startTime, now, interval, providerID), nil
}

// GetUserTrafficHistory returns recent user traffic deltas for chart display.
func (h *HistoryService) GetUserTrafficHistory(userID uint, period string, interval int) ([]monitoringModel.UserTrafficHistory, error) {
	now := time.Now()
	startTime, interval := resolveRecentTrafficWindow(period, interval, now)

	records, err := loadTrafficSeriesRecords(trafficSeriesScope{userID: userID}, startTime, now)
	if err != nil {
		return nil, err
	}

	type userPoint struct {
		history   monitoringModel.UserTrafficHistory
		instances map[uint]struct{}
	}
	points := make(map[time.Time]*userPoint)
	for _, delta := range computeTrafficDeltas(records, startTime) {
		if !shouldKeepTrafficInterval(delta.Timestamp, interval) {
			continue
		}
		point := points[delta.Timestamp]
		if point == nil {
			point = &userPoint{
				history: monitoringModel.UserTrafficHistory{
					UserID:     userID,
					Year:       delta.Timestamp.Year(),
					Month:      int(delta.Timestamp.Month()),
					Day:        delta.Timestamp.Day(),
					Hour:       delta.Timestamp.Hour(),
					RecordTime: delta.Timestamp,
				},
				instances: make(map[uint]struct{}),
			}
			points[delta.Timestamp] = point
		}
		point.history.TrafficIn += trafficBytesToMB(delta.RxDelta)
		point.history.TrafficOut += trafficBytesToMB(delta.TxDelta)
		point.history.TotalUsed += trafficBytesToMB(delta.RxDelta + delta.TxDelta)
		if delta.InstanceID > 0 {
			point.instances[delta.InstanceID] = struct{}{}
		}
	}

	histories := make([]monitoringModel.UserTrafficHistory, 0, len(points))
	for _, point := range points {
		point.history.InstanceCount = len(point.instances)
		histories = append(histories, point.history)
	}
	sort.Slice(histories, func(i, j int) bool {
		return histories[i].RecordTime.Before(histories[j].RecordTime)
	})

	return fillMissingUserTimePoints(histories, startTime, now, interval, userID), nil
}
