package agent

import (
	"reflect"
	"testing"

	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
)

func TestSelectConfiguredAgentTrafficSyncCandidatesFiltersAndOrders(t *testing.T) {
	providers := []providerModel.Provider{
		{ID: 9, EnableTrafficControl: true, TrafficSyncMethod: "agent"},
		{ID: 3, EnableTrafficControl: true, TrafficSyncMethod: "agent"},
		{ID: 4, EnableTrafficControl: true, TrafficSyncMethod: "pmacct"},
		{ID: 5, EnableTrafficControl: false, TrafficSyncMethod: "agent"},
	}
	configs := []monitoringModel.MonitoringConfig{
		{ProviderID: 9, MonitoringMode: "agent", AgentInstalled: true},
		{ProviderID: 3, MonitoringMode: "agent", AgentInstalled: true},
		{ProviderID: 4, MonitoringMode: "agent", AgentInstalled: true},
		{ProviderID: 5, MonitoringMode: "agent", AgentInstalled: true},
		{ProviderID: 7, MonitoringMode: "agent", AgentInstalled: false},
	}

	candidates := selectConfiguredAgentTrafficSyncCandidates(providers, configs)
	gotIDs := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		gotIDs = append(gotIDs, candidate.provider.ID)
	}
	if want := []uint{3, 9}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("candidate provider IDs = %v, want %v", gotIDs, want)
	}
}

func TestUniqueSortedProviderIDsDropsZeroAndDuplicates(t *testing.T) {
	if got, want := uniqueSortedProviderIDs([]uint{9, 0, 3, 9, 5, 3}), []uint{3, 5, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("uniqueSortedProviderIDs() = %v, want %v", got, want)
	}
}

func TestConfiguredAgentTrafficSyncKeepsEmptyScopeDistinctFromAllProviders(t *testing.T) {
	ids, allProviders := configuredAgentTrafficSyncScope(nil)
	if !allProviders || ids != nil {
		t.Fatalf("nil provider scope = (%v, %v), want (nil, true)", ids, allProviders)
	}

	ids, allProviders = configuredAgentTrafficSyncScope([]uint{0, 0})
	if allProviders || len(ids) != 0 {
		t.Fatalf("empty scoped provider list = (%v, %v), want (empty, false)", ids, allProviders)
	}
}
