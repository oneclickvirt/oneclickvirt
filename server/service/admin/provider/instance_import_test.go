package provider

import (
	"testing"

	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"
)

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
