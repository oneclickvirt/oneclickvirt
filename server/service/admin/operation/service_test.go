package operation

import (
	"path/filepath"
	"strings"
	"testing"

	"oneclickvirt/global"
	"oneclickvirt/model/monitoring"
	"oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTransferUnassignedInstanceUpdatesQuotaAndMonitoring(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "transfer.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	previousDB, previousLogger := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB, global.APP_LOG = previousDB, previousLogger
		sqlDB.Close()
	})
	const instanceID, firstOwner, nextOwner, limitedOwner = 1, 2, 3, 4
	for _, query := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, used_quota INTEGER, pending_quota INTEGER, total_quota INTEGER, updated_at DATETIME, deleted_at DATETIME)",
		"CREATE TABLE instances (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT, cpu INTEGER, memory INTEGER, disk INTEGER, updated_at DATETIME, deleted_at DATETIME)",
		"CREATE TABLE agent_monitors (id INTEGER PRIMARY KEY, instance_id INTEGER, user_id INTEGER, updated_at DATETIME, deleted_at DATETIME)",
		"INSERT INTO users (id, used_quota, pending_quota, total_quota) VALUES (2, 2, 0, 30), (3, 5, 0, 30), (4, 0, 5, 15)",
		"INSERT INTO instances (id, user_id, status, cpu, memory, disk) VALUES (1, 0, 'running', 2, 1024, 5)",
		"INSERT INTO agent_monitors (id, instance_id, user_id) VALUES (1, 1, 0)",
	} {
		if err := db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}

	assertAssignments := func(ownerID uint, quotas ...int) {
		t.Helper()
		var instance provider.Instance
		if err := db.First(&instance, instanceID).Error; err != nil {
			t.Fatal(err)
		}
		var monitor monitoring.AgentMonitor
		if err := db.Where("instance_id = ?", instanceID).First(&monitor).Error; err != nil {
			t.Fatal(err)
		}
		if instance.UserID != ownerID || monitor.UserID != ownerID {
			t.Errorf("instance owner = %d, monitor owner = %d, want %d", instance.UserID, monitor.UserID, ownerID)
		}
		for index, userID := range []uint{firstOwner, nextOwner, limitedOwner} {
			var user userModel.User
			if err := db.First(&user, userID).Error; err != nil {
				t.Fatal(err)
			}
			if user.UsedQuota != quotas[index] {
				t.Errorf("user %d quota used = %d, want %d", user.ID, user.UsedQuota, quotas[index])
			}
		}
	}

	service := &OperationService{}
	if err := service.TransferInstance(99, instanceID, firstOwner); err != nil {
		t.Fatal(err)
	}
	assertAssignments(firstOwner, 15, 5, 0)
	if err := service.TransferInstance(99, instanceID, nextOwner); err != nil {
		t.Fatal(err)
	}
	assertAssignments(nextOwner, 2, 18, 0)
	if err := service.TransferInstance(99, instanceID, limitedOwner); err == nil || !strings.Contains(err.Error(), "配额不足") {
		t.Fatalf("limited owner transfer error = %v", err)
	}
	assertAssignments(nextOwner, 2, 18, 0)
	if err := db.Exec("UPDATE instances SET status = 'deleting' WHERE id = ?", instanceID).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.TransferInstance(99, instanceID, firstOwner); err == nil || !strings.Contains(err.Error(), "正在操作") {
		t.Fatalf("transfer during deletion error = %v", err)
	}
	assertAssignments(nextOwner, 2, 18, 0)
}
