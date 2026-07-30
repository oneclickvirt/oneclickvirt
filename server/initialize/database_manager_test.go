package initialize

import (
	"context"
	"testing"
	"time"

	appConfig "oneclickvirt/config"
	"oneclickvirt/global"
	databaseConfig "oneclickvirt/model/config"

	"gorm.io/gorm"
)

func TestDatabaseManagerNotifiesConnectionRestoredHandler(t *testing.T) {
	dm := &DatabaseManager{}
	wantDB := &gorm.DB{}
	var gotDB *gorm.DB
	dm.SetConnectionRestoredHandler(func(db *gorm.DB) {
		gotDB = db
	})

	dm.notifyConnectionRestored(wantDB)
	if gotDB != wantDB {
		t.Fatalf("handler received %p, want %p", gotDB, wantDB)
	}
}

func TestDatabaseManagerRefreshesRuntimeConnectionConfig(t *testing.T) {
	original := global.GetAppConfig()
	t.Cleanup(func() { global.SetAppConfig(original) })

	runtime := appConfig.Server{}
	runtime.Mysql.Path = "db.internal"
	runtime.Mysql.Port = "3307"
	runtime.Mysql.Config = "charset=utf8mb4"
	runtime.Mysql.Dbname = "oneclickvirt_runtime"
	runtime.Mysql.Username = "runtime_user"
	runtime.Mysql.Password = ""
	runtime.Mysql.MaxIdleConns = 10
	runtime.Mysql.MaxOpenConns = 50
	runtime.Mysql.MaxLifetime = 600
	global.SetAppConfig(runtime)

	dm := &DatabaseManager{config: databaseConfig.MysqlConfig{
		Path:     "stale.internal",
		Port:     "3306",
		Dbname:   "stale",
		Username: "stale_user",
		Password: "stale_password",
	}}
	got := dm.refreshConnectionConfig()
	if got.Path != "db.internal" || got.Port != "3307" || got.Dbname != "oneclickvirt_runtime" || got.Username != "runtime_user" {
		t.Fatalf("refreshConnectionConfig() = %+v, want current runtime connection settings", got)
	}
	if got.Password != "" {
		t.Fatalf("refreshConnectionConfig() password = %q, want intentionally blank runtime password", got.Password)
	}
}

func TestDatabaseManagerReconnectDelayIsBounded(t *testing.T) {
	dm := &DatabaseManager{reconnectInterval: 5 * time.Second, ctx: context.Background()}
	tests := []struct {
		attempt int
		min     time.Duration
		max     time.Duration
	}{
		{attempt: 0, min: 5 * time.Second, max: 7500 * time.Millisecond},
		{attempt: 1, min: 10 * time.Second, max: 15 * time.Second},
		{attempt: 2, min: 20 * time.Second, max: 30 * time.Second},
		{attempt: 3, min: 40 * time.Second, max: time.Minute},
		{attempt: 20, min: 40 * time.Second, max: time.Minute},
	}
	for _, test := range tests {
		delay := dm.reconnectDelay(test.attempt)
		if delay < test.min || delay > test.max {
			t.Fatalf("reconnectDelay(%d) = %s, want [%s, %s]", test.attempt, delay, test.min, test.max)
		}
	}
}
