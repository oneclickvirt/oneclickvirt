package scheduler

import (
	"context"
	"errors"
	"fmt"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"testing"
	"time"
)

type scopedPmacctCollector struct {
	factoryCalls int
	collect      func(context.Context, *providerModel.Instance, *monitoringModel.PmacctMonitor) error
}

func (*scopedPmacctCollector) CollectTrafficFromSQLite(*providerModel.Instance, *monitoringModel.PmacctMonitor) error {
	panic("unscoped collector")
}
func (*scopedPmacctCollector) ResetPmacctDaemon(uint) error { return nil }
func (s *scopedPmacctCollector) NewProviderCollector(ctx context.Context, _ uint) (func(*providerModel.Instance, *monitoringModel.PmacctMonitor) error, error) {
	s.factoryCalls++
	return func(i *providerModel.Instance, m *monitoringModel.PmacctMonitor) error { return s.collect(ctx, i, m) }, nil
}

func collectionDB(t *testing.T, count int) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:collection_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, IgnoreRelationshipsWhenMigrating: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Instance{}, &monitoringModel.PmacctMonitor{}, &monitoringModel.PmacctTrafficRecord{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	oldDB, oldLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() { global.APP_DB, global.APP_LOG = oldDB, oldLog; sqlDB.Close() })
	for id := uint(1); id <= uint(count); id++ {
		if err := db.Create(&providerModel.Instance{ID: id, Name: fmt.Sprintf("fixture-%d", id), ProviderID: 7}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&monitoringModel.PmacctMonitor{ID: id, InstanceID: id, ProviderID: 7, IsEnabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestPmacctCollectionCancellationStopsRemainingInstances(t *testing.T) {
	collectionDB(t, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	collector := &scopedPmacctCollector{collect: func(ctx context.Context, _ *providerModel.Instance, _ *monitoringModel.PmacctMonitor) error {
		calls++
		cancel()
		return ctx.Err()
	}}
	err := NewMonitoringSchedulerService(collector).collectProviderTrafficInBatches(ctx, 7, 0, 1)
	if !errors.Is(err, context.Canceled) || calls != 1 || collector.factoryCalls != 1 {
		t.Fatalf("err=%v calls=%d factory=%d", err, calls, collector.factoryCalls)
	}
}

func TestPmacctCollectionKeysetSurvivesDeletionAndBoundsNewRows(t *testing.T) {
	db := collectionDB(t, 5)
	seen := make(map[uint]int)
	collector := &scopedPmacctCollector{collect: func(_ context.Context, i *providerModel.Instance, _ *monitoringModel.PmacctMonitor) error {
		seen[i.ID]++
		if i.ID == 1 {
			if err := db.Delete(&monitoringModel.PmacctMonitor{}, 1).Error; err != nil {
				return err
			}
			return db.Create(&monitoringModel.PmacctMonitor{ID: 100, InstanceID: 5, ProviderID: 7, IsEnabled: true}).Error
		}
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := NewMonitoringSchedulerService(collector).collectProviderTrafficInBatches(ctx, 7, 2, 1); err != nil {
		t.Fatal(err)
	}
	for id := uint(1); id <= 5; id++ {
		if seen[id] != 1 {
			t.Fatalf("instance %d collected %d times", id, seen[id])
		}
	}
	if collector.factoryCalls != 1 {
		t.Fatalf("factories=%d, want one per round", collector.factoryCalls)
	}
}
