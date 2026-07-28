package agent

import (
	"testing"

	monitoringModel "oneclickvirt/model/monitoring"
)

func TestClaimTrafficSyncItemsRejectsOverlappingRetry(t *testing.T) {
	fresh := &monitoringModel.AgentMonitor{
		ID:                  1,
		LastTrafficBytes:    100,
		LastTrafficBytesIn:  40,
		LastTrafficBytesOut: 60,
	}
	stale := &monitoringModel.AgentMonitor{
		ID:                  2,
		LastTrafficBytes:    200,
		LastTrafficBytesIn:  80,
		LastTrafficBytesOut: 120,
	}
	items := []trafficSyncItem{
		{monitor: fresh, deltaBytesIn: 10, deltaBytesOut: 20},
		{monitor: stale, deltaBytesIn: 30, deltaBytesOut: 40},
	}
	locked := map[uint]monitoringModel.AgentMonitor{
		1: *fresh,
		2: {
			ID:                  2,
			LastTrafficBytes:    270,
			LastTrafficBytesIn:  110,
			LastTrafficBytesOut: 160,
		},
	}

	claimed, trafficItems := claimTrafficSyncItems(items, locked)
	if len(claimed) != 1 || claimed[0].monitor.ID != fresh.ID {
		t.Fatalf("claimed = %#v, want only fresh monitor", claimed)
	}
	if len(trafficItems) != 1 || trafficItems[0].monitor.ID != fresh.ID {
		t.Fatalf("trafficItems = %#v, want only fresh monitor", trafficItems)
	}
}

func TestClaimTrafficSyncItemsAdvancesZeroDeltaTracking(t *testing.T) {
	monitor := &monitoringModel.AgentMonitor{
		ID:                  3,
		LastTrafficBytes:    300,
		LastTrafficBytesIn:  100,
		LastTrafficBytesOut: 200,
	}
	items := []trafficSyncItem{{monitor: monitor}}
	claimed, trafficItems := claimTrafficSyncItems(items, map[uint]monitoringModel.AgentMonitor{3: *monitor})
	if len(claimed) != 1 {
		t.Fatalf("claimed count = %d, want 1", len(claimed))
	}
	if len(trafficItems) != 0 {
		t.Fatalf("trafficItems count = %d, want 0", len(trafficItems))
	}
}
