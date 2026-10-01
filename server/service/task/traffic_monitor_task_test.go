package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTrafficMonitorTaskTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:traffic_monitor_%d?mode=memory&cache=shared", time.Now().UnixNano())),
		&gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&adminModel.SystemConfig{}, &adminModel.Task{}, &adminModel.TrafficMonitorTask{}); err != nil {
		t.Fatal(err)
	}
	previousDB, previousScheduler := global.APP_DB, global.APP_SCHEDULER
	global.APP_DB, global.APP_SCHEDULER = db, nil
	t.Cleanup(func() {
		global.APP_DB, global.APP_SCHEDULER = previousDB, previousScheduler
		_ = sqlDB.Close()
	})
	return db
}

func TestTrafficMonitorTaskRejectsOverlappingOperationsWithoutExtraRows(t *testing.T) {
	db := setupTrafficMonitorTaskTestDB(t)
	trafficTask, adminTask, err := CreateTrafficMonitorTask(7, "enable", 2)
	if err != nil || trafficTask.ID == 0 || adminTask.ID == 0 {
		t.Fatalf("first task = (%v, %v), err = %v", trafficTask, adminTask, err)
	}
	_, _, err = CreateTrafficMonitorTask(7, "disable", 2)
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != common.CodeConflict {
		t.Fatalf("overlapping operation error = %v, want HTTP 409", err)
	}
	var trafficCount, adminCount int64
	if err := db.Model(&adminModel.TrafficMonitorTask{}).Count(&trafficCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&adminModel.Task{}).Count(&adminCount).Error; err != nil {
		t.Fatal(err)
	}
	if trafficCount != 1 || adminCount != 1 {
		t.Fatalf("duplicate created rows: traffic=%d admin=%d", trafficCount, adminCount)
	}
	if err := db.Model(adminTask).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := CreateTrafficMonitorTask(7, "disable", 2); err != nil {
		t.Fatalf("completed operation should permit a new task: %v", err)
	}
}

func TestConcurrentTrafficMonitorSubmissionsCreateOneTask(t *testing.T) {
	db := setupTrafficMonitorTaskTestDB(t)
	operations := []string{"enable", "disable", "detect"}
	results := make(chan error, len(operations))
	var group sync.WaitGroup
	for _, operation := range operations {
		group.Add(1)
		go func(operation string) {
			defer group.Done()
			_, _, err := CreateTrafficMonitorTask(8, operation, 2)
			results <- err
		}(operation)
	}
	group.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		var appErr *common.AppError
		if errors.As(err, &appErr) && appErr.Code == common.CodeConflict {
			conflicts++
			continue
		}
		t.Fatalf("unexpected concurrent submission error: %v", err)
	}
	if successes != 1 || conflicts != 2 {
		t.Fatalf("successes=%d conflicts=%d, want 1 and 2", successes, conflicts)
	}
	var count int64
	if err := db.Model(&adminModel.TrafficMonitorTask{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("traffic task rows=%d, want 1", count)
	}
}
