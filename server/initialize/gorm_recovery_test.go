package initialize

import (
	"fmt"
	"testing"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestBackfillInstanceDesiredStatesUsesSetBasedConservativeRules(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	previousLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previousLog })

	type legacyRow struct {
		UUID           string
		Name           string
		Provider       string
		ProviderID     uint
		Status         string
		IsImported     bool
		TrafficStopped bool
		DesiredState   interface{}
	}
	rows := []legacyRow{
		{UUID: "00000000-0000-0000-0000-000000000101", Name: "running", Provider: "node", ProviderID: 1, Status: "running", DesiredState: nil},
		{UUID: "00000000-0000-0000-0000-000000000102", Name: "manual-stop", Provider: "node", ProviderID: 1, Status: "stopped", DesiredState: nil},
		{UUID: "00000000-0000-0000-0000-000000000103", Name: "imported", Provider: "node", ProviderID: 1, Status: "running", IsImported: true, DesiredState: nil},
		{UUID: "00000000-0000-0000-0000-000000000104", Name: "traffic-stop", Provider: "node", ProviderID: 1, Status: "stopped", TrafficStopped: true, DesiredState: ""},
	}
	for _, row := range rows {
		if err := db.Exec(`INSERT INTO instances (uuid, name, provider, provider_id, status, is_imported, traffic_stopped, desired_state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row.UUID, row.Name, row.Provider, row.ProviderID, row.Status, row.IsImported, row.TrafficStopped, row.DesiredState, time.Now(), time.Now()).Error; err != nil {
			t.Fatalf("insert legacy row %s: %v", row.Name, err)
		}
	}

	backfillInstanceDesiredStates(db)

	got := map[string]string{}
	var instances []providerModel.Instance
	if err := db.Select("name", "desired_state").Find(&instances).Error; err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		got[instance.Name] = instance.DesiredState
	}
	want := map[string]string{
		"running":      providerModel.InstanceDesiredStateRunning,
		"manual-stop":  providerModel.InstanceDesiredStateStopped,
		"imported":     providerModel.InstanceDesiredStateStopped,
		"traffic-stop": providerModel.InstanceDesiredStateRunning,
	}
	for name, expected := range want {
		if got[name] != expected {
			t.Fatalf("%s desired_state = %q, want %q", name, got[name], expected)
		}
	}
}
