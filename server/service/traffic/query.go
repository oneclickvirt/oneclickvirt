package traffic

import (
	"fmt"
	"time"

	"oneclickvirt/global"
)

// QueryService 流量查询服务 - 统一的流量数据查询入口
// 所有流量数据从 pmacct_traffic_records 实时聚合计算，确保数据一致性
type QueryService struct{}

// NewQueryService 创建流量查询服务
func NewQueryService() *QueryService {
	return &QueryService{}
}

// TrafficStats 流量统计结果
type TrafficStats struct {
	RxBytes       int64   `json:"rx_bytes"`        // 接收字节数
	TxBytes       int64   `json:"tx_bytes"`        // 发送字节数
	TotalBytes    int64   `json:"total_bytes"`     // 总字节数
	ActualUsageMB float64 `json:"actual_usage_mb"` // 实际使用量（MB，已应用流量计算模式）
}

// rawTrafficRecord 用于分段流量计算的原始记录类型
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
		return nil, fmt.Errorf("批量查询Provider流量配置失败: %w", err)
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

func computeWindowTraffic(records []rawTrafficRecord, baseline *rawTrafficRecord) (totalRx, totalTx int64) {
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
		return nil, fmt.Errorf("批量查询流量窗口基线失败: %w", err)
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
		return nil, fmt.Errorf("批量加载流量窗口原始记录失败: %w", err)
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
		return nil, fmt.Errorf("查询Provider配置失败: %w", err)
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
		return nil, fmt.Errorf("查询Provider流量实例列表失败: %w", err)
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
			return nil, fmt.Errorf("批量查询Provider流量实例列表失败: %w", err)
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
	if err := global.APP_DB.Unscoped().Table("instances").
		Select("id as instance_id, user_id").
		Where("user_id IN ?", userIDs).
		Find(&rows).Error; err != nil {
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
	type providerReset struct {
		TrafficResetDay *int
	}
	var rows []providerReset
	if err := global.APP_DB.Unscoped().Table("instances i").
		Joins("INNER JOIN providers p ON i.provider_id = p.id").
		Select("p.traffic_reset_day as traffic_reset_day").
		Where("i.user_id = ? AND p.enable_traffic_control = ?", userID, true).
		Group("p.id, p.traffic_reset_day").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询用户节点流量重置时间失败: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	now := time.Now()
	var next *time.Time
	for _, row := range rows {
		resetAt := NextTrafficResetTime(row.TrafficResetDay, now)
		if next == nil || resetAt.Before(*next) {
			value := resetAt
			next = &value
		}
	}
	return next, nil
}

// GetInstanceMonthlyTraffic 获取实例当月流量统计
// 返回原始流量和应用Provider流量计算模式后的实际使用量
func (s *QueryService) GetInstanceMonthlyTraffic(instanceID uint, year, month int) (*TrafficStats, error) {
	// 一次性加载当月所有原始记录，按时间戳排序，在 Go 层做分段检测（避免 O(n²) 关联子查询）
	var records []rawTrafficRecord
	err := global.APP_DB.Table("pmacct_traffic_records").
		Select("rx_bytes, tx_bytes").
		Where("instance_id = ? AND year = ? AND month = ?", instanceID, year, month).
		Order("timestamp ASC").
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("查询实例月度流量失败: %w", err)
	}

	rxBytes, txBytes := computeSegmentTraffic(records)

	// 获取Provider配置用于计算实际使用量（包含软删除的实例）
	var providerConfig struct {
		EnableTrafficControl bool
		TrafficCountMode     string
		TrafficMultiplier    float64
	}
	err = global.APP_DB.Unscoped().Table("instances i").
		Joins("INNER JOIN providers p ON i.provider_id = p.id").
		Select("p.enable_traffic_control as enable_traffic_control, COALESCE(p.traffic_count_mode, 'both') as traffic_count_mode, COALESCE(p.traffic_multiplier, 1.0) as traffic_multiplier").
		Where("i.id = ?", instanceID).
		Scan(&providerConfig).Error
	if err != nil {
		return nil, fmt.Errorf("查询Provider配置失败: %w", err)
	}

	stats := &TrafficStats{
		RxBytes:    rxBytes,
		TxBytes:    txBytes,
		TotalBytes: rxBytes + txBytes,
	}
	if providerConfig.EnableTrafficControl {
		stats.ActualUsageMB = s.calculateActualUsage(
			rxBytes,
			txBytes,
			providerConfig.TrafficCountMode,
			providerConfig.TrafficMultiplier,
		)
	}
	return stats, nil
}

// computeSegmentTraffic 在 Go 层执行 pmacct 重启检测与分段求和（O(n) 复杂度）。
// 输入记录必须按时间戳升序排列。
func computeSegmentTraffic(records []rawTrafficRecord) (totalRx, totalTx int64) {
	if len(records) == 0 {
		return 0, 0
	}

	var segMaxRx, segMaxTx int64
	var prevRx, prevTx int64

	for i, r := range records {
		if i > 0 {
			// Rx和Tx独立检测重置，避免一方重置导致另一方计数丢失
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
	return
}

// GetUserMonthlyTraffic 获取用户当月所有实例的流量统计
// 只统计启用了流量控制的Provider
// 处理pmacct重启导致的累积值重置问题
func (s *QueryService) GetUserMonthlyTraffic(userID uint, year, month int) (*TrafficStats, error) {
	// 获取用户所有实例列表（包含软删除的实例，以统计历史流量）
	var instanceIDs []uint
	err := global.APP_DB.Unscoped().Table("instances").
		Where("user_id = ?", userID).
		Pluck("id", &instanceIDs).Error
	if err != nil {
		return nil, fmt.Errorf("获取用户实例列表失败: %w", err)
	}

	if len(instanceIDs) == 0 {
		return &TrafficStats{}, nil
	}

	// 使用批量查询（已包含重启检测逻辑）
	instanceStats, err := s.BatchGetInstancesMonthlyTraffic(instanceIDs, year, month)
	if err != nil {
		return nil, err
	}

	// 汇总所有实例的流量（只统计启用了流量控制的Provider）
	var totalRxBytes int64
	var totalTxBytes int64
	var totalActualUsageMB float64

	for _, stats := range instanceStats {
		totalRxBytes += stats.RxBytes
		totalTxBytes += stats.TxBytes
		totalActualUsageMB += stats.ActualUsageMB
	}

	return &TrafficStats{
		RxBytes:       totalRxBytes,
		TxBytes:       totalTxBytes,
		TotalBytes:    totalRxBytes + totalTxBytes,
		ActualUsageMB: totalActualUsageMB,
	}, nil
}

// GetProviderMonthlyTraffic 获取Provider当月所有实例的流量统计
// 使用provider_traffic_histories聚合表，大幅提升性能
func (s *QueryService) GetProviderMonthlyTraffic(providerID uint, year, month int) (*TrafficStats, error) {
	// 首先检查Provider是否启用了流量控制
	var p struct {
		EnableTrafficControl bool
		TrafficCountMode     string
		TrafficMultiplier    float64
	}

	err := global.APP_DB.Table("providers").
		Select("enable_traffic_control, COALESCE(traffic_count_mode, 'both') as traffic_count_mode, COALESCE(traffic_multiplier, 1.0) as traffic_multiplier").
		Where("id = ?", providerID).
		Scan(&p).Error
	if err != nil {
		return nil, fmt.Errorf("查询Provider配置失败: %w", err)
	}

	if !p.EnableTrafficControl {
		// 未启用流量控制，返回0
		return &TrafficStats{}, nil
	}

	// 使用聚合表查询，性能大幅提升
	// day=0,hour=0 表示月度汇总数据
	var result struct {
		TrafficIn  float64
		TrafficOut float64
		TotalUsed  float64
	}

	err = global.APP_DB.Table("instance_traffic_histories").
		Select("COALESCE(SUM(traffic_in), 0) as traffic_in, COALESCE(SUM(traffic_out), 0) as traffic_out, COALESCE(SUM(traffic_in + traffic_out), 0) as total_used").
		Where("provider_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL",
			providerID, year, month).
		Scan(&result).Error
	if err != nil {
		return nil, fmt.Errorf("查询Provider流量失败: %w", err)
	}

	// 聚合表中存储的traffic_in/traffic_out/total_used都是MB单位
	// 根据流量模式计算实际使用量（MB）
	actualUsageMB := s.calculateActualUsageMB(result.TrafficIn, result.TrafficOut, p.TrafficCountMode, p.TrafficMultiplier)

	// 聚合表存储的是MB，转换为字节用于统一返回格式
	rxBytes := int64(result.TrafficIn * 1048576) // MB转字节：* 1024 * 1024
	txBytes := int64(result.TrafficOut * 1048576)

	return &TrafficStats{
		RxBytes:       rxBytes,
		TxBytes:       txBytes,
		TotalBytes:    rxBytes + txBytes,
		ActualUsageMB: actualUsageMB,
	}, nil
}

// BatchGetInstancesMonthlyTraffic 批量获取多个实例的月度流量
// 1. 优先使用缓存表（instance_traffic_histories）快速查询
// 2. 缓存未命中时，使用正确的分段计算逻辑
// 3. 支持增量更新缓存
func (s *QueryService) BatchGetInstancesMonthlyTraffic(instanceIDs []uint, year, month int) (map[uint]*TrafficStats, error) {
	if len(instanceIDs) == 0 {
		return make(map[uint]*TrafficStats), nil
	}

	// 策略1: 尝试从缓存表获取（日度汇总 hour=0, day=0 表示月度汇总）
	cachedStats := s.getBatchFromCache(instanceIDs, year, month)

	// 策略2: 识别缓存未命中的实例
	var uncachedIDs []uint
	for _, id := range instanceIDs {
		if _, ok := cachedStats[id]; !ok {
			uncachedIDs = append(uncachedIDs, id)
		}
	}

	// 策略3: 对未缓存的实例执行实时计算
	if len(uncachedIDs) > 0 {
		computedStats, err := s.computeBatchMonthlyTraffic(uncachedIDs, year, month)
		if err != nil {
			return nil, err
		}
		// 合并结果
		for id, stats := range computedStats {
			cachedStats[id] = stats
		}
	}

	// 确保所有实例都有结果（即使是空值）
	for _, id := range instanceIDs {
		if _, ok := cachedStats[id]; !ok {
			cachedStats[id] = &TrafficStats{}
		}
	}

	return cachedStats, nil
}

// getBatchFromCache 从缓存表批量获取流量数据
func (s *QueryService) getBatchFromCache(instanceIDs []uint, year, month int) map[uint]*TrafficStats {
	type CacheResult struct {
		InstanceID           uint
		TrafficIn            float64
		TrafficOut           float64
		TotalUsed            float64
		EnableTrafficControl bool
		TrafficCountMode     string
		TrafficMultiplier    float64
	}

	var results []CacheResult
	// 查询月度汇总记录 (day=0, hour=0)
	err := global.APP_DB.Table("instance_traffic_histories ith").
		Joins("INNER JOIN providers p ON ith.provider_id = p.id").
		Select("ith.instance_id, ith.traffic_in, ith.traffic_out, (ith.traffic_in + ith.traffic_out) as total_used, p.enable_traffic_control as enable_traffic_control, COALESCE(p.traffic_count_mode, 'both') as traffic_count_mode, COALESCE(p.traffic_multiplier, 1.0) as traffic_multiplier").
		Where("ith.instance_id IN ? AND ith.year = ? AND ith.month = ? AND ith.day = 0 AND ith.hour = 0 AND ith.deleted_at IS NULL", instanceIDs, year, month).
		Find(&results).Error

	if err != nil {
		return make(map[uint]*TrafficStats)
	}

	statsMap := make(map[uint]*TrafficStats)
	for _, r := range results {
		// 缓存表存储的是MB，转换为字节用于统一返回格式
		// RxBytes/TxBytes/TotalBytes: 字节单位
		// ActualUsageMB: MB单位（已应用流量计算模式）
		stats := &TrafficStats{
			RxBytes:    int64(r.TrafficIn * 1048576),  // MB -> Bytes
			TxBytes:    int64(r.TrafficOut * 1048576), // MB -> Bytes
			TotalBytes: int64((r.TrafficIn + r.TrafficOut) * 1048576),
		}
		if r.EnableTrafficControl {
			stats.ActualUsageMB = s.calculateActualUsageMB(r.TrafficIn, r.TrafficOut, r.TrafficCountMode, r.TrafficMultiplier)
		}
		statsMap[r.InstanceID] = stats
	}

	return statsMap
}

// computeBatchMonthlyTraffic 实时批量计算多个实例的月度流量（O(n) 复杂度，正确处理pmacct重启）
// 一次性加载所有原始记录，在 Go 层分组并分段求和，避免 O(n²) 关联子查询
func (s *QueryService) computeBatchMonthlyTraffic(instanceIDs []uint, year, month int) (map[uint]*TrafficStats, error) {
	if len(instanceIDs) == 0 {
		return make(map[uint]*TrafficStats), nil
	}

	// 一次性加载所有实例当月的原始记录，按 instance_id + timestamp 排序
	type BatchRawRecord struct {
		InstanceID uint
		RxBytes    int64
		TxBytes    int64
	}
	var allRecords []BatchRawRecord
	err := global.APP_DB.Table("pmacct_traffic_records").
		Select("instance_id, rx_bytes, tx_bytes").
		Where("instance_id IN ? AND year = ? AND month = ?", instanceIDs, year, month).
		Order("instance_id ASC, timestamp ASC").
		Find(&allRecords).Error
	if err != nil {
		return nil, fmt.Errorf("批量加载流量原始记录失败: %w", err)
	}

	// 按 instance_id 分组后在 Go 层做分段求和
	type groupSlice struct {
		records []rawTrafficRecord
	}
	groups := make(map[uint]*groupSlice, len(instanceIDs))
	for _, rec := range allRecords {
		g := groups[rec.InstanceID]
		if g == nil {
			g = &groupSlice{}
			groups[rec.InstanceID] = g
		}
		g.records = append(g.records, rawTrafficRecord{RxBytes: rec.RxBytes, TxBytes: rec.TxBytes})
	}

	// 批量获取 Provider 配置（一次查询，包含软删除的实例）
	var providerConfigs []struct {
		InstanceID           uint
		EnableTrafficControl bool
		TrafficCountMode     string
		TrafficMultiplier    float64
	}
	err = global.APP_DB.Unscoped().Table("instances i").
		Joins("INNER JOIN providers p ON i.provider_id = p.id").
		Select("i.id as instance_id, p.enable_traffic_control as enable_traffic_control, COALESCE(p.traffic_count_mode, 'both') as traffic_count_mode, COALESCE(p.traffic_multiplier, 1.0) as traffic_multiplier").
		Where("i.id IN ?", instanceIDs).
		Find(&providerConfigs).Error
	if err != nil {
		return nil, fmt.Errorf("批量查询Provider配置失败: %w", err)
	}

	type cfgEntry struct {
		Enabled    bool
		CountMode  string
		Multiplier float64
	}
	configMap := make(map[uint]cfgEntry, len(providerConfigs))
	for _, cfg := range providerConfigs {
		configMap[cfg.InstanceID] = cfgEntry{Enabled: cfg.EnableTrafficControl, CountMode: cfg.TrafficCountMode, Multiplier: cfg.TrafficMultiplier}
	}

	// 为每个实例计算分段流量并应用Provider配置
	statsMap := make(map[uint]*TrafficStats, len(instanceIDs))
	for _, id := range instanceIDs {
		g := groups[id]
		var rxBytes, txBytes int64
		if g != nil && len(g.records) > 0 {
			rxBytes, txBytes = computeSegmentTraffic(g.records)
		}

		stats := &TrafficStats{
			RxBytes:    rxBytes,
			TxBytes:    txBytes,
			TotalBytes: rxBytes + txBytes,
		}
		if cfg, ok := configMap[id]; ok && cfg.Enabled {
			stats.ActualUsageMB = s.calculateActualUsage(rxBytes, txBytes, cfg.CountMode, cfg.Multiplier)
		}
		statsMap[id] = stats
	}
	return statsMap, nil
}

// GetInstanceTrafficHistory 获取实例的流量历史（按天聚合）
// 实时从 pmacct_traffic_records 聚合生成历史数据
func (s *QueryService) GetInstanceTrafficHistory(instanceID uint, days int) ([]*HistoryPoint, error) {
	// 获取实例和Provider配置（用于计算实际用量）
	var config struct {
		TrafficCountMode  string
		TrafficMultiplier float64
	}
	if err := global.APP_DB.Table("instances i").
		Joins("INNER JOIN providers p ON i.provider_id = p.id").
		Select("p.traffic_count_mode, p.traffic_multiplier").
		Where("i.id = ?", instanceID).
		Scan(&config).Error; err != nil {
		return nil, fmt.Errorf("查询实例配置失败: %w", err)
	}

	// 计算起始日期
	startDate := time.Now().AddDate(0, 0, -days).Truncate(24 * time.Hour)

	// 按天聚合查询，处理pmacct重启问题
	var results []struct {
		Date    time.Time
		RxBytes int64
		TxBytes int64
	}

	// 兼容 MySQL 5.x - 不使用 CTE (WITH AS) 和窗口函数
	// MySQL 5.x 不支持 CTE，改用派生表（子查询）实现相同逻辑
	query := `
		SELECT 
			date,
			SUM(max_rx) as rx_bytes,
			SUM(max_tx) as tx_bytes
		FROM (
			-- 每天的每个段取MAX
			SELECT 
				date,
				segment_id,
				MAX(rx_bytes) as max_rx,
				MAX(tx_bytes) as max_tx
			FROM (
				-- 检测累积值重置点（使用相关子查询，兼容MySQL 5.x）
				SELECT 
					DATE(t1.timestamp) as date,
					t1.timestamp,
					t1.rx_bytes,
					t1.tx_bytes,
					(SELECT COUNT(*)
					 FROM pmacct_traffic_records t2
					 WHERE t2.instance_id = ? 
					   AND DATE(t2.timestamp) = DATE(t1.timestamp)
					   AND t2.timestamp <= t1.timestamp
					   AND (
						 (t2.rx_bytes < (SELECT COALESCE(MAX(t3.rx_bytes), 0)
										 FROM pmacct_traffic_records t3
										 WHERE t3.instance_id = ?
										   AND DATE(t3.timestamp) = DATE(t1.timestamp)
										   AND t3.timestamp < t2.timestamp))
						 OR
						 (t2.tx_bytes < (SELECT COALESCE(MAX(t3.tx_bytes), 0)
										 FROM pmacct_traffic_records t3
										 WHERE t3.instance_id = ?
										   AND DATE(t3.timestamp) = DATE(t1.timestamp)
										   AND t3.timestamp < t2.timestamp))
					   )
					) as segment_id
				FROM pmacct_traffic_records t1
				WHERE t1.instance_id = ? AND t1.timestamp >= ?
			) AS daily_segments
			GROUP BY date, segment_id
		) AS daily_segment_max
		GROUP BY date
		ORDER BY date ASC
	`

	if err := global.APP_DB.Raw(query, instanceID, instanceID, instanceID, instanceID, startDate).Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("查询实例流量历史失败: %w", err)
	}

	// 转换为历史点
	history := make([]*HistoryPoint, 0, len(results))
	for _, r := range results {
		actualUsageMB := s.calculateActualUsage(r.RxBytes, r.TxBytes, config.TrafficCountMode, config.TrafficMultiplier)
		history = append(history, &HistoryPoint{
			Date:          r.Date,
			Year:          r.Date.Year(),
			Month:         int(r.Date.Month()),
			Day:           r.Date.Day(),
			RxBytes:       r.RxBytes,
			TxBytes:       r.TxBytes,
			TotalBytes:    r.RxBytes + r.TxBytes,
			ActualUsageMB: actualUsageMB,
		})
	}

	return history, nil
}

// calculateActualUsage 根据流量计算模式计算实际使用量（MB）
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
	return (bytes * multiplier) / 1048576.0 // 转换为MB
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
