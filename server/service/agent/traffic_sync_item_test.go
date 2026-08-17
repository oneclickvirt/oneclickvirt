package agent

import (
	"testing"
	"time"

	monitoringModel "oneclickvirt/model/monitoring"
)

func TestBuildTrafficSyncItemKeepsPVERoutedIPv6CountersConsistent(t *testing.T) {
	monitor := &monitoringModel.AgentMonitor{
		InstanceID:          273,
		AgentMonitorID:      91,
		LastTrafficBytes:    2_080,
		LastTrafficBytesIn:  1_040,
		LastTrafficBytesOut: 1_040,
	}
	now := time.Date(2026, time.August, 17, 12, 9, 42, 0, time.FixedZone("CST", 8*60*60))
	item := buildTrafficSyncItem(monitor, &InfoResponse{
		ID:             91,
		UsedTraffic:    1,
		UsedTrafficIn:  2_080,
		UsedTrafficOut: 3_120,
		LastUpdateTime: now.Unix(),
	}, now)

	if item.currentTraffic != item.currentTrafficIn+item.currentTrafficOut {
		t.Fatalf("total %d does not equal inbound %d + outbound %d", item.currentTraffic, item.currentTrafficIn, item.currentTrafficOut)
	}
	if item.currentTraffic != 5_200 || item.deltaBytesIn != 1_040 || item.deltaBytesOut != 2_080 {
		t.Fatalf("unexpected synchronized counters: %#v", item)
	}
	if item.alignedTime.Minute() != 5 || item.alignedTime.Second() != 0 {
		t.Fatalf("traffic record did not align to five minutes: %s", item.alignedTime)
	}
}

func TestBuildTrafficSyncItemTreatsCounterResetAsFreshCumulativeTraffic(t *testing.T) {
	monitor := &monitoringModel.AgentMonitor{
		InstanceID:          274,
		LastTrafficBytes:    9_000,
		LastTrafficBytesIn:  4_000,
		LastTrafficBytesOut: 5_000,
	}
	item := buildTrafficSyncItem(monitor, &InfoResponse{
		UsedTraffic:    1_100,
		UsedTrafficIn:  400,
		UsedTrafficOut: 700,
	}, time.Now())

	if item.deltaBytesIn != 400 || item.deltaBytesOut != 700 || item.currentTraffic != 1_100 {
		t.Fatalf("counter reset was not synchronized as a fresh cumulative sample: %#v", item)
	}
}

func TestPVERoutedIPv6TrafficSyncStaysMonotonicAcrossAgentRuleReconciliation(t *testing.T) {
	monitor := &monitoringModel.AgentMonitor{
		InstanceID:     275,
		AgentMonitorID: 92,
		Interfaces:     "veth100i0,veth100i1",
	}
	now := time.Date(2026, time.August, 17, 14, 35, 0, 0, time.FixedZone("CST", 8*60*60))

	// First Agent sample includes the NAT IPv4 interface and the routed IPv6
	// interface. The controller must trust directional values over a stale total.
	first := buildTrafficSyncItem(monitor, &InfoResponse{
		UsedTraffic:    1,
		UsedTrafficIn:  2_000,
		UsedTrafficOut: 3_000,
	}, now)
	if first.currentTraffic != 5_000 || first.deltaBytesIn != 2_000 || first.deltaBytesOut != 3_000 {
		t.Fatalf("first dual-stack sample = %#v", first)
	}
	monitor.LastTrafficBytes = first.currentTraffic
	monitor.LastTrafficBytesIn = first.currentTrafficIn
	monitor.LastTrafficBytesOut = first.currentTrafficOut

	second := buildTrafficSyncItem(monitor, &InfoResponse{
		UsedTrafficIn:  2_120,
		UsedTrafficOut: 3_180,
	}, now.Add(5*time.Minute))
	if second.deltaBytesIn != 120 || second.deltaBytesOut != 180 || second.currentTraffic != 5_300 {
		t.Fatalf("incremental dual-stack sample = %#v", second)
	}
	monitor.LastTrafficBytes = second.currentTraffic
	monitor.LastTrafficBytesIn = second.currentTrafficIn
	monitor.LastTrafficBytesOut = second.currentTrafficOut

	// nft rule reconciliation/restart can reset the Agent-side cumulative
	// counters. Fallback sync accepts only the fresh counter values and never
	// subtracts prior traffic or lets the reported total drift from directions.
	afterReset := buildTrafficSyncItem(monitor, &InfoResponse{
		UsedTraffic:    999_999,
		UsedTrafficIn:  37,
		UsedTrafficOut: 53,
	}, now.Add(10*time.Minute))
	if afterReset.deltaBytesIn != 37 || afterReset.deltaBytesOut != 53 || afterReset.currentTraffic != 90 {
		t.Fatalf("reset fallback sample = %#v", afterReset)
	}
	if total := first.deltaBytesIn + first.deltaBytesOut + second.deltaBytesIn + second.deltaBytesOut + afterReset.deltaBytesIn + afterReset.deltaBytesOut; total != 5_390 {
		t.Fatalf("cumulative traffic after reset = %d, want 5390", total)
	}
}
