package task

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

func TestCleanupIdleKeepsPoolWithActiveTask(t *testing.T) {
	global.APP_LOG = zap.NewNop()

	manager := NewProviderPoolManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool := &ProviderWorkerPool{
		ProviderID:  1,
		TaskQueue:   make(chan TaskRequest, 1),
		WorkerCount: 1,
		Ctx:         ctx,
		Cancel:      cancel,
	}
	atomic.StoreInt64(&pool.activeCount, 1)

	old := time.Now().Add(-2 * time.Hour)
	manager.pools.Store(uint(1), pool)
	manager.lastAccess.Store(uint(1), old)
	manager.createdAt.Store(uint(1), old)
	manager.count.Add(1)

	if cleaned := manager.CleanupIdle(time.Minute); cleaned != 0 {
		t.Fatalf("CleanupIdle cleaned active pool: %d", cleaned)
	}

	select {
	case <-ctx.Done():
		t.Fatal("CleanupIdle cancelled active pool")
	default:
	}

	atomic.StoreInt64(&pool.activeCount, 0)
	if cleaned := manager.CleanupIdle(time.Minute); cleaned != 1 {
		t.Fatalf("CleanupIdle cleaned %d pools, want 1", cleaned)
	}

	select {
	case <-ctx.Done():
	default:
		t.Fatal("CleanupIdle did not cancel idle pool")
	}
}
