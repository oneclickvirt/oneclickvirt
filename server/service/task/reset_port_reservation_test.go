package task

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"
	"oneclickvirt/service/database"
)

func resetReservationDB(t *testing.T) (*gorm.DB, *TaskService) {
	t.Helper()
	resetPortTestLogger(t)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:reset_ports_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&providerModel.Port{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE instances (id INTEGER PRIMARY KEY, provider_id INTEGER, private_ip TEXT, ipv6_address TEXT, ssh_port INTEGER, updated_at DATETIME, deleted_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO instances (id, provider_id) VALUES (10, 3), (11, 3), (12, 4)").Error; err != nil {
		t.Fatal(err)
	}
	previous := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previous })
	return db, &TaskService{dbService: database.GetDatabaseService()}
}

func TestResetTransferPreservesAllFieldsAndNeverReleasesPorts(t *testing.T) {
	db, _ := resetReservationDB(t)
	ports := []providerModel.Port{
		{InstanceID: 10, ProviderID: 3, HostPort: 22000, GuestPort: 22, Status: "active", Protocol: "both", HostPortEnd: 22002, GuestPortEnd: 24, PortCount: 3, IPv6Enabled: true, IPv6Address: "2001:db8::10", MappingMethod: "device_proxy", Description: "keep range"},
		{InstanceID: 10, ProviderID: 3, HostPort: 23000, GuestPort: 80, Status: "inactive", Protocol: "tcp", MappingType: "controller", InternalHost: "custom.internal"},
		{InstanceID: 10, ProviderID: 3, HostPort: 24000, GuestPort: 443, Status: "failed", Protocol: "tcp", MappingMethod: "iptables"},
	}
	if err := db.Create(&ports).Error; err != nil {
		t.Fatal(err)
	}
	var before []providerModel.Port
	if err := db.Order("id").Find(&before).Error; err != nil {
		t.Fatal(err)
	}
	updates, deletes, inserts := 0, 0, 0
	_ = db.Callback().Update().Before("gorm:update").Register("count_reset_updates", func(*gorm.DB) { updates++ })
	_ = db.Callback().Delete().Before("gorm:delete").Register("count_reset_deletes", func(*gorm.DB) { deletes++ })
	_ = db.Callback().Create().Before("gorm:create").Register("count_reset_inserts", func(*gorm.DB) { inserts++ })
	resetCtx := &ResetTaskContext{Provider: providerModel.Provider{ID: 3}, OldInstanceID: 10, OldPortMappings: before[:1]}
	if err := db.Transaction(func(tx *gorm.DB) error { return transferResetPortMappingsInTx(tx, resetCtx, 11) }); err != nil {
		t.Fatal(err)
	}
	if updates != 1 || deletes != 0 || inserts != 0 {
		t.Fatalf("migration performed update/delete/create = %d/%d/%d", updates, deletes, inserts)
	}
	var after []providerModel.Port
	if err := db.Order("id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatal("port reservations disappeared")
	}
	for i, got := range after {
		want := before[i]
		want.InstanceID = 11
		if want.Status == "active" {
			want.Status = "restoring"
		}
		if resetPortDefinition(got) != resetPortDefinition(want) {
			t.Fatalf("mapping %d lost fields: got %+v want %+v", i, got, want)
		}
	}
}

func TestResetTransferRejectsConcurrentChangeAndRollsBack(t *testing.T) {
	for _, mode := range []string{"changed", "deleted", "outer_rollback"} {
		t.Run(mode, func(t *testing.T) {
			db, _ := resetReservationDB(t)
			port := providerModel.Port{InstanceID: 10, ProviderID: 3, HostPort: 22000, GuestPort: 22, Status: "active"}
			if err := db.Create(&port).Error; err != nil {
				t.Fatal(err)
			}
			resetCtx := &ResetTaskContext{Provider: providerModel.Provider{ID: 3}, OldInstanceID: 10, OldPortMappings: []providerModel.Port{port}}
			if mode == "changed" {
				if err := db.Model(&port).Update("guest_port", 23).Error; err != nil {
					t.Fatal(err)
				}
				resetCtx.OldPortMappings[0].GuestPort = 22
			}
			if mode == "deleted" {
				if err := db.Delete(&port).Error; err != nil {
					t.Fatal(err)
				}
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := transferResetPortMappingsInTx(tx, resetCtx, 11); err != nil {
					return err
				}
				if mode == "outer_rollback" {
					return errors.New("later replacement failure")
				}
				return nil
			})
			if err == nil {
				t.Fatal("unsafe transfer succeeded")
			}
			var count int64
			if err := db.Model(&providerModel.Port{}).Where("instance_id = 11").Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("partial transfer committed: count=%d err=%v", count, err)
			}
		})
	}
}

type reservedResetProvider struct {
	providerCore.Provider
	resetPortProvider
}

func TestResetRestorePersistsIndividualFailuresAndFreshIPv6(t *testing.T) {
	db, service := resetReservationDB(t)
	ports := []providerModel.Port{
		{InstanceID: 11, ProviderID: 3, HostPort: 22000, GuestPort: 22, Status: "restoring", Protocol: "tcp", MappingMethod: "device_proxy", IsSSH: true},
		{InstanceID: 11, ProviderID: 3, HostPort: 22001, GuestPort: 80, Status: "restoring", Protocol: "tcp", MappingMethod: "device_proxy"},
		{InstanceID: 11, ProviderID: 3, HostPort: 22002, GuestPort: 443, Status: "restoring", Protocol: "tcp", MappingMethod: "device_proxy", IPv6Enabled: true, IPv6Address: "2001:db8::10"},
	}
	if err := db.Create(&ports).Error; err != nil {
		t.Fatal(err)
	}
	p := &reservedResetProvider{resetPortProvider: resetPortProvider{ipv4: "192.0.2.20", ipv6: "2001:db8::20", failPort: 22000}}
	resetCtx := &ResetTaskContext{Provider: providerModel.Provider{ID: 3, Type: "incus", NetworkType: "nat_ipv4"}, Instance: providerModel.Instance{IPv6Address: "2001:db8::10"}, NewInstanceID: 11, OldInstanceName: "guest"}
	if err := service.restoreReservedPortMappings(context.Background(), p, resetCtx, ports); err == nil {
		t.Fatal("setup failure was swallowed")
	}
	var saved []providerModel.Port
	if err := db.Order("id").Find(&saved).Error; err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"failed", "active", "active"} {
		if saved[i].Status != want {
			t.Fatalf("port %d status=%s want=%s", i, saved[i].Status, want)
		}
	}
	if saved[2].IPv6Address != "2001:db8::20" {
		t.Fatalf("stale IPv6 retained: %s", saved[2].IPv6Address)
	}
	if len(p.calls) != 3 || p.ipv4Reads != 1 || p.ipv6Reads != 1 {
		t.Fatalf("lost mappings or per-port IP queries: %+v", p.resetPortProvider)
	}
	var instance providerModel.Instance
	if err := db.First(&instance, 11).Error; err != nil {
		t.Fatal(err)
	}
	if instance.SSHPort == 22000 || instance.PrivateIP != p.ipv4 || instance.IPv6Address != p.ipv6 {
		t.Fatalf("instance contains failed SSH or stale IP: %+v", instance)
	}
}

func TestResetRestoreCancellationRecordsFailureWithoutRemoteIO(t *testing.T) {
	db, service := resetReservationDB(t)
	ports := []providerModel.Port{{InstanceID: 11, ProviderID: 3, HostPort: 22000, GuestPort: 22, Status: "restoring", MappingMethod: "device_proxy"}}
	if err := db.Create(&ports).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &reservedResetProvider{}
	resetCtx := &ResetTaskContext{Provider: providerModel.Provider{ID: 3, Type: "incus"}, NewInstanceID: 11, OldInstanceName: "guest"}
	if err := service.restoreReservedPortMappings(ctx, p, resetCtx, ports); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	var saved providerModel.Port
	if err := db.First(&saved, ports[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != "failed" || len(p.calls) != 0 {
		t.Fatalf("cancellation result: status=%s calls=%v", saved.Status, p.calls)
	}
}
