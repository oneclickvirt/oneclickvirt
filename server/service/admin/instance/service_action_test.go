package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/service/interfaces"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type instanceActionTaskFixture struct {
	interfaces.TaskServiceInterface
	create func(uint, *uint, *uint, string, string, int) (*adminModel.Task, error)
}

func (s *instanceActionTaskFixture) CreateTask(userID uint, providerID, instanceID *uint, taskType, taskData string, timeout int) (*adminModel.Task, error) {
	return s.create(userID, providerID, instanceID, taskType, taskData, timeout)
}

func TestInstanceActionWithTaskReturnsStableIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		createError  bool
		wantCalls    int
	}{
		{name: "replaced_instance", status: "running", wantCalls: 1},
		{name: "task_insert_failure", status: "running", createError: true, wantCalls: 1},
		{name: "busy_instance", status: "rebuilding", wantCalls: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:admin_action_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			sqlDB.SetMaxOpenConns(1)
			previousDB, previousLog := global.APP_DB, global.APP_LOG
			global.APP_DB, global.APP_LOG = db, zap.NewNop()
			t.Cleanup(func() {
				global.APP_DB, global.APP_LOG = previousDB, previousLog
				_ = sqlDB.Close()
			})
			for _, query := range []string{
				"CREATE TABLE instances (id INTEGER PRIMARY KEY, user_id INTEGER, provider_id INTEGER, status TEXT, deleted_at DATETIME)",
				"CREATE TABLE tasks (id INTEGER PRIMARY KEY, instance_id INTEGER, task_type TEXT, status TEXT, deleted_at DATETIME)",
			} {
				if err := db.Exec(query).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Exec("INSERT INTO instances (id, user_id, provider_id, status) VALUES (3, 9, 2, ?)", tc.status).Error; err != nil {
				t.Fatal(err)
			}
			calls := 0
			creator := &instanceActionTaskFixture{create: func(userID uint, providerID, instanceID *uint, taskType, taskData string, timeout int) (*adminModel.Task, error) {
				calls++
				if userID != 9 || *providerID != 2 || *instanceID != 3 || taskType != "rebuild" || timeout != 1800 {
					t.Fatal("task submission lost the original operation identity")
				}
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(taskData), &data); err != nil || data["resetImage"] != "debian:12" {
					t.Fatalf("invalid rebuild payload: %s", taskData)
				}
				if tc.createError {
					return nil, errors.New("injected task insert failure")
				}
				// Simulate a fast replacement before the submission response.
				if err := db.Exec("UPDATE instances SET id = 4 WHERE id = 3").Error; err != nil {
					t.Fatal(err)
				}
				replacementID := uint(4)
				return &adminModel.Task{ID: 73, InstanceID: &replacementID}, nil
			}}
			taskID, err := NewService(creator).InstanceActionWithTask(3, adminModel.InstanceActionRequest{Action: "rebuild", Image: "debian:12"}, 0)
			if calls != tc.wantCalls {
				t.Fatalf("CreateTask calls=%d, want %d", calls, tc.wantCalls)
			}
			if tc.createError || tc.wantCalls == 0 {
				if err == nil || taskID != 0 {
					t.Fatalf("rejected action returned task ID %d, error=%v", taskID, err)
				}
				if tc.createError && !strings.Contains(err.Error(), "injected task insert failure") {
					t.Fatalf("lost task insertion error: %v", err)
				}
				return
			}
			if err != nil || taskID != 73 {
				t.Fatalf("submission returned task ID %d, error=%v; want stable ID 73", taskID, err)
			}
		})
	}
}
