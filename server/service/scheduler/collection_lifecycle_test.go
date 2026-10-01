package scheduler

import (
	"context"
	"go.uber.org/zap"
	"oneclickvirt/global"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollectionTimeoutRetainsOwnerUntilExit(t *testing.T) {
	m := NewProviderStateManager()
	state := m.GetOrCreate(7)
	ctx, started := state.StartCollectingContext(context.Background(), time.Hour)
	if !started {
		t.Fatal("collector did not start")
	}
	state.mu.Lock()
	state.collectStartTime = time.Now().Add(-10 * time.Minute)
	state.mu.Unlock()
	if m.ResetIfCollectingTooLong(time.Minute) != 1 {
		t.Fatal("timeout not cancelled")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("timeout did not cancel context")
	}
	if !state.IsCollecting() || state.StartCollecting() {
		t.Fatal("timeout admitted overlapping work")
	}
	if m.ResetIfCollectingTooLong(time.Minute) != 0 {
		t.Fatal("repeated timeout cancellation")
	}
	state.FinishCollecting()
	if !state.StartCollecting() {
		t.Fatal("collector did not recover after exit")
	}
	state.FinishCollecting()
}

func TestCollectionDeletionCancelsActiveWorkAndRetiresOldReferences(t *testing.T) {
	m := NewProviderStateManager()
	state := m.GetOrCreate(7)
	ctx, _ := state.StartCollectingContext(context.Background(), time.Hour)
	m.Delete(7)
	select {
	case <-ctx.Done():
	default:
		t.Fatal("deletion did not cancel context")
	}
	if m.GetOrCreate(7) != state || state.StartCollecting() {
		t.Fatal("deletion replaced an active owner")
	}
	state.FinishCollecting()
	m.Delete(7)
	replacement := m.GetOrCreate(7)
	if replacement == state || state.StartCollecting() {
		t.Fatal("retired state remains usable")
	}
	if m.Count() != 1 {
		t.Fatalf("state count = %d", m.Count())
	}
}

func TestCollectionCleanupCannotSplitProviderOwnership(t *testing.T) {
	m := NewProviderStateManager()
	var active atomic.Int32
	var overlaps atomic.Int32
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 100; attempt++ {
				state := m.GetOrCreate(7)
				if state.StartCollecting() {
					if active.Add(1) != 1 {
						overlaps.Add(1)
					}
					m.CleanupDeleted(nil)
					active.Add(-1)
					state.FinishCollecting()
				}
				m.Delete(7)
			}
		}()
	}
	wg.Wait()
	if overlaps.Load() != 0 {
		t.Fatalf("overlapping collectors = %d", overlaps.Load())
	}
	m.CleanupDeleted(nil)
	if m.Count() != 0 {
		t.Fatalf("remaining states = %d", m.Count())
	}
}

func TestProviderCleanupKeepsActiveAgentGuard(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })
	s := NewMonitoringSchedulerService(nil)
	if !s.tryStartAgentProviderWork(7) {
		t.Fatal("work did not start")
	}
	s.DeleteProviderState(7)
	if s.tryStartAgentProviderWork(7) {
		t.Fatal("cleanup admitted overlapping Agent work")
	}
	s.finishAgentProviderWork(7)
	if !s.tryStartAgentProviderWork(7) {
		t.Fatal("guard not released after exit")
	}
	s.finishAgentProviderWork(7)
}
