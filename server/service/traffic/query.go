package traffic

import (
	"fmt"
	"sort"
	"time"

	"oneclickvirt/global"
)

// QueryService is the unified traffic query service backed by pmacct raw records.
type QueryService struct{}

func NewQueryService() *QueryService {
	return &QueryService{}
}

// TrafficStats is the unified traffic statistics result.
type TrafficStats struct {
	RxBytes       int64   `json:"rx_bytes"`
	TxBytes       int64   `json:"tx_bytes"`
	TotalBytes    int64   `json:"total_bytes"`
	ActualUsageMB float64 `json:"actual_usage_mb"`
}

type rawTrafficRecord struct {
	RxBytes int64
	TxBytes int64
}
type instanceTrafficConfig struct {
	InstanceID           uint
	EnableTrafficControl bool
	TrafficCountMode     string
	TrafficMultiplier    float64
	TrafficResetDay      *int
}

func (s *QueryService) getInstanceTrafficConfigs(instanceIDs []uint) (map[uint]instanceTrafficConfig, error) {
	if len(instanceIDs) == 0 {
		return map[uint]instanceTrafficConfig{}, nil
	}

	var rows []instanceTrafficConfig
	err := global.APP_DB.Unscoped().Table("instances i").
		Joins("INNER JOIN providers p ON i.provider_id = p.id").
		Select("i.id as instance_id, p.enable_traffic_control as enable_traffic_control, COALESCE(p.traffic_count_mode, 'both') as traffic_count_mode, COALESCE(p.traffic_multiplier, 1.0) as traffic_multiplier, p.traffic_reset_day as traffic_reset_day").
		Where("i.id IN ?", instanceIDs).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("查询实例流量配置失败: %w", err)
	}

	configs := make(map[uint]instanceTrafficConfig, len(rows))
	for _, row := range rows {
		configs[row.InstanceID] = row
	}
	return configs, nil
}

func trafficWindowKey(start, end time.Time) string {
	return fmt.Sprintf("%d:%d", start.UnixNano(), end.UnixNano())
}

func trafficMonthWindow(year, month int) (time.Time, time.Time) {
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local)
	return start, start.AddDate(0, 1, 0)
}

func computeWindowTraffic(records []rawTrafficRecord, baseline *rawTrafficRecord) (totalRx, totalTx int64) {
	// pmacct exposes cumulative counters. A counter decrease starts a new
	// segment, so every segment contributes its delta instead of losing or
	// double-counting traffic across pmacct restarts.
	if len(records) == 0 {
		return 0, 0
	}

	prevRx, prevTx := int64(0), int64(0)
	hasPrev := false
	if baseline != nil {
		prevRx = baseline.RxBytes
		prevTx = baseline.TxBytes
		hasPrev = true
	}

	for _, r := range records {
		if hasPrev {
			if r.RxBytes >= prevRx {
				totalRx += r.RxBytes - prevRx
			} else {
				totalRx += r.RxBytes
			}
			if r.TxBytes >= prevTx {
				totalTx += r.TxBytes - prevTx
			} else {
				totalTx += r.TxBytes
			}
		} else {
			totalRx += r.RxBytes
			totalTx += r.TxBytes
			hasPrev = true
		}
		prevRx = r.RxBytes
		prevTx = r.TxBytes
	}

	return totalRx, totalTx
}

func (s *QueryService) batchGetInstancesTrafficInWindow(instanceIDs []uint, start, end time.Time, configs map[uint]instanceTrafficConfig) (map[uint]*TrafficStats, error) {
	statsMap := make(map[uint]*TrafficStats, len(instanceIDs))
	if len(instanceIDs) == 0 {
		return statsMap, nil
	}

	type baselineRow struct {
		InstanceID uint
		RxBytes    int64
		TxBytes    int64
	}
	subQuery := global.APP_DB.Table("pmacct_traffic_records").
		Select("instance_id, MAX(timestamp) AS max_timestamp").
		Where("instance_id IN ? AND timestamp < ? AND deleted_at IS NULL", instanceIDs, start).
		Group("instance_id")

	var baselines []baselineRow
	if err := global.APP_DB.Table("pmacct_traffic_records r").
		Select("r.instance_id, r.rx_bytes, r.tx_bytes").
		Joins("INNER JOIN (?) latest ON latest.instance_id = r.instance_id AND latest.max_timestamp = r.timestamp", subQuery).
		Where("r.deleted_at IS NULL").
		Find(&baselines).Error; err != nil {
		return nil, fmt.Errorf("查询流量周期基线失败: %w", err)
	}
	baselineMap := make(map[uint]rawTrafficRecord, len(baselines))
	for _, row := range baselines {
		baselineMap[row.InstanceID] = rawTrafficRecord{RxBytes: row.RxBytes, TxBytes: row.TxBytes}
	}

	type batchRawRecord struct {
		InstanceID uint
		RxBytes    int64
		TxBytes    int64
	}
	var allRecords []batchRawRecord
	if err := global.APP_DB.Table("pmacct_traffic_records").
		Select("instance_id, rx_bytes, tx_bytes").
		Where("instance_id IN ? AND timestamp >= ? AND timestamp < ? AND deleted_at IS NULL", instanceIDs, start, end).
		Order("instance_id ASC, timestamp ASC").
		Find(&allRecords).Error; err != nil {
		return nil, fmt.Errorf("查询流量周期记录失败: %w", err)
	}

	groups := make(map[uint][]rawTrafficRecord, len(instanceIDs))
	for _, rec := range allRecords {
		groups[rec.InstanceID] = append(groups[rec.InstanceID], rawTrafficRecord{RxBytes: rec.RxBytes, TxBytes: rec.TxBytes})
	}

	for _, id := range instanceIDs {
		var baseline *rawTrafficRecord
		if b, ok := baselineMap[id]; ok {
			baseline = &b
		}
		rxBytes, txBytes := computeWindowTraffic(groups[id], baseline)
		stats := &TrafficStats{
			RxBytes:    rxBytes,
			TxBytes:    txBytes,
			TotalBytes: rxBytes + txBytes,
		}
		if cfg, ok := configs[id]; ok && cfg.EnableTrafficControl {
			stats.ActualUsageMB = s.calculateActualUsage(rxBytes, txBytes, cfg.TrafficCountMode, cfg.TrafficMultiplier)
		}
		statsMap[id] = stats
	}

	return statsMap, nil
}

func (s *QueryService) computeBatchTrafficInWindow(instanceIDs []uint, start, end time.Time) (map[uint]*TrafficStats, error) {
	configs, err := s.getInstanceTrafficConfigs(instanceIDs)
	if err != nil {
		return nil, err
	}
	return s.batchGetInstancesTrafficInWindow(instanceIDs, start, end, configs)
}

func (s *QueryService) BatchGetInstancesCurrentCycleTraffic(instanceIDs []uint) (map[uint]*TrafficStats, error) {
	statsMap := make(map[uint]*TrafficStats, len(instanceIDs))
	if len(instanceIDs) == 0 {
		return statsMap, nil
	}

	configs, err := s.getInstanceTrafficConfigs(instanceIDs)
	if err != nil {
		return nil, err
	}

	type windowGroup struct {
		start time.Time
		end   time.Time
		ids   []uint
	}
	now := time.Now()
	groups := make(map[string]*windowGroup)
	for _, id := range instanceIDs {
		cfg, ok := configs[id]
		if !ok {
			statsMap[id] = &TrafficStats{}
			continue
		}
		start, end := CurrentTrafficWindow(cfg.TrafficResetDay, now)
		key := trafficWindowKey(start, end)
		group := groups[key]
		if group == nil {
			group = &windowGroup{start: start, end: end}
			groups[key] = group
		}
		group.ids = append(group.ids, id)
	}

	for _, group := range groups {
		groupStats, err := s.batchGetInstancesTrafficInWindow(group.ids, group.start, group.end, configs)
		if err != nil {
			return nil, err
		}
		for id, stats := range groupStats {
			statsMap[id] = stats
		}
	}

	for _, id := range instanceIDs {
		if _, ok := statsMap[id]; !ok {
			statsMap[id] = &TrafficStats{}
		}
	}
	return statsMap, nil
}

func (s *QueryService) GetInstanceCurrentCycleTraffic(instanceID uint) (*TrafficStats, error) {
	statsMap, err := s.BatchGetInstancesCurrentCycleTraffic([]uint{instanceID})
	if err != nil {
		return nil, err
	}
	if stats, ok := statsMap[instanceID]; ok {
		return stats, nil
	}
	return &TrafficStats{}, nil
}

func (s *QueryService) GetProviderCurrentCycleTraffic(providerID uint) (*TrafficStats, error) {
	var p struct {
		EnableTrafficControl bool
		TrafficResetDay      *int
	}
	if err := global.APP_DB.Table("providers").
		Select("enable_traffic_control, traffic_reset_day").
		Where("id = ?", providerID).
		Scan(&p).Error; err != nil {
		return nil, fmt.Errorf("查询Provider流量配置失败: %w", err)
	}
	if !p.EnableTrafficControl {
		return &TrafficStats{}, nil
	}

	start, end := CurrentTrafficWindow(p.TrafficResetDay, time.Now())
	var instanceIDs []uint
	if err := global.APP_DB.Table("pmacct_traffic_records").
		Where("provider_id = ? AND timestamp < ? AND deleted_at IS NULL", providerID, end).
		Distinct("instance_id").
		Pluck("instance_id", &instanceIDs).Error; err != nil {
		return nil, fmt.Errorf("查询Provider实例流量记录失败: %w", err)
	}
	if len(instanceIDs) == 0 {
		return &TrafficStats{}, nil
	}

	configs, err := s.getInstanceTrafficConfigs(instanceIDs)
	if err != nil {
		return nil, err
	}
	instanceStats, err := s.batchGetInstancesTrafficInWindow(instanceIDs, start, end, configs)
	if err != nil {
		return nil, err
	}

	total := &TrafficStats{}
	for _, stats := range instanceStats {
		total.RxBytes += stats.RxBytes
		total.TxBytes += stats.TxBytes
		total.TotalBytes += stats.TotalBytes
		total.ActualUsageMB += stats.ActualUsageMB
	}
	return total, nil
}

func (s *QueryService) BatchGetProvidersCurrentCycleTraffic(providerIDs []uint) (map[uint]*TrafficStats, error) {
	results := make(map[uint]*TrafficStats, len(providerIDs))
	if len(providerIDs) == 0 {
		return results, nil
	}

	type providerTrafficConfig struct {
		ProviderID           uint
		EnableTrafficControl bool
		TrafficResetDay      *int
	}
	var providerConfigs []providerTrafficConfig
	if err := global.APP_DB.Table("providers").
		Select("id as provider_id, enable_traffic_control, traffic_reset_day").
		Where("id IN ?", providerIDs).
		Find(&providerConfigs).Error; err != nil {
		return nil, fmt.Errorf("批量查询Provider流量配置失败: %w", err)
	}

	type windowGroup struct {
		start       time.Time
		end         time.Time
		providerIDs []uint
	}
	now := time.Now()
	groups := make(map[string]*windowGroup)
	for _, cfg := range providerConfigs {
		results[cfg.ProviderID] = &TrafficStats{}
		if !cfg.EnableTrafficControl {
			continue
		}
		start, end := CurrentTrafficWindow(cfg.TrafficResetDay, now)
		key := trafficWindowKey(start, end)
		group := groups[key]
		if group == nil {
			group = &windowGroup{start: start, end: end}
			groups[key] = group
		}
		group.providerIDs = append(group.providerIDs, cfg.ProviderID)
	}

	for _, providerID := range providerIDs {
		if _, ok := results[providerID]; !ok {
			results[providerID] = &TrafficStats{}
		}
	}

	for _, group := range groups {
		type instanceProviderRow struct {
			InstanceID uint
			ProviderID uint
		}
		var rows []instanceProviderRow
		if err := global.APP_DB.Table("pmacct_traffic_records").
			Select("DISTINCT instance_id, provider_id").
			Where("provider_id IN ? AND timestamp < ? AND deleted_at IS NULL", group.providerIDs, group.end).
			Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("批量查询Provider实例流量记录失败: %w", err)
		}
		if len(rows) == 0 {
			continue
		}

		instanceIDs := make([]uint, 0, len(rows))
		instanceProvider := make(map[uint]uint, len(rows))
		for _, row := range rows {
			if row.InstanceID == 0 || row.ProviderID == 0 {
				continue
			}
			instanceIDs = append(instanceIDs, row.InstanceID)
			instanceProvider[row.InstanceID] = row.ProviderID
		}
		if len(instanceIDs) == 0 {
			continue
		}

		configs, err := s.getInstanceTrafficConfigs(instanceIDs)
		if err != nil {
			return nil, err
		}
		instanceStats, err := s.batchGetInstancesTrafficInWindow(instanceIDs, group.start, group.end, configs)
		if err != nil {
			return nil, err
		}
		for instanceID, stats := range instanceStats {
			providerID := instanceProvider[instanceID]
			total := results[providerID]
			if total == nil {
				total = &TrafficStats{}
				results[providerID] = total
			}
			total.RxBytes += stats.RxBytes
			total.TxBytes += stats.TxBytes
			total.TotalBytes += stats.TotalBytes
			total.ActualUsageMB += stats.ActualUsageMB
		}
	}

	return results, nil
}

func (s *QueryService) GetUserCurrentCycleTraffic(userID uint) (*TrafficStats, error) {
	var instanceIDs []uint
	if err := global.APP_DB.Unscoped().Table("instances").
		Where("user_id = ?", userID).
		Pluck("id", &instanceIDs).Error; err != nil {
		return nil, fmt.Errorf("获取用户实例列表失败: %w", err)
	}
	if len(instanceIDs) == 0 {
		return &TrafficStats{}, nil
	}

	instanceStats, err := s.BatchGetInstancesCurrentCycleTraffic(instanceIDs)
	if err != nil {
		return nil, err
	}

	total := &TrafficStats{}
	for _, stats := range instanceStats {
		total.RxBytes += stats.RxBytes
		total.TxBytes += stats.TxBytes
		total.TotalBytes += stats.TotalBytes
		total.ActualUsageMB += stats.ActualUsageMB
	}
	return total, nil
}

func (s *QueryService) BatchGetUsersCurrentCycleTraffic(userIDs []uint) (map[uint]*TrafficStats, error) {
	return s.batchGetUsersCurrentCycleTraffic(userIDs, 0)
}

func (s *QueryService) BatchGetUsersCurrentCycleTrafficForOwner(userIDs []uint, ownerAdminID uint) (map[uint]*TrafficStats, error) {
	return s.batchGetUsersCurrentCycleTraffic(userIDs, ownerAdminID)
}

func (s *QueryService) batchGetUsersCurrentCycleTraffic(userIDs []uint, ownerAdminID uint) (map[uint]*TrafficStats, error) {
	results := make(map[uint]*TrafficStats, len(userIDs))
	if len(userIDs) == 0 {
		return results, nil
	}

	for _, userID := range userIDs {
		results[userID] = &TrafficStats{}
	}

	type instanceUserRow struct {
		InstanceID uint
		UserID     uint
	}
	var rows []instanceUserRow
	query := global.APP_DB.Unscoped().Table("instances AS instances").
		Select("instances.id as instance_id, instances.user_id").
		Where("instances.user_id IN ?", userIDs)
	if ownerAdminID > 0 {
		query = query.Joins("INNER JOIN providers ON providers.id = instances.provider_id").
			Where("providers.owner_admin_id = ?", ownerAdminID)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("批量获取用户实例列表失败: %w", err)
	}
	if len(rows) == 0 {
		return results, nil
	}

	instanceIDs := make([]uint, 0, len(rows))
	instanceUserMap := make(map[uint]uint, len(rows))
	for _, row := range rows {
		if row.InstanceID == 0 || row.UserID == 0 {
			continue
		}
		instanceIDs = append(instanceIDs, row.InstanceID)
		instanceUserMap[row.InstanceID] = row.UserID
	}
	if len(instanceIDs) == 0 {
		return results, nil
	}

	instanceStats, err := s.BatchGetInstancesCurrentCycleTraffic(instanceIDs)
	if err != nil {
		return nil, err
	}
	for instanceID, stats := range instanceStats {
		userID := instanceUserMap[instanceID]
		if userID == 0 {
			continue
		}
		total := results[userID]
		if total == nil {
			total = &TrafficStats{}
			results[userID] = total
		}
		total.RxBytes += stats.RxBytes
		total.TxBytes += stats.TxBytes
		total.TotalBytes += stats.TotalBytes
		total.ActualUsageMB += stats.ActualUsageMB
	}

	return results, nil
}

func (s *QueryService) GetUserNextTrafficResetTime(userID uint) (*time.Time, error) {
	resetTimes, err := s.BatchGetUsersNextTrafficResetTime([]uint{userID}, 0)
	if err != nil {
		return nil, err
	}
	return resetTimes[userID], nil
}

func (s *QueryService) BatchGetUsersNextTrafficResetTime(userIDs []uint, ownerAdminID uint) (map[uint]*time.Time, error) {
	results := make(map[uint]*time.Time, len(userIDs))
	if len(userIDs) == 0 {
		return results, nil
	}
	type providerReset struct {
		UserID          uint
		TrafficResetDay *int
	}
	var rows []providerReset
	query := global.APP_DB.Unscoped().Table("instances i").
		Joins("INNER JOIN providers p ON i.provider_id = p.id").
		Select("i.user_id, p.traffic_reset_day as traffic_reset_day").
		Where("i.user_id IN ? AND p.enable_traffic_control = ?", userIDs, true)
	if ownerAdminID > 0 {
		query = query.Where("p.owner_admin_id = ?", ownerAdminID)
	}
	if err := query.Group("i.user_id, p.id, p.traffic_reset_day").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("获取用户流量重置时间失败: %w", err)
	}

	now := time.Now()
	for _, row := range rows {
		resetAt := NextTrafficResetTime(row.TrafficResetDay, now)
		next := results[row.UserID]
		if next == nil || resetAt.Before(*next) {
			value := resetAt
			results[row.UserID] = &value
		}
	}
	return results, nil
}

// GetInstanceMonthlyTraffic returns monthly traffic for one instance from pmacct raw records.
func (s *QueryService) GetInstanceMonthlyTraffic(instanceID uint, year, month int) (*TrafficStats, error) {
	start, end := trafficMonthWindow(year, month)
	statsMap, err := s.computeBatchTrafficInWindow([]uint{instanceID}, start, end)
	if err != nil {
		return nil, fmt.Errorf("query instance monthly traffic failed: %w", err)
	}
	if stats, ok := statsMap[instanceID]; ok {
		return stats, nil
	}
	return &TrafficStats{}, nil
}

// computeSegmentTraffic sums monotonic counter segments and treats counter drops as restarts.
func computeSegmentTraffic(records []rawTrafficRecord) (totalRx, totalTx int64) {
	if len(records) == 0 {
		return 0, 0
	}

	var segMaxRx, segMaxTx int64
	var prevRx, prevTx int64
	for i, r := range records {
		if i > 0 {
			if r.RxBytes < prevRx {
				totalRx += segMaxRx
				segMaxRx = 0
			}
			if r.TxBytes < prevTx {
				totalTx += segMaxTx
				segMaxTx = 0
			}
		}
		if r.RxBytes > segMaxRx {
			segMaxRx = r.RxBytes
		}
		if r.TxBytes > segMaxTx {
			segMaxTx = r.TxBytes
		}
		prevRx, prevTx = r.RxBytes, r.TxBytes
	}
	totalRx += segMaxRx
	totalTx += segMaxTx
	return totalRx, totalTx
}

// GetUserMonthlyTraffic returns monthly traffic for all user instances from pmacct raw records.
func (s *QueryService) GetUserMonthlyTraffic(userID uint, year, month int) (*TrafficStats, error) {
	var instanceIDs []uint
	err := global.APP_DB.Unscoped().Table("instances").
		Where("user_id = ?", userID).
		Pluck("id", &instanceIDs).Error
	if err != nil {
		return nil, fmt.Errorf("query user instances failed: %w", err)
	}
	if len(instanceIDs) == 0 {
		return &TrafficStats{}, nil
	}

	instanceStats, err := s.BatchGetInstancesMonthlyTraffic(instanceIDs, year, month)
	if err != nil {
		return nil, err
	}

	total := &TrafficStats{}
	for _, stats := range instanceStats {
		total.RxBytes += stats.RxBytes
		total.TxBytes += stats.TxBytes
		total.TotalBytes += stats.TotalBytes
		total.ActualUsageMB += stats.ActualUsageMB
	}
	return total, nil
}

// GetProviderMonthlyTraffic returns provider monthly traffic from pmacct raw records.
func (s *QueryService) GetProviderMonthlyTraffic(providerID uint, year, month int) (*TrafficStats, error) {
	var p struct {
		EnableTrafficControl bool
	}
	err := global.APP_DB.Table("providers").
		Select("enable_traffic_control").
		Where("id = ?", providerID).
		Scan(&p).Error
	if err != nil {
		return nil, fmt.Errorf("query provider config failed: %w", err)
	}
	if !p.EnableTrafficControl {
		return &TrafficStats{}, nil
	}

	start, end := trafficMonthWindow(year, month)
	var instanceIDs []uint
	if err := global.APP_DB.Table("pmacct_traffic_records").
		Where("provider_id = ? AND timestamp >= ? AND timestamp < ? AND deleted_at IS NULL", providerID, start, end).
		Distinct("instance_id").
		Pluck("instance_id", &instanceIDs).Error; err != nil {
		return nil, fmt.Errorf("query provider traffic instances failed: %w", err)
	}
	if len(instanceIDs) == 0 {
		return &TrafficStats{}, nil
	}

	instanceStats, err := s.computeBatchTrafficInWindow(instanceIDs, start, end)
	if err != nil {
		return nil, err
	}
	total := &TrafficStats{}
	for _, stats := range instanceStats {
		total.RxBytes += stats.RxBytes
		total.TxBytes += stats.TxBytes
		total.TotalBytes += stats.TotalBytes
		total.ActualUsageMB += stats.ActualUsageMB
	}
	return total, nil
}

// BatchGetInstancesMonthlyTraffic batches instance monthly traffic from pmacct raw records.
func (s *QueryService) BatchGetInstancesMonthlyTraffic(instanceIDs []uint, year, month int) (map[uint]*TrafficStats, error) {
	if len(instanceIDs) == 0 {
		return make(map[uint]*TrafficStats), nil
	}
	return s.computeBatchMonthlyTraffic(instanceIDs, year, month)
}

func (s *QueryService) computeBatchMonthlyTraffic(instanceIDs []uint, year, month int) (map[uint]*TrafficStats, error) {
	if len(instanceIDs) == 0 {
		return make(map[uint]*TrafficStats), nil
	}

	start, end := trafficMonthWindow(year, month)
	statsMap, err := s.computeBatchTrafficInWindow(instanceIDs, start, end)
	if err != nil {
		return nil, fmt.Errorf("batch query monthly traffic failed: %w", err)
	}
	return statsMap, nil
}

// GetInstanceTrafficHistory aggregates daily traffic deltas from pmacct raw records.
func (s *QueryService) GetInstanceTrafficHistory(instanceID uint, days int) ([]*HistoryPoint, error) {
	if days <= 0 {
		days = 30
	}

	var config struct {
		TrafficCountMode  string
		TrafficMultiplier float64
	}
	if err := global.APP_DB.Table("instances i").
		Joins("INNER JOIN providers p ON i.provider_id = p.id").
		Select("p.traffic_count_mode, p.traffic_multiplier").
		Where("i.id = ?", instanceID).
		Scan(&config).Error; err != nil {
		return nil, fmt.Errorf("查询实例流量配置失败: %w", err)
	}

	startDate := trafficDayStart(time.Now().AddDate(0, 0, -days))
	records, err := loadTrafficSeriesRecords(trafficSeriesScope{instanceID: instanceID}, startDate, time.Now())
	if err != nil {
		return nil, fmt.Errorf("查询实例流量历史失败: %w", err)
	}

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
		point.RxBytes += delta.RxDelta
		point.TxBytes += delta.TxDelta
		point.TotalBytes += delta.RxDelta + delta.TxDelta
		point.ActualUsageMB += s.calculateActualUsage(delta.RxDelta, delta.TxDelta, config.TrafficCountMode, config.TrafficMultiplier)
	}

	history := make([]*HistoryPoint, 0, len(dayMap))
	for _, point := range dayMap {
		history = append(history, point)
	}
	sort.Slice(history, func(i, j int) bool {
		return history[i].Date.Before(history[j].Date)
	})

	return history, nil
}

// calculateActualUsage applies the provider count mode and multiplier, returning MB.
func (s *QueryService) calculateActualUsage(rxBytes, txBytes int64, countMode string, multiplier float64) float64 {
	var bytes float64
	countMode, multiplier = normalizeTrafficConfig(countMode, multiplier)
	switch countMode {
	case "out":
		bytes = float64(txBytes)
	case "in":
		bytes = float64(rxBytes)
	default: // "both"
		bytes = float64(rxBytes + txBytes)
	}
	return (bytes * multiplier) / 1048576.0
}

func (s *QueryService) calculateActualUsageMB(trafficInMB, trafficOutMB float64, countMode string, multiplier float64) float64 {
	countMode, multiplier = normalizeTrafficConfig(countMode, multiplier)
	switch countMode {
	case "out":
		return trafficOutMB * multiplier
	case "in":
		return trafficInMB * multiplier
	default:
		return (trafficInMB + trafficOutMB) * multiplier
	}
}

func normalizeTrafficConfig(countMode string, multiplier float64) (string, float64) {
	switch countMode {
	case "in", "out", "both":
	default:
		countMode = "both"
	}
	if multiplier <= 0 {
		multiplier = 1.0
	}
	return countMode, multiplier
}
