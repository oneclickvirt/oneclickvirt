package scheduler

import (
	"context"
	"sync"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	adminProviderService "oneclickvirt/service/admin/provider"
	agentService "oneclickvirt/service/agent"
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
)

// statusMismatchRecord tracks consecutive status mismatch detections for multi-confirm.
type statusMismatchRecord struct {
	InstanceID    uint
	ProviderID    uint
	RemoteStatus  string
	DBStatus      string
	ConfirmCount  int
	FirstDetected time.Time
	LastDetected  time.Time
}

const (
	// requiredConfirmations is how many consecutive checks must agree before applying a status change.
	requiredConfirmations = 3
	// mismatchExpiry is how long a mismatch record stays valid before being discarded.
	mismatchExpiry = 2 * time.Hour
)

// InstanceSyncSchedulerService Provider实例同步调度服务
type InstanceSyncSchedulerService struct {
	providerService *adminProviderService.Service
	stopChan        chan struct{}
	mu              sync.RWMutex
	isRunning       bool
	maxConcurrency  int
	semaphore       chan struct{}
	syncMu          sync.Mutex // 防止整轮Provider实例同步重叠
	refreshMu       sync.Mutex // 防止缺失网卡刷新任务重叠

	// mismatchTracker stores pending status mismatch records keyed by instanceID.
	mismatchMu      sync.Mutex
	mismatchTracker map[uint]*statusMismatchRecord
}

// NewInstanceSyncSchedulerService 创建实例同步调度服务
func NewInstanceSyncSchedulerService() *InstanceSyncSchedulerService {
	maxConcurrency := 2
	return &InstanceSyncSchedulerService{
		providerService: adminProviderService.NewService(),
		stopChan:        make(chan struct{}),
		isRunning:       false,
		maxConcurrency:  maxConcurrency,
		semaphore:       make(chan struct{}, maxConcurrency),
		mismatchTracker: make(map[uint]*statusMismatchRecord),
	}
}

// Start 启动实例同步调度器
func (s *InstanceSyncSchedulerService) Start(ctx context.Context) {
	if !global.GetAppConfig().System.EnableInstanceSync {
		global.APP_LOG.Debug("实例同步功能未启用，跳过调度器启动")
		return
	}

	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		global.APP_LOG.Warn("Provider实例同步调度器已在运行中")
		return
	}
	s.stopChan = make(chan struct{})
	s.isRunning = true
	s.mu.Unlock()

	global.APP_LOG.Info("启动Provider实例同步调度器",
		zap.Int("syncInterval", global.GetAppConfig().System.InstanceSyncInterval),
		zap.Int("requiredConfirmations", requiredConfirmations))

	go s.startSyncTask(ctx)
}

// Stop 停止实例同步调度器
func (s *InstanceSyncSchedulerService) Stop() {
	s.mu.Lock()
	if !s.isRunning {
		s.mu.Unlock()
		return
	}
	s.isRunning = false
	s.mu.Unlock()

	global.APP_LOG.Info("停止Provider实例同步调度器")
	close(s.stopChan)
}

// IsRunning 检查调度器是否正在运行
func (s *InstanceSyncSchedulerService) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

// startSyncTask 启动实例同步任务
func (s *InstanceSyncSchedulerService) startSyncTask(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Error("Provider实例同步goroutine panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Info("Provider实例同步任务已停止")
	}()

	// 延迟启动，等待系统初始化完成
	time.Sleep(2 * time.Minute)

	// 首次执行
	s.syncAllProvidersInstances()

	syncInterval := global.GetAppConfig().System.InstanceSyncInterval
	if syncInterval <= 0 {
		syncInterval = 30
	}

	ticker := time.NewTicker(time.Duration(syncInterval) * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopChan:
			return
		case <-ticker.C:
			if global.APP_DB == nil {
				continue
			}
			s.syncAllProvidersInstances()
		}
	}
}

// syncAllProvidersInstances 同步所有Provider的实例
func (s *InstanceSyncSchedulerService) syncAllProvidersInstances() {
	if !s.syncMu.TryLock() {
		global.APP_LOG.Debug("Provider实例同步仍在运行中，跳过本轮触发")
		return
	}
	defer s.syncMu.Unlock()

	startTime := time.Now()
	global.APP_LOG.Debug("开始Provider实例同步检查")

	var providers []providerModel.Provider
	if err := global.APP_DB.Where("status = ? AND is_frozen = ? AND (expires_at IS NULL OR expires_at > ?)",
		"active", false, time.Now()).
		Select("id", "name", "type").
		Find(&providers).Error; err != nil {
		global.APP_LOG.Error("查询Provider列表失败", zap.Error(err))
		return
	}

	if len(providers) == 0 {
		global.APP_LOG.Debug("没有活跃的Provider需要同步")
		return
	}

	global.APP_LOG.Debug("准备同步Provider实例", zap.Int("providerCount", len(providers)))

	var wg sync.WaitGroup
	successCount := 0
	failedCount := 0
	changedCount := 0
	appliedCount := 0
	var mu sync.Mutex

	for _, prov := range providers {
		wg.Add(1)
		s.semaphore <- struct{}{}

		go func(provider providerModel.Provider) {
			defer func() {
				if r := recover(); r != nil {
					global.APP_LOG.Error("Provider实例同步panic",
						zap.Uint("providerId", provider.ID),
						zap.String("providerName", provider.Name),
						zap.Any("panic", r))
				}
				<-s.semaphore
				wg.Done()
			}()

			report, err := s.providerService.CompareInstancesWithRemote(context.Background(), provider.ID)
			if err != nil {
				global.APP_LOG.Warn("Provider实例同步失败",
					zap.Uint("providerId", provider.ID),
					zap.String("providerName", provider.Name),
					zap.Error(err))
				mu.Lock()
				failedCount++
				mu.Unlock()
				return
			}

			mu.Lock()
			successCount++
			totalChanges := len(report.NewInstances) + len(report.DeletedInstances) + len(report.ChangedInstances)
			changedCount += totalChanges
			mu.Unlock()

			if totalChanges > 0 {
				global.APP_LOG.Warn("检测到Provider实例变化",
					zap.Uint("providerId", provider.ID),
					zap.String("providerName", provider.Name),
					zap.Int("newInstances", len(report.NewInstances)),
					zap.Int("deletedInstances", len(report.DeletedInstances)),
					zap.Int("changedInstances", len(report.ChangedInstances)))
			}

			// Process status changes through multi-confirm mechanism.
			// Only apply status changes for STABLE instances (running/stopped).
			// Never auto-update transitional or terminal states.
			applied := s.processStatusChanges(report)
			mu.Lock()
			appliedCount += applied
			mu.Unlock()
		}(prov)
	}

	wg.Wait()

	// Cleanup expired mismatch records
	s.cleanupExpiredMismatchRecords()

	// Refresh network interfaces for running instances that have not been recorded yet.
	// Run in a goroutine so it does not block the next sync cycle.
	go s.refreshMissingInterfaces()

	duration := time.Since(startTime)
	global.APP_LOG.Debug("Provider实例同步检查完成",
		zap.Int("totalProviders", len(providers)),
		zap.Int("successCount", successCount),
		zap.Int("failedCount", failedCount),
		zap.Int("totalChanges", changedCount),
		zap.Int("appliedChanges", appliedCount),
		zap.Duration("duration", duration))
}

// processStatusChanges handles status change detection with multi-confirmation.
// Returns the number of actually applied changes.
func (s *InstanceSyncSchedulerService) processStatusChanges(report *adminProviderService.InstanceSyncReport) int {
	if report == nil || len(report.ChangedInstances) == 0 {
		return 0
	}

	now := time.Now()
	applied := 0

	s.mismatchMu.Lock()
	defer s.mismatchMu.Unlock()

	for _, change := range report.ChangedInstances {
		// Only reconcile stable status transitions.
		// Skip transitional states (creating, resetting) — those are managed by task system.
		if constant.IsTransitionalStatus(change.OldStatus) || constant.IsTerminalStatus(change.OldStatus) {
			global.APP_LOG.Debug("跳过非稳定状态实例的同步",
				zap.Uint("instanceId", change.InstanceID),
				zap.String("oldStatus", change.OldStatus),
				zap.String("newStatus", change.NewStatus))
			continue
		}

		// Only allow sync to other stable states (e.g. running↔stopped).
		if !constant.IsStableStatus(change.NewStatus) && change.NewStatus != "error" {
			global.APP_LOG.Debug("跳过远端非稳定目标状态",
				zap.Uint("instanceId", change.InstanceID),
				zap.String("remoteStatus", change.NewStatus))
			continue
		}

		rec, exists := s.mismatchTracker[change.InstanceID]

		if !exists {
			// First detection of this mismatch — start tracking.
			s.mismatchTracker[change.InstanceID] = &statusMismatchRecord{
				InstanceID:    change.InstanceID,
				ProviderID:    report.ProviderID,
				RemoteStatus:  change.NewStatus,
				DBStatus:      change.OldStatus,
				ConfirmCount:  1,
				FirstDetected: now,
				LastDetected:  now,
			}
			global.APP_LOG.Debug("首次检测到实例状态不一致",
				zap.Uint("instanceId", change.InstanceID),
				zap.String("dbStatus", change.OldStatus),
				zap.String("remoteStatus", change.NewStatus),
				zap.Int("confirmCount", 1))
			continue
		}

		// If remote status changed again (e.g. was stopped, now running again), reset tracker.
		if rec.RemoteStatus != change.NewStatus {
			rec.RemoteStatus = change.NewStatus
			rec.ConfirmCount = 1
			rec.FirstDetected = now
			rec.LastDetected = now
			global.APP_LOG.Debug("实例远端状态发生变化，重置确认计数",
				zap.Uint("instanceId", change.InstanceID),
				zap.String("newRemoteStatus", change.NewStatus))
			continue
		}

		// Same mismatch detected again — increment confirmation count.
		rec.ConfirmCount++
		rec.LastDetected = now

		global.APP_LOG.Debug("实例状态不一致再次确认",
			zap.Uint("instanceId", change.InstanceID),
			zap.String("dbStatus", change.OldStatus),
			zap.String("remoteStatus", change.NewStatus),
			zap.Int("confirmCount", rec.ConfirmCount),
			zap.Int("required", requiredConfirmations))

		if rec.ConfirmCount >= requiredConfirmations {
			// Multi-confirmed: apply the status change.
			if err := s.applyStatusChange(change.InstanceID, change.OldStatus, change.NewStatus); err != nil {
				global.APP_LOG.Error("应用实例状态变更失败",
					zap.Uint("instanceId", change.InstanceID),
					zap.String("from", change.OldStatus),
					zap.String("to", change.NewStatus),
					zap.Error(err))
				// Don't remove the tracker — will retry next cycle.
				continue
			}

			global.APP_LOG.Info("实例状态已通过多次确认后同步",
				zap.Uint("instanceId", change.InstanceID),
				zap.String("from", change.OldStatus),
				zap.String("to", change.NewStatus),
				zap.Int("confirmations", rec.ConfirmCount),
				zap.Duration("detectionSpan", now.Sub(rec.FirstDetected)))

			delete(s.mismatchTracker, change.InstanceID)
			applied++
		}
	}

	// Clear tracker entries for instances of THIS provider whose status now matches (resolved).
	// Only touch entries belonging to the current report's provider.
	stillMismatchedIDs := make(map[uint]bool)
	for _, change := range report.ChangedInstances {
		stillMismatchedIDs[change.InstanceID] = true
	}
	for id, rec := range s.mismatchTracker {
		if rec.ProviderID != report.ProviderID {
			continue // Don't touch entries from other providers.
		}
		if !stillMismatchedIDs[id] {
			// The instance was in tracker but is no longer showing as mismatched.
			global.APP_LOG.Debug("实例状态已恢复一致，清除追踪记录",
				zap.Uint("instanceId", id))
			delete(s.mismatchTracker, id)
		}
	}

	return applied
}

// applyStatusChange updates the instance status in the database.
// Only updates if the current DB status still matches expectedOldStatus (optimistic locking).
func (s *InstanceSyncSchedulerService) applyStatusChange(instanceID uint, expectedOldStatus, newStatus string) error {
	result := global.APP_DB.Model(&providerModel.Instance{}).
		Where("id = ? AND status = ?", instanceID, expectedOldStatus).
		Update("status", newStatus)

	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		global.APP_LOG.Warn("状态变更未应用：数据库状态已发生变化",
			zap.Uint("instanceId", instanceID),
			zap.String("expectedOld", expectedOldStatus),
			zap.String("targetNew", newStatus))
	}

	return nil
}

// cleanupExpiredMismatchRecords removes stale mismatch tracking records.
func (s *InstanceSyncSchedulerService) cleanupExpiredMismatchRecords() {
	s.mismatchMu.Lock()
	defer s.mismatchMu.Unlock()

	now := time.Now()
	for id, rec := range s.mismatchTracker {
		if now.Sub(rec.LastDetected) > mismatchExpiry {
			global.APP_LOG.Debug("清除过期的状态不一致追踪记录",
				zap.Uint("instanceId", id),
				zap.Duration("age", now.Sub(rec.FirstDetected)))
			delete(s.mismatchTracker, id)
		}
	}
}

// refreshMissingInterfaces detects and persists pmacct_interface_v4/v6 for running
// instances that have not yet had their host-side network interface recorded.
// It groups instances by provider to reuse SSH connections and avoids N+1 DB queries.
func (s *InstanceSyncSchedulerService) refreshMissingInterfaces() {
	if !s.refreshMu.TryLock() {
		global.APP_LOG.Debug("refreshMissingInterfaces 已在运行中，跳过重叠触发")
		return
	}
	defer s.refreshMu.Unlock()

	// Query running instances that need interface detection:
	// 1. V4 interface is missing (fresh instance or never detected)
	// 2. IPv6-capable instances where V6 is missing OR V6 equals V4
	//    (V6==V4 indicates old buggy data where V6 was never separately detected)
	ipv6Types := []string{"nat_ipv4_ipv6", "dedicated_ipv4_ipv6", "ipv6_only"}
	var instances []providerModel.Instance
	if err := global.APP_DB.
		Where(
			"status = ? AND ("+
				"pmacct_interface_v4 = '' OR pmacct_interface_v4 IS NULL "+
				"OR (network_type IN ? AND ("+
				"pmacct_interface_v6 = '' OR pmacct_interface_v6 IS NULL "+
				"OR pmacct_interface_v6 = pmacct_interface_v4))"+
				")",
			"running", ipv6Types,
		).
		Find(&instances).Error; err != nil {
		global.APP_LOG.Warn("refreshMissingInterfaces: 查询实例失败", zap.Error(err))
		return
	}
	if len(instances) == 0 {
		return
	}

	global.APP_LOG.Debug("开始刷新缺失的网络接口记录", zap.Int("instanceCount", len(instances)))

	// Group by provider to reuse the provider connection.
	byProvider := make(map[uint][]*providerModel.Instance)
	for i := range instances {
		pid := instances[i].ProviderID
		byProvider[pid] = append(byProvider[pid], &instances[i])
	}

	for providerID, provInstances := range byProvider {
		prov, err := providerService.GetProviderInstanceByID(providerID)
		if err != nil {
			global.APP_LOG.Debug("refreshMissingInterfaces: 跳过未连接的provider",
				zap.Uint("providerId", providerID), zap.Error(err))
			continue
		}

		for _, inst := range provInstances {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := agentService.DetectAndSaveInstanceInterfaces(ctx, global.APP_DB, prov, inst, ""); err != nil {
				global.APP_LOG.Debug("refreshMissingInterfaces: 接口检测失败",
					zap.Uint("instanceId", inst.ID),
					zap.String("instanceName", inst.Name),
					zap.Error(err))
			}
			cancel()
		}
	}
}
