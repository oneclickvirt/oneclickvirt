package middleware

import (
	"testing"
	"time"
)

func TestShouldLogDatabaseUnavailableIsRateLimited(t *testing.T) {
	lastDatabaseUnavailableLogAt.Store(0)
	base := time.Unix(100, 0)
	if !shouldLogDatabaseUnavailable(base) {
		t.Fatal("first unavailable event should be logged")
	}
	if shouldLogDatabaseUnavailable(base.Add(databaseUnavailableLogInterval - time.Nanosecond)) {
		t.Fatal("event inside the rate-limit window should be suppressed")
	}
	if !shouldLogDatabaseUnavailable(base.Add(databaseUnavailableLogInterval)) {
		t.Fatal("event at the end of the rate-limit window should be logged")
	}
}
