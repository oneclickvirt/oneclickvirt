package traffic

import (
	"testing"
	"time"
)

func TestCurrentTrafficWindowDefaultNaturalMonth(t *testing.T) {
	now := time.Date(2026, time.July, 3, 12, 0, 0, 0, time.UTC)
	start, next := CurrentTrafficWindow(nil, now)

	wantStart := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	wantNext := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !next.Equal(wantNext) {
		t.Fatalf("window = %s/%s, want %s/%s", start, next, wantStart, wantNext)
	}
}

func TestCurrentTrafficWindowCustomDayBeforeReset(t *testing.T) {
	day := 15
	now := time.Date(2026, time.July, 3, 12, 0, 0, 0, time.UTC)
	start, next := CurrentTrafficWindow(&day, now)

	wantStart := time.Date(2026, time.June, 15, 0, 0, 0, 0, time.UTC)
	wantNext := time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !next.Equal(wantNext) {
		t.Fatalf("window = %s/%s, want %s/%s", start, next, wantStart, wantNext)
	}
}

func TestCurrentTrafficWindowClampsMonthEnd(t *testing.T) {
	day := 31
	now := time.Date(2026, time.March, 30, 12, 0, 0, 0, time.UTC)
	start, next := CurrentTrafficWindow(&day, now)

	wantStart := time.Date(2026, time.February, 28, 0, 0, 0, 0, time.UTC)
	wantNext := time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !next.Equal(wantNext) {
		t.Fatalf("window = %s/%s, want %s/%s", start, next, wantStart, wantNext)
	}
}

func TestNormalizeTrafficResetDay(t *testing.T) {
	zero := 0
	if normalized, err := NormalizeTrafficResetDay(&zero); err != nil || normalized != nil {
		t.Fatalf("zero normalized = %#v, err=%v; want nil nil", normalized, err)
	}

	day := 31
	normalized, err := NormalizeTrafficResetDay(&day)
	if err != nil || normalized == nil || *normalized != 31 {
		t.Fatalf("31 normalized = %#v, err=%v; want 31 nil", normalized, err)
	}

	invalid := 32
	if _, err := NormalizeTrafficResetDay(&invalid); err == nil {
		t.Fatalf("expected invalid day error")
	}
}
