package portmapping

import (
	"context"
	"fmt"
	"testing"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPortMappingModelRoundTripPreservesRangeAndIPv6(t *testing.T) {
	created := time.Date(2026, 9, 14, 10, 11, 12, 0, time.UTC)
	updated := created.Add(time.Minute)
	base := NewBaseProvider("incus", nil)
	result := &PortMappingResult{
		ID:            7,
		InstanceID:    "42",
		ProviderID:    3,
		HostPort:      22000,
		HostPortEnd:   22009,
		GuestPort:     22,
		GuestPortEnd:  31,
		PortCount:     10,
		Protocol:      "tcp",
		IPv6Address:   "2001:db8::42",
		MappingMethod: "device_proxy",
		MappingType:   "node",
		InternalHost:  "2001:db8::42",
		Status:        "active",
		Description:   "ssh",
		IsSSH:         true,
		IsAutomatic:   true,
		CreatedAt:     created.Format(time.RFC3339),
		UpdatedAt:     updated.Format(time.RFC3339),
	}
	model := base.ToDBModel(result)
	if model.HostPortEnd != 22009 || model.GuestPortEnd != 31 || model.PortCount != 10 {
		t.Fatalf("range fields lost: %#v", model)
	}
	if model.IPv6Address != result.IPv6Address || model.MappingMethod != result.MappingMethod || model.MappingType != result.MappingType || model.InternalHost != result.InternalHost {
		t.Fatalf("address/method fields lost: %#v", model)
	}
	if model.InstanceID != 42 || !model.IPv6Enabled {
		t.Fatalf("identity/IPv6 fields lost: %#v", model)
	}
	back := base.FromDBModel(model)
	if back == nil || back.HostPortEnd != result.HostPortEnd || back.GuestPortEnd != result.GuestPortEnd || back.PortCount != result.PortCount || back.IPv6Address != result.IPv6Address || back.MappingMethod != result.MappingMethod || back.MappingType != result.MappingType || back.InternalHost != result.InternalHost {
		t.Fatalf("round trip mismatch: %#v", back)
	}
}

func TestPortMappingFromDBModelDefaultsPortCount(t *testing.T) {
	base := NewBaseProvider("lxd", nil)
	back := base.FromDBModel(&providerModel.Port{HostPort: 20000, GuestPort: 80, MappingMethod: ""})
	if back == nil || back.PortCount != 1 || back.MappingMethod != "native" {
		t.Fatalf("unexpected defaults: %#v", back)
	}
}

func TestPortMappingRoundTripPreservesExplicitIPv6FlagWithoutAddress(t *testing.T) {
	base := NewBaseProvider("incus", nil)
	model := base.ToDBModel(&PortMappingResult{
		InstanceID: "42", ProviderID: 3, HostPort: 20000, GuestPort: 8080,
		IPv6Enabled: true, MappingMethod: "device_proxy",
	})
	if !model.IPv6Enabled || model.IPv6Address != "" {
		t.Fatalf("explicit IPv6 flag was lost: %#v", model)
	}
	result := base.FromDBModel(model)
	if !result.IPv6Enabled {
		t.Fatalf("round trip lost explicit IPv6 flag: %#v", result)
	}
}

func TestAllocatePortTreatsControllerMappingsAsOccupied(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:port_allocate_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&providerModel.Provider{}, &providerModel.Port{}); err != nil {
		t.Fatal(err)
	}
	previous := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previous })
	prov := providerModel.Provider{
		UUID: "00000000-0000-0000-0000-000000000901", Name: "allocate-test", Type: "incus",
		PortRangeStart: 20000, PortRangeEnd: 20002, NextAvailablePort: 20000,
	}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&providerModel.Port{ProviderID: prov.ID, HostPort: 20000, GuestPort: 22, MappingType: "controller"}).Error; err != nil {
		t.Fatal(err)
	}
	allocated, err := NewBaseProvider("incus", nil).AllocatePort(context.Background(), prov.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if allocated != 20001 {
		t.Fatalf("allocated port = %d, want 20001 after controller reservation", allocated)
	}
}

func TestValidateMappingRangeRejectsMismatchedEnds(t *testing.T) {
	if err := ValidateMappingRange(20000, 20002, 80, 82, 2); err == nil {
		t.Fatal("mismatched range end was accepted")
	}
	if err := ValidateMappingRange(20000, 20001, 80, 81, 2); err != nil {
		t.Fatalf("valid range rejected: %v", err)
	}
}
