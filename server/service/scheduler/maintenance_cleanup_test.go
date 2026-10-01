package scheduler

import (
	"testing"
	"time"

	adminModel "oneclickvirt/model/admin"
)

func TestCleanupOldTasksDeletesInBatchesAndPreservesActiveOrRecentRows(t *testing.T) {
	db := newInstanceRecoveryTestDB(t)
	threshold := time.Now().Add(-30 * 24 * time.Hour)
	old := threshold.Add(-24 * time.Hour)
	statuses := []string{"completed", "failed", "cancelled", "timeout"}
	rows := make([]adminModel.Task, 1203)
	for i := 0; i < 1200; i++ {
		rows[i] = adminModel.Task{
			TaskType:  "fixture",
			Status:    statuses[i%len(statuses)],
			TaskData:  `{}`,
			UpdatedAt: old,
		}
	}
	rows[1200] = adminModel.Task{TaskType: "fixture", Status: "running", TaskData: `{}`, UpdatedAt: old}
	rows[1201] = adminModel.Task{TaskType: "fixture", Status: "completed", TaskData: `{}`, UpdatedAt: time.Now()}
	rows[1202] = adminModel.Task{TaskType: "fixture", Status: "timeout", TaskData: `{}`, UpdatedAt: time.Now()}
	if err := db.CreateInBatches(&rows, 200).Error; err != nil {
		t.Fatal(err)
	}

	NewSchedulerService(nil).cleanupOldTasks()

	var remaining int64
	if err := db.Model(&adminModel.Task{}).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 3 {
		t.Fatalf("remaining task count = %d, want 3", remaining)
	}
	var activeOld, recentCompleted, recentTimeout int64
	if err := db.Model(&adminModel.Task{}).Where("status = ? AND updated_at < ?", "running", threshold).Count(&activeOld).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&adminModel.Task{}).Where("status = ? AND updated_at >= ?", "completed", threshold).Count(&recentCompleted).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&adminModel.Task{}).Where("status = ? AND updated_at >= ?", "timeout", threshold).Count(&recentTimeout).Error; err != nil {
		t.Fatal(err)
	}
	if activeOld != 1 || recentCompleted != 1 || recentTimeout != 1 {
		t.Fatalf("preserved counts = active:%d recent-completed:%d recent-timeout:%d", activeOld, recentCompleted, recentTimeout)
	}
}
