package task

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"sync"
	"testing"
	"time"
)

func dropSQLiteIndexes(t *testing.T, db *gorm.DB) {
	var indexes []struct{ Name string }
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type='index' AND name NOT LIKE 'sqlite_%'").Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		if err := db.Exec("DROP INDEX IF EXISTS \"" + index.Name + "\"").Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestLifecycleReservationIsAtomicAcrossCallers(t *testing.T) {
	db := setupCancellationTestDB(t)
	dropSQLiteIndexes(t, db)
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	instance := providerModel.Instance{ID: 1, UUID: "reservation", Name: "guest", UserID: 7, ProviderID: 2, Status: "running"}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	svc := newCancellationTaskService()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, action := range []string{"stop", "reset-password"} {
		wg.Add(1)
		go func(action string) {
			defer wg.Done()
			_, err := svc.CreateTask(7, &instance.ProviderID, &instance.ID, action, `{}`, 60)
			results <- err
		}(action)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d conflicting tasks", accepted)
	}
	var tasks []adminModel.Task
	if err := db.Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks=%d", len(tasks))
	}
	var saved providerModel.Instance
	if err := db.First(&saved, instance.ID).Error; err != nil {
		t.Fatal(err)
	}
	want := "running"
	if tasks[0].TaskType == "stop" {
		want = "stopping"
	}
	if saved.Status != want {
		t.Fatalf("status %s, task %s", saved.Status, tasks[0].TaskType)
	}
}

func TestLifecycleReservationRollsBackBusyStateWhenTaskInsertFails(t *testing.T) {
	db := setupCancellationTestDB(t)
	dropSQLiteIndexes(t, db)
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	i := providerModel.Instance{ID: 1, UUID: "rollback", Name: "guest", UserID: 7, ProviderID: 2, Status: "running"}
	if err := db.Create(&i).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER reject_task BEFORE INSERT ON tasks BEGIN SELECT RAISE(FAIL, 'injected insert failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := newCancellationTaskService().CreateTask(7, &i.ProviderID, &i.ID, "delete", `{"adminOperation":true}`, 60); err == nil {
		t.Fatal("expected insert failure")
	}
	if err := db.First(&i, i.ID).Error; err != nil {
		t.Fatal(err)
	}
	if i.Status != "running" {
		t.Fatalf("rollback left %q", i.Status)
	}
}

func TestDestructiveFailureKeepsTruthfulRetryableState(t *testing.T) {
	for _, tc := range []struct{ kind, phase, busy, want string }{
		{"reset", "", "resetting", "running"},
		{"delete", "", "deleting", "running"},
		{"reset", "delete_started", "resetting", "error"},
		{"reset", "old_deleted", "resetting", "error"},
		{"reset", "replacement_created", "creating", "error"},
		{"delete", "delete_started", "deleting", "error"},
	} {
		t.Run(tc.kind+tc.phase, func(t *testing.T) {
			db := setupCancellationTestDB(t)
			dropSQLiteIndexes(t, db)
			if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
				t.Fatal(err)
			}
			i := providerModel.Instance{ID: 1, UUID: "phase", Name: "guest", Status: tc.busy}
			if err := db.Create(&i).Error; err != nil {
				t.Fatal(err)
			}
			task := adminModel.Task{ID: 1, InstanceID: &i.ID, TaskType: tc.kind, Status: "running", TaskData: fmt.Sprintf(`{"originalStatus":"running","lifecyclePhase":%q}`, tc.phase)}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			svc := newCancellationTaskService()
			if err := svc.CompleteTask(task.ID, false, "injected lifecycle failure", nil); err != nil {
				t.Fatal(err)
			}
			if err := db.First(&i, i.ID).Error; err != nil {
				t.Fatal(err)
			}
			if i.Status != tc.want {
				t.Fatalf("got %s want %s", i.Status, tc.want)
			}
			if tc.want == "error" && i.DesiredState != "stopped" {
				t.Fatalf("unsafe desired state %s", i.DesiredState)
			}
		})
	}
}

func TestDestructivePhaseCannotBeginAfterCancellation(t *testing.T) {
	db := setupCancellationTestDB(t)
	task := adminModel.Task{ID: 1, TaskType: "reset", Status: "cancelling", TaskData: `{}`}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := newCancellationTaskService().recordLifecyclePhase(context.Background(), 1, "delete_started"); err == nil {
		t.Fatal("cancelled task crossed destructive boundary")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitTaskContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLifecycleWritebackMatchesConfirmedProviderResult(t *testing.T) {
	db := setupCancellationTestDB(t)
	dropSQLiteIndexes(t, db)
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}

	instance := providerModel.Instance{ID: 1, UUID: "late-result", Name: "guest", UserID: 7, ProviderID: 2, Status: "stopping", DesiredState: "stopped"}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	cancelledTask := adminModel.Task{ID: 1, InstanceID: &instance.ID, TaskType: "stop", Status: mainTaskStatusCancelling, TaskData: `{}`}
	if err := db.Create(&cancelledTask).Error; err != nil {
		t.Fatal(err)
	}

	if err := updateLifecycleResult(cancelledTask.ID, instance.ID, map[string]interface{}{"status": "stopped"}, "stopping"); err != nil {
		t.Fatalf("confirmed provider success was not persisted during cancellation: %v", err)
	}
	if err := db.First(&instance, instance.ID).Error; err != nil {
		t.Fatal(err)
	}
	if instance.Status != "stopped" || instance.DesiredState != "stopped" {
		t.Fatalf("confirmed stop result = %q/%q, want stopped/stopped", instance.Status, instance.DesiredState)
	}

	instance.ID = 2
	instance.UUID = "cancelled-failure"
	instance.Name = "guest-2"
	instance.Status = "restarting"
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	cancelledTask.ID = 2
	cancelledTask.InstanceID = &instance.ID
	cancelledTask.TaskType = "restart"
	if err := db.Create(&cancelledTask).Error; err != nil {
		t.Fatal(err)
	}
	if err := updateLifecycleStateWhileRunning(cancelledTask.ID, instance.ID, map[string]interface{}{"status": "running"}, "restarting"); !errors.Is(err, errLifecycleTaskCancelled) {
		t.Fatalf("late provider failure should not write after cancellation, got %v", err)
	}
	if err := db.First(&instance, instance.ID).Error; err != nil {
		t.Fatal(err)
	}
	if instance.Status != "restarting" {
		t.Fatalf("late failure changed cancelled restart state to %q", instance.Status)
	}

	instance.ID = 3
	instance.UUID = "stale-transition"
	instance.Name = "guest-3"
	instance.Status = "running"
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	runningTask := adminModel.Task{ID: 3, InstanceID: &instance.ID, TaskType: "stop", Status: mainTaskStatusRunning, TaskData: `{}`}
	if err := db.Create(&runningTask).Error; err != nil {
		t.Fatal(err)
	}
	if err := updateLifecycleResult(runningTask.ID, instance.ID, map[string]interface{}{"status": "stopped"}, "stopping"); err == nil {
		t.Fatal("late stop result overwrote an instance outside its transition state")
	}
	if err := db.First(&instance, instance.ID).Error; err != nil {
		t.Fatal(err)
	}
	if instance.Status != "running" {
		t.Fatalf("stale task changed current instance state to %q", instance.Status)
	}
}

func TestFullAndClosedPoolReturnsTasksToPending(t *testing.T) {
	db := setupCancellationTestDB(t)
	svc := newCancellationTaskService()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := &ProviderWorkerPool{TaskQueue: make(chan TaskRequest, 1), Ctx: ctx, Cancel: cancel, TaskService: svc}
	for id := uint(1); id <= 2; id++ {
		if err := db.Create(&adminModel.Task{ID: id, Status: "pending", TaskType: "start"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.trySubmit(adminModel.Task{ID: 1}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := pool.trySubmit(adminModel.Task{ID: 2}); err == nil {
		t.Fatal("accepted full queue")
	}
	if time.Since(start) > time.Second {
		t.Fatal("full queue blocked")
	}
	cancel()
	pool.drainQueuedTasks()
	if err := pool.trySubmit(adminModel.Task{ID: 2}); err == nil {
		t.Fatal("accepted cancelled pool")
	}
	var count int64
	if err := db.Model(&adminModel.Task{}).Where("status = ?", "pending").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("pending=%d", count)
	}
}

func TestOrphanedProcessingRecoveredWithoutTouchingLiveQueue(t *testing.T) {
	db := setupCancellationTestDB(t)
	svc := newCancellationTaskService()
	for id := uint(1); id <= 2; id++ {
		if err := db.Create(&adminModel.Task{ID: id, TaskType: "start", Status: "processing", UpdatedAt: time.Now().Add(-time.Hour)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc.queuedTasks.Store(uint(2), struct{}{})
	svc.CleanupTimeoutTasksWithLockRelease(time.Now())
	var tasks []adminModel.Task
	if err := db.Order("id").Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if tasks[0].Status != "pending" || tasks[1].Status != "processing" {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}
}

// Exercise the worker's genuine claim failure, not only a user cancellation.
func TestWorkerClaimFailureRequeuesUnstartedTask(t *testing.T) {
	db := setupCancellationTestDB(t)
	svc := newCancellationTaskService()
	task := adminModel.Task{ID: 1, Status: "processing", TaskType: "start", TimeoutDuration: 60}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Query().Before("gorm:query").Register("fail_claim", func(tx *gorm.DB) {
		if tx.Statement.Context != context.Background() {
			tx.AddError(errors.New("injected read failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	pool := &ProviderWorkerPool{Ctx: context.Background(), TaskService: svc}
	response := make(chan TaskResult, 1)
	pool.executeTask(TaskRequest{Task: task, ResponseCh: response})
	db.Callback().Query().Remove("fail_claim")
	if err := db.First(&task, 1).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "pending" {
		t.Fatalf("stranded task: %s", task.Status)
	}
}
