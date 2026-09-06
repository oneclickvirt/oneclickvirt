package provider

import (
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
)

const defaultRecoveryOfflineWindow = 30 * time.Minute

// RecoveryOfflineWindow returns the same configured outage threshold used by
// the recovery scheduler. It lives here so Agent connection edges and the
// health scheduler can persist one shared recovery window without importing
// the scheduler package.
func RecoveryOfflineWindow() time.Duration {
	window := time.Duration(global.GetAppConfig().System.InstanceRecoveryOfflineMinutes) * time.Minute
	if window <= 0 {
		return defaultRecoveryOfflineWindow
	}
	return window
}

func instanceRecoveryEnabled() bool {
	return global.GetAppConfig().System.EnableInstanceRecovery
}

// RecordProviderRecoveryOffline records the first confirmed outage edge. It
// is intentionally a single conditional write: repeated heartbeat failures or
// duplicate Agent disconnect notifications cannot extend the recovery window.
// It performs no remote I/O and never opens a transaction.
func RecordProviderRecoveryOffline(providerID uint, observedAt time.Time) {
	recordProviderRecoveryOffline(providerID, observedAt, "")
}

// RecordAgentProviderRecoveryOffline is the reverse-Agent counterpart of
// RecordProviderRecoveryOffline. It refuses to write after a Provider has
// been switched away from Agent mode, so a stale WebSocket cleanup cannot
// manufacture an outage window for a newly reconfigured SSH/API node.
func RecordAgentProviderRecoveryOffline(providerID uint, observedAt time.Time) {
	recordProviderRecoveryOffline(providerID, observedAt, "agent")
}

func recordProviderRecoveryOffline(providerID uint, observedAt time.Time, connectionType string) {
	if !instanceRecoveryEnabled() || global.APP_DB == nil || providerID == 0 {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}

	query := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ? AND recovery_offline_since IS NULL", providerID).
		Where("recovery_lease_expires_at IS NULL OR recovery_lease_expires_at <= ?", observedAt)
	if connectionType != "" {
		query = query.Where("LOWER(connection_type) = ?", connectionType)
		if connectionType == "agent" {
			query = query.Where("LOWER(COALESCE(execution_rule, '')) <> ?", "api_only")
		}
	}
	result := query.
		Updates(map[string]interface{}{
			"recovery_offline_since":            observedAt,
			"recovery_last_recovery_attempt_at": nil,
		})
	if result.Error != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("记录Provider离线恢复窗口失败",
				zap.Uint("provider_id", providerID), zap.Error(result.Error))
		}
		return
	}
	if result.RowsAffected > 0 && global.APP_LOG != nil {
		global.APP_LOG.Info("Provider进入实例恢复观察窗口",
			zap.Uint("provider_id", providerID), zap.Duration("offline_threshold", RecoveryOfflineWindow()))
	}
}

// ClearShortProviderRecoveryWindow clears an outage marker only when the node
// returned before the configured recovery threshold. A long outage marker is
// deliberately retained after a reconnect so the scheduler performs exactly
// one authoritative discovery and recovery pass.
func ClearShortProviderRecoveryWindow(providerID uint, observedAt time.Time) {
	clearShortProviderRecoveryWindow(providerID, observedAt, "")
}

// ClearShortAgentProviderRecoveryWindow applies the same short-flap policy as
// ClearShortProviderRecoveryWindow while guarding against stale Agent events
// after the node's connection mode changes.
func ClearShortAgentProviderRecoveryWindow(providerID uint, observedAt time.Time) {
	clearShortProviderRecoveryWindow(providerID, observedAt, "agent")
}

func clearShortProviderRecoveryWindow(providerID uint, observedAt time.Time, connectionType string) {
	if !instanceRecoveryEnabled() || global.APP_DB == nil || providerID == 0 {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	cutoff := observedAt.Add(-RecoveryOfflineWindow())
	query := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ? AND recovery_offline_since IS NOT NULL AND recovery_offline_since > ?", providerID, cutoff)
	if connectionType != "" {
		query = query.Where("LOWER(connection_type) = ?", connectionType)
		if connectionType == "agent" {
			query = query.Where("LOWER(COALESCE(execution_rule, '')) <> ?", "api_only")
		}
	}
	result := query.
		Update("recovery_offline_since", nil)
	if result.Error != nil && global.APP_LOG != nil {
		global.APP_LOG.Warn("清除短暂Provider离线恢复窗口失败",
			zap.Uint("provider_id", providerID), zap.Error(result.Error))
	}
}
