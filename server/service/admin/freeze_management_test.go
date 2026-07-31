package admin

import (
	"fmt"
	"testing"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUnfreezeInstanceRequiresExistingRow(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:unfreeze_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	previousDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previousDB })

	service := &FreezeManagementService{}
	if err := service.UnfreezeInstance(999); err == nil {
		t.Fatal("unfreezing a missing instance should fail")
	}

	instance := providerModel.Instance{
		Name: "frozen-instance", Provider: "node-a", ProviderID: 1,
		Status: "running", IsFrozen: true, FrozenReason: "manual",
	}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.UnfreezeInstance(instance.ID); err != nil {
		t.Fatal(err)
	}
	var updated providerModel.Instance
	if err := db.First(&updated, instance.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.IsFrozen || updated.FrozenReason != "" {
		t.Fatalf("instance was not unfrozen: %#v", updated)
	}
}
