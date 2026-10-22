package provider

import (
	"reflect"
	"testing"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
)

func TestIPv4RangeKeepsExistingInputAndDeduplicates(t *testing.T) {
	addresses, invalid := parseIPsFromText("# note\r\n192.0.2.1 - 192.0.2.4\r\n192.0.2.2\n192.0.2.0/30\n192.0.2.5/32\n192.0.2.6/31\n192.0.2.20 - 192.0.2.10\nbad")
	want := []string{"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4", "192.0.2.5", "192.0.2.6", "192.0.2.7"}
	if !reflect.DeepEqual(addresses, want) || len(invalid) != 2 {
		t.Fatalf("addresses=%v, invalid=%v", addresses, invalid)
	}
	addresses, invalid = parseIPsFromText("10.0.0.0 - 10.0.16.0\n192.0.2.9")
	if !reflect.DeepEqual(addresses, []string{"192.0.2.9"}) || len(invalid) != 1 {
		t.Fatalf("oversized range leaked partial addresses: %v %v", addresses, invalid)
	}
}

func TestIPv4RangeAppendPreservesAllocatedAddresses(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	oldDB, oldLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() { global.APP_DB, global.APP_LOG = oldDB, oldLog })
	if err := db.AutoMigrate(&providerModel.ProviderIPv4Pool{}); err != nil {
		t.Fatal(err)
	}
	instanceID := uint(99)
	allocated := providerModel.ProviderIPv4Pool{ProviderID: 7, Address: "192.0.2.2", IsAllocated: true, InstanceID: &instanceID}
	if err := db.Create(&allocated).Error; err != nil {
		t.Fatal(err)
	}
	service := NewIPv4PoolService()
	added, invalid, err := service.SetIPv4Pool(7, "192.0.2.1 - 192.0.2.3\n192.0.2.1")
	if err != nil || len(added) != 2 || len(invalid) != 0 {
		t.Fatalf("%v %v %v", added, invalid, err)
	}
	if err := db.First(&allocated, allocated.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !allocated.IsAllocated || allocated.InstanceID == nil || *allocated.InstanceID != 99 {
		t.Fatal("allocated address changed")
	}
	added, _, err = service.SetIPv4Pool(7, "192.0.2.1 - 192.0.2.3")
	if err != nil || len(added) != 0 {
		t.Fatalf("repeat append: %v, %v", added, err)
	}
	var count int64
	db.Model(&providerModel.ProviderIPv4Pool{}).Where("provider_id = ?", 7).Count(&count)
	if count != 3 {
		t.Fatalf("pool has %d rows", count)
	}
}
