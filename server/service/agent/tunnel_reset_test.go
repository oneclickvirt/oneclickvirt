package agent

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
)

func TestRestoreControllerPortsBatchTargetsWithoutPerPortQueries(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:tunnel_reset_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
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
	previous := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previous })
	ports := make([]providerModel.Port, 100)
	for index := range ports {
		ports[index] = providerModel.Port{InstanceID: 11, ProviderID: 3, HostPort: 22000 + index, GuestPort: 80, Protocol: "tcp", Status: "restoring", MappingType: "controller", InternalHost: "192.0.2.10"}
	}
	if err := db.Create(&ports).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads, updates := 0, 0
	_ = db.Callback().Query().Before("gorm:query").Register("count_reset_reads", func(*gorm.DB) { reads++ })
	_ = db.Callback().Update().After("gorm:update").Register("cancel_reset_after_batch", func(*gorm.DB) { updates++; cancel() })
	for index := range ports {
		ports[index].InternalHost = "192.0.2.20"
	}
	failures := RestoreControllerPortForwards(ctx, 11, 3, ports)
	if reads != 1 || updates != 1 {
		t.Fatalf("N+1 reads/updates=%d/%d", reads, updates)
	}
	if len(failures) != len(ports) {
		t.Fatalf("canceled mappings=%d", len(failures))
	}
	for _, err := range failures {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	var saved []providerModel.Port
	if err := db.Find(&saved).Error; err != nil {
		t.Fatal(err)
	}
	for _, port := range saved {
		if port.InternalHost != "192.0.2.20" || port.Status != "pending" {
			t.Fatalf("stale target or premature active: %+v", port)
		}
	}
	ctrlListenerMu.RLock()
	defer ctrlListenerMu.RUnlock()
	for _, port := range saved {
		if ctrlListeners[port.ID] != nil {
			t.Fatal("canceled batch started a listener")
		}
	}
}
