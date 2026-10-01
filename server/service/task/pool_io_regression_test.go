package task

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	adminModel "oneclickvirt/model/admin"
)

func TestSlowClaimDoesNotBlockPoolCancellationOrOtherProviders(t *testing.T) {
	db := setupCancellationTestDB(t)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(2)
	keeper, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer keeper.Close()
	svc := newCancellationTaskService()
	item := adminModel.Task{ID: 900, Status: "pending", TaskType: "start", TaskData: `{}`}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	if err := db.Callback().Update().Before("gorm:update").Register("test:slow_claim", func(tx *gorm.DB) {
		if tx.Statement.Table == "tasks" && tx.Statement.Context.Value("slow") == true {
			close(entered)
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Update().Remove("test:slow_claim")
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), "slow", true))
	defer cancel()
	pool := &ProviderWorkerPool{ProviderID: 1, WorkerCount: 1, Ctx: ctx, TaskService: svc, TaskQueue: make(chan TaskRequest, 2)}
	pool.Cancel = func() { pool.submitMu.Lock(); pool.closed = true; cancel(); pool.submitMu.Unlock() }
	done := make(chan error, 1)
	go func() { done <- pool.trySubmit(item) }()
	<-entered
	cancelled := make(chan struct{})
	go func() { pool.Cancel(); close(cancelled) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("slow claim blocked cancellation")
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("cancelled pool accepted work")
	}
	if pool.hasLocalWork() || len(pool.TaskQueue) != 0 {
		t.Fatal("claim reservation leaked")
	}
	var saved adminModel.Task
	if err := db.First(&saved, item.ID).Error; err != nil || saved.Status != "pending" {
		t.Fatalf("durable state: %s, %v", saved.Status, err)
	}
}

func TestAsyncOwnerPreventsResizeAndIdleCleanup(t *testing.T) {
	setupCancellationTestDB(t)
	manager := NewProviderPoolManager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := &ProviderWorkerPool{ProviderID: 7, WorkerCount: 1, Ctx: ctx, Cancel: cancel, TaskQueue: make(chan TaskRequest, 2)}
	atomic.StoreInt64(&pool.ownedContexts, 1)
	manager.pools.Store(uint(7), pool)
	manager.count.Add(1)
	manager.lastAccess.Store(uint(7), time.Now().Add(-time.Hour))
	if manager.GetOrCreate(7, 2, nil) != pool || ctx.Err() != nil {
		t.Fatal("resize cancelled async owner")
	}
	manager.lastAccess.Store(uint(7), time.Now().Add(-time.Hour))
	if manager.CleanupIdle(time.Minute, map[uint]struct{}{}) != 0 {
		t.Fatal("stale DB snapshot removed async owner")
	}
}
