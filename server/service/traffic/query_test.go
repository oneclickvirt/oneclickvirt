package traffic

import (
	"testing"
	"time"
)

func TestCalculateActualUsageMB(t *testing.T) {
	s := NewQueryService()

	tests := []struct {
		name       string
		inMB       float64
		outMB      float64
		mode       string
		multiplier float64
		want       float64
	}{
		{name: "both", inMB: 100, outMB: 25, mode: "both", multiplier: 1, want: 125},
		{name: "out", inMB: 100, outMB: 25, mode: "out", multiplier: 2, want: 50},
		{name: "in", inMB: 100, outMB: 25, mode: "in", multiplier: 0.5, want: 50},
		{name: "bad mode and multiplier default", inMB: 100, outMB: 25, mode: "", multiplier: 0, want: 125},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.calculateActualUsageMB(tt.inMB, tt.outMB, tt.mode, tt.multiplier)
			if got != tt.want {
				t.Fatalf("calculateActualUsageMB() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComputeSegmentTrafficIndependentDirectionReset(t *testing.T) {
	records := []rawTrafficRecord{
		{RxBytes: 100, TxBytes: 20},
		{RxBytes: 150, TxBytes: 10},
		{RxBytes: 170, TxBytes: 25},
		{RxBytes: 5, TxBytes: 40},
	}

	rx, tx := computeSegmentTraffic(records)
	if rx != 175 {
		t.Fatalf("rx = %d, want 175", rx)
	}
	if tx != 60 {
		t.Fatalf("tx = %d, want 60", tx)
	}
}

func TestComputeWindowTrafficAccumulatesAcrossRestarts(t *testing.T) {
	baseline := &rawTrafficRecord{RxBytes: 1000, TxBytes: 500}
	records := []rawTrafficRecord{
		{RxBytes: 1200, TxBytes: 650},
		{RxBytes: 50, TxBytes: 700},
		{RxBytes: 80, TxBytes: 20},
	}

	rx, tx := computeWindowTraffic(records, baseline)
	if rx != 280 {
		t.Fatalf("rx = %d, want 280", rx)
	}
	if tx != 220 {
		t.Fatalf("tx = %d, want 220", tx)
	}
}

func TestComputeTrafficDeltasUsesWindowBaselineAndReset(t *testing.T) {
	start := mustParseTrafficTestTime(t, "2026-07-06T10:00:00Z")
	records := []trafficRawPoint{
		{InstanceID: 1, Timestamp: start.Add(-5 * time.Minute), RxBytes: 1000, TxBytes: 500},
		{InstanceID: 1, Timestamp: start.Add(5 * time.Minute), RxBytes: 1200, TxBytes: 650},
		{InstanceID: 1, Timestamp: start.Add(10 * time.Minute), RxBytes: 50, TxBytes: 700},
	}

	deltas := computeTrafficDeltas(records, start)
	if len(deltas) != 2 {
		t.Fatalf("len(deltas) = %d, want 2", len(deltas))
	}
	if deltas[0].RxDelta != 200 || deltas[0].TxDelta != 150 {
		t.Fatalf("first delta = (%d,%d), want (200,150)", deltas[0].RxDelta, deltas[0].TxDelta)
	}
	if deltas[1].RxDelta != 50 || deltas[1].TxDelta != 50 {
		t.Fatalf("reset delta = (%d,%d), want (50,50)", deltas[1].RxDelta, deltas[1].TxDelta)
	}
}

func TestShouldKeepTrafficInterval(t *testing.T) {
	ts := mustParseTrafficTestTime(t, "2026-07-06T10:15:00Z")
	if !shouldKeepTrafficInterval(ts, 15) {
		t.Fatal("15-minute aligned point should be kept")
	}
	if shouldKeepTrafficInterval(ts, 30) {
		t.Fatal("non-30-minute point should be skipped")
	}
}

func mustParseTrafficTestTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return parsed
}
