package provider

import "testing"

func TestRuntimeReloadTaskPolicyQueuesBehindRunningWork(t *testing.T) {
	// This documents the convergence contract used by
	// createConvergentProviderTask: pending work can absorb a later edit, but a
	// task that has already started must not be reused.
	reusable := map[string]bool{
		"pending":    true,
		"processing": false,
		"running":    false,
		"cancelling": false,
		"completed":  false,
	}
	for status, want := range reusable {
		got := status == convergentTaskReusableStatus()
		if got != want {
			t.Fatalf("reload task status %q reusable=%v, want %v", status, got, want)
		}
	}
}
