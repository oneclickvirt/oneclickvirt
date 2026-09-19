package task

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/database"
)

func TestDeletePortTaskKeepsAllocationWhenNodeUnavailable(t *testing.T) {
	driverName := fmt.Sprintf("port_cleanup_sqlite_%d", time.Now().UnixNano())
	// Supply the MySQL string functions used by unrelated task progress logs
	// so this SQLite fixture can execute the complete deletion task.
	sql.Register(driverName, &sqlite3.SQLiteDriver{ConnectHook: func(conn *sqlite3.SQLiteConn) error {
		for _, fn := range []struct {
			name  string
			value interface{}
		}{
			{"CONCAT", func(parts ...string) string { return strings.Join(parts, "") }},
			{"CHAR_LENGTH", func(value string) int { return len([]rune(value)) }},
			{"LEFT", func(value string, count int) string {
				chars := []rune(value)
				if count < 0 {
					count = 0
				}
				if count > len(chars) {
					count = len(chars)
				}
				return string(chars[:count])
			}},
		} {
			if err := conn.RegisterFunc(fn.name, fn.value, true); err != nil {
				return err
			}
		}
		return nil
	}})
	db, err := gorm.Open(sqlite.Dialector{DriverName: driverName, DSN: "file:" + driverName + "?mode=memory&cache=shared"}, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []interface{}{&adminModel.Task{}, &providerModel.Provider{}, &providerModel.Port{}} {
		if err := db.AutoMigrate(schema); err != nil {
			t.Fatal(err)
		}
	}
	// SQLite index names are database-wide; the production MySQL schemas use
	// some table-local index names twice. Define this fixture table explicitly.
	if err := db.Exec(`CREATE TABLE instances (id INTEGER PRIMARY KEY, name TEXT, provider_id INTEGER, private_ip TEXT, ipv6_address TEXT, provider_vm_id TEXT, deleted_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	previousDB, previousLogger := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() { global.APP_DB, global.APP_LOG = previousDB, previousLogger })
	node := providerModel.Provider{ID: 9191, Name: "unavailable", Type: "incus", Status: "inactive"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	instance := providerModel.Instance{ID: 9191, Name: "guest", ProviderID: node.ID}
	if err := db.Exec("INSERT INTO instances (id, name, provider_id) VALUES (?, ?, ?)", instance.ID, instance.Name, instance.ProviderID).Error; err != nil {
		t.Fatal(err)
	}
	port := providerModel.Port{ID: 9191, ProviderID: node.ID, InstanceID: instance.ID, HostPort: 22000, GuestPort: 22, Protocol: "tcp", MappingType: "node", MappingMethod: "iptables", Status: "active"}
	if err := db.Create(&port).Error; err != nil {
		t.Fatal(err)
	}
	task := adminModel.Task{ID: 9191, TaskType: "delete_port_mapping", Status: "running", TaskData: `{"portId":9191}`}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	service := &TaskService{dbService: database.GetDatabaseService()}
	previousStateManager := globalTaskStateManager
	globalTaskStateManager = NewTaskStateManager(service)
	t.Cleanup(func() { globalTaskStateManager = previousStateManager })
	if err := service.executeDeletePortMappingTask(context.Background(), &task); err == nil || !strings.Contains(err.Error(), "保留端口记录") {
		t.Fatalf("unavailable node deletion error = %v", err)
	}
	var saved providerModel.Port
	if err := db.First(&saved, port.ID).Error; err != nil {
		t.Fatalf("live allocation was released after remote cleanup failed: %v", err)
	}
	if saved.HostPort != 22000 || saved.InstanceID != instance.ID {
		t.Fatalf("allocation changed: %+v", saved)
	}
	if err := db.First(&task, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status == "completed" {
		t.Fatal("failed cleanup reported completed")
	}
	// Deliberately removing an orphan whose provider no longer exists remains
	// supported; retaining failed live-node allocations must not break it.
	if err := db.Delete(&node).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.executeDeletePortMappingTask(context.Background(), &task); err != nil {
		t.Fatalf("orphan cleanup: %v", err)
	}
	var count int64
	if err := db.Unscoped().Model(&providerModel.Port{}).Where("id = ?", port.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("orphan allocation was not cleaned up")
	}
}
