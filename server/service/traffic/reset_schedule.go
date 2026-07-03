package traffic

import (
	"fmt"
	"time"
)

const defaultTrafficResetDay = 1

// NormalizeTrafficResetDay validates the optional provider traffic reset day.
// nil and 0 both mean the natural-month default: reset on day 1.
func NormalizeTrafficResetDay(day *int) (*int, error) {
	if day == nil || *day == 0 {
		return nil, nil
	}
	if *day < 1 || *day > 31 {
		return nil, fmt.Errorf("流量重置日期必须为空或介于1到31之间")
	}
	normalized := *day
	return &normalized, nil
}

func effectiveTrafficResetDay(day *int) int {
	if day == nil || *day <= 0 {
		return defaultTrafficResetDay
	}
	if *day > 31 {
		return 31
	}
	return *day
}

func resetTimeForMonth(year int, month time.Month, day int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	lastDay := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(year, month, day, 0, 0, 0, 0, loc)
}

// CurrentTrafficWindow returns [start, nextReset) for the given reset day.
// Reset days 29-31 are clamped to each month's last day.
func CurrentTrafficWindow(day *int, now time.Time) (time.Time, time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	resetDay := effectiveTrafficResetDay(day)
	loc := now.Location()
	thisMonthReset := resetTimeForMonth(now.Year(), now.Month(), resetDay, loc)

	if now.Before(thisMonthReset) {
		prevMonthReset := resetTimeForMonth(now.Year(), now.Month()-1, resetDay, loc)
		return prevMonthReset, thisMonthReset
	}

	nextMonthReset := resetTimeForMonth(now.Year(), now.Month()+1, resetDay, loc)
	return thisMonthReset, nextMonthReset
}

func NextTrafficResetTime(day *int, now time.Time) time.Time {
	_, next := CurrentTrafficWindow(day, now)
	return next
}
