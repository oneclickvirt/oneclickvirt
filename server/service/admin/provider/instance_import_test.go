package provider

import (
	"errors"
	"strings"
	"testing"
	"time"

	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestNormalizeImportedInstanceUUIDMatchesDatabaseIdentity(t *testing.T) {
	if got := normalizeImportedInstanceUUID("  B12DF9C1-18BB-52E4-B509-471524819DF7  "); got != "b12df9c1-18bb-52e4-b509-471524819df7" {
		t.Fatalf("normalized UUID = %q", got)
	}
}

func TestDuplicateImportResourceErrorClassification(t *testing.T) {
	for _, message := range []string{
		"Error 1062: Duplicate entry '22022' for key 'idx_provider_host_port'",
		"UNIQUE constraint failed: ports.provider_id, ports.host_port",
	} {
		if !isDuplicateImportResourceError(errors.New(message)) {
			t.Fatalf("duplicate resource error was not classified: %s", message)
		}
	}
	if isDuplicateImportResourceError(errors.New("connection reset")) {
		t.Fatal("unrelated database error was classified as duplicate")
	}
}

func TestSelectDiscoveredInstancesRejectsMissingAndSelectsOneAmbiguousName(t *testing.T) {
	instances := []providerCore.DiscoveredInstance{
		{UUID: "uuid-a", ProviderInstanceID: "id-a", Name: "same"},
		{UUID: "uuid-b", ProviderInstanceID: "id-b", Name: "same"},
	}
	if _, err := selectDiscoveredInstances(instances, []string{"missing"}); err == nil {
		t.Fatal("missing selector was accepted")
	}
	byName, err := selectDiscoveredInstances(instances, []string{"same"})
	if err != nil || len(byName) != 1 || byName[0].ProviderInstanceID != "id-a" {
		t.Fatalf("ambiguous name must deterministically select one instance: selected=%#v err=%v", byName, err)
	}
	selected, err := selectDiscoveredInstances(instances, []string{"uuid-a", "uuid-a"})
	if err != nil || len(selected) != 1 || selected[0].ProviderInstanceID != "id-a" {
		t.Fatalf("UUID selection/dedup failed: selected=%#v err=%v", selected, err)
	}
}

func TestDeduplicateDiscoveredInstancesKeepsOneForAnyManagedResourceConflict(t *testing.T) {
	instances := []providerCore.DiscoveredInstance{
		{UUID: "uuid-b", ProviderInstanceID: "id-b", Name: "beta", PrivateIP: "10.0.0.3", SSHPort: 2200},
		{UUID: "uuid-a", ProviderInstanceID: "id-a", Name: "alpha", PrivateIP: "10.0.0.2", SSHPort: 2200},
		{UUID: "uuid-c", ProviderInstanceID: "id-c", Name: "alpha", PrivateIP: "10.0.0.4", SSHPort: 2201},
		{UUID: "uuid-d", ProviderInstanceID: "id-d", Name: "delta", PrivateIP: "10.0.0.2", SSHPort: 2202},
	}
	kept, duplicates := deduplicateDiscoveredInstances(instances)
	if len(kept) != 1 || kept[0].Name != "alpha" {
		t.Fatalf("stable first winner not kept: %#v", kept)
	}
	if len(duplicates) != 3 {
		t.Fatalf("duplicates=%d, want 3: %#v", len(duplicates), duplicates)
	}
}

func TestConflictingInstanceNameUsesRemoteIdentity(t *testing.T) {
	existing := []providerModel.Instance{{Name: "guest", ProviderVMID: "remote-a", UUID: "local-a"}}
	if !hasConflictingInstanceName("docker", providerCore.DiscoveredInstance{Name: "guest", ProviderInstanceID: "remote-b", UUID: "local-b"}, existing) {
		t.Fatal("same name with a different remote identity must conflict")
	}
	if hasConflictingInstanceName("docker", providerCore.DiscoveredInstance{Name: "guest", ProviderInstanceID: "remote-a", UUID: "local-b"}, existing) {
		t.Fatal("same remote identity must not be reported as a name conflict")
	}
}

func TestConflictingExistingInstanceResourceKeepsManagedWinner(t *testing.T) {
	existing := []providerModel.Instance{{Name: "managed", PrivateIP: "10.0.0.2", IPv6Address: "2001:db8::1"}}
	if reason := conflictingExistingInstanceResource(providerCore.DiscoveredInstance{PrivateIP: "10.0.0.2"}, existing); reason == "" {
		t.Fatal("duplicate private IP was not detected")
	}
	if reason := conflictingExistingInstanceResource(providerCore.DiscoveredInstance{IPv6Address: "2001:DB8::1"}, existing); reason == "" {
		t.Fatal("duplicate IPv6 address was not detected")
	}
	if reason := conflictingExistingInstanceResource(providerCore.DiscoveredInstance{PrivateIP: "10.0.0.3"}, existing); reason != "" {
		t.Fatalf("unique resource rejected: %s", reason)
	}
}

func TestHistoricalImportOwnerCanOnlyBeRestoredOnSameProvider(t *testing.T) {
	deletedAt := time.Now()
	tests := []struct {
		name       string
		owner      providerModel.Instance
		providerID uint
		want       bool
	}{
		{
			name:       "same provider tombstone",
			owner:      providerModel.Instance{ProviderID: 7, DeletedAt: gorm.DeletedAt{Time: deletedAt, Valid: true}},
			providerID: 7,
			want:       true,
		},
		{
			name:       "active owner",
			owner:      providerModel.Instance{ProviderID: 7},
			providerID: 7,
			want:       false,
		},
		{
			name:       "different provider tombstone",
			owner:      providerModel.Instance{ProviderID: 8, DeletedAt: gorm.DeletedAt{Time: deletedAt, Valid: true}},
			providerID: 7,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canRestoreHistoricalImportedInstance(tt.owner, tt.providerID); got != tt.want {
				t.Fatalf("canRestoreHistoricalImportedInstance() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRestoreHistoricalImportedInstanceClearsTombstoneAtomically(t *testing.T) {
	db, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "gorm:gorm@tcp(localhost:9910)/gorm?charset=utf8&parseTime=True&loc=Local",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open dry-run database: %v", err)
	}

	instance := providerModel.Instance{ID: 12, UUID: "provider-scoped-uuid", Name: "restored", ProviderID: 7}
	result := restoreHistoricalImportedInstance(db, &instance, 12)
	if result.Error != nil {
		t.Fatalf("build restore update: %v", result.Error)
	}
	statement := result.Statement.SQL.String()
	if !strings.Contains(statement, "`deleted_at`=") {
		t.Fatalf("restore update does not clear deleted_at: %s", statement)
	}
	if !strings.Contains(statement, "id = ? AND deleted_at IS NOT NULL") {
		t.Fatalf("restore update is not guarded by the tombstone state: %s", statement)
	}
	if strings.Contains(statement, "`created_at`=") || strings.Contains(statement, "`id`=") {
		t.Fatalf("restore update overwrites immutable identity fields: %s", statement)
	}
}
