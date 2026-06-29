package traffic

import "testing"

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
