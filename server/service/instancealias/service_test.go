package instancealias

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
)

func aliasFixture(t *testing.T) (*gorm.DB, providerModel.Instance) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	previousDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previousDB; _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE providers (id INTEGER PRIMARY KEY, owner_admin_id INTEGER, deleted_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO providers (id, owner_admin_id) VALUES (7, 11)").Error; err != nil {
		t.Fatal(err)
	}
	instance := providerModel.Instance{ID: 3, Name: "host-vm", Provider: "node", ProviderID: 7, UserID: 9, ProviderVMID: "101", Status: "rebuilding", DesiredState: "stopped", CPU: 2, Memory: 2048, IsFrozen: true, TrafficLimited: true}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	return db, instance
}

func TestAliasUpdatesPreserveIdentityLifecycleAndNewerMetadata(t *testing.T) {
	db, original := aliasFixture(t)
	ctx := context.Background()
	alias, err := UpdateForUser(ctx, 9, 3, "  数据库 VM  ")
	if err != nil || alias != "数据库 VM" {
		t.Fatalf("alias=%q err=%v", alias, err)
	}
	// A lifecycle worker saved a copy loaded before the alias edit.
	original.Status = "stopped"
	if err := db.Save(&original).Error; err != nil {
		t.Fatal(err)
	}
	var after providerModel.Instance
	if err := db.First(&after, 3).Error; err != nil {
		t.Fatal(err)
	}
	if after.Alias != alias || after.Name != "host-vm" || after.ProviderInstanceIdentifier() != "101" || after.UserID != 9 || after.ProviderID != 7 || after.CPU != 2 || after.Memory != 2048 || !after.IsFrozen || !after.TrafficLimited || after.DesiredState != "stopped" || after.Status != "stopped" {
		t.Fatalf("metadata or core fields changed: %+v", after)
	}
	if _, err := UpdateForAdmin(ctx, 11, 3, "管理员备注"); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateForAdmin(ctx, 0, 3, "超级管理员备注"); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateForUser(ctx, 9, 3, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&after, 3).Error; err != nil || after.Alias != "" {
		t.Fatalf("clear failed: %q %v", after.Alias, err)
	}
	if _, err := UpdateForUser(ctx, 9, 3, ""); err != nil {
		t.Fatal("idempotent clear failed", err)
	}
}

func TestAliasScopeAndValidation(t *testing.T) {
	db, _ := aliasFixture(t)
	ctx := context.Background()
	for _, change := range []func() (string, error){
		func() (string, error) { return UpdateForUser(ctx, 8, 3, "other user") },
		func() (string, error) { return UpdateForUser(ctx, 0, 3, "anonymous") },
		func() (string, error) { return UpdateForAdmin(ctx, 22, 3, "other admin") },
		func() (string, error) { return UpdateForAdmin(ctx, 0, 999, "missing") },
		func() (string, error) { return UpdateForUser(ctx, 9, 3, strings.Repeat("中", 129)) },
		func() (string, error) { return UpdateForUser(ctx, 9, 3, "a\nb") },
	} {
		if _, err := change(); err == nil {
			t.Fatal("invalid alias edit was accepted")
		}
	}
	if _, err := UpdateForUser(ctx, 9, 3, strings.Repeat("中", 128)); err != nil {
		t.Fatal(err)
	}
	if err := db.Table("providers").Where("id = ?", 7).Update("deleted_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateForAdmin(ctx, 11, 3, "deleted provider"); err == nil {
		t.Fatal("deleted provider passed scope")
	}
	if err := db.Delete(&providerModel.Instance{}, 3).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateForUser(ctx, 9, 3, "deleted"); err == nil {
		t.Fatal("deleted instance was edited")
	}
}

func TestAliasMigrationDefaultsExistingRowsWithoutRenaming(t *testing.T) {
	db, _ := aliasFixture(t)
	if err := db.Migrator().DropColumn(&providerModel.Instance{}, "Alias"); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	var existing providerModel.Instance
	if err := db.First(&existing, 3).Error; err != nil {
		t.Fatal(err)
	}
	if existing.Alias != "" || existing.Name != "host-vm" || existing.ProviderVMID != "101" || existing.UserID != 9 {
		t.Fatalf("migration changed existing instance: %+v", existing)
	}
}
