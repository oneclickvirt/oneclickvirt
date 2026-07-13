package scheduler

import "testing"

func TestTaskAllowedWhenProviderUnavailable(t *testing.T) {
	for _, taskType := range []string{"delete", "stop", "provider-instance-sync", "provider-orphan-cleanup", "provider-health-check", "provider-io-limit-sync", "provider-runtime-reload", "provider-delete"} {
		if !taskAllowedWhenProviderUnavailable(taskType) {
			t.Fatalf("maintenance task %q must remain runnable", taskType)
		}
	}
	for _, taskType := range []string{"create", "start", "reset", "provider-image-cleanup"} {
		if taskAllowedWhenProviderUnavailable(taskType) {
			t.Fatalf("regular task %q unexpectedly allowed", taskType)
		}
	}
}

func TestOnlyProviderDeleteRunsAfterDeletionStarts(t *testing.T) {
	if !taskAllowedWhenProviderDeleting("provider-delete") {
		t.Fatal("provider-delete must remain runnable after the node enters deleting state")
	}
	for _, taskType := range []string{"create", "delete", "provider-health-check", "provider-runtime-reload"} {
		if taskAllowedWhenProviderDeleting(taskType) {
			t.Fatalf("task %q must not start while Provider deletion is pending", taskType)
		}
	}
}
