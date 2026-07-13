package scheduler

import "testing"

func TestTaskAllowedWhenProviderUnavailable(t *testing.T) {
	for _, taskType := range []string{"delete", "stop", "provider-instance-sync", "provider-orphan-cleanup", "provider-health-check", "provider-io-limit-sync"} {
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
