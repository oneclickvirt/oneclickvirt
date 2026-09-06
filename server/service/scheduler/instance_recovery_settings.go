package scheduler

import (
	"time"

	"oneclickvirt/global"
)

const (
	defaultInstanceRecoveryInterval        = 3 * time.Minute
	defaultInstanceRecoveryOfflineWindow   = 30 * time.Minute
	defaultAutoFrozenProbeCooldown         = 30 * time.Minute
	instanceRecoveryStartupGrace           = 90 * time.Second
	instanceRecoveryProviderBatchSize      = 20
	instanceRecoveryMaxProviderConcurrency = 2
	instanceRecoveryRemoteProbeTimeout     = 2 * time.Minute
	instanceRecoveryLeasePadding           = 30 * time.Second
	// Manual recovery is intentionally available without waiting for the
	// half-hour outage window, but repeated clicks must not turn into a remote
	// probe loop. The lease still protects concurrent workers cross-controller.
	instanceRecoveryManualCooldown = time.Minute
	// The remote discovery itself is capped at two minutes. The task also has
	// to wait for an Agent reconnect and finish short queue/repair bookkeeping.
	instanceRecoveryManualTaskTimeout = 5 * time.Minute
	// Keep every recovery transaction well under common SQLite/MySQL bind and
	// lock budgets. A large node is reconciled through several short batches;
	// discovery itself remains exactly once per Provider recovery attempt.
	instanceRecoveryInstanceBatchSize   = 50
	instanceRecoveryTaskInsertBatchSize = 25
)

type instanceRecoverySettings struct {
	Enabled          bool
	Interval         time.Duration
	OfflineThreshold time.Duration
	RetryCooldown    time.Duration
	// AutoFrozenProbeCooldown is intentionally much longer than the normal
	// recovery retry.  Health-auto-frozen SSH/API nodes are no longer covered
	// by the regular health sweep, so this is their bounded liveness probe.
	// It must never turn a permanently unreachable node into a 2-minute poll.
	AutoFrozenProbeCooldown time.Duration
}

func getInstanceRecoverySettings() instanceRecoverySettings {
	cfg := global.GetAppConfig().System
	interval := time.Duration(cfg.InstanceRecoveryInterval) * time.Minute
	if interval <= 0 {
		interval = defaultInstanceRecoveryInterval
	}
	offlineThreshold := time.Duration(cfg.InstanceRecoveryOfflineMinutes) * time.Minute
	if offlineThreshold <= 0 {
		offlineThreshold = defaultInstanceRecoveryOfflineWindow
	}
	// The claim timestamp is a cross-controller recovery lease. It must outlive
	// the bounded remote discovery itself, otherwise a short configured interval
	// could let two controllers probe the same node concurrently.
	retryCooldown := interval * 2 / 3
	minimumRecoveryLease := instanceRecoveryRemoteProbeTimeout + instanceRecoveryLeasePadding
	if retryCooldown < minimumRecoveryLease {
		retryCooldown = minimumRecoveryLease
	}
	autoFrozenProbeCooldown := offlineThreshold
	if autoFrozenProbeCooldown < defaultAutoFrozenProbeCooldown {
		autoFrozenProbeCooldown = defaultAutoFrozenProbeCooldown
	}
	return instanceRecoverySettings{
		Enabled:                 cfg.EnableInstanceRecovery,
		Interval:                interval,
		OfflineThreshold:        offlineThreshold,
		RetryCooldown:           retryCooldown,
		AutoFrozenProbeCooldown: autoFrozenProbeCooldown,
	}
}
