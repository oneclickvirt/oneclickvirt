package scheduler

import (
	"context"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
)

const backgroundMonitorRepairLimit = 8

type agentReconcileSchedule struct {
	Failures int
	NextRun  time.Time
}

func agentReconcileSuccessDelay(providerID uint) time.Duration {
	jitterSeconds := (uint64(providerID) * 2654435761) % 60
	return 5*time.Minute + time.Duration(jitterSeconds)*time.Second
}

func (s *MonitoringSchedulerService) agentReconcileDue(providerID uint, now time.Time) bool {
	value, ok := s.agentReconcileState.Load(providerID)
	if !ok {
		return true
	}
	schedule, ok := value.(agentReconcileSchedule)
	return !ok || !now.Before(schedule.NextRun)
}

func (s *MonitoringSchedulerService) finishAgentReconcile(providerID uint, success, deferred bool) {
	now := time.Now()
	if success {
		delay := agentReconcileSuccessDelay(providerID)
		if deferred {
			delay = 30*time.Second + time.Duration(providerID%15)*time.Second
		}
		s.agentReconcileState.Store(providerID, agentReconcileSchedule{
			NextRun: now.Add(delay),
		})
		return
	}

	failures := 1
	if value, ok := s.agentReconcileState.Load(providerID); ok {
		if previous, valid := value.(agentReconcileSchedule); valid {
			failures = previous.Failures + 1
		}
	}
	shift := failures - 1
	if shift > 5 {
		shift = 5
	}
	delay := time.Minute * time.Duration(1<<shift)
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	s.agentReconcileState.Store(providerID, agentReconcileSchedule{
		Failures: failures,
		NextRun:  now.Add(delay),
	})
}

func (s *MonitoringSchedulerService) startAgentCollection(ctx context.Context) {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Error("agent traffic collection panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Info("agent traffic collection stopped")
	}()

	global.APP_LOG.Info("starting agent traffic collection")

	// Wait for DB
	for global.APP_DB == nil {
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-s.stopChan:
			timer.Stop()
			return
		case <-timer.C:
			timer.Stop()
		}
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Track last collection time per provider
	lastAgentCollect := make(map[uint]time.Time)

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			// Get all providers with agent mode monitoring
			var configs []monitoringModel.MonitoringConfig
			if err := global.APP_DB.Where("monitoring_mode = ? AND agent_installed = ?", "agent", true).
				Find(&configs).Error; err != nil {
				global.APP_LOG.Error("query agent monitoring configs failed", zap.Error(err))
				continue
			}

			// Pre-filter: only keep configs whose provider still exists
			if len(configs) > 0 {
				candidateIDs := make([]uint, 0, len(configs))
				for _, cfg := range configs {
					candidateIDs = append(candidateIDs, cfg.ProviderID)
				}
				var existingIDs []uint
				if err := global.APP_DB.Model(&providerModel.Provider{}).
					Where("id IN ?", candidateIDs).
					Pluck("id", &existingIDs).Error; err != nil {
					global.APP_LOG.Error("query existing providers failed", zap.Error(err))
					continue
				}
				existingSet := make(map[uint]bool, len(existingIDs))
				for _, id := range existingIDs {
					existingSet[id] = true
				}
				filtered := configs[:0]
				for _, cfg := range configs {
					if existingSet[cfg.ProviderID] {
						filtered = append(filtered, cfg)
					}
				}
				configs = filtered
			}

			now := time.Now()

			// Batch load provider traffic settings to avoid N+1 queries
			providerIDs := make([]uint, 0, len(configs))
			for _, cfg := range configs {
				providerIDs = append(providerIDs, cfg.ProviderID)
			}
			var providers []providerModel.Provider
			if err := global.APP_DB.Select("id, enable_traffic_control, traffic_sync_method").
				Where("id IN ?", providerIDs).Find(&providers).Error; err != nil {
				continue
			}
			providerMap := make(map[uint]providerModel.Provider, len(providers))
			for _, p := range providers {
				providerMap[p.ID] = p
			}

			for _, cfg := range configs {
				// Check collection interval
				interval := time.Duration(cfg.CollectInterval) * time.Second
				if interval < 5*time.Second {
					interval = 5 * time.Second
				}

				last, ok := lastAgentCollect[cfg.ProviderID]
				if ok && now.Sub(last) < interval {
					continue
				}

				// Check if provider has traffic control enabled and uses agent sync method
				p, ok := providerMap[cfg.ProviderID]
				if !ok {
					continue
				}
				if !p.EnableTrafficControl || p.TrafficSyncMethod != "agent" {
					continue
				}

				if !s.tryAcquireAgentWorkSlot() {
					global.APP_LOG.Debug("agent work slots full, defer traffic sync",
						zap.Uint("provider_id", cfg.ProviderID))
					continue
				}
				if !s.tryStartAgentProviderWork(cfg.ProviderID) {
					s.releaseAgentWorkSlot()
					global.APP_LOG.Debug("agent traffic sync already running, skip overlapping run",
						zap.Uint("provider_id", cfg.ProviderID))
					continue
				}

				lastAgentCollect[cfg.ProviderID] = now

				s.wg.Add(1)
				go func(providerID uint, config monitoringModel.MonitoringConfig) {
					defer s.wg.Done()
					defer s.releaseAgentWorkSlot()
					defer s.finishAgentProviderWork(providerID)
					defer func() {
						if r := recover(); r != nil {
							global.APP_LOG.Error("agent traffic sync panic",
								zap.Uint("provider_id", providerID),
								zap.Any("panic", r),
								zap.Stack("stack"))
						}
					}()

					baseCtx, baseCancel := context.WithCancel(context.Background())
					go func() {
						select {
						case <-s.stopChan:
							baseCancel()
						case <-baseCtx.Done():
						}
					}()
					syncCtx, cancel := context.WithTimeout(baseCtx, 3*time.Minute)
					defer func() { cancel(); baseCancel() }()

					syncSvc := agentService.NewSyncService(syncCtx, global.APP_DB)
					if err := syncSvc.SyncProviderTraffic(providerID, &config); err != nil {
						global.APP_LOG.Error("agent traffic sync failed",
							zap.Uint("provider_id", providerID),
							zap.Error(err))
					} else {
						global.APP_LOG.Debug("agent traffic sync completed",
							zap.Uint("provider_id", providerID))
					}
				}(cfg.ProviderID, cfg)
			}
		}
	}
}

// startAgentResourceCollection starts agent-based resource monitoring collection.
func (s *MonitoringSchedulerService) startAgentResourceCollection(ctx context.Context) {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Error("agent resource collection panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Info("agent resource collection stopped")
	}()

	global.APP_LOG.Info("starting agent resource collection")

	// Wait for DB
	for global.APP_DB == nil {
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-s.stopChan:
			timer.Stop()
			return
		case <-timer.C:
			timer.Stop()
		}
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	lastResourceCollect := make(map[uint]time.Time)

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			var configs []monitoringModel.MonitoringConfig
			if err := global.APP_DB.Where("monitoring_mode = ? AND agent_installed = ?", "agent", true).
				Find(&configs).Error; err != nil {
				continue
			}

			// Pre-filter: only keep configs whose provider still exists
			if len(configs) > 0 {
				candidateIDs := make([]uint, 0, len(configs))
				for _, cfg := range configs {
					candidateIDs = append(candidateIDs, cfg.ProviderID)
				}
				var existingIDs []uint
				if err := global.APP_DB.Model(&providerModel.Provider{}).
					Where("id IN ?", candidateIDs).
					Pluck("id", &existingIDs).Error; err != nil {
					continue
				}
				existingSet := make(map[uint]bool, len(existingIDs))
				for _, id := range existingIDs {
					existingSet[id] = true
				}
				filtered := configs[:0]
				for _, cfg := range configs {
					if existingSet[cfg.ProviderID] {
						filtered = append(filtered, cfg)
					}
				}
				configs = filtered
			}

			now := time.Now()
			for _, cfg := range configs {
				interval := time.Duration(cfg.ResourceCollectInterval) * time.Second
				if interval < 10*time.Second {
					interval = 30 * time.Second
				}

				last, ok := lastResourceCollect[cfg.ProviderID]
				if ok && now.Sub(last) < interval {
					continue
				}

				if !s.tryAcquireAgentWorkSlot() {
					global.APP_LOG.Debug("agent work slots full, defer resource sync",
						zap.Uint("provider_id", cfg.ProviderID))
					continue
				}
				if !s.tryStartAgentProviderWork(cfg.ProviderID) {
					s.releaseAgentWorkSlot()
					global.APP_LOG.Debug("agent resource sync already running, skip overlapping run",
						zap.Uint("provider_id", cfg.ProviderID))
					continue
				}

				lastResourceCollect[cfg.ProviderID] = now

				s.wg.Add(1)
				go func(providerID uint, config monitoringModel.MonitoringConfig) {
					defer s.wg.Done()
					defer s.releaseAgentWorkSlot()
					defer s.finishAgentProviderWork(providerID)
					defer func() {
						if r := recover(); r != nil {
							global.APP_LOG.Error("agent resource sync panic",
								zap.Uint("provider_id", providerID),
								zap.Any("panic", r),
								zap.Stack("stack"))
						}
					}()

					baseCtx, baseCancel := context.WithCancel(context.Background())
					go func() {
						select {
						case <-s.stopChan:
							baseCancel()
						case <-baseCtx.Done():
						}
					}()
					syncCtx, cancel := context.WithTimeout(baseCtx, 2*time.Minute)
					defer func() { cancel(); baseCancel() }()

					resSvc := agentService.NewResourceSyncService(syncCtx, global.APP_DB)
					if err := resSvc.SyncProviderResources(providerID, &config); err != nil {
						global.APP_LOG.Error("agent resource sync failed",
							zap.Uint("provider_id", providerID),
							zap.Error(err))
					}

					// Cleanup old metrics periodically
					if now.Minute() == 0 { // top of each hour
						if err := resSvc.CleanupOldResourceMetrics(); err != nil {
							global.APP_LOG.Warn("cleanup old resource metrics failed", zap.Error(err))
						}
					}
				}(cfg.ProviderID, cfg)
			}
		}
	}
}

// startAgentMonitorReconciliation periodically compares controller metadata with the
// Agent's desired/active binding health and only probes provider interfaces on drift.
func (s *MonitoringSchedulerService) startAgentMonitorReconciliation(ctx context.Context) {
	defer s.wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			global.APP_LOG.Error("agent monitor reconciliation panic",
				zap.Any("panic", recovered),
				zap.Stack("stack"))
		}
		global.APP_LOG.Info("agent monitor reconciliation stopped")
	}()

	global.APP_LOG.Info("starting agent monitor reconciliation")
	for global.APP_DB == nil {
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-s.stopChan:
			timer.Stop()
			return
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopChan:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			var configs []monitoringModel.MonitoringConfig
			if err := global.APP_DB.Where("monitoring_mode = ? AND agent_installed = ?", "agent", true).
				Find(&configs).Error; err != nil {
				global.APP_LOG.Error("query agent reconcile configs failed", zap.Error(err))
				continue
			}
			if len(configs) == 0 {
				continue
			}

			providerIDs := make([]uint, 0, len(configs))
			for _, config := range configs {
				providerIDs = append(providerIDs, config.ProviderID)
			}
			var providers []providerModel.Provider
			if err := global.APP_DB.Select("id", "enable_traffic_control", "traffic_sync_method").
				Where("id IN ?", providerIDs).Find(&providers).Error; err != nil {
				global.APP_LOG.Error("query providers for agent reconcile failed", zap.Error(err))
				continue
			}
			providerByID := make(map[uint]providerModel.Provider, len(providers))
			for _, providerRecord := range providers {
				providerByID[providerRecord.ID] = providerRecord
			}

			now := time.Now()
			for _, config := range configs {
				providerRecord, exists := providerByID[config.ProviderID]
				if !exists || !providerRecord.EnableTrafficControl || providerRecord.TrafficSyncMethod != "agent" {
					continue
				}
				if !s.agentReconcileDue(config.ProviderID, now) {
					continue
				}
				if !s.tryAcquireAgentWorkSlot() {
					continue
				}
				if !s.tryStartAgentProviderWork(config.ProviderID) {
					s.releaseAgentWorkSlot()
					continue
				}

				s.wg.Add(1)
				go func(providerID uint, monitoringConfig monitoringModel.MonitoringConfig) {
					defer s.wg.Done()
					defer s.releaseAgentWorkSlot()
					defer s.finishAgentProviderWork(providerID)
					success := false
					deferred := false
					defer func() {
						s.finishAgentReconcile(providerID, success, deferred)
						if recovered := recover(); recovered != nil {
							global.APP_LOG.Error("provider agent monitor reconciliation panic",
								zap.Uint("provider_id", providerID),
								zap.Any("panic", recovered),
								zap.Stack("stack"))
						}
					}()

					baseCtx, baseCancel := context.WithCancel(ctx)
					go func() {
						select {
						case <-s.stopChan:
							baseCancel()
						case <-baseCtx.Done():
						}
					}()
					reconcileCtx, cancel := context.WithTimeout(baseCtx, 6*time.Minute)
					defer func() { cancel(); baseCancel() }()

					providerInstance, err := providerService.GetProviderInstanceByID(providerID)
					if err != nil {
						global.APP_LOG.Warn("agent monitor reconcile provider unavailable",
							zap.Uint("provider_id", providerID),
							zap.Error(err))
						return
					}
					monitorService := agentService.NewMonitorService(reconcileCtx, global.APP_DB)
					summary, err := monitorService.ReconcileMonitorsForProvider(
						providerInstance,
						providerID,
						&monitoringConfig,
						backgroundMonitorRepairLimit,
					)
					if err != nil {
						global.APP_LOG.Warn("agent monitor reconciliation failed",
							zap.Uint("provider_id", providerID),
							zap.Error(err))
						return
					}
					success = summary.Failed == 0
					deferred = summary.Deferred > 0
					global.APP_LOG.Debug("agent monitor reconciliation completed",
						zap.Uint("provider_id", providerID),
						zap.Int("created", summary.Created),
						zap.Int("updated", summary.Updated),
						zap.Int("deferred", summary.Deferred),
						zap.Int("failed", summary.Failed))
				}(config.ProviderID, config)
			}
		}
	}
}
