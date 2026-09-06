package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	adminProviderService "oneclickvirt/service/admin/provider"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/service/task"
	"oneclickvirt/utils"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const taskTypeProviderRecoverySync = "provider-recovery-sync"

var providerRecoveryTaskCreateMu sync.Mutex

// ManualProviderRecoveryResult is intentionally compact. The detailed remote
// list remains in the provider service, while an admin task records the
// durable outcome without keeping the HTTP request open for a two-minute
// provider probe.
type ManualProviderRecoveryResult struct {
	ProviderID            uint
	DiscoveredInstances   int
	MatchedInstances      int
	QueuedStartTasks      int
	NDPRecoveryAttempted  bool
	EgressReplayScheduled bool
	TunnelRepairScheduled bool
}

func init() {
	task.RegisterExternalTaskHandler(taskTypeProviderRecoverySync, executeProviderRecoverySyncTask)
}

// CreateProviderRecoverySyncTask queues one explicit, provider-scoped reboot
// recovery. It deliberately uses the normal task pool so a slow SSH/Agent/API
// discovery never occupies an administrator HTTP request.
func CreateProviderRecoverySyncTask(providerID, requestedBy uint) (*adminModel.Task, error) {
	if global.APP_DB == nil {
		return nil, fmt.Errorf("数据库连接不可用")
	}
	if providerID == 0 || requestedBy == 0 {
		return nil, common.NewError(common.CodeValidationError, "强制恢复同步任务参数无效")
	}

	providerRecoveryTaskCreateMu.Lock()
	defer providerRecoveryTaskCreateMu.Unlock()
	taskService := task.GetTaskService()

	var created *adminModel.Task
	createErr := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// Lock the Provider row while checking active recovery tasks. This makes
		// two controllers clicking the same node serialize before either inserts
		// a pending task; the execution lease remains the protection for remote
		// discovery itself.
		var provider providerModel.Provider
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "status", "is_frozen", "frozen_reason", "expires_at", "connection_type", "agent_status", "execution_rule", "recovery_last_recovery_attempt_at", "recovery_lease_expires_at").
			First(&provider, providerID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return common.NewError(common.CodeNotFound, "Provider不存在")
			}
			return fmt.Errorf("读取Provider失败: %w", err)
		}
		now := time.Now()
		if provider.ExpiresAt != nil && !provider.ExpiresAt.After(now) {
			return common.NewError(common.CodeConflict, "Provider已过期，不能强制恢复同步")
		}
		if provider.IsFrozen && !isHealthAutoFrozen(provider) {
			return common.NewError(common.CodeConflict, "Provider已手工冻结，不能强制恢复同步")
		}
		if provider.Status != "active" && provider.Status != "partial" && provider.Status != "inactive" {
			return common.NewError(common.CodeConflict, "当前Provider状态不允许强制恢复同步")
		}
		if isReverseAgentUnavailable(provider) {
			return common.NewError(common.CodeConflict, "Agent未在线，无法执行强制恢复同步")
		}
		if provider.RecoveryLeaseExpiresAt != nil && provider.RecoveryLeaseExpiresAt.After(now) {
			return common.NewError(common.CodeConflict, "该节点已有恢复同步正在运行，请稍后重试")
		}
		if provider.RecoveryLastRecoveryAttemptAt != nil && provider.RecoveryLastRecoveryAttemptAt.After(now.Add(-instanceRecoveryManualCooldown)) {
			return common.NewError(common.CodeTooManyRequests, "该节点刚完成恢复同步，请稍后重试")
		}

		var activeCount int64
		if err := tx.Model(&adminModel.Task{}).
			Where("provider_id = ? AND task_type = ? AND status IN ?", providerID, taskTypeProviderRecoverySync,
				recoveryActiveTaskStatuses).
			Count(&activeCount).Error; err != nil {
			return fmt.Errorf("检查强制恢复同步任务失败: %w", err)
		}
		if activeCount > 0 {
			return common.NewError(common.CodeConflict, "该节点已有强制恢复同步任务，请在任务列表查看进度")
		}
		if err := taskService.EnsureTaskPoolAcceptingInTx(tx); err != nil {
			return err
		}

		providerIDCopy := providerID
		created = &adminModel.Task{
			UserID:            requestedBy,
			ProviderID:        &providerIDCopy,
			TaskType:          taskTypeProviderRecoverySync,
			Status:            "pending",
			TaskData:          "{}",
			TimeoutDuration:   int(instanceRecoveryManualTaskTimeout.Seconds()),
			EstimatedDuration: utils.GetEstimatedTaskDuration(taskTypeProviderRecoverySync, ""),
			IsForceStoppable:  true,
		}
		return tx.Create(created).Error
	})
	if createErr != nil {
		return nil, createErr
	}
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return created, nil
}

func executeProviderRecoverySyncTask(ctx context.Context, adminTask *adminModel.Task) error {
	if adminTask == nil || adminTask.ProviderID == nil || *adminTask.ProviderID == 0 {
		return fmt.Errorf("强制恢复同步任务缺少ProviderID")
	}
	providerID := *adminTask.ProviderID
	utils.UpdateTaskProgress(adminTask.ID, 5, "正在校验强制恢复同步资格")
	result, err := RecoverProviderManually(ctx, providerID)
	if err != nil {
		return err
	}
	utils.UpdateTaskProgress(adminTask.ID, 100, fmt.Sprintf(
		"强制恢复同步完成：远端 %d，已匹配 %d，已提交启动 %d；NDP=%t，出口重放=%t，控制端口修复=%t",
		result.DiscoveredInstances, result.MatchedInstances, result.QueuedStartTasks,
		result.NDPRecoveryAttempted, result.EgressReplayScheduled, result.TunnelRepairScheduled,
	))
	return nil
}

// RecoverProviderManually performs exactly one bounded discovery after taking
// the same tokenized DB lease used by the periodic scheduler. It is exported
// for the durable task executor and tests, not for HTTP handlers directly.
func RecoverProviderManually(ctx context.Context, providerID uint) (*ManualProviderRecoveryResult, error) {
	if global.APP_DB == nil {
		return nil, fmt.Errorf("数据库连接不可用")
	}
	if providerID == 0 {
		return nil, common.NewError(common.CodeValidationError, "ProviderID无效")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now()
	claim, claimed, err := claimManualProviderRecoveryContext(ctx, providerID, now)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, common.NewError(common.CodeConflict, "该节点正在恢复同步或刚完成同步，请稍后重试")
	}
	completed := false
	defer func() {
		if !completed {
			releaseProviderRecoveryLeaseContext(ctx, providerID, claim.Token)
		}
	}()

	candidate, err := loadManualRecoveryProviderContext(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if isReverseAgentUnavailable(candidate) {
		return nil, common.NewError(common.CodeConflict, "Agent未在线，无法执行强制恢复同步")
	}
	allowed, err := recoveryProviderMayBeProbedWithOptionsContext(ctx, providerID, true)
	if err != nil {
		return nil, fmt.Errorf("复核Provider强制恢复同步资格失败: %w", err)
	}
	if !allowed {
		return nil, common.NewError(common.CodeConflict, "Provider已冻结、过期或状态不允许强制恢复同步")
	}

	probeCtx, cancel := context.WithTimeout(ctx, instanceRecoveryRemoteProbeTimeout)
	report, err := adminProviderService.NewService().CompareInstancesWithRemoteForRecovery(probeCtx, providerID)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("Provider强制恢复同步远端核对失败: %w", err)
	}

	queued, err := enqueueRecoveredInstanceStartsWithOptionsContext(ctx, providerID, report.MatchedInstances, true)
	if err != nil {
		return nil, fmt.Errorf("提交Provider强制恢复启动任务失败: %w", err)
	}

	// Restore node-level facilities only after the short, guarded task-insert
	// transactions finish. These helpers do no remote discovery and are all
	// outside database transactions.
	if err := restoreNDPResponderAfterRecovery(ctx, candidate); err != nil {
		global.APP_LOG.Debug("NDP responder恢复未完成（不影响强制恢复同步）",
			zap.Uint("provider_id", candidate.ID), zap.Error(err))
	}
	if err := completeProviderRecoveryContext(ctx, candidate, claim); err != nil {
		return nil, fmt.Errorf("标记Provider强制恢复同步完成失败: %w", err)
	}
	completed = true
	scheduleProviderPostRecoveryRepairs(candidate)
	if queued > 0 {
		agentService.ScheduleProviderRecoveryRuntimeNetworkRefresh(global.APP_DB, candidate.ID)
		if global.APP_SCHEDULER != nil {
			global.APP_SCHEDULER.TriggerTaskProcessing()
		}
	}

	result := &ManualProviderRecoveryResult{
		ProviderID:           providerID,
		DiscoveredInstances:  report.TotalRemote,
		MatchedInstances:     len(report.MatchedInstances),
		QueuedStartTasks:     queued,
		NDPRecoveryAttempted: ndpRecoveryEligible(candidate),
		EgressReplayScheduled: candidate.IsReverseAgent() ||
			agentService.ProviderHasInstalledAgent(global.APP_DB, candidate.ID),
		TunnelRepairScheduled: candidate.IsReverseAgent(),
	}
	global.APP_LOG.Info("Provider强制恢复同步完成",
		zap.Uint("provider_id", providerID),
		zap.String("provider", candidate.Name),
		zap.Int("remote_instances", result.DiscoveredInstances),
		zap.Int("matched_instances", result.MatchedInstances),
		zap.Int("start_tasks", result.QueuedStartTasks))
	return result, nil
}

func claimManualProviderRecovery(providerID uint, now time.Time) (*providerRecoveryClaim, bool, error) {
	return claimManualProviderRecoveryContext(context.Background(), providerID, now)
}

func claimManualProviderRecoveryContext(ctx context.Context, providerID uint, now time.Time) (*providerRecoveryClaim, bool, error) {
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
	result := manualRecoveryProviderCandidateScope(
		global.APP_DB.WithContext(ctx).Model(&providerModel.Provider{}).Where("id = ?", providerID), now,
	).Updates(map[string]interface{}{
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

// manualRecoveryProviderCandidateScope intentionally does not require a
// half-hour offline marker: an administrator may repair a node whose health
// status has not yet converged. It retains the short click cooldown, expiry,
// manual-freeze, and cross-controller lease guards.
func manualRecoveryProviderCandidateScope(db *gorm.DB, now time.Time) *gorm.DB {
	return db.
		Where("expires_at IS NULL OR expires_at > ?", now).
		Where("status IN ?", []string{"active", "partial", "inactive"}).
		Where("is_frozen = ? OR frozen_reason LIKE ? OR frozen_reason LIKE ?", false,
			healthAutoFreezeReasonPattern(), agentHealthAutoFreezeReasonPattern()).
		Where("recovery_lease_expires_at IS NULL OR recovery_lease_expires_at <= ?", now).
		Where("recovery_last_recovery_attempt_at IS NULL OR recovery_last_recovery_attempt_at <= ?", now.Add(-instanceRecoveryManualCooldown))
}

func loadManualRecoveryProvider(providerID uint) (providerModel.Provider, error) {
	return loadManualRecoveryProviderContext(context.Background(), providerID)
}

func loadManualRecoveryProviderContext(ctx context.Context, providerID uint) (providerModel.Provider, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var provider providerModel.Provider
	if err := global.APP_DB.WithContext(ctx).
		Select("id", "name", "type", "status", "is_frozen", "frozen_reason", "expires_at", "connection_type", "agent_status", "execution_rule", "recovery_offline_since").
		Where("id = ?", providerID).
		First(&provider).Error; err != nil {
		return providerModel.Provider{}, fmt.Errorf("读取Provider强制恢复同步信息失败: %w", err)
	}
	return provider, nil
}

func ndpRecoveryEligible(candidate providerModel.Provider) bool {
	if strings.EqualFold(strings.TrimSpace(candidate.ExecutionRule), "api_only") {
		return false
	}
	_, ok := ndpRecoveryCommand(candidate.Type)
	return ok
}
