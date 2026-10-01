package task

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/service/database"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupCancellationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:task_cancel_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger:                                   logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&adminModel.Task{}, &adminModel.ConfigurationTask{}); err != nil {
		t.Fatal(err)
	}
	previousDB, previousLog, previousScheduler := global.APP_DB, global.APP_LOG, global.APP_SCHEDULER
	global.APP_DB, global.APP_LOG, global.APP_SCHEDULER = db, zap.NewNop(), nil
	t.Cleanup(func() {
		global.APP_DB, global.APP_LOG, global.APP_SCHEDULER = previousDB, previousLog, previousScheduler
		_ = sqlDB.Close()
	})
	return db
}

func newCancellationTaskService() *TaskService {
	return &TaskService{
		dbService:      database.GetDatabaseService(),
		contextManager: NewTaskContextManager(16, time.Hour),
	}
}

func TestCompleteTaskCannotResurrectCancelledTask(t *testing.T) {
	db := setupCancellationTestDB(t)
	service := newCancellationTaskService()
	t.Cleanup(func() { service.wg.Wait() })
	task := &adminModel.Task{ID: 1, TaskType: "start", Status: mainTaskStatusCancelling, Progress: 42, TaskData: `{}`}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(task).Update("cancel_reason", "用户取消").Error; err != nil {
		t.Fatal(err)
	}

	if err := service.CompleteTask(task.ID, true, "late provider success", nil); err != nil {
		t.Fatalf("CompleteTask returned error: %v", err)
	}
	var saved adminModel.Task
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusCancelled || saved.Progress != 42 {
		t.Fatalf("late success changed status/progress to %q/%d, want cancelled/42", saved.Status, saved.Progress)
	}
	if saved.CancelReason != "用户取消" {
		t.Fatalf("cancel reason changed to %q", saved.CancelReason)
	}
}

func TestShutdownLeavesUnconfirmedTaskForStartupRecovery(t *testing.T) {
	db := setupCancellationTestDB(t)
	task := adminModel.Task{ID: 90, TaskType: "reset", Status: mainTaskStatusRunning, TaskData: `{}`}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	service := newCancellationTaskService()
	service.poolManager = NewProviderPoolManager()
	service.shutdown = make(chan struct{})
	service.ctx, service.cancel = context.WithCancel(context.Background())
	service.Shutdown()

	var saved adminModel.Task
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusRunning {
		t.Fatalf("shutdown terminalized an unconfirmed task as %q", saved.Status)
	}
}

func TestDrainQueuedTasksUsesOneBatchUpdate(t *testing.T) {
	db := setupCancellationTestDB(t)
	service := newCancellationTaskService()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := &ProviderWorkerPool{
		TaskQueue:   make(chan TaskRequest, 3),
		Ctx:         ctx,
		TaskService: service,
	}
	for id := uint(21); id <= 23; id++ {
		task := adminModel.Task{ID: id, Status: mainTaskStatusProcessing, TaskType: "start", TaskData: `{}`}
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
		service.queuedTasks.Store(id, struct{}{})
		atomic.AddInt64(&pool.outstanding, 1)
		pool.TaskQueue <- TaskRequest{Task: task, ResponseCh: make(chan TaskResult, 1)}
	}

	updateCount := 0
	callbackName := "test:count_batch_requeue"
	if err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "tasks" {
			updateCount++
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Update().Remove(callbackName)

	pool.drainQueuedTasks()
	if updateCount != 1 {
		t.Fatalf("task row updates = %d, want one batch update", updateCount)
	}
	var tasks []adminModel.Task
	if err := db.Order("id").Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("tasks=%d, want 3", len(tasks))
	}
	for _, task := range tasks {
		if task.Status != mainTaskStatusPending {
			t.Errorf("task %d status = %q, want pending", task.ID, task.Status)
		}
		if _, owned := service.queuedTasks.Load(task.ID); owned {
			t.Errorf("task %d retained local queue ownership", task.ID)
		}
	}
	if got := atomic.LoadInt64(&pool.outstanding); got != 0 {
		t.Fatalf("outstanding = %d, want 0", got)
	}
}

func TestLateMainProgressDoesNotChangeTerminalTask(t *testing.T) {
	db := setupCancellationTestDB(t)
	task := &adminModel.Task{
		ID:            5,
		TaskType:      "start",
		Status:        mainTaskStatusCancelled,
		Progress:      42,
		StatusMessage: "任务已取消",
		TaskData:      `{}`,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	utils.UpdateTaskProgress(task.ID, 99, "late provider update")
	var saved adminModel.Task
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusCancelled || saved.Progress != 42 || saved.StatusMessage != "任务已取消" {
		t.Fatalf("late progress changed terminal task to status=%q progress=%d message=%q", saved.Status, saved.Progress, saved.StatusMessage)
	}
}

func TestLateFailureDoesNotOverwriteCancelledTask(t *testing.T) {
	db := setupCancellationTestDB(t)
	task := &adminModel.Task{
		ID:       7,
		TaskType: "start",
		Status:   mainTaskStatusCancelled,
		Progress: 42,
		TaskData: `{}`,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	utils.MarkTaskFailed(task.ID, "late provider failure")
	var saved adminModel.Task
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusCancelled || saved.ErrorMessage != "" {
		t.Fatalf("late failure changed cancelled task to status=%q error=%q", saved.Status, saved.ErrorMessage)
	}
}

func TestLateConfigProgressDoesNotChangeTerminalTask(t *testing.T) {
	db := setupCancellationTestDB(t)
	task := &adminModel.ConfigurationTask{
		ID:         6,
		ProviderID: 9,
		TaskType:   "auto_configure",
		Status:     adminModel.TaskStatusCancelled,
		Progress:   37,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	manager := NewTaskStateManager(newCancellationTaskService())
	if err := manager.UpdateTaskProgress(task.ID, "configuration_tasks", 99, "late provider update"); err != nil {
		t.Fatal(err)
	}
	var saved adminModel.ConfigurationTask
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != adminModel.TaskStatusCancelled || saved.Progress != 37 {
		t.Fatalf("late config progress changed terminal task to status=%q progress=%d", saved.Status, saved.Progress)
	}
}

func TestCancelProcessingTaskIsTerminal(t *testing.T) {
	db := setupCancellationTestDB(t)
	service := newCancellationTaskService()
	t.Cleanup(func() { service.wg.Wait() })
	task := &adminModel.Task{ID: 2, TaskType: "create", Status: mainTaskStatusProcessing, UserID: 7, IsForceStoppable: true, TaskData: `{}`}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.CancelTask(task.ID, task.UserID); err != nil {
		t.Fatalf("CancelTask returned error: %v", err)
	}
	var saved adminModel.Task
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusCancelled || saved.CompletedAt == nil {
		t.Fatalf("processing task = status %q completedAt=%v, want terminal cancelled", saved.Status, saved.CompletedAt)
	}
}

func TestConfigTaskCompletionDoesNotOverwriteCancellation(t *testing.T) {
	db := setupCancellationTestDB(t)
	task := &adminModel.ConfigurationTask{ID: 3, ProviderID: 9, TaskType: "auto_configure", Status: adminModel.TaskStatusCancelled, ErrorMessage: "任务已取消"}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	manager := NewTaskStateManager(newCancellationTaskService())
	if err := manager.CompleteConfigTask(task.ID, true, "late success", nil); err != nil {
		t.Fatalf("CompleteConfigTask returned error: %v", err)
	}
	var saved adminModel.ConfigurationTask
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != adminModel.TaskStatusCancelled {
		t.Fatalf("late config completion changed status to %q", saved.Status)
	}
}

func TestCancelConfigTaskIsIdempotent(t *testing.T) {
	db := setupCancellationTestDB(t)
	task := &adminModel.ConfigurationTask{ID: 4, ProviderID: 9, TaskType: "auto_configure", Status: adminModel.TaskStatusCancelled, ErrorMessage: "用户取消"}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	manager := NewTaskStateManager(newCancellationTaskService())
	if err := manager.CancelConfigTask(task.ID, "重复取消"); err != nil {
		t.Fatalf("repeated CancelConfigTask returned error: %v", err)
	}
	var saved adminModel.ConfigurationTask
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != adminModel.TaskStatusCancelled || saved.ErrorMessage != "用户取消" {
		t.Fatalf("repeated cancellation changed task: status=%q error=%q", saved.Status, saved.ErrorMessage)
	}
}

func TestCancelPendingConfigTaskIsTerminal(t *testing.T) {
	db := setupCancellationTestDB(t)
	task := &adminModel.ConfigurationTask{ID: 10, ProviderID: 9, TaskType: "auto_configure", Status: adminModel.TaskStatusPending}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	manager := NewTaskStateManager(newCancellationTaskService())
	if err := manager.CancelConfigTask(task.ID, "用户取消"); err != nil {
		t.Fatalf("CancelConfigTask returned error: %v", err)
	}
	var saved adminModel.ConfigurationTask
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != adminModel.TaskStatusCancelled || saved.CompletedAt == nil {
		t.Fatalf("pending config task = status %q completedAt=%v, want terminal cancelled", saved.Status, saved.CompletedAt)
	}
}

func TestCompleteConfigTaskFinalizesCancellationAfterWorkerReturns(t *testing.T) {
	db := setupCancellationTestDB(t)
	task := &adminModel.ConfigurationTask{ID: 11, ProviderID: 9, TaskType: "auto_configure", Status: adminModel.TaskStatusCancelling, Progress: 45}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	manager := NewTaskStateManager(newCancellationTaskService())
	if err := manager.CompleteConfigTask(task.ID, true, "late provider success", nil); err != nil {
		t.Fatalf("CompleteConfigTask returned error: %v", err)
	}
	var saved adminModel.ConfigurationTask
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != adminModel.TaskStatusCancelled || saved.Success || saved.Progress != 45 {
		t.Fatalf("cancelled config task = status %q success %v progress %d", saved.Status, saved.Success, saved.Progress)
	}
}

func TestCompleteConfigTaskRejectsMissingTask(t *testing.T) {
	setupCancellationTestDB(t)
	manager := NewTaskStateManager(newCancellationTaskService())
	if err := manager.CompleteConfigTask(404, true, "", nil); err == nil {
		t.Fatal("CompleteConfigTask should report a missing task")
	}
}

func TestCompleteConfigTaskPersistsResultAndIsIdempotent(t *testing.T) {
	db := setupCancellationTestDB(t)
	manager := NewTaskStateManager(newCancellationTaskService())
	task := &adminModel.ConfigurationTask{ID: 5, ProviderID: 9, TaskType: "auto_configure", Status: adminModel.TaskStatusRunning}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	if err := manager.CompleteConfigTask(task.ID, true, "", map[string]interface{}{"providerId": float64(9)}); err != nil {
		t.Fatalf("CompleteConfigTask returned error: %v", err)
	}
	var saved adminModel.ConfigurationTask
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != adminModel.TaskStatusCompleted || saved.Progress != 100 || !saved.Success {
		t.Fatalf("completed config task = status %q progress %d success %v", saved.Status, saved.Progress, saved.Success)
	}
	if saved.ResultData != `{"providerId":9}` {
		t.Fatalf("result data = %q, want serialized provider id", saved.ResultData)
	}

	// A late duplicate completion is harmless and must not alter the stored result.
	if err := manager.CompleteConfigTask(task.ID, false, "late failure", map[string]interface{}{"providerId": 10}); err != nil {
		t.Fatalf("duplicate CompleteConfigTask returned error: %v", err)
	}
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != adminModel.TaskStatusCompleted || saved.ResultData != `{"providerId":9}` {
		t.Fatalf("duplicate completion changed terminal task: status=%q result=%q", saved.Status, saved.ResultData)
	}
}

func TestStartConfigTaskUsesCompareAndSwap(t *testing.T) {
	db := setupCancellationTestDB(t)
	manager := NewTaskStateManager(newCancellationTaskService())
	task := &adminModel.ConfigurationTask{ID: 6, ProviderID: 9, TaskType: "auto_configure", Status: adminModel.TaskStatusPending}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}

	if err := manager.StartConfigTask(task.ID); err != nil {
		t.Fatalf("StartConfigTask returned error: %v", err)
	}
	if err := manager.StartConfigTask(task.ID); err == nil {
		t.Fatal("second StartConfigTask call should reject the running task")
	}

	var saved adminModel.ConfigurationTask
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != adminModel.TaskStatusRunning || saved.StartedAt == nil {
		t.Fatalf("started config task = status %q startedAt=%v", saved.Status, saved.StartedAt)
	}
}

func TestStartConfigTaskCannotResurrectCancelledTask(t *testing.T) {
	db := setupCancellationTestDB(t)
	manager := NewTaskStateManager(newCancellationTaskService())
	task := &adminModel.ConfigurationTask{ID: 7, ProviderID: 9, TaskType: "auto_configure", Status: adminModel.TaskStatusPending}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	if err := manager.CancelConfigTask(task.ID, "用户取消"); err != nil {
		t.Fatalf("CancelConfigTask returned error: %v", err)
	}
	if err := manager.StartConfigTask(task.ID); err == nil {
		t.Fatal("StartConfigTask should reject a cancelled task")
	}

	var saved adminModel.ConfigurationTask
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != adminModel.TaskStatusCancelled {
		t.Fatalf("cancelled config task was resurrected as %q", saved.Status)
	}
}

func TestCancelRunningTaskCancelsWorkerContext(t *testing.T) {
	db := setupCancellationTestDB(t)
	service := newCancellationTaskService()
	t.Cleanup(func() { service.wg.Wait() })
	oldGracePeriod := cancellationGracePeriod
	cancellationGracePeriod = time.Millisecond
	t.Cleanup(func() { cancellationGracePeriod = oldGracePeriod })

	task := &adminModel.Task{ID: 8, TaskType: "start", Status: mainTaskStatusRunning, UserID: 11, IsForceStoppable: true, TaskData: `{}`}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.contextManager.Add(task.ID, ctx, cancel); err != nil {
		t.Fatal(err)
	}
	if err := service.CancelTask(task.ID, task.UserID); err != nil {
		t.Fatalf("CancelTask returned error: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("worker context was not cancelled")
	}
	time.Sleep(10 * time.Millisecond)
	var saved adminModel.Task
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusCancelling {
		t.Fatalf("task status = %q while worker context remains registered, want cancelling", saved.Status)
	}
	if err := service.CompleteTask(task.ID, false, context.Canceled.Error(), nil); err != nil {
		t.Fatalf("worker completion returned error: %v", err)
	}
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusCancelled {
		t.Fatalf("task status after worker completion = %q, want cancelled", saved.Status)
	}
}

func TestRepeatedAdminCancellationSchedulesOneCleanup(t *testing.T) {
	db := setupCancellationTestDB(t)
	service := newCancellationTaskService()
	t.Cleanup(func() { service.wg.Wait() })
	oldGracePeriod := cancellationGracePeriod
	cancellationGracePeriod = 50 * time.Millisecond
	t.Cleanup(func() { cancellationGracePeriod = oldGracePeriod })

	task := &adminModel.Task{ID: 81, TaskType: "start", Status: mainTaskStatusRunning, UserID: 11, IsForceStoppable: true, TaskData: `{}`}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.contextManager.Add(task.ID, ctx, cancel); err != nil {
		t.Fatal(err)
	}

	if err := service.CancelTaskByAdmin(task.ID, "first request"); err != nil {
		t.Fatalf("first cancellation returned error: %v", err)
	}
	firstCleanup, scheduled := service.cancellationCleanup.Load(task.ID)
	if !scheduled {
		t.Fatal("first cancellation did not schedule cleanup")
	}
	if service.scheduleCancellationCleanup(task.ID, cancellationCleanupRunning) {
		t.Fatal("repeated cancellation scheduled a second cleanup")
	}
	if err := service.CancelTaskByAdmin(task.ID, "repeated request"); err != nil {
		t.Fatalf("repeated cancellation returned error: %v", err)
	}
	if current, ok := service.cancellationCleanup.Load(task.ID); !ok || current != firstCleanup {
		t.Fatal("repeated cancellation replaced the active cleanup")
	}
	service.wg.Wait()
	if _, ok := service.cancellationCleanup.Load(task.ID); ok {
		t.Fatal("completed cleanup remained marked active")
	}
	var saved adminModel.Task
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusCancelling {
		t.Fatalf("task status = %q while its worker context is still registered", saved.Status)
	}
}

func TestAdminCancellationFinalizesWhenWorkerContextIsAbsent(t *testing.T) {
	db := setupCancellationTestDB(t)
	service := newCancellationTaskService()
	t.Cleanup(func() { service.wg.Wait() })
	oldGracePeriod := cancellationGracePeriod
	cancellationGracePeriod = time.Millisecond
	t.Cleanup(func() { cancellationGracePeriod = oldGracePeriod })

	task := adminModel.Task{ID: 82, TaskType: "start", Status: mainTaskStatusRunning, IsForceStoppable: true, TaskData: `{}`}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.ForceStopTask(task.ID, "local fixture"); err != nil {
		t.Fatalf("ForceStopTask returned error: %v", err)
	}
	service.wg.Wait()

	var saved adminModel.Task
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusCancelled || saved.CompletedAt == nil {
		t.Fatalf("task status = %q completedAt=%v, want cancelled with completion time", saved.Status, saved.CompletedAt)
	}
}

func TestTimeoutCleanupWaitsForWorkerBeforeReleasingTask(t *testing.T) {
	db := setupCancellationTestDB(t)
	service := newCancellationTaskService()
	t.Cleanup(func() { service.wg.Wait() })
	task := &adminModel.Task{
		ID:              9,
		TaskType:        "start",
		Status:          mainTaskStatusRunning,
		TimeoutDuration: 1,
		StartedAt:       timePtr(time.Now().Add(-time.Minute)),
		TaskData:        `{}`,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.contextManager.Add(task.ID, ctx, cancel); err != nil {
		t.Fatal(err)
	}

	timedOut, finalized := service.CleanupTimeoutTasksWithLockRelease(time.Now().Add(-time.Second))
	if timedOut != 1 || finalized != 0 {
		t.Fatalf("timeout cleanup counts = (%d, %d), want (1, 0)", timedOut, finalized)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("timeout cleanup did not cancel the worker context")
	}
	var saved adminModel.Task
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusCancelling {
		t.Fatalf("task status = %q before worker completion, want cancelling", saved.Status)
	}

	if err := service.CompleteTask(task.ID, false, context.Canceled.Error(), nil); err != nil {
		t.Fatalf("CompleteTask returned error: %v", err)
	}
	if err := db.First(&saved, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusTimeout {
		t.Fatalf("task status after worker completion = %q, want timeout", saved.Status)
	}
}

func TestBatchTaskTransitionReturnsOnlyRowsStillInSourceState(t *testing.T) {
	db := setupCancellationTestDB(t)
	now := time.Now()
	active := &adminModel.Task{ID: 90, TaskType: "start", Status: mainTaskStatusRunning, StartedAt: timePtr(now.Add(-time.Minute)), TaskData: `{}`}
	changed := &adminModel.Task{ID: 91, TaskType: "start", Status: mainTaskStatusCancelled, TaskData: `{}`}
	if err := db.Create(active).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(changed).Error; err != nil {
		t.Fatal(err)
	}

	transitioned, err := transitionTaskRows(context.Background(), []uint{active.ID, changed.ID}, mainTaskStatusRunning, map[string]interface{}{
		"status":        mainTaskStatusTimeout,
		"cancel_reason": taskTimeoutCancelReason,
		"completed_at":  &now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(transitioned) != 1 || transitioned[0].ID != active.ID {
		t.Fatalf("transitioned rows = %#v, want only active task", transitioned)
	}
	var saved adminModel.Task
	if err := db.First(&saved, changed.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusCancelled {
		t.Fatalf("concurrent terminal task changed to %q", saved.Status)
	}
}

func timePtr(value time.Time) *time.Time { return &value }
