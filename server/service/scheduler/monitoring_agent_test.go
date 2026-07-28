package scheduler

import (
	"testing"
	"time"
)

func TestAgentReconcileSuccessDelayIsDeterministicAndJittered(t *testing.T) {
	first := agentReconcileSuccessDelay(42)
	second := agentReconcileSuccessDelay(42)
	if first != second {
		t.Fatalf("delay changed for same provider: %v != %v", first, second)
	}
	if first < 5*time.Minute || first >= 6*time.Minute {
		t.Fatalf("delay %v is outside expected [5m, 6m) window", first)
	}
}

func TestAgentWorkSlotsAndProviderGuardAreBounded(t *testing.T) {
	service := NewMonitoringSchedulerService(nil)
	for index := 0; index < cap(service.agentWorkSlots); index++ {
		if !service.tryAcquireAgentWorkSlot() {
			t.Fatalf("slot %d should be available", index)
		}
	}
	if service.tryAcquireAgentWorkSlot() {
		t.Fatal("work slot acquisition should be bounded")
	}
	for index := 0; index < cap(service.agentWorkSlots); index++ {
		service.releaseAgentWorkSlot()
	}

	if !service.tryStartAgentProviderWork(7) {
		t.Fatal("first provider operation should start")
	}
	if service.tryStartAgentProviderWork(7) {
		t.Fatal("overlapping provider operation should be rejected")
	}
	service.finishAgentProviderWork(7)
	if !service.tryStartAgentProviderWork(7) {
		t.Fatal("provider operation should start after release")
	}
}

func TestAgentReconcileFailureBackoffIncreases(t *testing.T) {
	service := NewMonitoringSchedulerService(nil)
	service.finishAgentReconcile(9, false, false)
	firstValue, ok := service.agentReconcileState.Load(uint(9))
	if !ok {
		t.Fatal("missing first failure schedule")
	}
	first := firstValue.(agentReconcileSchedule)

	service.finishAgentReconcile(9, false, false)
	secondValue, ok := service.agentReconcileState.Load(uint(9))
	if !ok {
		t.Fatal("missing second failure schedule")
	}
	second := secondValue.(agentReconcileSchedule)
	if second.Failures != first.Failures+1 {
		t.Fatalf("failures = %d, want %d", second.Failures, first.Failures+1)
	}
	if !second.NextRun.After(first.NextRun) {
		t.Fatalf("second backoff %v should be after first %v", second.NextRun, first.NextRun)
	}
}
