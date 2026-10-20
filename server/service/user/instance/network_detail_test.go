package instance

import (
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"testing"
)

func TestInstanceDetailAndListKeepSelectedNetworkAfterNodeDefaultChanges(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	pool.SetMaxOpenConns(1)
	defer pool.Close()
	previousDB, previousLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	defer func() { global.APP_DB, global.APP_LOG = previousDB, previousLog }()
	if err := db.AutoMigrate(&providerModel.Provider{}, &providerModel.Port{}); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		"CREATE TABLE instances (id INTEGER PRIMARY KEY, user_id INTEGER, provider_id INTEGER, status TEXT, network_type TEXT, public_ip TEXT, public_ipv6 TEXT, deleted_at DATETIME)",
		"CREATE TABLE tasks (id INTEGER PRIMARY KEY, instance_id INTEGER, status TEXT, created_at DATETIME, deleted_at DATETIME)",
		"CREATE TABLE users (id INTEGER PRIMARY KEY, traffic_limited BOOLEAN, deleted_at DATETIME)",
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	node := providerModel.Provider{ID: 1, Name: "pve", Type: "proxmox", Status: "active", Endpoint: "107.172.105.194", NetworkType: "dedicated_ipv4_ipv6"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	modes := []string{"nat_ipv4", "dedicated_ipv4_ipv6", "ipv6_only", ""}
	for i, mode := range modes {
		if err := db.Table("instances").Create(map[string]interface{}{"id": i + 1, "user_id": 1, "provider_id": 1, "status": "running", "network_type": mode, "public_ipv6": "2001:db8::3"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, mode := range modes {
		expected := mode
		if expected == "" {
			expected = node.NetworkType
		}
		detail, err := NewService().GetInstanceDetail(1, uint(i+1))
		if err != nil {
			t.Fatal(err)
		}
		if detail.NetworkType != expected {
			t.Fatalf("detail %d rewrote %q to %q", i+1, expected, detail.NetworkType)
		}
		if mode == "ipv6_only" && detail.PublicIP != "" {
			t.Fatal("IPv6-only detail invented a public IPv4")
		}
	}
	rows, _, err := NewService().GetUserInstances(1, userModel.UserInstanceListRequest{PageInfo: common.PageInfo{Page: 1, PageSize: 20}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("list length %d", len(rows))
	}
	for _, row := range rows {
		expected := modes[row.ID-1]
		if expected == "" {
			expected = node.NetworkType
		}
		if row.NetworkType != expected {
			t.Fatalf("list rewrote %q to %q", expected, row.NetworkType)
		}
		if expected == "ipv6_only" && row.PublicIP != "" {
			t.Fatal("IPv6-only list invented public IPv4")
		}
	}
}
