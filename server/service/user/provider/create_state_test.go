package provider

import (
	"testing"

	"gorm.io/gorm"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
)

func TestCreateStateRejectsCancellationAndPreservesReservations(t *testing.T) {
	db := finalizedSSHPortDB(t)
	// Minimal schema avoids MySQL's table-scoped index names colliding in SQLite.
	for _, ddl := range []string{
		"CREATE TABLE tasks (id INTEGER PRIMARY KEY, status TEXT, task_data TEXT)",
		"CREATE TABLE instances (id INTEGER PRIMARY KEY, name TEXT, provider_id INTEGER, user_id INTEGER, status TEXT, desired_state TEXT, updated_at DATETIME, deleted_at DATETIME)",
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(&providerModel.ProviderIPv4Pool{}); err != nil {
		t.Fatal(err)
	}

	for i, status := range []string{"processing", "cancelling", "cancelled", "timeout", "failed", "completed"} {
		task := adminModel.Task{ID: uint(i + 1), Status: status, TaskData: `{}`}
		if err := db.Table("tasks").Create(map[string]interface{}{"id": task.ID, "status": task.Status, "task_data": task.TaskData}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Transaction(func(tx *gorm.DB) error { return lockRunningCreate(tx, task.ID) }); err == nil {
			t.Fatalf("accepted %s", status)
		}
	}
	instance := providerModel.Instance{ID: 1, Name: "quarantine", Status: "creating", ProviderID: 2}
	if err := db.Table("instances").Create(map[string]interface{}{"id": instance.ID, "name": instance.Name, "provider_id": instance.ProviderID, "user_id": 0, "status": instance.Status}).Error; err != nil {
		t.Fatal(err)
	}
	address := providerModel.ProviderIPv4Pool{ProviderID: 2, InstanceID: &instance.ID, IsAllocated: true}
	if err := db.Create(&address).Error; err != nil {
		t.Fatal(err)
	}
	port := providerModel.Port{InstanceID: instance.ID, ProviderID: 2, Status: "active"}
	if err := db.Create(&port).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return quarantineFailedCreate(tx, &instance) }); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&address, address.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !address.IsAllocated || address.InstanceID == nil || *address.InstanceID != instance.ID {
		t.Fatal("unconfirmed remote deletion released address")
	}
	if err := db.First(&instance, instance.ID).Error; err != nil {
		t.Fatal(err)
	}
	if instance.Status != "error" || instance.DesiredState != "stopped" {
		t.Fatalf("quarantine status: %s/%s", instance.Status, instance.DesiredState)
	}
	if err := db.First(&port, port.ID).Error; err != nil || port.Status != "deleting" {
		t.Fatalf("cleanup mapping: %s, %v", port.Status, err)
	}
}
