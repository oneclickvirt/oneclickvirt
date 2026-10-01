package task

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTaskContextManagerNeverEvictsActiveContexts(t *testing.T) {
	manager := NewTaskContextManager(1, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := manager.Add(1, ctx, cancel); err != nil {
		t.Fatal(err)
	}
	stored, ok := manager.Get(1)
	if !ok {
		t.Fatal("active task context was not registered")
	}
	stored.StartTime = time.Now().Add(-time.Hour)

	if cleaned := manager.CleanupStale(); cleaned != 0 {
		t.Fatalf("CleanupStale removed %d active contexts", cleaned)
	}
	if cleaned := manager.ForceLimitSize(); cleaned != 0 {
		t.Fatalf("ForceLimitSize removed %d active contexts", cleaned)
	}
	if err := manager.Add(2, context.Background(), func() {}); !errors.Is(err, ErrContextPoolFull) {
		t.Fatalf("Add at capacity error = %v, want ErrContextPoolFull", err)
	}
	if _, ok := manager.Get(1); !ok {
		t.Fatal("active task context was evicted when capacity was reached")
	}
}

func TestDuplicateTaskContextDoesNotReplaceOwner(t *testing.T) {
	m := NewTaskContextManager(4, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	released := 0
	if err := m.addWithRelease(1, ctx, cancel, func() { released++ }); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(1, context.Background(), func() { t.Fatal("duplicate cancelled owner") }); !errors.Is(err, ErrTaskContextExists) {
		t.Fatalf("duplicate: %v", err)
	}
	owner, _ := m.Get(1)
	if owner.Context != ctx || released != 0 {
		t.Fatal("owner changed")
	}
	m.Delete(1)
	m.Delete(1)
	if released != 1 || m.Count() != 0 {
		t.Fatalf("release count = %d", released)
	}
}
