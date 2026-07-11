package traffic

import (
	"fmt"
	"sort"
	"time"

	"oneclickvirt/global"
)

// GetUserTrafficHistory 获取用户的流量历史（按天聚合）
// 实时从 pmacct_traffic_records 聚合所有实例的流量
func (s *QueryService) GetUserTrafficHistory(userID uint, days int) ([]*HistoryPoint, error) {
	if days <= 0 {
		days = 30
	}
	startDate := trafficDayStart(time.Now().AddDate(0, 0, -days))

	// 查询用户所有实例的配置（用于计算实际用量）（包含软删除的实例）
	var instanceConfigs []struct {
		InstanceID           uint
		EnableTrafficControl bool
		TrafficCountMode     string
		TrafficMultiplier    float64
	}
	if err := global.APP_DB.Unscoped().Table("instances i").
		Joins("INNER JOIN providers p ON i.provider_id = p.id").
		Select("i.id as instance_id, p.enable_traffic_control as enable_traffic_control, COALESCE(p.traffic_count_mode, 'both') as traffic_count_mode, COALESCE(p.traffic_multiplier, 1.0) as traffic_multiplier").
		Where("i.user_id = ?", userID).
		Find(&instanceConfigs).Error; err != nil {
		return nil, fmt.Errorf("查询用户实例配置失败: %w", err)
	}

	// 构建实例ID->配置的映射
	configMap := make(map[uint]struct {
		Enabled    bool
		CountMode  string
		Multiplier float64
	})
	for _, cfg := range instanceConfigs {
		configMap[cfg.InstanceID] = struct {
			Enabled    bool
			CountMode  string
			Multiplier float64
		}{
			Enabled:    cfg.EnableTrafficControl,
			CountMode:  cfg.TrafficCountMode,
			Multiplier: cfg.TrafficMultiplier,
		}
	}

	records, err := loadTrafficSeriesRecords(trafficSeriesScope{userID: userID}, startDate, time.Now())
	if err != nil {
		return nil, fmt.Errorf("查询用户流量历史失败: %w", err)
	}

	// 按天汇总所有实例
	dayMap := make(map[time.Time]*HistoryPoint)
	for _, delta := range computeTrafficDeltas(records, startDate) {
		day := trafficDayStart(delta.Timestamp)
		point := dayMap[day]
		if point == nil {
			point = &HistoryPoint{
				Date:  day,
				Year:  day.Year(),
				Month: int(day.Month()),
				Day:   day.Day(),
			}
			dayMap[day] = point
		}

		// 累加原始字节
		point.RxBytes += delta.RxDelta
		point.TxBytes += delta.TxDelta
		point.TotalBytes += delta.RxDelta + delta.TxDelta

		// 根据实例配置计算实际用量
		if config, ok := configMap[delta.InstanceID]; ok && config.Enabled {
			point.ActualUsageMB += s.calculateActualUsage(delta.RxDelta, delta.TxDelta, config.CountMode, config.Multiplier)
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
