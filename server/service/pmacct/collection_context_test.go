package pmacct

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"testing"
	"time"
)

func TestProviderCollectorLoadsSnapshotOnceAndUsesCancellation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:pmacct_scope_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{IgnoreRelationshipsWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Provider{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&providerModel.Provider{ID: 7, Name: "fixture", Type: "docker"}).Error; err != nil {
		t.Fatal(err)
	}
	oldDB := global.APP_DB
	global.APP_DB = db
	sqlDB, _ := db.DB()
	t.Cleanup(func() { global.APP_DB = oldDB; sqlDB.Close() })
	queries := 0
	db.Callback().Query().Before("gorm:query").Register("count_provider", func(tx *gorm.DB) {
		if tx.Statement.Table == "providers" {
			queries++
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	service := NewServiceWithContext(context.Background())
	service.providerID = 99
	collect, err := service.NewProviderCollector(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for attempt := 0; attempt < 100; attempt++ {
		if err := collect(&providerModel.Instance{ProviderID: 7, Name: "fixture"}, &monitoringModel.PmacctMonitor{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	}
	if queries != 1 || service.providerID != 99 {
		t.Fatalf("queries=%d shared provider=%d", queries, service.providerID)
	}
}

func TestCancelledTrafficGapStopsBeforeAllocatingOrWriting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	old := time.Now().AddDate(-100, 0, 0)
	service := NewServiceWithContext(ctx)
	service.fillGapRecords(1, &providerModel.Instance{}, &monitoringModel.PmacctMonitor{}, lastMaxTraffic{LastTimestamp: &old}, time.Now(), "")
}
