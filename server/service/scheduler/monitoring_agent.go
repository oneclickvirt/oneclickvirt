package scheduler

import (
	"context"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"

	"go.uber.org/zap"
)

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

				lastAgentCollect[cfg.ProviderID] = now

				s.wg.Add(1)
				go func(providerID uint, config monitoringModel.MonitoringConfig) {
					defer s.wg.Done()
					defer func() {
						if r := recover(); r != nil {
							global.APP_LOG.Error("agent traffic sync panic",
								zap.Uint("provider_id", providerID),
								zap.Any("panic", r),
								zap.Stack("stack"))
						}
					}()

					syncCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
					defer cancel()

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

				lastResourceCollect[cfg.ProviderID] = now

				s.wg.Add(1)
				go func(providerID uint, config monitoringModel.MonitoringConfig) {
					defer s.wg.Done()
					defer func() {
						if r := recover(); r != nil {
							global.APP_LOG.Error("agent resource sync panic",
								zap.Uint("provider_id", providerID),
								zap.Any("panic", r),
								zap.Stack("stack"))
						}
					}()

					syncCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()

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
