package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"
	adminProviderService "oneclickvirt/service/admin/provider"
	agentService "oneclickvirt/service/agent"
	providerRuntimeService "oneclickvirt/service/provider"
	"oneclickvirt/service/taskgate"
	"oneclickvirt/utils"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var recoveryActiveTaskTypes = []string{
	"create", "create_instance", "create_redemption_instance",
	"start", "stop", "restart", "reset", "rebuild", "delete", "reset-password",
}

var recoveryActiveTaskStatuses = []string{"pending", "processing", "running", "cancelling"}

// InstanceRecoverySchedulerService restores only instances whose persisted
// DesiredState says they should be running after a provider was offline long
// enough to be a real node-reboot/outage event.
type InstanceRecoverySchedulerService struct {
	providerService *adminProviderService.Service
	stopChan        chan struct{}
	mu              sync.RWMutex
	isRunning       bool
	recoveryMu      sync.Mutex
	semaphore       chan struct{}
}

func NewInstanceRecoverySchedulerService() *InstanceRecoverySchedulerService {
	return &InstanceRecoverySchedulerService{
		providerService: adminProviderService.NewService(),
		stopChan:        make(chan struct{}),
		semaphore:       make(chan struct{}, instanceRecoveryMaxProviderConcurrency),
	}
}

func (s *InstanceRecoverySchedulerService) Start(ctx context.Context) {
	settings := getInstanceRecoverySettings()
	if !settings.Enabled {
		global.APP_LOG.Info("实例恢复调度器未启用，跳过启动")
		return
	}
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return
	}
	s.stopChan = make(chan struct{})
	s.isRunning = true
	s.mu.Unlock()

	global.APP_LOG.Info("启动实例恢复调度器",
		zap.Duration("interval", settings.Interval),
		zap.Duration("offline_threshold", settings.OfflineThreshold),
		zap.Int("provider_batch_size", instanceRecoveryProviderBatchSize))
	go s.run(ctx)
}

func (s *InstanceRecoverySchedulerService) Stop() {
	s.mu.Lock()
	if !s.isRunning {
		s.mu.Unlock()
		return
	}
	s.isRunning = false
	s.mu.Unlock()
	close(s.stopChan)
}

func (s *InstanceRecoverySchedulerService) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

func (s *InstanceRecoverySchedulerService) run(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			global.APP_LOG.Error("实例恢复调度器 panic", zap.Any("panic", recovered), zap.Stack("stack"))
		}
		global.APP_LOG.Info("实例恢复调度器已停止")
	}()

	startupTimer := time.NewTimer(instanceRecoveryStartupGrace)
	defer startupTimer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-s.stopChan:
		return
	case <-startupTimer.C:
	}

	for {
		if global.APP_DB != nil {
			s.recoverEligibleProviders(ctx)
		}
		settings := getInstanceRecoverySettings()
		timer := time.NewTimer(settings.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.stopChan:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *InstanceRecoverySchedulerService) recoverEligibleProviders(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return
	}
	if !s.recoveryMu.TryLock() {
		global.APP_LOG.Debug("实例恢复调度仍在运行，跳过重叠触发")
		return
	}
	defer s.recoveryMu.Unlock()

	settings := getInstanceRecoverySettings()
	if !settings.Enabled {
		return
	}
	now := time.Now()
	var providers []providerModel.Provider
	query := global.APP_DB.Model(&providerModel.Provider{}).
		Select("id", "name", "type", "status", "is_frozen", "frozen_reason", "expires_at", "connection_type", "agent_status", "execution_rule", "recovery_offline_since", "recovery_last_recovery_attempt_at").
		Order("recovery_offline_since ASC, id ASC").
		Limit(instanceRecoveryProviderBatchSize)
	if err := recoveryProviderCandidateScope(query, now, settings).
		WithContext(ctx).
		Find(&providers).Error; err != nil {
		global.APP_LOG.Warn("查询待恢复Provider失败", zap.Error(err))
		return
	}
	if len(providers) == 0 {
		return
	}

	var wg sync.WaitGroup
launchLoop:
	for _, provider := range providers {
		provider := provider
		select {
		case s.semaphore <- struct{}{}:
		case <-ctx.Done():
			break launchLoop
		}
		wg.Add(1)
		go func() {
			defer func() {
				<-s.semaphore
				wg.Done()
			}()
			s.recoverProvider(ctx, provider, settings)
		}()
	}
	wg.Wait()
}

func (s *InstanceRecoverySchedulerService) recoverProvider(ctx context.Context, candidate providerModel.Provider, settings instanceRecoverySettings) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return
	}
	claimAt := time.Now()
	claim, claimed, err := claimScheduledProviderRecoveryContext(ctx, candidate.ID, claimAt, settings)
	if err != nil {
		global.APP_LOG.Warn("领取Provider恢复任务失败", zap.Uint("provider_id", candidate.ID), zap.Error(err))
		return
	}
	if !claimed {
		return
	}
	completed := false
	defer func() {
		if !completed {
			releaseProviderRecoveryLeaseContext(ctx, candidate.ID, claim.Token)
		}
	}()
	allowed, err := recoveryProviderMayBeProbedWithOptionsContext(ctx, candidate.ID, false)
	if err != nil {
		global.APP_LOG.Warn("复核Provider恢复探测资格失败",
			zap.Uint("provider_id", candidate.ID), zap.Error(err))
		return
	}
	if !allowed {
		// A user may freeze, expire, or take a Provider offline immediately after
		// the atomic claim. Do not make a remote call in that race; the claim's
		// timestamp still provides the normal cooldown.
		global.APP_LOG.Debug("Provider恢复探测资格已变化，跳过本次远端调用",
			zap.Uint("provider_id", candidate.ID))
		return
	}

	probeCtx, cancel := context.WithTimeout(ctx, instanceRecoveryRemoteProbeTimeout)
	report, err := s.providerService.CompareInstancesWithRemoteForRecovery(probeCtx, candidate.ID)
	cancel()
	if err != nil {
		// Keep the outage marker.  The claimed timestamp enforces a bounded
		// cooldown before the next remote discovery attempt.
		global.APP_LOG.Warn("Provider恢复远端核对失败，将在冷却后重试",
			zap.Uint("provider_id", candidate.ID), zap.String("provider", candidate.Name), zap.Error(err))
		return
	}

	started, err := enqueueRecoveredInstanceStartsWithOptionsContext(ctx, candidate.ID, report.MatchedInstances, false)
	if err != nil {
		// Most importantly, a closed task gate must not make us mark the outage
		// processed: the next bounded pass should submit work after maintenance.
		global.APP_LOG.Warn("提交Provider实例恢复任务失败，将在冷却后重试",
			zap.Uint("provider_id", candidate.ID), zap.Int("already_queued", started), zap.Error(err))
		return
	}

	// NDP responder is node-scoped. Restore it after the short queue transaction
	// succeeds but before explicitly waking task processing, so routed IPv6
	// guests do not race a stopped responder. It remains bounded, best effort,
	// and outside every database transaction.
	if err := restoreNDPResponderAfterRecovery(ctx, candidate); err != nil {
		global.APP_LOG.Debug("NDP responder恢复未完成（不影响实例恢复）",
			zap.Uint("provider_id", candidate.ID), zap.Error(err))
	}

	if err := completeProviderRecoveryContext(ctx, candidate, claim); err != nil {
		global.APP_LOG.Warn("标记Provider恢复完成失败",
			zap.Uint("provider_id", candidate.ID), zap.Error(err))
		return
	}
	completed = true
	scheduleProviderPostRecoveryRepairs(candidate)
	if started > 0 {
		// Starts in this recovery batch intentionally defer their post-start
		// discovery. The provider-scoped worker waits for the batch to settle and
		// then refreshes addresses and controller mappings exactly once. When no
		// guest was stopped, the authoritative discovery above already completed
		// address synchronization, so do not add a redundant remote probe.
		agentService.ScheduleProviderRecoveryRuntimeNetworkRefresh(global.APP_DB, candidate.ID)
		if global.APP_SCHEDULER != nil {
			global.APP_SCHEDULER.TriggerTaskProcessing()
		}
	}

	global.APP_LOG.Info("Provider恢复核对完成",
		zap.Uint("provider_id", candidate.ID),
		zap.String("provider", candidate.Name),
		zap.Int("start_tasks", started))
}

func healthAutoFreezeReasonPattern() string {
	return "健康检查连续失败%"
}

func agentHealthAutoFreezeReasonPattern() string {
	return "Agent 反向连接连续断开%"
}

func isHealthAutoFrozen(provider providerModel.Provider) bool {
	reason := strings.TrimSpace(provider.FrozenReason)
	return strings.HasPrefix(reason, "健康检查连续失败") || strings.HasPrefix(reason, "Agent 反向连接连续断开")
}

func isReverseAgentUnavailable(provider providerModel.Provider) bool {
	return provider.IsReverseAgent() &&
		!strings.EqualFold(strings.TrimSpace(provider.AgentStatus), "online")
}

// recoveryProviderCandidateScope is shared by the selection and atomic claim
// paths.  Normal recovered nodes may be retried after the short cooldown when
// discovery itself failed.  A node which has already been health-auto-frozen
// is deliberately probed no more than once per long window: SSH/API health
// checks stop for such nodes, but a later reboot recovery still needs one way
// to notice that the host has returned.
func recoveryProviderCandidateScope(db *gorm.DB, now time.Time, settings instanceRecoverySettings) *gorm.DB {
	return db.
		Where("expires_at IS NULL OR expires_at > ?", now).
		Where("recovery_offline_since IS NOT NULL AND recovery_offline_since <= ?", now.Add(-settings.OfflineThreshold)).
		Where("recovery_lease_expires_at IS NULL OR recovery_lease_expires_at <= ?", now).
		// A reverse-Agent Provider cannot be discovered until its WebSocket is
		// online. Filtering it here prevents a long outage from becoming one
		// 45-second reconnect wait per recovery interval.
		Where("connection_type IS NULL OR LOWER(connection_type) <> ? OR LOWER(COALESCE(execution_rule, '')) = ? OR LOWER(COALESCE(agent_status, '')) = ?", "agent", "api_only", "online").
		Where(`(
			(status IN ?
				AND (is_frozen = ? OR frozen_reason LIKE ? OR frozen_reason LIKE ?)
				AND (recovery_last_recovery_attempt_at IS NULL OR recovery_last_recovery_attempt_at <= ?))
			OR
			(status = ? AND is_frozen = ?
				AND (frozen_reason LIKE ? OR frozen_reason LIKE ?)
				AND (recovery_last_recovery_attempt_at IS NULL OR recovery_last_recovery_attempt_at <= ?))
		)`,
			[]string{"active", "partial"}, false, healthAutoFreezeReasonPattern(), agentHealthAutoFreezeReasonPattern(), now.Add(-settings.RetryCooldown),
			"inactive", true, healthAutoFreezeReasonPattern(), agentHealthAutoFreezeReasonPattern(), now.Add(-settings.AutoFrozenProbeCooldown),
		)
}

func claimProviderRecovery(providerID uint, now time.Time, settings instanceRecoverySettings) (bool, error) {
	_, claimed, err := claimScheduledProviderRecoveryContext(context.Background(), providerID, now, settings)
	return claimed, err
}

type providerRecoveryClaim struct {
	Token     string
	ClaimedAt time.Time
}

// claimScheduledProviderRecovery writes a tokenized, expiring database lease.
// The token is essential: a timed-out worker must not clear a newer worker's
// lease after another controller safely takes over.
func claimScheduledProviderRecovery(providerID uint, now time.Time, settings instanceRecoverySettings) (*providerRecoveryClaim, bool, error) {
	return claimScheduledProviderRecoveryContext(context.Background(), providerID, now, settings)
}

func claimScheduledProviderRecoveryContext(ctx context.Context, providerID uint, now time.Time, settings instanceRecoverySettings) (*providerRecoveryClaim, bool, error) {
	if global.APP_DB == nil || providerID == 0 {
		return nil, false, fmt.Errorf("Provider恢复数据库连接不可用")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	claim := &providerRecoveryClaim{Token: uuid.NewString(), ClaimedAt: now}
	leaseExpiresAt := now.Add(instanceRecoveryRemoteProbeTimeout + instanceRecoveryLeasePadding)
	query := global.APP_DB.WithContext(ctx).Model(&providerModel.Provider{}).Where("id = ?", providerID)
	result := recoveryProviderCandidateScope(query, now, settings).
		Updates(map[string]interface{}{
			"recovery_last_recovery_attempt_at": now,
			"recovery_lease_token":              claim.Token,
			"recovery_lease_expires_at":         leaseExpiresAt,
		})
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, false, nil
	}
	return claim, true, nil
}

// releaseProviderRecoveryLease is deliberately best-effort. Every update is
// conditional on the owner token, so it cannot erase a lease acquired by a
// later retry on another controller.
func releaseProviderRecoveryLease(providerID uint, token string) {
	releaseProviderRecoveryLeaseContext(context.Background(), providerID, token)
}

func releaseProviderRecoveryLeaseContext(ctx context.Context, providerID uint, token string) {
	if global.APP_DB == nil || providerID == 0 || strings.TrimSpace(token) == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := global.APP_DB.WithContext(ctx).Model(&providerModel.Provider{}).
		Where("id = ? AND recovery_lease_token = ?", providerID, token).
		Updates(map[string]interface{}{
			"recovery_lease_token":      "",
			"recovery_lease_expires_at": nil,
		}).Error; err != nil {
		global.APP_LOG.Warn("释放Provider恢复租约失败", zap.Uint("provider_id", providerID), zap.Error(err))
	}
}

// recoveryProviderMayBeProbed performs one cheap post-claim authorization
// check before a potentially slow provider connection. It closes the race
// where an operator manually freezes a node between the atomic claim and the
// remote discovery, without placing remote I/O in a transaction.
func recoveryProviderMayBeProbed(providerID uint) (bool, error) {
	return recoveryProviderMayBeProbedWithOptionsContext(context.Background(), providerID, false)
}

// recoveryProviderMayBeProbedWithOptions is the post-claim authorization
// check shared by periodic and explicitly requested recovery. A manual run may
// verify an inactive-but-not-frozen node whose health status has not caught up
// yet; it still never bypasses expiry or a manual freeze.
func recoveryProviderMayBeProbedWithOptions(providerID uint, allowInactive bool) (bool, error) {
	return recoveryProviderMayBeProbedWithOptionsContext(context.Background(), providerID, allowInactive)
}

func recoveryProviderMayBeProbedWithOptionsContext(ctx context.Context, providerID uint, allowInactive bool) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var provider providerModel.Provider
	if err := global.APP_DB.WithContext(ctx).
		Select("id", "status", "is_frozen", "frozen_reason", "expires_at", "connection_type", "agent_status", "execution_rule").
		Where("id = ?", providerID).
		First(&provider).Error; err != nil {
		return false, err
	}
	if isReverseAgentUnavailable(provider) {
		return false, nil
	}
	if provider.ExpiresAt != nil && !provider.ExpiresAt.After(time.Now()) {
		return false, nil
	}
	autoFrozen := provider.IsFrozen && isHealthAutoFrozen(provider)
	if provider.IsFrozen && !autoFrozen {
		return false, nil
	}
	switch provider.Status {
	case "active", "partial":
		return true, nil
	case "inactive":
		return autoFrozen || allowInactive, nil
	default:
		return false, nil
	}
}

type recoveryStartTaskData struct {
	InstanceID           uint   `json:"instanceId"`
	ProviderID           uint   `json:"providerId"`
	Recovery             bool   `json:"recovery"`
	RecoveryNode         string `json:"recoveryNode,omitempty"`
	RecoveryInstanceID   string `json:"recoveryInstanceId,omitempty"`
	RecoveryInstanceType string `json:"recoveryInstanceType,omitempty"`
}

type recoveryStartCandidate struct {
	InstanceID uint
	Identity   *providerCore.RecoveryInstanceIdentity
}

func cloneRecoveryIdentity(identity *providerCore.RecoveryInstanceIdentity) *providerCore.RecoveryInstanceIdentity {
	if identity == nil {
		return nil
	}
	copy := *identity
	copy.Node = strings.TrimSpace(copy.Node)
	copy.ID = strings.TrimSpace(copy.ID)
	copy.Type = strings.ToLower(strings.TrimSpace(copy.Type))
	return &copy
}

func providerInstanceRecoveryID(instance providerModel.Instance) string {
	if value := strings.TrimSpace(instance.ProviderVMID); value != "" {
		return value
	}
	return strings.TrimSpace(instance.Name)
}

// enqueueRecoveredInstanceStarts keeps every recovery transaction bounded.
// Remote discovery has already finished, so each database batch only loads a
// small set of fresh rows, checks active tasks for that same set, inserts its
// start tasks, and moves the accepted rows to starting atomically.
func enqueueRecoveredInstanceStarts(providerID uint, snapshots []adminProviderService.MatchedInstanceSnapshot) (int, error) {
	return enqueueRecoveredInstanceStartsWithOptionsContext(context.Background(), providerID, snapshots, false)
}

func enqueueRecoveredInstanceStartsWithOptions(providerID uint, snapshots []adminProviderService.MatchedInstanceSnapshot, allowInactive bool) (int, error) {
	return enqueueRecoveredInstanceStartsWithOptionsContext(context.Background(), providerID, snapshots, allowInactive)
}

func enqueueRecoveredInstanceStartsWithOptionsContext(ctx context.Context, providerID uint, snapshots []adminProviderService.MatchedInstanceSnapshot, allowInactive bool) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	candidates := make([]recoveryStartCandidate, 0, len(snapshots))
	seenCandidateIDs := make(map[uint]struct{}, len(snapshots))
	now := time.Now()
	for _, snapshot := range snapshots {
		if recoverySnapshotEligible(snapshot, now) {
			if _, exists := seenCandidateIDs[snapshot.InstanceID]; exists {
				continue
			}
			seenCandidateIDs[snapshot.InstanceID] = struct{}{}
			candidates = append(candidates, recoveryStartCandidate{
				InstanceID: snapshot.InstanceID,
				Identity:   cloneRecoveryIdentity(snapshot.RuntimeIdentity),
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].InstanceID < candidates[j].InstanceID })

	if len(candidates) == 0 {
		return enqueueRecoveredInstanceStartBatchContext(ctx, providerID, nil, allowInactive)
	}

	totalStarted := 0
	for start := 0; start < len(candidates); start += instanceRecoveryInstanceBatchSize {
		if err := ctx.Err(); err != nil {
			return totalStarted, err
		}
		end := start + instanceRecoveryInstanceBatchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		started, err := enqueueRecoveredInstanceStartBatchContext(ctx, providerID, candidates[start:end], allowInactive)
		totalStarted += started
		if err != nil {
			return totalStarted, err
		}
	}
	return totalStarted, nil
}

func enqueueRecoveredInstanceStartBatch(providerID uint, candidates []recoveryStartCandidate, allowInactive bool) (int, error) {
	return enqueueRecoveredInstanceStartBatchContext(context.Background(), providerID, candidates, allowInactive)
}

func enqueueRecoveredInstanceStartBatchContext(parentCtx context.Context, providerID uint, candidates []recoveryStartCandidate, allowInactive bool) (int, error) {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	if err := parentCtx.Err(); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	acceptedCount := 0
	err := global.APP_DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var provider providerModel.Provider
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "status", "is_frozen", "frozen_reason", "frozen_at", "allow_claim", "expires_at", "connection_type", "agent_status", "execution_rule").
			Where("id = ?", providerID).First(&provider).Error; err != nil {
			return err
		}
		if isReverseAgentUnavailable(provider) {
			return fmt.Errorf("Agent已离线，取消恢复提交")
		}
		if provider.ExpiresAt != nil && !provider.ExpiresAt.After(time.Now()) {
			return fmt.Errorf("Provider已过期，取消恢复提交")
		}
		autoFrozen := provider.IsFrozen && isHealthAutoFrozen(provider)
		if provider.Status != "active" && provider.Status != "partial" {
			if provider.Status != "inactive" || (!autoFrozen && !allowInactive) {
				return fmt.Errorf("Provider已不在线，取消恢复提交")
			}
		}
		if provider.IsFrozen && !autoFrozen {
			return fmt.Errorf("Provider已手工冻结，取消恢复提交")
		}
		if autoFrozen || (provider.Status == "inactive" && allowInactive) {
			// A successful authoritative discovery is stronger evidence than the
			// prior health-failure auto-freeze.  Never unfreeze a manual reason.
			updates := map[string]interface{}{
				"is_frozen":     false,
				"frozen_reason": "",
				"frozen_at":     nil,
				"allow_claim":   true,
			}
			if provider.Status == "inactive" {
				updates["status"] = "active"
			}
			if err := tx.Model(&providerModel.Provider{}).Where("id = ?", provider.ID).Updates(updates).Error; err != nil {
				return err
			}
		}

		// The provider itself is now confirmed healthy even if there are no
		// stopped managed guests.  This lets an auto-frozen SSH/API node recover
		// its status without fabricating an instance task.
		if len(candidates) == 0 {
			return nil
		}
		candidateIDs := make([]uint, 0, len(candidates))
		identityByInstanceID := make(map[uint]*providerCore.RecoveryInstanceIdentity, len(candidates))
		for _, candidate := range candidates {
			candidateIDs = append(candidateIDs, candidate.InstanceID)
			identityByInstanceID[candidate.InstanceID] = cloneRecoveryIdentity(candidate.Identity)
		}

		var instances []providerModel.Instance
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "provider_id", "user_id", "name", "provider_vm_id", "instance_type", "status", "desired_state", "is_frozen", "expires_at", "traffic_limited", "traffic_stopped", "expiry_stopped").
			Where("provider_id = ? AND id IN ?", providerID, candidateIDs).
			Find(&instances).Error; err != nil {
			return err
		}
		if len(instances) == 0 {
			return nil
		}

		instanceIDs := make([]uint, 0, len(instances))
		for _, instance := range instances {
			instanceIDs = append(instanceIDs, instance.ID)
		}
		var activeTasks []adminModel.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("instance_id").
			Where("provider_id = ? AND instance_id IN ? AND task_type IN ? AND status IN ?", providerID, instanceIDs, recoveryActiveTaskTypes, recoveryActiveTaskStatuses).
			Find(&activeTasks).Error; err != nil {
			return err
		}
		activeByInstanceID := make(map[uint]struct{}, len(activeTasks))
		for _, task := range activeTasks {
			if task.InstanceID != nil {
				activeByInstanceID[*task.InstanceID] = struct{}{}
			}
		}

		tasks := make([]adminModel.Task, 0, len(instances))
		acceptedIDs := make([]uint, 0, len(instances))
		now := time.Now()
		for _, instance := range instances {
			if !recoveryInstanceEligible(instance, now) {
				continue
			}
			if _, active := activeByInstanceID[instance.ID]; active {
				continue
			}
			identity := identityByInstanceID[instance.ID]
			if identity == nil {
				// Non-clustered providers can use their stable managed ID/name as
				// the runtime identity. Proxmox must always carry the node/VMID/type
				// returned by discovery; guessing there could start a different guest.
				if strings.EqualFold(strings.TrimSpace(provider.Type), "proxmox") ||
					strings.EqualFold(strings.TrimSpace(provider.Type), "proxmoxve") ||
					strings.EqualFold(strings.TrimSpace(provider.Type), "pve") {
					return fmt.Errorf("Proxmox实例%d缺少发现到的node/VMID/type，拒绝恢复启动", instance.ID)
				}
				identity = &providerCore.RecoveryInstanceIdentity{ID: providerInstanceRecoveryID(instance), Type: instance.InstanceType}
			}
			isProxmox := strings.EqualFold(strings.TrimSpace(provider.Type), "proxmox") ||
				strings.EqualFold(strings.TrimSpace(provider.Type), "proxmoxve") ||
				strings.EqualFold(strings.TrimSpace(provider.Type), "pve")
			if !identity.Valid() || (isProxmox && strings.TrimSpace(identity.Node) == "") {
				return fmt.Errorf("实例%d恢复启动运行时身份无效", instance.ID)
			}
			taskData, err := json.Marshal(recoveryStartTaskData{
				InstanceID: instance.ID, ProviderID: providerID, Recovery: true,
				RecoveryNode: identity.Node, RecoveryInstanceID: identity.ID, RecoveryInstanceType: identity.Type,
			})
			if err != nil {
				return err
			}
			providerIDCopy := providerID
			instanceIDCopy := instance.ID
			tasks = append(tasks, adminModel.Task{
				UserID:            instance.UserID,
				ProviderID:        &providerIDCopy,
				InstanceID:        &instanceIDCopy,
				TaskType:          "start",
				Status:            "pending",
				TaskData:          string(taskData),
				TimeoutDuration:   utils.GetDefaultTaskTimeout("start"),
				EstimatedDuration: utils.GetEstimatedTaskDuration("start", instance.InstanceType),
				IsForceStoppable:  true,
			})
			acceptedIDs = append(acceptedIDs, instance.ID)
		}
		if len(tasks) == 0 {
			return nil
		}
		if err := taskgate.EnsureAcceptingInTx(tx); err != nil {
			return err
		}
		if err := tx.CreateInBatches(&tasks, instanceRecoveryTaskInsertBatchSize).Error; err != nil {
			return err
		}
		result := tx.Model(&providerModel.Instance{}).
			Where("id IN ? AND status IN ? AND desired_state = ? AND is_frozen = ?", acceptedIDs,
				[]string{constant.InstanceStatusRunning, constant.InstanceStatusStopped}, providerModel.InstanceDesiredStateRunning, false).
			Where("traffic_limited = ? AND traffic_stopped = ? AND expiry_stopped = ?", false, false, false).
			Where("expires_at IS NULL OR expires_at > ?", now).
			Updates(map[string]interface{}{"status": constant.InstanceStatusStarting})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(acceptedIDs)) {
			return fmt.Errorf("恢复实例状态在提交期间发生变化")
		}
		acceptedCount = len(acceptedIDs)
		return nil
	})
	return acceptedCount, err
}

func recoverySnapshotEligible(snapshot adminProviderService.MatchedInstanceSnapshot, now time.Time) bool {
	if snapshot.InstanceID == 0 || snapshot.RemoteStatus != constant.InstanceStatusStopped {
		return false
	}
	if snapshot.DesiredState != providerModel.InstanceDesiredStateRunning || snapshot.IsFrozen ||
		snapshot.TrafficLimited || snapshot.TrafficStopped || snapshot.ExpiryStopped {
		return false
	}
	if snapshot.ExpiresAt != nil && !snapshot.ExpiresAt.After(now) {
		return false
	}
	return snapshot.DatabaseStatus == constant.InstanceStatusRunning || snapshot.DatabaseStatus == constant.InstanceStatusStopped
}

func recoveryInstanceEligible(instance providerModel.Instance, now time.Time) bool {
	if instance.DesiredState != providerModel.InstanceDesiredStateRunning || instance.IsFrozen ||
		instance.TrafficLimited || instance.TrafficStopped || instance.ExpiryStopped {
		return false
	}
	if instance.ExpiresAt != nil && !instance.ExpiresAt.After(now) {
		return false
	}
	return instance.Status == constant.InstanceStatusRunning || instance.Status == constant.InstanceStatusStopped
}

func completeProviderRecovery(candidate providerModel.Provider, claim *providerRecoveryClaim) error {
	return completeProviderRecoveryContext(context.Background(), candidate, claim)
}

func completeProviderRecoveryContext(ctx context.Context, candidate providerModel.Provider, claim *providerRecoveryClaim) error {
	if claim == nil || strings.TrimSpace(claim.Token) == "" {
		return fmt.Errorf("Provider恢复租约无效")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	updates := map[string]interface{}{
		"recovery_lease_token":      "",
		"recovery_lease_expires_at": nil,
	}
	query := global.APP_DB.WithContext(ctx).Model(&providerModel.Provider{}).
		Where("id = ? AND recovery_lease_token = ?", candidate.ID, claim.Token)
	if candidate.RecoveryOfflineSince != nil {
		query = query.Where("recovery_offline_since IS NOT NULL").
			Where("recovery_offline_since <= ?", candidate.RecoveryOfflineSince.Add(time.Second))
		updates["recovery_offline_since"] = nil
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("Provider恢复租约或恢复窗口在核对期间已变化")
	}
	return nil
}

// scheduleProviderPostRecoveryRepairs repairs node-scoped runtime state that
// discovery cannot infer from an unchanged guest address. In particular, an
// Agent restart can lose its local egress rules or the controller's tunnel
// manager can still reference the previous Agent connection. Both helpers are
// debounced per Provider; they do not add a remote call per instance.
func scheduleProviderPostRecoveryRepairs(candidate providerModel.Provider) {
	if global.APP_DB == nil || candidate.ID == 0 {
		return
	}
	isReverseAgent := candidate.IsReverseAgent()
	// SSH Providers may also have the node-side Agent installed for egress
	// control.  Read that capability once per Provider; do not create a
	// provider-wide replay worker for ordinary SSH/API-only nodes.
	if isReverseAgent || agentService.ProviderHasInstalledAgent(global.APP_DB, candidate.ID) {
		agentService.ScheduleProviderEgressRefresh(global.APP_DB, candidate.ID, true)
	}
	// Controller port forwarding depends on the reverse WebSocket Agent
	// connection.  SSH+Agent uses direct Agent HTTP only and has no TunnelManager
	// to rebuild here.
	if isReverseAgent {
		agentService.ScheduleProviderControllerPortForwardRecovery(candidate.ID)
	}
}

func restoreNDPResponderAfterRecovery(ctx context.Context, candidate providerModel.Provider) error {
	if strings.EqualFold(strings.TrimSpace(candidate.ExecutionRule), "api_only") {
		global.APP_LOG.Debug("跳过主机级NDP恢复：当前Provider仅允许API执行",
			zap.Uint("provider_id", candidate.ID), zap.String("connection_type", candidate.ConnectionType), zap.String("execution_rule", candidate.ExecutionRule))
		return nil
	}
	if isReverseAgentUnavailable(candidate) {
		return fmt.Errorf("Agent未在线，跳过NDP恢复")
	}

	command, ok := ndpRecoveryCommand(candidate.Type)
	if !ok {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ndpCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	providerInstance, err := providerRuntimeService.GetProviderInstanceByIDForRecoveryContext(ndpCtx, candidate.ID)
	if err != nil {
		global.APP_LOG.Debug("获取Provider执行NDP恢复命令失败",
			zap.Uint("provider_id", candidate.ID), zap.Error(err))
		return err
	}
	if _, err := providerInstance.ExecuteSSHCommand(ndpCtx, command); err != nil {
		global.APP_LOG.Warn("NDP responder恢复命令失败（不影响实例恢复）",
			zap.Uint("provider_id", candidate.ID), zap.Error(err))
		return err
	}
	global.APP_LOG.Info("NDP responder恢复命令执行完成", zap.Uint("provider_id", candidate.ID))
	return nil
}

func ndpRecoveryCommand(providerType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "proxmox", "proxmoxve", "pve":
		return ndpSystemdRecoveryCommand(), true
	case "docker", "orbstack":
		return "if command -v docker >/dev/null 2>&1 && docker inspect ndpresponder >/dev/null 2>&1 && [ \"$(docker inspect -f '{{.State.Running}}' ndpresponder 2>/dev/null)\" != true ]; then docker start ndpresponder >/dev/null; fi", true
	case "podman":
		return "if command -v podman >/dev/null 2>&1 && podman container exists ndpresponder && [ \"$(podman inspect -f '{{.State.Running}}' ndpresponder 2>/dev/null)\" != true ]; then podman start ndpresponder >/dev/null; fi", true
	case "containerd":
		// The Containerd provider manages the Docker-compatible namespace through
		// nerdctl, not raw ctr/default.  Using the same CLI prevents a restart
		// command from missing a responder created in nerdctl's namespace.
		return "if command -v nerdctl >/dev/null 2>&1 && nerdctl inspect ndpresponder >/dev/null 2>&1 && [ \"$(nerdctl inspect -f '{{.State.Running}}' ndpresponder 2>/dev/null)\" != true ]; then nerdctl start ndpresponder >/dev/null; fi", true
	case "lxd", "incus", "qemu", "kubevirt", "vmware", "virtualbox", "multipass", "vagrant":
		return ndpSystemdRecoveryCommand(), true
	default:
		return "", false
	}
}

// ndpSystemdRecoveryCommand is deliberately a start-if-needed operation. A
// healthy responder must not be restarted on every recovery pass; the command
// is also guarded for non-systemd SSH hosts.
func ndpSystemdRecoveryCommand() string {
	return "if command -v systemctl >/dev/null 2>&1 && systemctl cat ndpresponder.service >/dev/null 2>&1 && ! systemctl is-active --quiet ndpresponder.service; then systemctl start ndpresponder.service; fi"
}
