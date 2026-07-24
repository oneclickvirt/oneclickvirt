package task

import (
	"strings"
	"testing"
	"time"

	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestResetReplacementInstancePreservesLogicalNetworkIdentity(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	resetCtx := &ResetTaskContext{
		Instance: providerModel.Instance{
			UUID: "logical-instance-uuid", Image: "debian", InstanceType: "container",
			CPU: 2, Memory: 2048, Disk: 10240, Bandwidth: 100,
			OSType: "linux", NetworkType: "dedicated_ipv4_ipv6", EgressProfileID: "exit-a",
		},
		Provider:               providerModel.Provider{ID: 7, Name: "node-a", Endpoint: "203.0.113.7"},
		OldInstanceName:        "instance-a",
		OriginalUserID:         9,
		OriginalExpiresAt:      &expiresAt,
		OriginalIsManualExpiry: true,
		OriginalMaxTraffic:     4096,
	}

	replacement := resetReplacementInstance(resetCtx)
	if replacement.UUID != resetCtx.Instance.UUID || replacement.EgressProfileID != "exit-a" {
		t.Fatalf("logical identity was not preserved: %#v", replacement)
	}
	if replacement.NetworkType != "dedicated_ipv4_ipv6" || replacement.ProviderID != 7 || replacement.Name != "instance-a" {
		t.Fatalf("network/provider identity was not preserved: %#v", replacement)
	}
	if replacement.Status != "creating" || replacement.ExpiresAt == nil || !replacement.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("replacement lifecycle fields = %#v", replacement)
	}
}

func TestTransferResetEgressBindingInTxMovesOnlyActiveBinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:reset_egress_active?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&monitoringModel.EgressDesiredBinding{}); err != nil {
		t.Fatal(err)
	}
	binding := monitoringModel.EgressDesiredBinding{
		InstanceID: 11, InstanceKey: "stable-key", ProviderID: 3, ProfileID: "exit-a",
		SourcesJSON: `["10.0.0.11/32"]`, Enabled: true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return transferResetEgressBindingInTx(tx, binding.ID, 3, 11, 12)
	}); err != nil {
		t.Fatalf("transferResetEgressBindingInTx() error = %v", err)
	}
	var moved monitoringModel.EgressDesiredBinding
	if err := db.First(&moved, binding.ID).Error; err != nil {
		t.Fatal(err)
	}
	if moved.InstanceID != 12 || moved.InstanceKey != "stable-key" || moved.ProfileID != "exit-a" {
		t.Fatalf("moved binding = %#v", moved)
	}
}

func TestTransferResetEgressBindingInTxRejectsPendingDelete(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:reset_egress_pending?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&monitoringModel.EgressDesiredBinding{}); err != nil {
		t.Fatal(err)
	}
	binding := monitoringModel.EgressDesiredBinding{
		InstanceID: 21, InstanceKey: "pending-key", ProviderID: 4, ProfileID: "exit-b",
		SourcesJSON: `["10.0.0.21/32"]`, PendingDelete: true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		return transferResetEgressBindingInTx(tx, binding.ID, 4, 21, 22)
	})
	if err == nil || !strings.Contains(err.Error(), "并发变化") {
		t.Fatalf("transfer error = %v, want pending-delete rejection", err)
	}
	var unchanged monitoringModel.EgressDesiredBinding
	if err := db.First(&unchanged, binding.ID).Error; err != nil {
		t.Fatal(err)
	}
	if unchanged.InstanceID != 21 || !unchanged.PendingDelete {
		t.Fatalf("pending binding changed: %#v", unchanged)
	}
}
