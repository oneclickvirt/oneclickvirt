package task

import (
	"testing"

	"gorm.io/gorm"
	adminModel "oneclickvirt/model/admin"
)

func TestCleanupInterruptedTasksDoesNotTouchTaskCreatedAfterSnapshot(t *testing.T) {
	db := setupCancellationTestDB(t)
	service := newCancellationTaskService()
	interrupted := &adminModel.Task{
		ID:       101,
		TaskType: "maintenance",
		Status:   mainTaskStatusRunning,
		TaskData: `{}`,
	}
	if err := db.Create(interrupted).Error; err != nil {
		t.Fatal(err)
	}

	injected := false
	db.Callback().Query().After("gorm:query").Register("test:startup_cleanup_inject", func(tx *gorm.DB) {
		if injected || tx.Statement.Table != "tasks" {
			return
		}
		injected = true
		if err := db.Create(&adminModel.Task{
			ID:       102,
			TaskType: "maintenance",
			Status:   mainTaskStatusRunning,
			TaskData: `{}`,
		}).Error; err != nil {
			t.Errorf("inject task: %v", err)
		}
	})
	defer db.Callback().Query().Remove("test:startup_cleanup_inject")

	service.cleanupInterruptedTasks("服务重启，任务被中断")

	var created adminModel.Task
	if err := db.First(&created, 102).Error; err != nil {
		t.Fatal(err)
	}
	if created.Status != mainTaskStatusRunning {
		t.Fatalf("task created after startup snapshot changed to %q", created.Status)
	}
	var saved adminModel.Task
	if err := db.First(&saved, interrupted.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != mainTaskStatusFailed {
		t.Fatalf("snapshot task status = %q, want failed", saved.Status)
	}
}
