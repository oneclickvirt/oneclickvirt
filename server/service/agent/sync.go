package agent

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"strings"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
	"oneclickvirt/utils/dbcompat"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type trafficUserIDBackfill struct {
	monitorID  uint
	instanceID uint
	oldUserID  uint
	newUserID  uint
}

type trafficSyncItem struct {
	monitor           *monitoringModel.AgentMonitor
	currentTraffic    uint64
	currentTrafficIn  uint64
	currentTrafficOut uint64
	deltaBytesIn      uint64
	deltaBytesOut     uint64
	rxMB              float64
	txMB              float64
	alignedTime       time.Time
}

// SyncService synchronizes traffic data from the agent into MySQL history tables.
type SyncService struct {
	db  *gorm.DB
	ctx context.Context
}

// NewSyncService creates a new traffic sync service.
func NewSyncService(ctx context.Context, db *gorm.DB) *SyncService {
	return &SyncService{db: db, ctx: ctx}
}

// SyncProviderTraffic collects traffic from the agent for all monitors under a provider.
// It computes deltas, updates instance/provider/user history tables, and updates mappings.
func (s *SyncService) SyncProviderTraffic(providerID uint, config *monitoringModel.MonitoringConfig) error {
	// Load provider for traffic settings
	var p providerModel.Provider
	if err := s.db.First(&p, providerID).Error; err != nil {
		return fmt.Errorf("load provider %d: %w", providerID, err)
	}

	if !p.EnableTrafficControl {
		return nil
	}

	var monitors []monitoringModel.AgentMonitor
	if err := s.db.Where("provider_id = ? AND is_enabled = ?", providerID, true).Find(&monitors).Error; err != nil {
		return fmt.Errorf("list monitors: %w", err)
	}
	if len(monitors) == 0 {
		return nil
	}

	agentIDs := make([]int64, 0, len(monitors))
	monitorByAgentID := make(map[int64]*monitoringModel.AgentMonitor, len(monitors))
	instanceIDs := make([]uint, 0, len(monitors))
	for i := range monitors {
		agentIDs = append(agentIDs, monitors[i].AgentMonitorID)
		monitorByAgentID[monitors[i].AgentMonitorID] = &monitors[i]
		instanceIDs = append(instanceIDs, monitors[i].InstanceID)
	}
	sort.Slice(agentIDs, func(i, j int) bool { return agentIDs[i] < agentIDs[j] })
	sort.Slice(instanceIDs, func(i, j int) bool { return instanceIDs[i] < instanceIDs[j] })

	host := ResolveAgentHost(p.Endpoint, p.AgentRemoteIP)
	if host == "" {
		if p.ConnectionType == "agent" {
			host = "127.0.0.1" // loopback fallback; calls are routed through WS fallback
		} else {
			return fmt.Errorf("provider %d has no endpoint", providerID)
		}
	}
	port := config.AgentPort
	if port == 0 {
		port = AgentPort
	}
	client := GetClientWithMode(providerID, host, port, config.AgentToken, p.ConnectionType == "agent")

	// Batch fetch traffic info
	infoMap, err := client.BatchGetInfo(agentIDs)
	if err != nil {
		return fmt.Errorf("batch get info: %w", err)
	}
	if len(infoMap) == 0 {
		return nil
	}

	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	day := now.Day()
	hour := now.Hour()

	// Batch-load authoritative instance owners outside the write transactions.
	type idPair struct {
		ID     uint
		UserID uint
	}
	var pairs []idPair
	if err := s.db.Model(&providerModel.Instance{}).Select("id, user_id").Where("id IN ?", instanceIDs).Find(&pairs).Error; err != nil {
		return fmt.Errorf("batch load instance user_ids: %w", err)
	}
	instanceUserMap := make(map[uint]uint, len(pairs))
	for _, pair := range pairs {
		instanceUserMap[pair.ID] = pair.UserID
	}

	backfills := make([]trafficUserIDBackfill, 0)
	for i := range monitors {
		monitor := &monitors[i]
		if currentUID, ok := instanceUserMap[monitor.InstanceID]; ok && currentUID != monitor.UserID {
			backfills = append(backfills, trafficUserIDBackfill{
				monitorID:  monitor.ID,
				instanceID: monitor.InstanceID,
				oldUserID:  monitor.UserID,
				newUserID:  currentUID,
			})
			monitor.UserID = currentUID
		}
	}
	if len(backfills) > 0 {
		const backfillBatchSize = 100
		for start := 0; start < len(backfills); start += backfillBatchSize {
			end := start + backfillBatchSize
			if end > len(backfills) {
				end = len(backfills)
			}
			batch := backfills[start:end]
			if err := s.retryDB(func() error {
				return s.db.Transaction(func(tx *gorm.DB) error {
					return applyTrafficUserIDBackfills(tx, batch)
				})
			}); err != nil {
				return fmt.Errorf("backfill traffic ownership batch starting at %d: %w", start, err)
			}
		}
	}

	affectedUsers := make(map[uint]bool, len(monitors))
	syncItems := make([]trafficSyncItem, 0, len(infoMap))
	for _, agentID := range agentIDs {
		info := infoMap[agentID]
		if info == nil {
			continue
		}
		monitor := monitorByAgentID[agentID]
		if monitor == nil {
			continue
		}
		affectedUsers[monitor.UserID] = true

		currentTraffic := info.UsedTraffic
		currentTrafficIn := info.UsedTrafficIn
		currentTrafficOut := info.UsedTrafficOut

		var deltaBytesIn, deltaBytesOut uint64
		if currentTrafficIn >= monitor.LastTrafficBytesIn {
			deltaBytesIn = currentTrafficIn - monitor.LastTrafficBytesIn
		} else {
			deltaBytesIn = currentTrafficIn
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("Agent入站流量计数器重置检测",
					zap.Uint("instanceID", monitor.InstanceID),
					zap.Uint64("lastIn", monitor.LastTrafficBytesIn),
					zap.Uint64("currentIn", currentTrafficIn))
			}
		}
		if currentTrafficOut >= monitor.LastTrafficBytesOut {
			deltaBytesOut = currentTrafficOut - monitor.LastTrafficBytesOut
		} else {
			deltaBytesOut = currentTrafficOut
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("Agent出站流量计数器重置检测",
					zap.Uint("instanceID", monitor.InstanceID),
					zap.Uint64("lastOut", monitor.LastTrafficBytesOut),
					zap.Uint64("currentOut", currentTrafficOut))
			}
		}

		// History tables store raw in/out usage. Count mode and multiplier are
		// applied only when querying usage so agent and pmacct data stay consistent.
		rxMB := float64(deltaBytesIn) / 1048576.0
		txMB := float64(deltaBytesOut) / 1048576.0
		minute := (now.Minute() / 5) * 5
		alignedTime := time.Date(year, time.Month(month), day, hour, minute, 0, 0, now.Location())

		syncItems = append(syncItems, trafficSyncItem{
			monitor:           monitor,
			currentTraffic:    currentTraffic,
			currentTrafficIn:  currentTrafficIn,
			currentTrafficOut: currentTrafficOut,
			deltaBytesIn:      deltaBytesIn,
			deltaBytesOut:     deltaBytesOut,
			rxMB:              rxMB,
			txMB:              txMB,
			alignedTime:       alignedTime,
		})
	}

	var firstErr error
	const writeBatchSize = 25
	monthlyInstanceIDs := make([]uint, 0, len(syncItems))
	for _, item := range syncItems {
		if item.deltaBytesIn+item.deltaBytesOut > 0 {
			monthlyInstanceIDs = append(monthlyInstanceIDs, item.monitor.InstanceID)
		}
	}
	for start := 0; start < len(syncItems); start += writeBatchSize {
		end := start + writeBatchSize
		if end > len(syncItems) {
			end = len(syncItems)
		}
		batch := syncItems[start:end]
		err := s.retryDB(func() error {
			return s.db.Transaction(func(tx *gorm.DB) error {
				return s.applyTrafficSyncBatch(tx, batch, year, month, day, hour, now)
			})
		})
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("sync traffic batch starting at %d: %w", start, err)
			}
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("traffic sync batch failed",
					zap.Int("batch_start", start),
					zap.Int("batch_size", len(batch)),
					zap.Error(err))
			}
		}
	}
	if err := s.retryDB(func() error {
		return s.upsertInstancesMonthlyTraffic(monthlyInstanceIDs, year, month, now)
	}); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("upsert instance monthly traffic: %w", err)
		}
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("aggregate instance monthly traffic failed",
				zap.Int("instance_count", len(monthlyInstanceIDs)),
				zap.Error(err))
		}
	}

	if err := s.retryDB(func() error {
		return s.aggregateProviderTraffic(providerID, year, month, day, hour, now)
	}); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("aggregate provider traffic: %w", err)
		}
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("aggregate provider traffic failed",
				zap.Uint("provider_id", providerID),
				zap.Error(err))
		}
	}

	// Aggregate user traffic for all affected users
	affectedUserIDs := make([]uint, 0, len(affectedUsers))
	for userID := range affectedUsers {
		affectedUserIDs = append(affectedUserIDs, userID)
	}
	sort.Slice(affectedUserIDs, func(i, j int) bool { return affectedUserIDs[i] < affectedUserIDs[j] })
	if err := s.retryDB(func() error {
		return s.aggregateUsersTraffic(affectedUserIDs, year, month, day, hour, now)
	}); err != nil {
		if firstErr == nil {
			firstErr = fmt.Errorf("aggregate user traffic: %w", err)
		}
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("aggregate user traffic batch failed",
				zap.Int("user_count", len(affectedUserIDs)),
				zap.Error(err))
		}
	}

	return firstErr
}

func (s *SyncService) retryDB(operation func() error) error {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return utils.RetryableDBOperation(ctx, operation, 8)
}

func (s *SyncService) applyTrafficSyncBatch(
	tx *gorm.DB,
	items []trafficSyncItem,
	year, month, day, hour int,
	now time.Time,
) error {
	if len(items) == 0 {
		return nil
	}

	monitorIDs := make([]uint, 0, len(items))
	for _, item := range items {
		monitorIDs = append(monitorIDs, item.monitor.ID)
	}
	var lockedMonitors []monitoringModel.AgentMonitor
	if err := tx.Select("id, last_traffic_bytes, last_traffic_bytes_in, last_traffic_bytes_out").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id IN ?", monitorIDs).
		Find(&lockedMonitors).Error; err != nil {
		return fmt.Errorf("lock agent monitor tracking rows: %w", err)
	}
	lockedByID := make(map[uint]monitoringModel.AgentMonitor, len(lockedMonitors))
	for _, monitor := range lockedMonitors {
		lockedByID[monitor.ID] = monitor
	}

	claimed, trafficItems := claimTrafficSyncItems(items, lockedByID)
	if len(claimed) == 0 {
		return nil
	}

	if err := updateAgentMonitorTrackingBatch(tx, claimed, now); err != nil {
		return fmt.Errorf("update agent monitor tracking batch: %w", err)
	}
	if err := upsertInstanceTrafficHistoriesBatch(tx, trafficItems, year, month, day, hour, now); err != nil {
		return fmt.Errorf("upsert instance traffic history batch: %w", err)
	}
	if err := upsertAgentPmacctRecordsBatch(tx, trafficItems, year, month, day, hour, now); err != nil {
		return fmt.Errorf("upsert pmacct traffic record batch: %w", err)
	}

	return nil
}

func claimTrafficSyncItems(
	items []trafficSyncItem,
	lockedByID map[uint]monitoringModel.AgentMonitor,
) ([]trafficSyncItem, []trafficSyncItem) {
	claimed := make([]trafficSyncItem, 0, len(items))
	trafficItems := make([]trafficSyncItem, 0, len(items))
	for _, item := range items {
		current, ok := lockedByID[item.monitor.ID]
		if !ok || current.LastTrafficBytes != item.monitor.LastTrafficBytes ||
			current.LastTrafficBytesIn != item.monitor.LastTrafficBytesIn ||
			current.LastTrafficBytesOut != item.monitor.LastTrafficBytesOut {
			continue
		}
		claimed = append(claimed, item)
		if item.deltaBytesIn+item.deltaBytesOut > 0 {
			trafficItems = append(trafficItems, item)
		}
	}
	return claimed, trafficItems
}

func updateAgentMonitorTrackingBatch(tx *gorm.DB, items []trafficSyncItem, now time.Time) error {
	if len(items) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString("UPDATE agent_monitors SET last_traffic_bytes = CASE id")
	args := make([]interface{}, 0, len(items)*8+2)
	for _, item := range items {
		query.WriteString(" WHEN ? THEN ?")
		args = append(args, item.monitor.ID, item.currentTraffic)
	}
	query.WriteString(" ELSE last_traffic_bytes END, last_traffic_bytes_in = CASE id")
	for _, item := range items {
		query.WriteString(" WHEN ? THEN ?")
		args = append(args, item.monitor.ID, item.currentTrafficIn)
	}
	query.WriteString(" ELSE last_traffic_bytes_in END, last_traffic_bytes_out = CASE id")
	for _, item := range items {
		query.WriteString(" WHEN ? THEN ?")
		args = append(args, item.monitor.ID, item.currentTrafficOut)
	}
	query.WriteString(" ELSE last_traffic_bytes_out END, last_sync_at = ?, updated_at = ? WHERE deleted_at IS NULL AND id IN (")
	args = append(args, now, now)
	for index, item := range items {
		if index > 0 {
			query.WriteByte(',')
		}
		query.WriteByte('?')
		args = append(args, item.monitor.ID)
	}
	query.WriteByte(')')
	return tx.Exec(query.String(), args...).Error
}

func upsertInstanceTrafficHistoriesBatch(
	tx *gorm.DB,
	items []trafficSyncItem,
	year, month, day, hour int,
	now time.Time,
) error {
	if len(items) == 0 {
		return nil
	}
	const columns = 13
	row := "(" + strings.TrimSuffix(strings.Repeat("?,", columns), ",") + ")"
	rows := strings.TrimSuffix(strings.Repeat(row+",", len(items)), ",")
	valuesSQL := `INSERT INTO instance_traffic_histories
		(instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at)
		VALUES ` + rows + `
		ON DUPLICATE KEY UPDATE
			traffic_in = traffic_in + VALUES(traffic_in),
			traffic_out = traffic_out + VALUES(traffic_out),
			total_used = total_used + VALUES(total_used),
			provider_id = VALUES(provider_id), user_id = VALUES(user_id),
			record_time = VALUES(record_time), updated_at = VALUES(updated_at)`
	rowAliasSQL := `INSERT INTO instance_traffic_histories
		(instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at)
		VALUES ` + rows + ` AS _new
		ON DUPLICATE KEY UPDATE
			traffic_in = instance_traffic_histories.traffic_in + _new.traffic_in,
			traffic_out = instance_traffic_histories.traffic_out + _new.traffic_out,
			total_used = instance_traffic_histories.total_used + _new.total_used,
			provider_id = _new.provider_id, user_id = _new.user_id,
			record_time = _new.record_time, updated_at = _new.updated_at`
	args := make([]interface{}, 0, columns*len(items))
	for _, item := range items {
		args = append(args,
			item.monitor.InstanceID, item.monitor.ProviderID, item.monitor.UserID,
			item.rxMB, item.txMB, item.rxMB+item.txMB,
			year, month, day, hour, now, now, now,
		)
	}
	return dbcompat.Exec(tx, valuesSQL, rowAliasSQL, args...).Error
}

func upsertAgentPmacctRecordsBatch(
	tx *gorm.DB,
	items []trafficSyncItem,
	year, month, day, hour int,
	now time.Time,
) error {
	if len(items) == 0 {
		return nil
	}
	const columns = 16
	row := "(?, ?, ?, 'agent', " + strings.TrimSuffix(strings.Repeat("?,", columns-3), ",") + ")"
	rows := strings.TrimSuffix(strings.Repeat(row+",", len(items)), ",")
	valuesSQL := `INSERT INTO pmacct_traffic_records
		(instance_id, user_id, provider_id, provider_type, mapped_ip,
		 rx_bytes, tx_bytes, total_bytes, timestamp, year, month, day, hour, minute,
		 record_time, created_at, updated_at)
		VALUES ` + rows + `
		ON DUPLICATE KEY UPDATE
			rx_bytes = VALUES(rx_bytes), tx_bytes = VALUES(tx_bytes), total_bytes = VALUES(total_bytes),
			record_time = VALUES(record_time), updated_at = VALUES(updated_at)`
	rowAliasSQL := `INSERT INTO pmacct_traffic_records
		(instance_id, user_id, provider_id, provider_type, mapped_ip,
		 rx_bytes, tx_bytes, total_bytes, timestamp, year, month, day, hour, minute,
		 record_time, created_at, updated_at)
		VALUES ` + rows + ` AS _new
		ON DUPLICATE KEY UPDATE
			rx_bytes = _new.rx_bytes, tx_bytes = _new.tx_bytes, total_bytes = _new.total_bytes,
			record_time = _new.record_time, updated_at = _new.updated_at`
	args := make([]interface{}, 0, columns*len(items))
	for _, item := range items {
		args = append(args,
			item.monitor.InstanceID, item.monitor.UserID, item.monitor.ProviderID,
			item.monitor.Interfaces,
			int64(item.currentTrafficIn), int64(item.currentTrafficOut), int64(item.currentTraffic),
			item.alignedTime, year, month, day, hour, item.alignedTime.Minute(),
			now, now, now,
		)
	}
	return dbcompat.Exec(tx, valuesSQL, rowAliasSQL, args...).Error
}

func (s *SyncService) upsertInstancesMonthlyTraffic(
	instanceIDs []uint,
	year, month int,
	now time.Time,
) error {
	if len(instanceIDs) == 0 {
		return nil
	}
	uniqueIDs := make([]uint, 0, len(instanceIDs))
	seen := make(map[uint]struct{}, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		if _, exists := seen[instanceID]; exists {
			continue
		}
		seen[instanceID] = struct{}{}
		uniqueIDs = append(uniqueIDs, instanceID)
	}
	const batchSize = 200
	for start := 0; start < len(uniqueIDs); start += batchSize {
		end := start + batchSize
		if end > len(uniqueIDs) {
			end = len(uniqueIDs)
		}
		if err := s.upsertInstancesMonthlyTrafficBatch(uniqueIDs[start:end], year, month, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *SyncService) upsertInstancesMonthlyTrafficBatch(
	instanceIDs []uint,
	year, month int,
	now time.Time,
) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(instanceIDs)), ",")
	valuesSQL := fmt.Sprintf(`INSERT INTO instance_traffic_histories
		(instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at)
		SELECT instance_id, MAX(provider_id), MAX(user_id),
			SUM(traffic_in), SUM(traffic_out), SUM(total_used), year, month, 0, 0, ?, ?, ?
		FROM instance_traffic_histories
		WHERE instance_id IN (%s) AND year = ? AND month = ? AND day > 0 AND deleted_at IS NULL
		GROUP BY instance_id, year, month
		ON DUPLICATE KEY UPDATE
			traffic_in = VALUES(traffic_in), traffic_out = VALUES(traffic_out),
			total_used = VALUES(total_used), provider_id = VALUES(provider_id), user_id = VALUES(user_id),
			record_time = VALUES(record_time), updated_at = VALUES(updated_at)`, placeholders)
	rowAliasSQL := fmt.Sprintf(`INSERT INTO instance_traffic_histories
		(instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at)
		SELECT instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at
		FROM (
			SELECT instance_id, MAX(provider_id) AS provider_id, MAX(user_id) AS user_id,
				SUM(traffic_in) AS traffic_in, SUM(traffic_out) AS traffic_out, SUM(total_used) AS total_used,
				year, month, 0 AS day, 0 AS hour, ? AS record_time, ? AS created_at, ? AS updated_at
			FROM instance_traffic_histories
			WHERE instance_id IN (%s) AND year = ? AND month = ? AND day > 0 AND deleted_at IS NULL
			GROUP BY instance_id, year, month
		) AS _src
		ON DUPLICATE KEY UPDATE
			traffic_in = _src.traffic_in, traffic_out = _src.traffic_out,
			total_used = _src.total_used, provider_id = _src.provider_id, user_id = _src.user_id,
			record_time = _src.record_time, updated_at = _src.updated_at`, placeholders)
	args := make([]interface{}, 0, 5+len(instanceIDs))
	args = append(args, now, now, now)
	for _, instanceID := range instanceIDs {
		args = append(args, instanceID)
	}
	args = append(args, year, month)
	return dbcompat.Exec(s.db, valuesSQL, rowAliasSQL, args...).Error
}

func applyTrafficUserIDBackfills(tx *gorm.DB, changes []trafficUserIDBackfill) error {
	if len(changes) == 0 {
		return nil
	}

	monitorIDs := make([]uint, 0, len(changes))
	monitorArgs := make([]interface{}, 0, len(changes)*2)
	var monitorCase strings.Builder
	monitorCase.WriteString("CASE id")
	for _, change := range changes {
		monitorIDs = append(monitorIDs, change.monitorID)
		monitorCase.WriteString(" WHEN ? THEN ?")
		monitorArgs = append(monitorArgs, change.monitorID, change.newUserID)
	}
	monitorCase.WriteString(" ELSE user_id END")
	if err := tx.Model(&monitoringModel.AgentMonitor{}).
		Where("id IN ?", monitorIDs).
		Update("user_id", gorm.Expr(monitorCase.String(), monitorArgs...)).Error; err != nil {
		return fmt.Errorf("backfill monitor user_id: %w", err)
	}

	if err := applyTrafficHistoryUserIDBackfill(tx, "pmacct_traffic_records", changes); err != nil {
		return fmt.Errorf("backfill pmacct user_id: %w", err)
	}
	if err := applyTrafficHistoryUserIDBackfill(tx, "instance_traffic_histories", changes); err != nil {
		return fmt.Errorf("backfill history user_id: %w", err)
	}

	return nil
}

func applyTrafficHistoryUserIDBackfill(tx *gorm.DB, table string, changes []trafficUserIDBackfill) error {
	var query strings.Builder
	args := make([]interface{}, 0, len(changes)*5)

	query.WriteString("UPDATE ")
	query.WriteString(table)
	query.WriteString(" SET user_id = CASE")
	for _, change := range changes {
		query.WriteString(" WHEN instance_id = ? AND user_id = ? THEN ?")
		args = append(args, change.instanceID, change.oldUserID, change.newUserID)
	}
	query.WriteString(" ELSE user_id END WHERE ")
	for i, change := range changes {
		if i > 0 {
			query.WriteString(" OR ")
		}
		query.WriteString("(instance_id = ? AND user_id = ?)")
		args = append(args, change.instanceID, change.oldUserID)
	}

	return tx.Exec(query.String(), args...).Error
}

// aggregateProviderTraffic aggregates instance data into provider traffic history.
func (s *SyncService) aggregateProviderTraffic(
	providerID uint,
	year, month, day, hour int,
	now time.Time,
) error {
	// Hourly aggregation
	err := dbcompat.Exec(s.db,
		// MariaDB / MySQL < 9
		`INSERT INTO provider_traffic_histories
			(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT
			?, SUM(traffic_in), SUM(traffic_out), SUM(total_used), COUNT(DISTINCT instance_id),
			?, ?, ?, ?, ?, ?, ?
		FROM instance_traffic_histories
		WHERE provider_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
		ON DUPLICATE KEY UPDATE
			traffic_in     = VALUES(traffic_in),
			traffic_out    = VALUES(traffic_out),
			total_used     = VALUES(total_used),
			instance_count = VALUES(instance_count),
			record_time    = VALUES(record_time),
			updated_at     = VALUES(updated_at)`,
		// MySQL 9.0+
		`INSERT INTO provider_traffic_histories
			(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at
		FROM (
			SELECT
				? AS provider_id, SUM(traffic_in) AS traffic_in, SUM(traffic_out) AS traffic_out,
				SUM(total_used) AS total_used, COUNT(DISTINCT instance_id) AS instance_count,
				? AS year, ? AS month, ? AS day, ? AS hour, ? AS record_time, ? AS created_at, ? AS updated_at
			FROM instance_traffic_histories
			WHERE provider_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
		) AS _src
		ON DUPLICATE KEY UPDATE
			traffic_in     = _src.traffic_in,
			traffic_out    = _src.traffic_out,
			total_used     = _src.total_used,
			instance_count = _src.instance_count,
			record_time    = _src.record_time,
			updated_at     = _src.updated_at`,
		providerID, year, month, day, hour, now, now, now,
		providerID, year, month, day, hour).Error

	if err != nil {
		return err
	}

	// Monthly aggregation (day=0, hour=0)
	return dbcompat.Exec(s.db,
		// MariaDB / MySQL < 9
		`INSERT INTO provider_traffic_histories
			(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT
			?, SUM(traffic_in), SUM(traffic_out), SUM(total_used), COUNT(DISTINCT instance_id),
			?, ?, 0, 0, ?, ?, ?
		FROM instance_traffic_histories
		WHERE provider_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
		ON DUPLICATE KEY UPDATE
			traffic_in     = VALUES(traffic_in),
			traffic_out    = VALUES(traffic_out),
			total_used     = VALUES(total_used),
			instance_count = VALUES(instance_count),
			record_time    = VALUES(record_time),
			updated_at     = VALUES(updated_at)`,
		// MySQL 9.0+
		`INSERT INTO provider_traffic_histories
			(provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT provider_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at
		FROM (
			SELECT
				? AS provider_id, SUM(traffic_in) AS traffic_in, SUM(traffic_out) AS traffic_out,
				SUM(total_used) AS total_used, COUNT(DISTINCT instance_id) AS instance_count,
				? AS year, ? AS month, 0 AS day, 0 AS hour, ? AS record_time, ? AS created_at, ? AS updated_at
			FROM instance_traffic_histories
			WHERE provider_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
		) AS _src
		ON DUPLICATE KEY UPDATE
			traffic_in     = _src.traffic_in,
			traffic_out    = _src.traffic_out,
			total_used     = _src.total_used,
			instance_count = _src.instance_count,
			record_time    = _src.record_time,
			updated_at     = _src.updated_at`,
		providerID, year, month, now, now, now,
		providerID, year, month).Error
}

// aggregateUserTraffic aggregates instance data into user traffic history.
func (s *SyncService) aggregateUserTraffic(
	userID uint,
	year, month, day, hour int,
	now time.Time,
) error {
	// Hourly aggregation
	err := dbcompat.Exec(s.db,
		// MariaDB / MySQL < 9
		`INSERT INTO user_traffic_histories
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT
			?, SUM(traffic_in), SUM(traffic_out), SUM(total_used), COUNT(DISTINCT instance_id),
			?, ?, ?, ?, ?, ?, ?
		FROM instance_traffic_histories
		WHERE user_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
		ON DUPLICATE KEY UPDATE
			traffic_in     = VALUES(traffic_in),
			traffic_out    = VALUES(traffic_out),
			total_used     = VALUES(total_used),
			instance_count = VALUES(instance_count),
			record_time    = VALUES(record_time),
			updated_at     = VALUES(updated_at)`,
		// MySQL 9.0+
		`INSERT INTO user_traffic_histories
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at
		FROM (
			SELECT
				? AS user_id, SUM(traffic_in) AS traffic_in, SUM(traffic_out) AS traffic_out,
				SUM(total_used) AS total_used, COUNT(DISTINCT instance_id) AS instance_count,
				? AS year, ? AS month, ? AS day, ? AS hour, ? AS record_time, ? AS created_at, ? AS updated_at
			FROM instance_traffic_histories
			WHERE user_id = ? AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
		) AS _src
		ON DUPLICATE KEY UPDATE
			traffic_in     = _src.traffic_in,
			traffic_out    = _src.traffic_out,
			total_used     = _src.total_used,
			instance_count = _src.instance_count,
			record_time    = _src.record_time,
			updated_at     = _src.updated_at`,
		userID, year, month, day, hour, now, now, now,
		userID, year, month, day, hour).Error

	if err != nil {
		return err
	}

	// Monthly aggregation (day=0, hour=0)
	return dbcompat.Exec(s.db,
		// MariaDB / MySQL < 9
		`INSERT INTO user_traffic_histories
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT
			?, SUM(traffic_in), SUM(traffic_out), SUM(total_used), COUNT(DISTINCT instance_id),
			?, ?, 0, 0, ?, ?, ?
		FROM instance_traffic_histories
		WHERE user_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
		ON DUPLICATE KEY UPDATE
			traffic_in     = VALUES(traffic_in),
			traffic_out    = VALUES(traffic_out),
			total_used     = VALUES(total_used),
			instance_count = VALUES(instance_count),
			record_time    = VALUES(record_time),
			updated_at     = VALUES(updated_at)`,
		// MySQL 9.0+
		`INSERT INTO user_traffic_histories
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
		SELECT user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at
		FROM (
			SELECT
				? AS user_id, SUM(traffic_in) AS traffic_in, SUM(traffic_out) AS traffic_out,
				SUM(total_used) AS total_used, COUNT(DISTINCT instance_id) AS instance_count,
				? AS year, ? AS month, 0 AS day, 0 AS hour, ? AS record_time, ? AS created_at, ? AS updated_at
			FROM instance_traffic_histories
			WHERE user_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
		) AS _src
		ON DUPLICATE KEY UPDATE
			traffic_in     = _src.traffic_in,
			traffic_out    = _src.traffic_out,
			total_used     = _src.total_used,
			instance_count = _src.instance_count,
			record_time    = _src.record_time,
			updated_at     = _src.updated_at`,
		userID, year, month, now, now, now,
		userID, year, month).Error
}

func (s *SyncService) aggregateUsersTraffic(
	userIDs []uint,
	year, month, day, hour int,
	now time.Time,
) error {
	const batchSize = 200
	for start := 0; start < len(userIDs); start += batchSize {
		end := start + batchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}
		batch := userIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")

		hourlyValuesSQL := fmt.Sprintf(`INSERT INTO user_traffic_histories
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
			SELECT user_id, SUM(traffic_in), SUM(traffic_out), SUM(total_used), COUNT(DISTINCT instance_id),
				year, month, day, hour, ?, ?, ?
			FROM instance_traffic_histories
			WHERE user_id IN (%s) AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
			GROUP BY user_id, year, month, day, hour
			ON DUPLICATE KEY UPDATE
				traffic_in = VALUES(traffic_in), traffic_out = VALUES(traffic_out),
				total_used = VALUES(total_used), instance_count = VALUES(instance_count),
				record_time = VALUES(record_time), updated_at = VALUES(updated_at)`, placeholders)
		hourlyAliasSQL := fmt.Sprintf(`INSERT INTO user_traffic_histories
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
			SELECT user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at
			FROM (
				SELECT user_id, SUM(traffic_in) AS traffic_in, SUM(traffic_out) AS traffic_out,
					SUM(total_used) AS total_used, COUNT(DISTINCT instance_id) AS instance_count,
					year, month, day, hour, ? AS record_time, ? AS created_at, ? AS updated_at
				FROM instance_traffic_histories
				WHERE user_id IN (%s) AND year = ? AND month = ? AND day = ? AND hour = ? AND deleted_at IS NULL
				GROUP BY user_id, year, month, day, hour
			) AS _src
			ON DUPLICATE KEY UPDATE
				traffic_in = _src.traffic_in, traffic_out = _src.traffic_out,
				total_used = _src.total_used, instance_count = _src.instance_count,
				record_time = _src.record_time, updated_at = _src.updated_at`, placeholders)
		hourlyArgs := make([]interface{}, 0, len(batch)+7)
		hourlyArgs = append(hourlyArgs, now, now, now)
		for _, userID := range batch {
			hourlyArgs = append(hourlyArgs, userID)
		}
		hourlyArgs = append(hourlyArgs, year, month, day, hour)
		if err := dbcompat.Exec(s.db, hourlyValuesSQL, hourlyAliasSQL, hourlyArgs...).Error; err != nil {
			return err
		}

		monthlyValuesSQL := fmt.Sprintf(`INSERT INTO user_traffic_histories
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
			SELECT user_id, SUM(traffic_in), SUM(traffic_out), SUM(total_used), COUNT(DISTINCT instance_id),
				year, month, 0, 0, ?, ?, ?
			FROM instance_traffic_histories
			WHERE user_id IN (%s) AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
			GROUP BY user_id, year, month
			ON DUPLICATE KEY UPDATE
				traffic_in = VALUES(traffic_in), traffic_out = VALUES(traffic_out),
				total_used = VALUES(total_used), instance_count = VALUES(instance_count),
				record_time = VALUES(record_time), updated_at = VALUES(updated_at)`, placeholders)
		monthlyAliasSQL := fmt.Sprintf(`INSERT INTO user_traffic_histories
			(user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at)
			SELECT user_id, traffic_in, traffic_out, total_used, instance_count, year, month, day, hour, record_time, created_at, updated_at
			FROM (
				SELECT user_id, SUM(traffic_in) AS traffic_in, SUM(traffic_out) AS traffic_out,
					SUM(total_used) AS total_used, COUNT(DISTINCT instance_id) AS instance_count,
					year, month, 0 AS day, 0 AS hour, ? AS record_time, ? AS created_at, ? AS updated_at
				FROM instance_traffic_histories
				WHERE user_id IN (%s) AND year = ? AND month = ? AND day = 0 AND hour = 0 AND deleted_at IS NULL
				GROUP BY user_id, year, month
			) AS _src
			ON DUPLICATE KEY UPDATE
				traffic_in = _src.traffic_in, traffic_out = _src.traffic_out,
				total_used = _src.total_used, instance_count = _src.instance_count,
				record_time = _src.record_time, updated_at = _src.updated_at`, placeholders)
		monthlyArgs := make([]interface{}, 0, len(batch)+5)
		monthlyArgs = append(monthlyArgs, now, now, now)
		for _, userID := range batch {
			monthlyArgs = append(monthlyArgs, userID)
		}
		monthlyArgs = append(monthlyArgs, year, month)
		if err := dbcompat.Exec(s.db, monthlyValuesSQL, monthlyAliasSQL, monthlyArgs...).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetMonitoringConfig gets or creates the monitoring config for a provider.
func GetMonitoringConfig(db *gorm.DB, providerID uint) (*monitoringModel.MonitoringConfig, error) {
	var config monitoringModel.MonitoringConfig
	err := db.Where("provider_id = ?", providerID).First(&config).Error
	if err == gorm.ErrRecordNotFound {
		var provider providerModel.Provider
		agentInstalled := false
		if providerErr := db.Select("connection_type").Where("id = ?", providerID).First(&provider).Error; providerErr == nil {
			agentInstalled = provider.ConnectionType == "agent"
		}
		config = monitoringModel.MonitoringConfig{
			ProviderID:              providerID,
			MonitoringMode:          "agent",
			AgentToken:              GenerateAgentToken(),
			AgentPort:               AgentPort,
			AgentInstalled:          agentInstalled,
			CollectInterval:         5,
			ResourceCollectInterval: 30,
		}
		if err := db.Create(&config).Error; err != nil {
			return nil, err
		}
		return &config, nil
	}
	if err == nil {
		var provider providerModel.Provider
		if providerErr := db.Select("connection_type").Where("id = ?", providerID).First(&provider).Error; providerErr == nil && provider.ConnectionType == "agent" {
			if !config.AgentInstalled || config.MonitoringMode != "agent" {
				config.AgentInstalled = true
				config.MonitoringMode = "agent"
				if saveErr := db.Model(&config).Updates(map[string]interface{}{
					"agent_installed": true,
					"monitoring_mode": "agent",
				}).Error; saveErr != nil {
					return nil, saveErr
				}
			}
		}
	}
	return &config, err
}

// GenerateAgentToken creates a cryptographically random token for agent authentication.
func GenerateAgentToken() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback: should never happen
		panic("crypto/rand failed: " + err.Error())
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}
