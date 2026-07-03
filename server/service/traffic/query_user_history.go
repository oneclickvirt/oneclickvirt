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
	startDate := time.Now().AddDate(0, 0, -days).Truncate(24 * time.Hour)

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

	// 从 pmacct_traffic_records 按天聚合查询（包含 instance_id 用于计算实际用量）
	// 处理pmacct重启导致的累积值重置问题
	var rawResults []struct {
		Date       time.Time
		InstanceID uint
		RxBytes    int64
		TxBytes    int64
	}

	query := `
		SELECT 
			DATE(t1.timestamp) as date,
			instance_id,
			SUM(max_rx) as rx_bytes,
			SUM(max_tx) as tx_bytes
		FROM (
			-- 检测重启并分段，每段取MAX
			SELECT 
				instance_id,
				timestamp,
				segment_id,
				MAX(rx_bytes) as max_rx,
				MAX(tx_bytes) as max_tx
			FROM (
				-- 计算每条记录的segment_id（累积重启次数）
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
									AND DATE(timestamp) = DATE(t2.timestamp)
							)
						WHERE t2.instance_id = t1.instance_id
							AND t2.user_id = ?
							AND t2.timestamp >= ?
							AND t2.timestamp <= t1.timestamp
							AND DATE(t2.timestamp) = DATE(t1.timestamp)
							AND (
								(t3.rx_bytes IS NOT NULL AND t2.rx_bytes < t3.rx_bytes)
								OR
								(t3.tx_bytes IS NOT NULL AND t2.tx_bytes < t3.tx_bytes)
							)
					) as segment_id
				FROM pmacct_traffic_records t1
				WHERE t1.user_id = ? AND t1.timestamp >= ?
			) AS segments
			GROUP BY instance_id, DATE(timestamp), segment_id, timestamp
		) AS daily_segments
		GROUP BY DATE(timestamp), instance_id
		ORDER BY date ASC, instance_id
	`

	if err := global.APP_DB.Raw(query, userID, startDate, userID, startDate).Scan(&rawResults).Error; err != nil {
		return nil, fmt.Errorf("查询用户流量历史失败: %w", err)
	}

	// 按天汇总所有实例
	dayMap := make(map[string]*HistoryPoint)
	for _, r := range rawResults {
		dateKey := r.Date.Format("2006-01-02")

		if _, exists := dayMap[dateKey]; !exists {
			dayMap[dateKey] = &HistoryPoint{
				Date:          r.Date,
				Year:          r.Date.Year(),
				Month:         int(r.Date.Month()),
				Day:           r.Date.Day(),
				RxBytes:       0,
				TxBytes:       0,
				TotalBytes:    0,
				ActualUsageMB: 0,
			}
		}

		// 累加原始字节
		dayMap[dateKey].RxBytes += r.RxBytes
		dayMap[dateKey].TxBytes += r.TxBytes
		dayMap[dateKey].TotalBytes += r.RxBytes + r.TxBytes

		// 根据实例配置计算实际用量
		if config, ok := configMap[r.InstanceID]; ok && config.Enabled {
			actualMB := s.calculateActualUsage(r.RxBytes, r.TxBytes, config.CountMode, config.Multiplier)
			dayMap[dateKey].ActualUsageMB += actualMB
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
