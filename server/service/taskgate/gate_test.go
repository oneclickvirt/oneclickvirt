package taskgate

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
)

func TestGetStatusOnlyShowsMaintenanceMessageWhileDisabled(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:task_gate_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&adminModel.SystemConfig{}, &adminModel.Task{}, &adminModel.ConfigurationTask{}); err != nil {
		t.Fatal(err)
	}
	oldDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = oldDB })

	for _, test := range []struct {
		name    string
		enabled string
		want    string
	}{
		{name: "enabled", enabled: "true", want: ""},
		{name: "disabled", enabled: "false", want: "Finish maintenance"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := db.Where("category = ?", configCategory).Delete(&adminModel.SystemConfig{}).Error; err != nil {
				t.Fatal(err)
			}
			configs := []adminModel.SystemConfig{
				{Category: configCategory, Key: enabledKey, Value: test.enabled, Type: "string"},
				{Category: configCategory, Key: messageKey, Value: "Finish maintenance", Type: "string"},
			}
			if err := db.Create(&configs).Error; err != nil {
				t.Fatal(err)
			}
			status, err := GetStatus()
			if err != nil {
				t.Fatal(err)
			}
			if status.Message != test.want {
				t.Fatalf("status message = %q, want %q", status.Message, test.want)
			}
		})
	}
}
