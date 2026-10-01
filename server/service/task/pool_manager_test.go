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

func TestPoolResizeDrainsAcceptedWorkWithoutCancellingIt(t *testing.T) {
	global.APP_LOG = zap.NewNop()
	manager := NewProviderPoolManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := &ProviderWorkerPool{ProviderID: 1, WorkerCount: 1, Ctx: ctx, Cancel: cancel, TaskQueue: make(chan TaskRequest, 2)}
	atomic.StoreInt64(&pool.outstanding, 1)
	manager.pools.Store(uint(1), pool)
	manager.count.Add(1)
	got := manager.GetOrCreate(1, 3, nil)
	if got != pool || ctx.Err() != nil {
		t.Fatal("resize cancelled accepted work")
	}
	if !pool.closed {
		t.Fatal("retiring pool still accepts new work")
	}
	// Returning the desired concurrency to the old value reopens admission.
	got = manager.GetOrCreate(1, 1, nil)
	if got != pool || pool.closed {
		t.Fatal("failed to resume matching pool")
	}
}

func TestDeleteDefersPoolRemovalWhileWorkIsOwned(t *testing.T) {
	global.APP_LOG = zap.NewNop()
	manager := NewProviderPoolManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var pool *ProviderWorkerPool
	pool = &ProviderWorkerPool{
		ProviderID:  9,
		WorkerCount: 1,
		Ctx:         ctx,
		Cancel: func() {
			pool.submitMu.Lock()
			pool.closed = true
			cancel()
			pool.submitMu.Unlock()
		},
		TaskQueue: make(chan TaskRequest, 1),
	}
	atomic.StoreInt64(&pool.outstanding, 1)
	atomic.StoreInt64(&pool.activeCount, 1)
	manager.pools.Store(uint(9), pool)
	manager.count.Add(1)

	manager.Delete(9)
	if _, ok := manager.pools.Load(uint(9)); !ok {
		t.Fatal("deleted pool with accepted work")
	}
	if ctx.Err() != nil {
		t.Fatal("deleted pool cancellation interrupted accepted work")
	}
	if !pool.closed {
		t.Fatal("pool still accepts work after deferred deletion")
	}
	if got := manager.GetOrCreate(9, 1, nil); got != pool || !pool.closed {
		t.Fatal("deleted Provider pool was reopened while work was still owned")
	}

	atomic.StoreInt64(&pool.outstanding, 0)
	atomic.StoreInt64(&pool.activeCount, 0)
	manager.Delete(9)
	if _, ok := manager.pools.Load(uint(9)); ok {
		t.Fatal("idle pool remained after deletion")
	}
	if ctx.Err() == nil {
		t.Fatal("idle pool was not cancelled")
	}
}

func TestCancelAllAndWaitDrainsActiveOwnershipAndClosesAdmission(t *testing.T) {
	manager := NewProviderPoolManager()
	poolCtx, cancelPool := context.WithCancel(context.Background())
	pool := &ProviderWorkerPool{
		ProviderID:  11,
		WorkerCount: 1,
		Ctx:         poolCtx,
		TaskQueue:   make(chan TaskRequest, 1),
	}
	pool.Cancel = func() {
		pool.submitMu.Lock()
		pool.closed = true
		cancelPool()
		pool.submitMu.Unlock()
	}
	atomic.StoreInt64(&pool.liveWorkers, 1)
	atomic.StoreInt64(&pool.outstanding, 1)
	atomic.StoreInt64(&pool.activeCount, 1)
	atomic.StoreInt64(&pool.ownedContexts, 1)
	manager.pools.Store(uint(11), pool)
	manager.count.Add(1)

	workExited := make(chan struct{})
	go func() {
		<-poolCtx.Done()
		time.Sleep(30 * time.Millisecond)
		atomic.StoreInt64(&pool.ownedContexts, 0)
		atomic.StoreInt64(&pool.activeCount, 0)
		atomic.StoreInt64(&pool.outstanding, 0)
		atomic.StoreInt64(&pool.liveWorkers, 0)
		close(workExited)
	}()

	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	started := time.Now()
	if err := manager.CancelAllAndWait(waitCtx); err != nil {
		t.Fatalf("CancelAllAndWait() error = %v", err)
	}
	if time.Since(started) < 20*time.Millisecond {
		t.Fatal("CancelAllAndWait returned before active ownership drained")
	}
	<-workExited
	if pool.Ctx.Err() == nil || !pool.closed {
		t.Fatal("pool was not closed and cancelled")
	}
	if manager.GetOrCreate(12, 1, nil) != nil {
		t.Fatal("pool manager accepted a new pool after shutdown")
	}
}
