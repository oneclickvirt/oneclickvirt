package agent

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils/dbcompat"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

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
	host := p.Endpoint
	if host == "" {
		return fmt.Errorf("provider %d has no endpoint", providerID)
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

	countMode := p.TrafficCountMode
	if countMode == "" {
		countMode = "both"
	}
	multiplier := p.TrafficMultiplier
	if multiplier <= 0 {
		multiplier = 1.0
	}

	// Process each monitor within a single transaction to reduce commit overhead
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		txSync := &SyncService{db: tx, ctx: s.ctx}
		for agentID, info := range infoMap {
			monitor := monitorByAgentID[agentID]
			if monitor == nil {
				continue
			}

			currentTraffic := info.UsedTraffic

			// Compute delta since last sync
			var deltaBytes uint64
			if currentTraffic >= monitor.LastTrafficBytes {
				deltaBytes = currentTraffic - monitor.LastTrafficBytes
			} else {
				// Agent may have been reset or data loss, use current as delta
				deltaBytes = currentTraffic
			}

			if deltaBytes == 0 {
				// Still update sync time
				tx.Model(monitor).Updates(map[string]interface{}{
					"last_sync_at": now,
				})
				continue
			}

			// Convert bytes to MB
			// Since agent reports total (rx+tx combined), we split evenly for now
			// TODO: modify agent to report rx/tx separately for proper count mode support
			totalMB := float64(deltaBytes) * multiplier / 1048576.0
			var rxMB, txMB float64
			switch countMode {
			case "out":
				txMB = totalMB
			case "in":
				rxMB = totalMB
			default: // "both"
				rxMB = totalMB / 2
				txMB = totalMB / 2
			}

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
				"last_traffic_bytes": currentTraffic,
				"last_sync_at":       now,
			})
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
		config = monitoringModel.MonitoringConfig{
			ProviderID:              providerID,
			MonitoringMode:          "agent",
			AgentPort:               AgentPort,
			CollectInterval:         5,
			ResourceCollectInterval: 30,
		}
		if err := db.Create(&config).Error; err != nil {
			return nil, err
		}
		return &config, nil
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
