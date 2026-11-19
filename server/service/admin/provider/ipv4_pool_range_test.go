package provider

import (
	"errors"
	"net"
	"os"
	"sync/atomic"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm/logger"
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

func TestIPv4RangeOverlappingWrites(t *testing.T) {
	address := os.Getenv("OCV_LOCAL_IPV4_MYSQL_ADDR")
	if address == "" {
		t.Skip("requires an isolated local MySQL or MariaDB fixture")
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("use a loopback database fixture")
	}
	cfg := mysqlDriver.NewConfig()
	cfg.Net, cfg.Addr, cfg.User = "tcp", address, "root"
	cfg.DBName, cfg.Passwd, cfg.ParseTime = "ocv_local_rc5", os.Getenv("OCV_LOCAL_MYSQL_PASSWORD"), true
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if db.Migrator().HasTable(&providerModel.ProviderIPv4Pool{}) {
		t.Fatal("fixture database must not contain an IPv4 pool")
	}
	if err := db.AutoMigrate(&providerModel.ProviderIPv4Pool{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Migrator().DropTable(&providerModel.ProviderIPv4Pool{}) })
	oldDB, oldLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() { global.APP_DB, global.APP_LOG = oldDB, oldLog })
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var batches atomic.Int32
	if err := db.Callback().Create().Before("gorm:create").Register("test:overlap", func(tx *gorm.DB) {
		if tx.Statement.Table == "provider_ipv4_pools" && batches.Add(1) <= 2 {
			entered <- struct{}{}
			select {
			case <-release:
			case <-time.After(10 * time.Second):
				_ = tx.AddError(errors.New("both inserts did not reach the fixture"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	type result struct {
		added []string
		err   error
	}
	results := make(chan result, 2)
	service := NewIPv4PoolService()
	for _, input := range []string{"192.0.2.1 - 192.0.2.6", "192.0.2.4 - 192.0.2.9"} {
		go func() {
			added, _, err := service.SetIPv4Pool(7, input)
			results <- result{added, err}
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			close(release)
			<-results
			<-results
			t.Fatal("timed out waiting for overlapping inserts")
		}
	}
	close(release)
	added := make(map[string]bool)
	for range 2 {
		outcome := <-results
		if outcome.err != nil {
			t.Error(outcome.err)
		}
		for _, address := range outcome.added {
			if added[address] {
				t.Errorf("both responses claim to have inserted %s", address)
			}
			added[address] = true
		}
	}
	total, _, _ := service.GetPoolStats(7)
	if total != 9 || len(added) != 9 {
		t.Fatalf("pool has %d rows; responses reported %d unique inserts", total, len(added))
	}
}
