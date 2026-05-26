package agent

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils/dbcompat"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type trafficUserIDBackfill struct {
	monitorID  uint
	instanceID uint
	oldUserID  uint
	newUserID  uint
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

	// Get all active monitors for this provider
	var monitors []monitoringModel.AgentMonitor
	if err := s.db.Where("provider_id = ? AND is_enabled = ?", providerID, true).Find(&monitors).Error; err != nil {
		return fmt.Errorf("list monitors: %w", err)
	}
	if len(monitors) == 0 {
		return nil
	}

	// Collect agent monitor IDs
	agentIDs := make([]int64, 0, len(monitors))
	monitorByAgentID := make(map[int64]*monitoringModel.AgentMonitor)
	for i := range monitors {
		agentIDs = append(agentIDs, monitors[i].AgentMonitorID)
		monitorByAgentID[monitors[i].AgentMonitorID] = &monitors[i]
	}

	// Get agent client
	// Agent runs on the Endpoint host — PortIP is the external NAT IP used for port mapping,
	// NOT the machine where the agent is installed.
	// For agent-mode providers behind NAT, the HTTP API is not directly reachable;
	// the WS fallback in Client.doRequest handles connectivity via WebSocket.
	host := ResolveAgentHost(p.Endpoint, p.AgentRemoteIP)
	if host == "" {
		if p.ConnectionType == "agent" {
			host = "127.0.0.1" // placeholder; actual calls go through WS fallback
		} else {
			return fmt.Errorf("provider %d has no endpoint", providerID)
		}
	}
	port := config.AgentPort
	if port == 0 {
		port = AgentPort
	}
	client := GetClient(providerID, host, port, config.AgentToken)

	// Batch fetch traffic info
	infoMap, err := client.BatchGetInfo(agentIDs)
	if err != nil {
		return fmt.Errorf("batch get info: %w", err)
	}

	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	day := now.Day()
	hour := now.Hour()

	multiplier := p.TrafficMultiplier
	if multiplier <= 0 {
		multiplier = 1.0
	}

	// Process each monitor within a single transaction to reduce commit overhead
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		// Batch-load current UserID for all monitored instances so we use the
		// authoritative value rather than the potentially stale monitor.UserID.
		instanceIDs := make([]uint, 0, len(monitors))
		for i := range monitors {
			instanceIDs = append(instanceIDs, monitors[i].InstanceID)
		}
		type idPair struct {
			ID     uint
			UserID uint
		}
		var pairs []idPair
		if err := tx.Model(&providerModel.Instance{}).Select("id, user_id").Where("id IN ?", instanceIDs).Find(&pairs).Error; err != nil {
			return fmt.Errorf("batch load instance user_ids: %w", err)
		}
		instanceUserMap := make(map[uint]uint, len(pairs))
		for _, p := range pairs {
			instanceUserMap[p.ID] = p.UserID
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
		if err := applyTrafficUserIDBackfills(tx, backfills); err != nil {
			return err
		}

		txSync := &SyncService{db: tx, ctx: s.ctx}
		for agentID, info := range infoMap {
			monitor := monitorByAgentID[agentID]
			if monitor == nil {
				continue
			}

			currentTraffic := info.UsedTraffic
			currentTrafficIn := info.UsedTrafficIn
			currentTrafficOut := info.UsedTrafficOut

			// Compute delta since last sync for in/out separately
			var deltaBytesIn, deltaBytesOut uint64
			if currentTrafficIn >= monitor.LastTrafficBytesIn {
				deltaBytesIn = currentTrafficIn - monitor.LastTrafficBytesIn
			} else {
				deltaBytesIn = currentTrafficIn
				global.APP_LOG.Warn("Agent入站流量计数器重置检测",
					zap.Uint("instanceID", monitor.InstanceID),
					zap.Uint64("lastIn", monitor.LastTrafficBytesIn),
					zap.Uint64("currentIn", currentTrafficIn))
			}
			if currentTrafficOut >= monitor.LastTrafficBytesOut {
				deltaBytesOut = currentTrafficOut - monitor.LastTrafficBytesOut
			} else {
				deltaBytesOut = currentTrafficOut
				global.APP_LOG.Warn("Agent出站流量计数器重置检测",
					zap.Uint("instanceID", monitor.InstanceID),
					zap.Uint64("lastOut", monitor.LastTrafficBytesOut),
					zap.Uint64("currentOut", currentTrafficOut))
			}

			deltaBytes := deltaBytesIn + deltaBytesOut
			if deltaBytes == 0 {
				// Still update sync time
				if err := tx.Model(monitor).Updates(map[string]interface{}{
					"last_sync_at": now,
				}).Error; err != nil {
					return fmt.Errorf("update sync time: %w", err)
				}
				continue
			}

			// Convert bytes to MB using real rx/tx from agent
			rxMB := float64(deltaBytesIn) * multiplier / 1048576.0
			txMB := float64(deltaBytesOut) * multiplier / 1048576.0

			// Update instance traffic history (hourly)
			if err := txSync.upsertInstanceTrafficHistory(
				monitor.InstanceID, monitor.ProviderID, monitor.UserID,
				rxMB, txMB, year, month, day, hour, now,
			); err != nil {
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("upsert instance traffic history failed",
						zap.Uint("instance_id", monitor.InstanceID),
						zap.Error(err))
				}
			}

			// Update instance monthly total (day=0, hour=0)
			if err := txSync.upsertInstanceMonthlyTraffic(
				monitor.InstanceID, monitor.ProviderID, monitor.UserID,
				year, month, now,
			); err != nil {
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("upsert instance monthly traffic failed",
						zap.Uint("instance_id", monitor.InstanceID),
						zap.Error(err))
				}
			}

			// Update agent monitor tracking
			tx.Model(monitor).Updates(map[string]interface{}{
				"last_traffic_bytes":     currentTraffic,
				"last_traffic_bytes_in":  currentTrafficIn,
				"last_traffic_bytes_out": currentTrafficOut,
				"last_sync_at":           now,
			})

			// Also write cumulative values to pmacct_traffic_records for unified chart query support.
			// Agent cumulative counters are monotonically increasing (no reset on rebuild),
			// so they are naturally compatible with the segment detection logic used by chart queries.
			minute := (now.Minute() / 5) * 5
			alignedTime := time.Date(year, time.Month(month), day, hour, minute, 0, 0, now.Location())
			tx.Exec(`
				INSERT INTO pmacct_traffic_records
					(instance_id, user_id, provider_id, provider_type, mapped_ip,
					 rx_bytes, tx_bytes, total_bytes, timestamp, year, month, day, hour, minute,
					 record_time, created_at, updated_at)
				VALUES (?, ?, ?, 'agent', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON DUPLICATE KEY UPDATE
					rx_bytes = ?, tx_bytes = ?, total_bytes = ?,
					record_time = ?, updated_at = ?`,
				monitor.InstanceID, monitor.UserID, monitor.ProviderID,
				monitor.Interfaces,
				int64(currentTrafficIn), int64(currentTrafficOut), int64(currentTraffic),
				alignedTime, year, month, day, hour, minute, now, now, now,
				int64(currentTrafficIn), int64(currentTrafficOut), int64(currentTraffic),
				now, now)
		}
		return nil
	})
	if txErr != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Error("traffic sync transaction failed",
				zap.Uint("provider_id", providerID),
				zap.Error(txErr))
		}
	}

	// Aggregate provider and user traffic
	if err := s.aggregateProviderTraffic(providerID, year, month, day, hour, now); err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("aggregate provider traffic failed",
				zap.Uint("provider_id", providerID),
				zap.Error(err))
		}
	}

	// Aggregate user traffic for all affected users
	userIDs := make(map[uint]bool)
	for _, m := range monitors {
		userIDs[m.UserID] = true
	}
	for userID := range userIDs {
		if err := s.aggregateUserTraffic(userID, year, month, day, hour, now); err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("aggregate user traffic failed",
					zap.Uint("user_id", userID),
					zap.Error(err))
			}
		}
	}

	return nil
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

// upsertInstanceTrafficHistory upserts instance traffic for the current hour.
// Uses incremental addition (traffic_in = traffic_in + delta).
func (s *SyncService) upsertInstanceTrafficHistory(
	instanceID, providerID, userID uint,
	rxMB, txMB float64,
	year, month, day, hour int,
	now time.Time,
) error {
	totalMB := rxMB + txMB

	return s.db.Exec(`
		INSERT INTO instance_traffic_histories
			(instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			traffic_in   = traffic_in + ?,
			traffic_out  = traffic_out + ?,
			total_used   = total_used + ?,
			provider_id  = ?,
			user_id      = ?,
			record_time  = ?,
			updated_at   = ?
	`, instanceID, providerID, userID,
		rxMB, txMB, totalMB,
		year, month, day, hour,
		now, now, now,
		rxMB, txMB, totalMB,
		providerID, userID, now, now).Error
}

// upsertInstanceMonthlyTraffic aggregates hourly data into the monthly record (day=0, hour=0).
func (s *SyncService) upsertInstanceMonthlyTraffic(
	instanceID, providerID, userID uint,
	year, month int,
	now time.Time,
) error {
	return dbcompat.Exec(s.db,
		// MariaDB / MySQL < 9
		`INSERT INTO instance_traffic_histories
			(instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at)
		SELECT
			instance_id, MAX(provider_id), MAX(user_id),
			SUM(traffic_in), SUM(traffic_out), SUM(total_used),
			year, month, 0, 0, ?, ?, ?
		FROM instance_traffic_histories
		WHERE instance_id = ? AND year = ? AND month = ? AND day > 0 AND deleted_at IS NULL
		GROUP BY instance_id, year, month
		ON DUPLICATE KEY UPDATE
			traffic_in  = VALUES(traffic_in),
			traffic_out = VALUES(traffic_out),
			total_used  = VALUES(total_used),
			provider_id = VALUES(provider_id),
			user_id     = VALUES(user_id),
			record_time = VALUES(record_time),
			updated_at  = VALUES(updated_at)`,
		// MySQL 9.0+
		`INSERT INTO instance_traffic_histories
			(instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at)
		SELECT instance_id, provider_id, user_id, traffic_in, traffic_out, total_used, year, month, day, hour, record_time, created_at, updated_at
		FROM (
			SELECT
				instance_id, MAX(provider_id) AS provider_id, MAX(user_id) AS user_id,
				SUM(traffic_in) AS traffic_in, SUM(traffic_out) AS traffic_out, SUM(total_used) AS total_used,
				year, month, 0 AS day, 0 AS hour, ? AS record_time, ? AS created_at, ? AS updated_at
			FROM instance_traffic_histories
			WHERE instance_id = ? AND year = ? AND month = ? AND day > 0 AND deleted_at IS NULL
			GROUP BY instance_id, year, month
		) AS _src
		ON DUPLICATE KEY UPDATE
			traffic_in  = _src.traffic_in,
			traffic_out = _src.traffic_out,
			total_used  = _src.total_used,
			provider_id = _src.provider_id,
			user_id     = _src.user_id,
			record_time = _src.record_time,
			updated_at  = _src.updated_at`,
		now, now, now, instanceID, year, month).Error
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
