package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/gorm"
)

const configuredAgentTrafficSyncWorkers = 4

// configuredAgentTrafficSyncCandidate is a fully loaded Agent-capable provider.
// It keeps configuration lookup separate from remote Agent calls so callers do
// not issue a database query for every provider or hold a transaction open.
type configuredAgentTrafficSyncCandidate struct {
	provider providerModel.Provider
	config   monitoringModel.MonitoringConfig
}

// SyncConfiguredAgentTraffic refreshes traffic from enabled Agent providers.
// A nil providerIDs slice means every eligible provider; a concrete empty slice
// means the caller has no affected provider and performs no remote pull.
func SyncConfiguredAgentTraffic(ctx context.Context, db *gorm.DB, providerIDs []uint) error {
	if db == nil {
		return fmt.Errorf("agent traffic sync database is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	providerIDs, allProviders := configuredAgentTrafficSyncScope(providerIDs)
	// nil is the explicit all-providers sentinel used by the global manual
	// refresh. A concrete empty scope means the caller has no affected providers
	// and must never expand into an accidental fleet-wide Agent pull.
	if !allProviders && len(providerIDs) == 0 {
		return nil
	}

	candidates, err := loadConfiguredAgentTrafficSyncCandidates(db.WithContext(ctx), providerIDs)
	if err != nil || len(candidates) == 0 {
		return err
	}

	return syncConfiguredAgentTrafficCandidates(ctx, db, candidates)
}

func configuredAgentTrafficSyncScope(providerIDs []uint) ([]uint, bool) {
	if providerIDs == nil {
		return nil, true
	}
	return uniqueSortedProviderIDs(providerIDs), false
}

func loadConfiguredAgentTrafficSyncCandidates(db *gorm.DB, providerIDs []uint) ([]configuredAgentTrafficSyncCandidate, error) {
	ids := uniqueSortedProviderIDs(providerIDs)
	configQuery := db.Where("monitoring_mode = ? AND agent_installed = ?", "agent", true)
	if len(ids) > 0 {
		configQuery = configQuery.Where("provider_id IN ?", ids)
	}

	var configs []monitoringModel.MonitoringConfig
	if err := configQuery.Find(&configs).Error; err != nil {
		return nil, fmt.Errorf("list agent monitoring configs: %w", err)
	}
	if len(configs) == 0 {
		return nil, nil
	}

	configuredProviderIDs := make([]uint, 0, len(configs))
	for _, config := range configs {
		configuredProviderIDs = append(configuredProviderIDs, config.ProviderID)
	}
	configuredProviderIDs = uniqueSortedProviderIDs(configuredProviderIDs)

	var providers []providerModel.Provider
	if err := db.Where("id IN ? AND enable_traffic_control = ? AND traffic_sync_method = ?",
		configuredProviderIDs, true, "agent").Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("list enabled agent traffic providers: %w", err)
	}

	return selectConfiguredAgentTrafficSyncCandidates(providers, configs), nil
}

func selectConfiguredAgentTrafficSyncCandidates(
	providers []providerModel.Provider,
	configs []monitoringModel.MonitoringConfig,
) []configuredAgentTrafficSyncCandidate {
	configByProviderID := make(map[uint]monitoringModel.MonitoringConfig, len(configs))
	for _, config := range configs {
		if config.ProviderID == 0 || config.MonitoringMode != "agent" || !config.AgentInstalled {
			continue
		}
		configByProviderID[config.ProviderID] = config
	}

	candidates := make([]configuredAgentTrafficSyncCandidate, 0, len(providers))
	for _, provider := range providers {
		if !provider.EnableTrafficControl || provider.TrafficSyncMethod != "agent" {
			continue
		}
		config, ok := configByProviderID[provider.ID]
		if !ok {
			continue
		}
		candidates = append(candidates, configuredAgentTrafficSyncCandidate{
			provider: provider,
			config:   config,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].provider.ID < candidates[j].provider.ID
	})
	return candidates
}

func syncConfiguredAgentTrafficCandidates(
	ctx context.Context,
	db *gorm.DB,
	candidates []configuredAgentTrafficSyncCandidate,
) error {
	workerCount := configuredAgentTrafficSyncWorkers
	if workerCount > len(candidates) {
		workerCount = len(candidates)
	}
	if workerCount == 0 {
		return nil
	}

	jobs := make(chan configuredAgentTrafficSyncCandidate)
	errs := make(chan error, len(candidates))
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for candidate := range jobs {
				if err := ctx.Err(); err != nil {
					errs <- err
					continue
				}
				if err := NewSyncService(ctx, db).syncProviderTraffic(&candidate.provider, &candidate.config); err != nil {
					errs <- fmt.Errorf("sync Agent traffic for provider %d: %w", candidate.provider.ID, err)
				}
			}
		}()
	}

	for _, candidate := range candidates {
		select {
		case jobs <- candidate:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	close(errs)

	var result error
	for err := range errs {
		result = errors.Join(result, err)
	}
	return result
}

func uniqueSortedProviderIDs(values []uint) []uint {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[uint]struct{}, len(values))
	unique := make([]uint, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	return unique
}
