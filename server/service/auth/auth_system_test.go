package auth

import (
	"path/filepath"
	"testing"

	"oneclickvirt/global"
	userModel "oneclickvirt/model/user"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestInitSystemWithUsersDefaultsTheOptionalUserLevel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "init.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.AutoMigrate(&userModel.User{}); err != nil {
		t.Fatal(err)
	}
	oldDB, oldConfig := global.APP_DB, global.GetAppConfig()
	global.APP_DB = db
	cfg := oldConfig
	cfg.Quota.DefaultLevel = 0
	global.SetAppConfig(cfg)
	t.Cleanup(func() {
		global.APP_DB = oldDB
		global.SetAppConfig(oldConfig)
	})

	admin := UserInfo{Username: "test-admin", Password: "fixture"}
	initialUser := UserInfo{Username: "initial-user", Password: "fixture", Enabled: true}
	if err := (&AuthService{}).InitSystemWithUsers(admin, &initialUser); err != nil {
		t.Fatal(err)
	}
	var user userModel.User
	if err := db.Where("username = ?", initialUser.Username).First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if user.Level != 1 || user.Status != 1 || user.UserType != "user" {
		t.Fatalf("initial user level=%d status=%d role=%s", user.Level, user.Status, user.UserType)
	}
}
