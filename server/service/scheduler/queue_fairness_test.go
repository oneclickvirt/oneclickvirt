package scheduler

import (
	"fmt"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"testing"
	"time"
)

type fullQueueTaskService struct{ seen []uint }

func (s *fullQueueTaskService) StartTask(id uint) error {
	s.seen = append(s.seen, id)
	return fmt.Errorf("queue full")
}
func (s *fullQueueTaskService) CancelTaskByAdmin(uint, string) error { return nil }
func (s *fullQueueTaskService) CleanupTimeoutTasksWithLockRelease(time.Time) (int64, int64) {
	return 0, 0
}

func TestSchedulerFullProviderCannotHideLaterPendingTasks(t *testing.T) {
	db := newInstanceRecoveryTestDB(t)
	var indexes []struct{ Name string }
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type='index' AND name NOT LIKE 'sqlite_%'").Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		_ = db.Exec("DROP INDEX IF EXISTS \"" + index.Name + "\"").Error
	}
	if err := db.AutoMigrate(&providerModel.Provider{}); err != nil {
		t.Fatal(err)
	}
	for id := uint(1); id <= 2; id++ {
		if err := db.Create(&providerModel.Provider{ID: id, Name: fmt.Sprint(id), UUID: fmt.Sprintf("provider-%d", id), Status: "active"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for id := uint(1); id <= 51; id++ {
		p := uint(1)
		if id == 51 {
			p = 2
		}
		if err := db.Create(&adminModel.Task{ID: id, ProviderID: &p, Status: "pending", TaskType: "start"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	fake := &fullQueueTaskService{}
	svc := NewSchedulerService(fake)
	svc.processPendingTasks()
	svc.processPendingTasks()
	if len(fake.seen) != 51 || fake.seen[50] != 51 {
		t.Fatalf("later provider was starved: %v", fake.seen)
	}
}

func TestSchedulerRescansOlderTasksWhileNewTasksArrive(t *testing.T) {
	db := newInstanceRecoveryTestDB(t)
	var indexes []struct{ Name string }
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type='index' AND name NOT LIKE 'sqlite_%'").Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		_ = db.Exec("DROP INDEX IF EXISTS \"" + index.Name + "\"").Error
	}
	if err := db.AutoMigrate(&providerModel.Provider{}); err != nil {
		t.Fatal(err)
	}
	providerID := uint(1)
	if err := db.Create(&providerModel.Provider{ID: providerID, Name: "1", UUID: "provider-1", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	for id := uint(1); id <= 51; id++ {
		if err := db.Create(&adminModel.Task{ID: id, ProviderID: &providerID, Status: "pending", TaskType: "start"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	fake := &fullQueueTaskService{}
	svc := NewSchedulerService(fake)
	svc.processPendingTasks()
	for id := uint(52); id <= 101; id++ {
		if err := db.Create(&adminModel.Task{ID: id, ProviderID: &providerID, Status: "pending", TaskType: "start"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc.processPendingTasks()
	svc.processPendingTasks()
	seenFirstTaskAgain := false
	for _, id := range fake.seen[51:] {
		if id == 1 {
			seenFirstTaskAgain = true
			break
		}
	}
	if !seenFirstTaskAgain {
		t.Fatalf("older pending task was not revisited after new tasks arrived; processed %v", fake.seen)
	}
}
