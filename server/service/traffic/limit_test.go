package traffic

import (
	"fmt"
	"math"
	"testing"
	"time"

	"oneclickvirt/global"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTrafficUsageDetailsPreserveSubMegabyteUsage(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:traffic_usage_details_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL,
			total_traffic INTEGER NOT NULL,
			traffic_limited BOOLEAN NOT NULL DEFAULT FALSE,
			deleted_at DATETIME
		)`,
		`CREATE TABLE providers (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			enable_traffic_control BOOLEAN NOT NULL,
			traffic_sync_method TEXT NOT NULL,
			traffic_count_mode TEXT NOT NULL,
			traffic_multiplier REAL NOT NULL,
			traffic_reset_day INTEGER,
			max_traffic INTEGER NOT NULL,
			traffic_limited BOOLEAN NOT NULL DEFAULT FALSE
		)`,
		`CREATE TABLE instances (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			provider_id INTEGER NOT NULL,
			traffic_limited BOOLEAN NOT NULL DEFAULT FALSE,
			deleted_at DATETIME
		)`,
		`CREATE TABLE pmacct_traffic_records (
			id INTEGER PRIMARY KEY,
			instance_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			provider_id INTEGER NOT NULL,
			rx_bytes INTEGER NOT NULL,
			tx_bytes INTEGER NOT NULL,
			total_bytes INTEGER NOT NULL,
			timestamp DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create traffic test schema: %v", err)
		}
	}
	previousDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previousDB })

	if err := db.Exec(`INSERT INTO users (id, username, total_traffic, traffic_limited) VALUES (?, ?, ?, ?)`, 1, "fractional-traffic-user", 1024, false).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Exec(`INSERT INTO providers (id, name, enable_traffic_control, traffic_sync_method, traffic_count_mode, traffic_multiplier, max_traffic, traffic_limited) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, 1, "fractional-traffic-provider", true, "agent", "both", 1, 1024, false).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := db.Exec(`INSERT INTO instances (id, user_id, provider_id, traffic_limited) VALUES (?, ?, ?, ?)`, 1, 1, 1, false).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	now := time.Now()
	const directionBytes int64 = 46_080
	if err := db.Exec(`INSERT INTO pmacct_traffic_records (id, instance_id, user_id, provider_id, rx_bytes, tx_bytes, total_bytes, timestamp) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, 1, 1, 1, 1, directionBytes, directionBytes, directionBytes*2, now).Error; err != nil {
		t.Fatalf("create traffic record: %v", err)
	}

	service := NewLimitService()
	userUsage, err := service.GetUserTrafficUsageWithPmacct(1)
	if err != nil {
		t.Fatalf("get user traffic usage: %v", err)
	}
	assertFractionalUsage(t, userUsage, "user")

	providerUsage, err := service.GetProviderTrafficUsageWithPmacct(1)
	if err != nil {
		t.Fatalf("get provider traffic usage: %v", err)
	}
	assertFractionalUsage(t, providerUsage, "provider")
}

func assertFractionalUsage(t *testing.T, usage map[string]interface{}, subject string) {
	t.Helper()
	currentUsage, ok := usage["current_month_usage"].(float64)
	if !ok {
		t.Fatalf("%s current_month_usage type = %T, want float64", subject, usage["current_month_usage"])
	}
	want := float64(92_160) / (1024 * 1024)
	if math.Abs(currentUsage-want) > 0.000001 {
		t.Fatalf("%s current_month_usage = %f, want %f", subject, currentUsage, want)
	}
	formatted, ok := usage["formatted"].(map[string]string)
	if !ok {
		t.Fatalf("%s formatted type = %T, want map[string]string", subject, usage["formatted"])
	}
	if got := formatted["current_usage"]; got != "90.00 KB" {
		t.Fatalf("%s formatted current_usage = %q, want %q", subject, got, "90.00 KB")
	}
}
