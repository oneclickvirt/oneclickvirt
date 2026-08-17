package monitoring

import "testing"

func TestMonitorSyncTaskStatusContract(t *testing.T) {
	if MonitorSyncTaskStatusCompleted != "completed" {
		t.Fatalf("completed status = %q", MonitorSyncTaskStatusCompleted)
	}
	if MonitorSyncTaskStatusFailed == MonitorSyncTaskStatusCompleted ||
		MonitorSyncTaskStatusPending == MonitorSyncTaskStatusCompleted ||
		MonitorSyncTaskStatusRunning == MonitorSyncTaskStatusCompleted {
		t.Fatal("monitor sync terminal status must remain distinct from active and failed statuses")
	}
}
