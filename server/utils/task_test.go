package utils

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type taskLogTestRow struct {
	ID           uint `gorm:"primarykey"`
	ProgressLogs string
}

func (taskLogTestRow) TableName() string { return "task_log_test_rows" }

func TestGetCreateTaskTimeout(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		instanceType string
		want         int
	}{
		{name: "docker container", providerType: "docker", instanceType: "container", want: 1800},
		{name: "incus container", providerType: "incus", instanceType: "container", want: 3600},
		{name: "incus vm", providerType: "incus", instanceType: "vm", want: 7200},
		{name: "lxd vm mixed case", providerType: "LXD", instanceType: "VM", want: 7200},
		{name: "kubevirt vm", providerType: "kubevirt", instanceType: "vm", want: 7200},
		{name: "kubevirt container", providerType: "kubevirt", instanceType: "container", want: 3600},
		{name: "generic vm", providerType: "cloud", instanceType: "vm", want: 3600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetCreateTaskTimeout(tt.providerType, tt.instanceType); got != tt.want {
				t.Fatalf("GetCreateTaskTimeout(%q, %q) = %d, want %d", tt.providerType, tt.instanceType, got, tt.want)
			}
		})
	}
}

func TestTaskLogAppendSQLiteRepairsLegacyValues(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:task_log_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&taskLogTestRow{}); err != nil {
		t.Fatal(err)
	}
	entry := `{"t":"12:00:00","p":10,"m":"step"}`
	for _, initial := range []string{"", "[]", "null", "not-json", `{"old":true}`, `[{"t":"11:59:00","p":1,"m":"old"}]`} {
		row := taskLogTestRow{ProgressLogs: initial}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&taskLogTestRow{}).Where("id = ?", row.ID).
			Update("progress_logs", gorm.Expr(taskLogAppendSQL(db), entry, entry, entry, entry, entry)).Error; err != nil {
			t.Fatalf("append %q: %v", initial, err)
		}
		var saved taskLogTestRow
		if err := db.First(&saved, row.ID).Error; err != nil {
			t.Fatal(err)
		}
		var values []map[string]interface{}
		if err := json.Unmarshal([]byte(saved.ProgressLogs), &values); err != nil {
			t.Fatalf("append %q produced invalid JSON %q: %v", initial, saved.ProgressLogs, err)
		}
		if len(values) == 0 || values[len(values)-1]["m"] != "step" {
			t.Fatalf("append %q produced unexpected log %q", initial, saved.ProgressLogs)
		}
	}
}
