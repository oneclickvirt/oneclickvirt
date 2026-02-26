package traffic

import (
	"time"

	monitoringModel "oneclickvirt/model/monitoring"
)

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
