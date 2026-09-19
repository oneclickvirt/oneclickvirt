package global

import (
	"sync"
	"testing"

	"oneclickvirt/config"
)

func TestOldDatabaseDetectionCannotOverwriteNewConfig(t *testing.T) {
	old := GetAppConfig()
	t.Cleanup(func() { SetAppConfig(old) })
	cfg := config.Server{Mysql: config.Mysql{Path: "db-a", Port: "3306", Username: "user", Password: "private", Config: "old"}}
	SetAppConfig(cfg)
	source := cfg.Mysql.ConnectionConfig()
	UpdateAppConfig(func(c *config.Server) { c.Mysql.Path = "db-b"; c.System.Addr = 9000 })
	ReconcileDatabaseConfig(source, "mariadb", "repaired")
	got := GetAppConfig()
	if got.Mysql.Path != "db-b" || got.Mysql.Config != "old" || got.System.Addr != 9000 {
		t.Fatal("stale probe replaced newer config")
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); UpdateAppConfig(func(c *config.Server) { c.System.Addr++ }) }()
	}
	wg.Wait()
	if GetAppConfig().System.Addr != 9100 {
		t.Fatal("lost concurrent config update")
	}
}
