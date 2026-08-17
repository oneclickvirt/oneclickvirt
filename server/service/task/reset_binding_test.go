package task

import (
	"strings"
	"testing"
	"time"

	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	ipv6PoolService "oneclickvirt/service/ipv6pool"

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

func TestApplyIPv6AllocationMetadataPreservesRoutedFields(t *testing.T) {
	metadata := map[string]string{"network_type": "nat_ipv4_ipv6"}
	applyIPv6AllocationMetadata(metadata, ipv6PoolService.IPv6AllocationMetadata{
		Address: "2001:db8::2", CIDR: "2001:db8::/126", Gateway: "2001:db8::1", Bridge: "oneclickvirt6", TunnelID: 17, TunnelInterface: "he-ipv6",
	})
	for key, want := range map[string]string{
		"static_ipv6":                  "2001:db8::2",
		"static_ipv6_cidr":             "2001:db8::/126",
		"static_ipv6_gateway":          "2001:db8::1",
		"static_ipv6_bridge":           "oneclickvirt6",
		"static_ipv6_tunnel_id":        "17",
		"static_ipv6_tunnel_interface": "he-ipv6",
		"static_ipv6_network":          "2001:db8::/126",
	} {
		if got := metadata[key]; got != want {
			t.Fatalf("metadata[%q] = %q, want %q", key, got, want)
		}
	}

	native := map[string]string{}
	applyIPv6AllocationMetadata(native, ipv6PoolService.IPv6AllocationMetadata{Address: "2001:db8::30"})
	if native["static_ipv6"] != "2001:db8::30" || len(native) != 1 {
		t.Fatalf("native metadata = %#v", native)
	}
}

func TestValidateResetRoutedIPv6RejectsMissingOrLegacyTunnelMetadata(t *testing.T) {
	valid := ipv6PoolService.IPv6AllocationMetadata{
		Address: "2001:db8::2", CIDR: "2001:db8::/126", Gateway: "2001:db8::1",
		Bridge: "oneclickvirt6", TunnelID: 17, TunnelInterface: "he-ipv6",
	}
	if err := validateResetRoutedIPv6("qemu", "nat_ipv4_ipv6", "2001:db8::2", valid); err != nil {
		t.Fatalf("valid routed reset unexpectedly failed: %v", err)
	}
	if err := validateResetRoutedIPv6("qemu", "nat_ipv4_ipv6", "", valid); err == nil || !strings.Contains(err.Error(), "缺少可迁移") {
		t.Fatalf("missing allocation error = %v", err)
	}
	legacy := valid
	legacy.CIDR = ""
	if err := validateResetRoutedIPv6("virtualbox", "nat_ipv4_ipv6", "2001:db8::2", legacy); err == nil || !strings.Contains(err.Error(), "缺少隧道路由") {
		t.Fatalf("legacy pool error = %v", err)
	}
	if err := validateResetRoutedIPv6("vmware", "nat_ipv4", "2001:db8::2", legacy); err == nil {
		t.Fatal("stale routed allocation must not be silently discarded during reset")
	}
	if err := validateResetRoutedIPv6("proxmox", "nat_ipv4_ipv6", "", ipv6PoolService.IPv6AllocationMetadata{}); err != nil {
		t.Fatalf("native IPv6 backend should retain its existing reset behavior: %v", err)
	}
}
