package instancealias

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLocalMySQLAliasMigrationAndLifecycleIsolation(t *testing.T) {
	dsn := os.Getenv("OCV_ALIAS_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires an isolated loopback MySQL fixture")
	}
	cfg, err := mysqlDriver.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "ocv_feature_test_") || cfg.Net != "tcp" || !strings.HasPrefix(cfg.Addr, "127.0.0.1:") {
		t.Fatal("refusing nonlocal or non-fixture database")
	}
	cfg.ParseTime = true
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 5*time.Second, 5*time.Second, 5*time.Second
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	if db.Migrator().HasTable(&providerModel.Instance{}) || db.Migrator().HasTable(&providerModel.Provider{}) {
		t.Fatal("fixture must not contain instance/provider tables")
	}
	previousDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() {
		_ = db.Migrator().DropTable(&providerModel.Instance{}, &providerModel.Provider{})
		global.APP_DB = previousDB
	})
	if err := db.AutoMigrate(&providerModel.Provider{}, &providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&providerModel.Provider{ID: 7, Name: "fixture", Type: "incus", OwnerAdminID: 11}).Error; err != nil {
		t.Fatal(err)
	}
	row := providerModel.Instance{ID: 3, Name: "host-vm", Provider: "fixture", ProviderID: 7, ProviderVMID: "101", UserID: 9, Status: "running", CPU: 2}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&providerModel.Instance{}, "Alias"); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	var stale providerModel.Instance
	if err := db.First(&stale, 3).Error; err != nil || stale.Alias != "" || stale.Name != "host-vm" {
		t.Fatalf("migration: %+v %v", stale, err)
	}
	ctx := context.Background()
	if _, err := UpdateForAdmin(ctx, 22, 3, "wrong owner"); err == nil {
		t.Fatal("admin isolation failed")
	}
	if _, err := UpdateForAdmin(ctx, 11, 3, "初始备注"); err != nil {
		t.Fatal(err)
	}
	// Exercise independent metadata and lifecycle writers against real MySQL.
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if _, err := UpdateForUser(ctx, 9, 3, "用户备注"); err != nil {
				errors <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			copy := stale
			copy.Status = "stopped"
			if err := db.Save(&copy).Error; err != nil {
				errors <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	var after providerModel.Instance
	if err := db.First(&after, 3).Error; err != nil {
		t.Fatal(err)
	}
	if after.Alias != "用户备注" || after.Name != "host-vm" || after.ProviderVMID != "101" || after.Status != "stopped" || after.UserID != 9 || after.CPU != 2 {
		t.Fatalf("concurrent save changed identity/alias: %+v", after)
	}
	if _, err := UpdateForUser(ctx, 9, 3, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateForUser(ctx, 9, 3, ""); err != nil {
		t.Fatal("idempotent clear", err)
	}
}
