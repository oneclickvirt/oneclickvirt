package config

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	taskManager "oneclickvirt/service/task"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestFinishedConfigurationRetriesPersistenceWithoutRerunning(t *testing.T) {
	db := setupConfigTaskTest(t)
	item := adminModel.ConfigurationTask{ProviderID: 88, Status: adminModel.TaskStatusPending, TaskType: "auto_configure"}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	svc := &TaskService{runningTasks: make(map[uint]*TaskContext)}
	if err := svc.StartTask(item.ID); err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int64
	if err := db.Callback().Update().Before("gorm:update").Register("test:finish_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "configuration_tasks" && writes.Add(1) == 1 {
			tx.AddError(errors.New("temporary fixture write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Update().Remove("test:finish_failure")
	if err := svc.FinishTask(item.ID, true, "", nil); err == nil {
		t.Fatal("expected injected failure")
	}
	if err := svc.WaitForTaskRelease(item.ProviderID, item.ID, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&item, item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if item.Status != adminModel.TaskStatusCompleted || !item.Success {
		t.Fatalf("lost completion: %s", item.Status)
	}
	if writes.Load() != 2 {
		t.Fatalf("unexpected persistence retries: %d", writes.Load())
	}
}

func setupConfigTaskTest(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:config_task_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{
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
	if err := db.AutoMigrate(&adminModel.ConfigurationTask{}); err != nil {
		t.Fatal(err)
	}
	previousDB, previousLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	taskManager.InitTaskStateManager(nil)
	t.Cleanup(func() {
		global.APP_DB, global.APP_LOG = previousDB, previousLog
		_ = sqlDB.Close()
	})
	return db
}

func TestConfigurationTasksKeepProviderOrderAndWaitForCancellation(t *testing.T) {
	db := setupConfigTaskTest(t)
	older := &adminModel.ConfigurationTask{ID: 1, ProviderID: 7, TaskType: "auto_configure", Status: adminModel.TaskStatusPending}
	newer := &adminModel.ConfigurationTask{ID: 2, ProviderID: 7, TaskType: "auto_configure", Status: adminModel.TaskStatusPending}
	if err := db.Create(older).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(newer).Error; err != nil {
		t.Fatal(err)
	}

	service := &TaskService{runningTasks: make(map[uint]*TaskContext)}
	if err := service.StartTask(newer.ID); err == nil {
		t.Fatal("newer config task started ahead of the older pending task")
	}
	if err := service.StartTask(older.ID); err != nil {
		t.Fatalf("start older config task: %v", err)
	}
	if err := service.StartTask(newer.ID); err == nil {
		t.Fatal("second config task overlapped the running provider task")
	}
	if err := service.CancelTask(older.ID); err != nil {
		t.Fatalf("cancel config task: %v", err)
	}
	if err := service.WaitForTaskRelease(older.ProviderID, older.ID, 5*time.Millisecond); err == nil {
		t.Fatal("provider slot was released before the cancelled task finished")
	}
	if err := service.FinishTask(older.ID, false, "任务被取消", nil); err != nil {
		t.Fatalf("finish cancelled config task: %v", err)
	}
	if err := service.WaitForTaskRelease(older.ProviderID, older.ID, time.Second); err != nil {
		t.Fatalf("provider slot was not released after worker completion: %v", err)
	}
	if err := service.StartTask(newer.ID); err != nil {
		t.Fatalf("start next config task after prior worker exited: %v", err)
	}
}

func TestConfigurationTaskStartupCleanupFinalizesUnfinishedTasks(t *testing.T) {
	db := setupConfigTaskTest(t)
	statuses := []string{
		adminModel.TaskStatusPending,
		adminModel.TaskStatusRunning,
		adminModel.TaskStatusCancelling,
	}
	for i, status := range statuses {
		task := &adminModel.ConfigurationTask{
			ID:         uint(i + 1),
			ProviderID: 20,
			TaskType:   "auto_configure",
			Status:     status,
			Success:    true,
		}
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
	}

	service := &TaskService{runningTasks: make(map[uint]*TaskContext)}
	service.cleanupUnfinishedTasks()
	service.cleanupUnfinishedTasks()

	var tasks []adminModel.ConfigurationTask
	if err := db.Order("id ASC").Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != len(statuses) {
		t.Fatalf("got %d configuration tasks, want %d", len(tasks), len(statuses))
	}
	for _, task := range tasks {
		if task.Status != adminModel.TaskStatusCancelled {
			t.Errorf("task %d status = %q, want cancelled", task.ID, task.Status)
		}
		if task.Success || task.CompletedAt == nil {
			t.Errorf("task %d success=%v completedAt=%v, want failed and finalized", task.ID, task.Success, task.CompletedAt)
		}
		if task.ErrorMessage != "服务重启，任务已取消" {
			t.Errorf("task %d error message = %q", task.ID, task.ErrorMessage)
		}
	}
}
