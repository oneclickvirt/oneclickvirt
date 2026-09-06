package scheduler

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"oneclickvirt/config"
	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	adminProviderService "oneclickvirt/service/admin/provider"
	providerRuntimeService "oneclickvirt/service/provider"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newInstanceRecoveryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE providers (
		id integer primary key autoincrement,
		name text, type text, status text, is_frozen boolean, frozen_reason text,
		frozen_at datetime, allow_claim boolean, expires_at datetime,
		connection_type text, agent_status text, execution_rule text,
		recovery_offline_since datetime, recovery_last_recovery_attempt_at datetime,
		recovery_lease_token text, recovery_lease_expires_at datetime,
		created_at datetime, updated_at datetime,
		deleted_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE tasks (
		id integer primary key autoincrement, uuid text, created_at datetime, updated_at datetime, deleted_at datetime,
		task_type text, status text, progress integer, error_message text, cancel_reason text, status_message text, task_data text,
		started_at datetime, completed_at datetime, estimated_duration integer, timeout_duration integer,
		preallocated_cpu integer, preallocated_memory integer, preallocated_disk integer, preallocated_bandwidth integer,
		user_id integer, provider_id integer, instance_id integer, can_force_stop boolean, is_force_stoppable boolean, progress_logs text
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE system_configs (
		id integer primary key autoincrement, key text, value text, description text, category text, type text,
		is_public boolean, created_at datetime, updated_at datetime, deleted_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}
	previousDB := global.APP_DB
	previousLog := global.APP_LOG
	global.APP_DB = db
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB = previousDB
		global.APP_LOG = previousLog
	})
	return db
}

func createRecoveryTestProvider(t *testing.T, db *gorm.DB, provider providerModel.Provider) providerModel.Provider {
	t.Helper()
	if err := db.Exec(`INSERT INTO providers
		(name, type, status, is_frozen, frozen_reason, frozen_at, allow_claim, expires_at, connection_type, agent_status, execution_rule, recovery_offline_since, recovery_last_recovery_attempt_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		provider.Name, provider.Type, provider.Status, provider.IsFrozen, provider.FrozenReason, provider.FrozenAt,
		provider.AllowClaim, provider.ExpiresAt, provider.ConnectionType, provider.AgentStatus, provider.ExecutionRule, provider.RecoveryOfflineSince, provider.RecoveryLastRecoveryAttemptAt).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw("SELECT id FROM providers WHERE name = ? ORDER BY id DESC LIMIT 1", provider.Name).Scan(&provider.ID).Error; err != nil {
		t.Fatal(err)
	}
	return provider
}

func enableInstanceRecoveryForTest(t *testing.T) {
	t.Helper()
	previousConfig := global.GetAppConfig()
	configured := previousConfig
	configured.System = config.System{
		EnableInstanceRecovery:         true,
		InstanceRecoveryInterval:       3,
		InstanceRecoveryOfflineMinutes: 30,
	}
	global.SetAppConfig(configured)
	t.Cleanup(func() {
		global.SetAppConfig(previousConfig)
	})
}

func TestAgentRecoveryWindowKeepsLongOutageAndClearsShortFlap(t *testing.T) {
	db := newInstanceRecoveryTestDB(t)
	enableInstanceRecoveryForTest(t)
	provider := createRecoveryTestProvider(t, db, providerModel.Provider{
		Name: "agent-recovery-window", Type: "docker", Status: "partial",
		ConnectionType: "agent", AgentStatus: "offline",
	})
	now := time.Now()
	longOfflineSince := now.Add(-31 * time.Minute)
	providerRuntimeService.RecordProviderRecoveryOffline(provider.ID, longOfflineSince)
	providerRuntimeService.RecordProviderRecoveryOffline(provider.ID, now)

	var stored providerModel.Provider
	if err := db.Select("recovery_offline_since").First(&stored, provider.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.RecoveryOfflineSince == nil || stored.RecoveryOfflineSince.After(longOfflineSince.Add(time.Second)) {
		t.Fatalf("offline marker = %v, want original first edge near %v", stored.RecoveryOfflineSince, longOfflineSince)
	}

	// A reconnect after the threshold must retain the marker for exactly one
	// remote reconciliation pass.
	providerRuntimeService.ClearShortProviderRecoveryWindow(provider.ID, now)
	stored = providerModel.Provider{}
	if err := db.Select("recovery_offline_since").First(&stored, provider.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.RecoveryOfflineSince == nil {
		t.Fatal("long Agent outage marker was cleared before recovery")
	}

	shortOfflineSince := now.Add(-2 * time.Minute)
	if err := db.Model(&providerModel.Provider{}).Where("id = ?", provider.ID).
		Update("recovery_offline_since", shortOfflineSince).Error; err != nil {
		t.Fatal(err)
	}
	providerRuntimeService.ClearShortProviderRecoveryWindow(provider.ID, now)
	stored = providerModel.Provider{}
	if err := db.Select("recovery_offline_since").First(&stored, provider.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.RecoveryOfflineSince != nil {
		t.Fatalf("short Agent outage marker = %v, want nil", stored.RecoveryOfflineSince)
	}
}

func TestAgentHealthSnapshotDoesNotTouchRecoveryWindowAfterModeChange(t *testing.T) {
	db := newInstanceRecoveryTestDB(t)
	enableInstanceRecoveryForTest(t)
	provider := createRecoveryTestProvider(t, db, providerModel.Provider{
		Name: "agent-mode-change-window", Type: "docker", Status: "inactive",
		ConnectionType: "agent", AgentStatus: "offline",
	})

	// Simulate a health worker that read an Agent snapshot just before an
	// administrator moved the node to SSH/API. Its late completion must not
	// manufacture a reboot-recovery window for the new transport.
	if err := db.Model(&providerModel.Provider{}).Where("id = ?", provider.ID).
		Update("connection_type", "ssh").Error; err != nil {
		t.Fatal(err)
	}
	health := NewProviderHealthSchedulerService()
	health.trackProviderRecoveryWindow(providerModel.Provider{
		ID: provider.ID, ConnectionType: "agent", AgentStatus: "offline", Status: "inactive",
	})

	var stored providerModel.Provider
	if err := db.Select("recovery_offline_since").First(&stored, provider.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.RecoveryOfflineSince != nil {
		t.Fatalf("stale Agent health snapshot wrote recovery marker %v after mode change", stored.RecoveryOfflineSince)
	}

	shortOfflineSince := time.Now().Add(-2 * time.Minute)
	if err := db.Model(&providerModel.Provider{}).Where("id = ?", provider.ID).
		Update("recovery_offline_since", shortOfflineSince).Error; err != nil {
		t.Fatal(err)
	}
	health.trackProviderRecoveryWindow(providerModel.Provider{
		ID: provider.ID, ConnectionType: "agent", AgentStatus: "online", Status: "active",
	})
	stored = providerModel.Provider{}
	if err := db.Select("recovery_offline_since").First(&stored, provider.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.RecoveryOfflineSince == nil {
		t.Fatal("stale Agent health snapshot cleared SSH recovery marker after mode change")
	}
}

func TestRecoveryScopeDefersOfflineAgentUntilReconnect(t *testing.T) {
	db := newInstanceRecoveryTestDB(t)
	now := time.Now()
	offlineSince := now.Add(-31 * time.Minute)
	providers := []providerModel.Provider{
		{Name: "ssh-recovery", Type: "docker", Status: "active", RecoveryOfflineSince: &offlineSince},
		{Name: "agent-offline", Type: "docker", Status: "active", ConnectionType: "agent", AgentStatus: "offline", RecoveryOfflineSince: &offlineSince},
		{Name: "agent-online", Type: "docker", Status: "active", ConnectionType: "agent", AgentStatus: "online", RecoveryOfflineSince: &offlineSince},
	}
	for i := range providers {
		providers[i] = createRecoveryTestProvider(t, db, providers[i])
	}
	settings := instanceRecoverySettings{
		Enabled:                 true,
		OfflineThreshold:        30 * time.Minute,
		RetryCooldown:           time.Minute,
		AutoFrozenProbeCooldown: 30 * time.Minute,
	}
	var candidates []providerModel.Provider
	if err := recoveryProviderCandidateScope(db.Model(&providerModel.Provider{}), now, settings).
		Order("id ASC").Find(&candidates).Error; err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Name != "ssh-recovery" || candidates[1].Name != "agent-online" {
		t.Fatalf("recovery candidates = %#v, want ssh-recovery and agent-online", candidates)
	}
	if allowed, err := recoveryProviderMayBeProbed(providers[1].ID); err != nil || allowed {
		t.Fatalf("offline Agent probe = (%v, %v), want (false, nil)", allowed, err)
	}
}

func TestAPIOnlyAgentUsesGenericRecoveryWindowWithoutReverseAgentGate(t *testing.T) {
	db := newInstanceRecoveryTestDB(t)
	enableInstanceRecoveryForTest(t)
	now := time.Now()
	provider := createRecoveryTestProvider(t, db, providerModel.Provider{
		Name: "api-only-agent-recovery", Type: "proxmox", Status: "inactive",
		ConnectionType: "agent", ExecutionRule: "api_only", AgentStatus: "offline",
	})
	if provider.IsReverseAgent() {
		t.Fatal("api_only provider must not be classified as a reverse Agent")
	}
	if isReverseAgentUnavailable(provider) {
		t.Fatal("api_only provider must not wait for an Agent WebSocket reconnect")
	}

	// API-only health is authoritative through its provider API. It must record
	// the same durable outage edge as SSH/API nodes even when AgentStatus is
	// offline, instead of routing through the reverse-Agent-only helper.
	health := NewProviderHealthSchedulerService()
	health.trackProviderRecoveryWindow(provider)
	var stored providerModel.Provider
	if err := db.Select("recovery_offline_since").First(&stored, provider.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.RecoveryOfflineSince == nil {
		t.Fatal("api_only Agent provider did not receive a generic recovery marker")
	}

	longOfflineSince := now.Add(-31 * time.Minute)
	if err := db.Model(&providerModel.Provider{}).Where("id = ?", provider.ID).
		Updates(map[string]interface{}{"recovery_offline_since": longOfflineSince, "status": "active"}).Error; err != nil {
		t.Fatal(err)
	}
	provider.Status = "active"
	health.trackProviderRecoveryWindow(provider)
	if err := db.Select("recovery_offline_since").First(&stored, provider.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.RecoveryOfflineSince == nil {
		t.Fatal("long API-only outage marker was cleared before recovery")
	}

	settings := instanceRecoverySettings{
		Enabled:                 true,
		OfflineThreshold:        30 * time.Minute,
		RetryCooldown:           time.Minute,
		AutoFrozenProbeCooldown: 30 * time.Minute,
	}
	var candidates []providerModel.Provider
	if err := recoveryProviderCandidateScope(db.Model(&providerModel.Provider{}), now, settings).
		Find(&candidates).Error; err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != provider.ID {
		t.Fatalf("api_only recovery candidates = %#v, want provider %d", candidates, provider.ID)
	}
	if allowed, err := recoveryProviderMayBeProbed(provider.ID); err != nil || !allowed {
		t.Fatalf("api_only recovery probe = (%v, %v), want (true, nil)", allowed, err)
	}
}

func TestRecoveryEligibilityRequiresExplicitRunningIntent(t *testing.T) {
	now := time.Now()
	expires := now.Add(-time.Minute)
	baseSnapshot := adminProviderService.MatchedInstanceSnapshot{
		InstanceID:     1,
		DatabaseStatus: constant.InstanceStatusRunning,
		DesiredState:   providerModel.InstanceDesiredStateRunning,
		RemoteStatus:   constant.InstanceStatusStopped,
	}
	baseInstance := providerModel.Instance{
		Status:       constant.InstanceStatusRunning,
		DesiredState: providerModel.InstanceDesiredStateRunning,
	}
	cases := []struct {
		name         string
		modify       func(*adminProviderService.MatchedInstanceSnapshot, *providerModel.Instance)
		wantSnapshot bool
		wantInstance bool
	}{
		{name: "expected running remote stopped", wantSnapshot: true, wantInstance: true},
		{name: "manual stop", modify: func(s *adminProviderService.MatchedInstanceSnapshot, i *providerModel.Instance) {
			s.DesiredState, i.DesiredState = providerModel.InstanceDesiredStateStopped, providerModel.InstanceDesiredStateStopped
		}},
		{name: "remote still running", modify: func(s *adminProviderService.MatchedInstanceSnapshot, _ *providerModel.Instance) {
			s.RemoteStatus = constant.InstanceStatusRunning
		}, wantInstance: true},
		{name: "frozen", modify: func(s *adminProviderService.MatchedInstanceSnapshot, i *providerModel.Instance) {
			s.IsFrozen, i.IsFrozen = true, true
		}},
		{name: "traffic stopped", modify: func(s *adminProviderService.MatchedInstanceSnapshot, i *providerModel.Instance) {
			s.TrafficStopped, i.TrafficStopped = true, true
		}},
		{name: "expired", modify: func(s *adminProviderService.MatchedInstanceSnapshot, i *providerModel.Instance) {
			s.ExpiresAt, i.ExpiresAt = &expires, &expires
		}},
		{name: "transitional database state", modify: func(s *adminProviderService.MatchedInstanceSnapshot, i *providerModel.Instance) {
			s.DatabaseStatus, i.Status = constant.InstanceStatusStarting, constant.InstanceStatusStarting
		}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			snapshot := baseSnapshot
			instance := baseInstance
			if test.modify != nil {
				test.modify(&snapshot, &instance)
			}
			if got := recoverySnapshotEligible(snapshot, now); got != test.wantSnapshot {
				t.Fatalf("recoverySnapshotEligible() = %v, want %v", got, test.wantSnapshot)
			}
			if got := recoveryInstanceEligible(instance, now); got != test.wantInstance {
				t.Fatalf("recoveryInstanceEligible() = %v, want %v", got, test.wantInstance)
			}
		})
	}
}

func TestRecoveryProviderClaimHonorsCooldownAndManualFreeze(t *testing.T) {
	db := newInstanceRecoveryTestDB(t)
	now := time.Now()
	offlineSince := now.Add(-31 * time.Minute)
	future := now.Add(time.Hour)
	providers := []providerModel.Provider{
		{Name: "eligible", Type: "docker", Status: "active", RecoveryOfflineSince: &offlineSince},
		{Name: "auto-frozen", Type: "docker", Status: "inactive", IsFrozen: true, FrozenReason: "健康检查连续失败 20 次", RecoveryOfflineSince: &offlineSince},
		{Name: "manual-frozen", Type: "docker", Status: "active", IsFrozen: true, FrozenReason: "manual", RecoveryOfflineSince: &offlineSince},
		{Name: "not-long-enough", Type: "docker", Status: "active", RecoveryOfflineSince: ptrTime(now.Add(-10 * time.Minute))},
		{Name: "expired", Type: "docker", Status: "active", RecoveryOfflineSince: &offlineSince, ExpiresAt: &future},
	}
	providers[4].ExpiresAt = ptrTime(now.Add(-time.Minute))
	for i := range providers {
		providers[i] = createRecoveryTestProvider(t, db, providers[i])
	}
	settings := instanceRecoverySettings{
		Enabled:                 true,
		OfflineThreshold:        30 * time.Minute,
		RetryCooldown:           time.Minute,
		AutoFrozenProbeCooldown: 30 * time.Minute,
	}
	var got []providerModel.Provider
	if err := recoveryProviderCandidateScope(db.Model(&providerModel.Provider{}), now, settings).Order("id ASC").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "eligible" || got[1].Name != "auto-frozen" {
		t.Fatalf("candidate scope = %#v, want eligible and auto-frozen", got)
	}
	claimed, err := claimProviderRecovery(providers[0].ID, now, settings)
	if err != nil || !claimed {
		t.Fatalf("first claim = (%v, %v), want (true, nil)", claimed, err)
	}
	claimed, err = claimProviderRecovery(providers[0].ID, now.Add(10*time.Second), settings)
	if err != nil || claimed {
		t.Fatalf("cooldown claim = (%v, %v), want (false, nil)", claimed, err)
	}
	if allowed, err := recoveryProviderMayBeProbed(providers[1].ID); err != nil || !allowed {
		t.Fatalf("auto-frozen probe = (%v, %v), want (true, nil)", allowed, err)
	}
	if allowed, err := recoveryProviderMayBeProbed(providers[2].ID); err != nil || allowed {
		t.Fatalf("manual-frozen probe = (%v, %v), want (false, nil)", allowed, err)
	}
}

func TestManualRecoveryClaimSharesTokenizedLeaseAndCooldown(t *testing.T) {
	db := newInstanceRecoveryTestDB(t)
	now := time.Now()
	offlineSince := now.Add(-31 * time.Minute)
	providers := []providerModel.Provider{
		{Name: "manual-active", Type: "docker", Status: "active"},
		{Name: "manual-inactive", Type: "docker", Status: "inactive"},
		{Name: "manual-frozen", Type: "docker", Status: "active", IsFrozen: true, FrozenReason: "operator freeze"},
		{Name: "scheduled-lease", Type: "docker", Status: "active", RecoveryOfflineSince: &offlineSince},
		{Name: "manual-expired", Type: "docker", Status: "active", ExpiresAt: ptrTime(now.Add(-time.Minute))},
	}
	for i := range providers {
		providers[i] = createRecoveryTestProvider(t, db, providers[i])
	}

	claim, claimed, err := claimManualProviderRecovery(providers[0].ID, now)
	if err != nil || !claimed || claim == nil || claim.Token == "" {
		t.Fatalf("manual claim = (%#v, %v, %v), want tokenized success", claim, claimed, err)
	}
	var leased providerModel.Provider
	if err := db.Select("recovery_lease_token", "recovery_lease_expires_at").First(&leased, providers[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if leased.RecoveryLeaseToken != claim.Token || leased.RecoveryLeaseExpiresAt == nil || !leased.RecoveryLeaseExpiresAt.After(now) {
		t.Fatalf("stored manual lease = %#v, want active tokenized lease", leased)
	}
	if _, second, err := claimManualProviderRecovery(providers[0].ID, now.Add(10*time.Second)); err != nil || second {
		t.Fatalf("overlapping manual claim = (%v, %v), want (false, nil)", second, err)
	}
	if _, frozen, err := claimManualProviderRecovery(providers[2].ID, now); err != nil || frozen {
		t.Fatalf("manual-frozen claim = (%v, %v), want (false, nil)", frozen, err)
	}
	if _, expired, err := claimManualProviderRecovery(providers[4].ID, now); err != nil || expired {
		t.Fatalf("manual-expired claim = (%v, %v), want (false, nil)", expired, err)
	}

	settings := instanceRecoverySettings{
		Enabled:                 true,
		OfflineThreshold:        30 * time.Minute,
		RetryCooldown:           time.Minute,
		AutoFrozenProbeCooldown: 30 * time.Minute,
	}
	scheduledClaim, scheduled, err := claimScheduledProviderRecovery(providers[3].ID, now, settings)
	if err != nil || !scheduled || scheduledClaim == nil {
		t.Fatalf("scheduled claim = (%#v, %v, %v), want success", scheduledClaim, scheduled, err)
	}
	if _, manual, err := claimManualProviderRecovery(providers[3].ID, now.Add(10*time.Second)); err != nil || manual {
		t.Fatalf("manual claim during scheduled lease = (%v, %v), want (false, nil)", manual, err)
	}

	// Releasing a completed worker's lease permits a later manual recovery only
	// after the explicit short cooldown; it never turns rapid clicking into a
	// remote-discovery loop.
	releaseProviderRecoveryLease(providers[0].ID, claim.Token)
	if _, early, err := claimManualProviderRecovery(providers[0].ID, now.Add(30*time.Second)); err != nil || early {
		t.Fatalf("manual cooldown claim = (%v, %v), want (false, nil)", early, err)
	}
	if _, later, err := claimManualProviderRecovery(providers[0].ID, now.Add(instanceRecoveryManualCooldown+time.Second)); err != nil || !later {
		t.Fatalf("manual claim after cooldown = (%v, %v), want (true, nil)", later, err)
	}

	// The inactive-but-not-frozen provider is intentionally eligible only for a
	// human-triggered repair; the periodic scope still requires an outage marker.
	if _, inactive, err := claimManualProviderRecovery(providers[1].ID, now); err != nil || !inactive {
		t.Fatalf("manual inactive claim = (%v, %v), want (true, nil)", inactive, err)
	}
}

func TestEnqueueRecoveredInstanceStartsFiltersUnsafeRows(t *testing.T) {
	db := newInstanceRecoveryTestDB(t)
	provider := createRecoveryTestProvider(t, db, providerModel.Provider{Name: "recovery-node", Type: "docker", Status: "active"})
	expiredAt := time.Now().Add(-time.Minute)
	instances := []providerModel.Instance{
		{Name: "eligible", Provider: provider.Name, ProviderID: provider.ID, Status: "running", DesiredState: providerModel.InstanceDesiredStateRunning},
		{Name: "manual-stop", Provider: provider.Name, ProviderID: provider.ID, Status: "running", DesiredState: providerModel.InstanceDesiredStateStopped},
		{Name: "imported", Provider: provider.Name, ProviderID: provider.ID, Status: "running", IsImported: true, DesiredState: providerModel.InstanceDesiredStateStopped},
		{Name: "frozen", Provider: provider.Name, ProviderID: provider.ID, Status: "running", DesiredState: providerModel.InstanceDesiredStateRunning, IsFrozen: true},
		{Name: "traffic", Provider: provider.Name, ProviderID: provider.ID, Status: "running", DesiredState: providerModel.InstanceDesiredStateRunning, TrafficStopped: true},
		{Name: "expired", Provider: provider.Name, ProviderID: provider.ID, Status: "running", DesiredState: providerModel.InstanceDesiredStateRunning, ExpiresAt: &expiredAt},
		{Name: "busy", Provider: provider.Name, ProviderID: provider.ID, Status: "running", DesiredState: providerModel.InstanceDesiredStateRunning},
	}
	if err := db.Create(&instances).Error; err != nil {
		t.Fatal(err)
	}
	providerID := provider.ID
	busyID := instances[6].ID
	if err := db.Create(&adminModel.Task{ProviderID: &providerID, InstanceID: &busyID, TaskType: "stop", Status: "pending"}).Error; err != nil {
		t.Fatal(err)
	}

	snapshots := make([]adminProviderService.MatchedInstanceSnapshot, 0, len(instances))
	for _, instance := range instances {
		snapshots = append(snapshots, adminProviderService.MatchedInstanceSnapshot{
			InstanceID:     instance.ID,
			DatabaseStatus: instance.Status,
			DesiredState:   instance.DesiredState,
			IsImported:     instance.IsImported,
			IsFrozen:       instance.IsFrozen,
			ExpiresAt:      instance.ExpiresAt,
			TrafficStopped: instance.TrafficStopped,
			RemoteStatus:   "stopped",
		})
	}
	started, err := enqueueRecoveredInstanceStarts(provider.ID, snapshots)
	if err != nil {
		t.Fatal(err)
	}
	if started != 1 {
		t.Fatalf("started = %d, want 1", started)
	}
	var count int64
	if err := db.Model(&adminModel.Task{}).Where("task_type = ?", "start").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("recovery start task count = %d, want 1", count)
	}
	var recoveryTask adminModel.Task
	if err := db.Where("task_type = ?", "start").First(&recoveryTask).Error; err != nil {
		t.Fatal(err)
	}
	var taskData recoveryStartTaskData
	if err := json.Unmarshal([]byte(recoveryTask.TaskData), &taskData); err != nil {
		t.Fatal(err)
	}
	if !taskData.Recovery || taskData.InstanceID != instances[0].ID || taskData.ProviderID != provider.ID {
		t.Fatalf("recovery task data = %#v, want explicit recovery marker", taskData)
	}
	var eligible providerModel.Instance
	if err := db.First(&eligible, instances[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if eligible.Status != constant.InstanceStatusStarting {
		t.Fatalf("eligible status = %q, want %q", eligible.Status, constant.InstanceStatusStarting)
	}
	for _, instance := range instances[1:] {
		var current providerModel.Instance
		if err := db.First(&current, instance.ID).Error; err != nil {
			t.Fatal(err)
		}
		if current.Status != constant.InstanceStatusRunning {
			t.Fatalf("unsafe instance %q changed status to %q", current.Name, current.Status)
		}
	}
}

func TestEnqueueRecoveredInstanceStartsUsesBoundedBatches(t *testing.T) {
	db := newInstanceRecoveryTestDB(t)
	provider := createRecoveryTestProvider(t, db, providerModel.Provider{Name: "large-recovery-node", Type: "docker", Status: "active"})
	instances := make([]providerModel.Instance, 0, instanceRecoveryInstanceBatchSize+1)
	for i := 0; i <= instanceRecoveryInstanceBatchSize; i++ {
		instances = append(instances, providerModel.Instance{
			Name:         fmt.Sprintf("guest-%03d", i),
			Provider:     provider.Name,
			ProviderID:   provider.ID,
			Status:       "running",
			DesiredState: providerModel.InstanceDesiredStateRunning,
		})
	}
	if err := db.CreateInBatches(&instances, instanceRecoveryTaskInsertBatchSize).Error; err != nil {
		t.Fatal(err)
	}
	snapshots := make([]adminProviderService.MatchedInstanceSnapshot, 0, len(instances))
	for _, instance := range instances {
		snapshots = append(snapshots, adminProviderService.MatchedInstanceSnapshot{
			InstanceID: instance.ID, DatabaseStatus: "running", DesiredState: providerModel.InstanceDesiredStateRunning, RemoteStatus: "stopped",
		})
	}
	started, err := enqueueRecoveredInstanceStarts(provider.ID, snapshots)
	if err != nil {
		t.Fatal(err)
	}
	if started != len(instances) {
		t.Fatalf("started = %d, want %d", started, len(instances))
	}
	var taskCount int64
	if err := db.Model(&adminModel.Task{}).Where("provider_id = ? AND task_type = ?", provider.ID, "start").Count(&taskCount).Error; err != nil {
		t.Fatal(err)
	}
	if taskCount != int64(len(instances)) {
		t.Fatalf("task count = %d, want %d", taskCount, len(instances))
	}
}

func TestNDPRecoveryCommandUsesRuntimeSpecificCLI(t *testing.T) {
	tests := []struct {
		providerType string
		want         string
		ok           bool
	}{
		{providerType: "proxmox", want: "systemctl start ndpresponder.service", ok: true},
		{providerType: "docker", want: "docker start ndpresponder", ok: true},
		{providerType: "podman", want: "podman start ndpresponder", ok: true},
		{providerType: "containerd", want: "nerdctl start ndpresponder", ok: true},
		{providerType: "lxd", want: "systemctl start ndpresponder.service", ok: true},
		{providerType: "qemu", want: "systemctl start ndpresponder.service", ok: true},
		{providerType: "api-provider", ok: false},
	}
	for _, test := range tests {
		command, ok := ndpRecoveryCommand(test.providerType)
		if ok != test.ok {
			t.Fatalf("ndpRecoveryCommand(%q) ok = %v, want %v", test.providerType, ok, test.ok)
		}
		if test.want != "" && !strings.Contains(command, test.want) {
			t.Fatalf("ndpRecoveryCommand(%q) = %q, want substring %q", test.providerType, command, test.want)
		}
		if test.providerType == "containerd" && strings.Contains(command, "ctr ") {
			t.Fatalf("containerd command must use nerdctl namespace, got %q", command)
		}
		if test.providerType == "proxmox" && strings.Contains(command, "systemctl restart") {
			t.Fatalf("NDP recovery must not restart a healthy responder, got %q", command)
		}
	}
}

func TestNDPRecoveryEligibilityKeepsLocalQEMUEnabled(t *testing.T) {
	localQEMU := providerModel.Provider{Type: "qemu", ConnectionType: "local", ExecutionRule: "auto"}
	if !ndpRecoveryEligible(localQEMU) {
		t.Fatal("local QEMU provider must restore an eligible NDP responder after recovery")
	}
	apiOnlyQEMU := localQEMU
	apiOnlyQEMU.ExecutionRule = "api_only"
	if ndpRecoveryEligible(apiOnlyQEMU) {
		t.Fatal("api_only provider must not attempt a host-level NDP command")
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
