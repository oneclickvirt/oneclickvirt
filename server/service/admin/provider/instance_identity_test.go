package provider

import (
	"testing"

	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"
)

func TestProxmoxIdentityMatchesVMIDAcrossGuestRename(t *testing.T) {
	db := providerModel.Instance{
		UUID:         "54ddcb4e-5b93-4bd3-8325-d08f1c293943",
		Name:         "old-guest-name",
		ProviderVMID: "120",
	}
	remote := providerCore.DiscoveredInstance{
		UUID:               "proxmox-vm-120",
		ProviderInstanceID: "120",
		Name:               "renamed-guest",
	}

	if !discoveredInstanceMatchesDB("proxmox", remote, db) {
		t.Fatal("same Proxmox VMID must match after the guest is renamed")
	}
}

func TestImportedProxmoxInstancePreservesScopedUUIDAndUsesVMID(t *testing.T) {
	remoteAtImport := providerCore.DiscoveredInstance{
		UUID:               "proxmox-vm-120",
		ProviderInstanceID: "120",
		Name:               "guest-at-import",
	}
	imported := providerModel.Instance{
		UUID:         remoteAtImport.UUID,
		Name:         remoteAtImport.Name,
		ProviderVMID: remoteAtImport.ProviderInstanceID,
	}
	if err := imported.BeforeCreate(nil); err != nil {
		t.Fatalf("BeforeCreate failed: %v", err)
	}
	if imported.UUID != remoteAtImport.UUID {
		t.Fatal("model hook must preserve the provider-scoped discovery UUID")
	}

	remoteAfterRename := providerCore.DiscoveredInstance{
		UUID:               "proxmox-vm-120",
		ProviderInstanceID: "120",
		Name:               "guest-after-rename",
	}
	if !discoveredInstanceMatchesDB("proxmox", remoteAfterRename, imported) {
		t.Fatal("re-discovery must match the imported row by VMID despite local UUID generation and guest rename")
	}
}

func TestNonProxmoxIdentityUsesRemoteIDBeforeReusedName(t *testing.T) {
	db := providerModel.Instance{UUID: "local", Name: "reused", ProviderVMID: "container-a"}
	remote := providerCore.DiscoveredInstance{UUID: "scoped", Name: "reused", ProviderInstanceID: "container-b"}
	if discoveredInstanceMatchesDB("docker", remote, db) {
		t.Fatal("different container IDs must not match merely because names are reused")
	}
}

func TestNonProxmoxLegacyNameFallbackAndBackfill(t *testing.T) {
	dbInstances := []providerModel.Instance{{ID: 8, UUID: "legacy-local", Name: "legacy-container"}}
	remoteInstances := []providerCore.DiscoveredInstance{{UUID: "scoped", Name: "legacy-container", ProviderInstanceID: "sha256:abc"}}
	matches := matchDiscoveredAndDBInstances("docker", remoteInstances, dbInstances)
	if matches.RemoteToDB[0] != 0 {
		t.Fatalf("legacy row was not matched: %#v", matches)
	}
	backfills := providerInstanceIDBackfills("docker", remoteInstances, dbInstances, matches)
	if len(backfills) != 1 || backfills[0].ProviderInstanceID != "sha256:abc" {
		t.Fatalf("legacy remote ID was not backfilled: %#v", backfills)
	}
}

func TestCreatedContainerNameIdentityUpgradesToRuntimeID(t *testing.T) {
	dbInstances := []providerModel.Instance{{ID: 9, UUID: "local", Name: "guest", ProviderVMID: "guest"}}
	remoteInstances := []providerCore.DiscoveredInstance{{UUID: "scoped", Name: "guest", ProviderInstanceID: "container-sha"}}
	matches := matchDiscoveredAndDBInstances("docker", remoteInstances, dbInstances)
	if matches.RemoteToDB[0] != 0 {
		t.Fatalf("name-based legacy identity was not matched: %#v", matches)
	}
	backfills := providerInstanceIDBackfills("docker", remoteInstances, dbInstances, matches)
	if len(backfills) != 1 || backfills[0].PreviousProviderInstanceID != "guest" || backfills[0].ProviderInstanceID != "container-sha" {
		t.Fatalf("runtime identity upgrade missing: %#v", backfills)
	}
}

func TestProxmoxIdentityDoesNotFallBackWhenVMIDsConflict(t *testing.T) {
	db := providerModel.Instance{
		UUID:         "54ddcb4e-5b93-4bd3-8325-d08f1c293943",
		Name:         "reused-name",
		ProviderVMID: "120",
	}
	remote := providerCore.DiscoveredInstance{
		UUID:               "proxmox-vm-121",
		ProviderInstanceID: "121",
		Name:               "reused-name",
	}

	if discoveredInstanceMatchesDB("proxmoxve", remote, db) {
		t.Fatal("different Proxmox VMIDs must not match merely because names are equal")
	}
}

func TestProxmoxIdentityKeepsLegacyNameFallback(t *testing.T) {
	db := providerModel.Instance{
		UUID: "54ddcb4e-5b93-4bd3-8325-d08f1c293943",
		Name: "legacy-guest",
		// ProviderVMID intentionally empty: this represents pre-migration data.
	}
	remote := providerCore.DiscoveredInstance{
		UUID:               "proxmox-lxc-220",
		ProviderInstanceID: "220",
		Name:               "legacy-guest",
	}

	if !discoveredInstanceMatchesDB("pve", remote, db) {
		t.Fatal("legacy Proxmox rows without provider_vm_id must retain name fallback")
	}
}

func TestMatchDiscoveredAndDBInstancesUsesVMIDBeforeLegacyName(t *testing.T) {
	dbInstances := []providerModel.Instance{
		{ID: 1, UUID: "local-1", Name: "shared-name"},
		{ID: 2, UUID: "local-2", Name: "old-name", ProviderVMID: "120", Status: "running"},
	}
	remoteInstances := []providerCore.DiscoveredInstance{
		{UUID: "proxmox-vm-120", ProviderInstanceID: "120", Name: "shared-name", Status: "stopped"},
	}

	matches := matchDiscoveredAndDBInstances("proxmox", remoteInstances, dbInstances)
	if got := matches.RemoteToDB[0]; got != 1 {
		t.Fatalf("VMID match should select db index 1, got %d", got)
	}
	if _, legacyConsumed := matches.DBToRemote[0]; legacyConsumed {
		t.Fatal("legacy same-name row must not override an exact VMID match")
	}
}

func TestMatchDiscoveredAndDBInstancesTreatsRenamedGuestAsManaged(t *testing.T) {
	dbInstances := []providerModel.Instance{
		{ID: 7, UUID: "random-local-uuid", Name: "before-rename", ProviderVMID: "305", Status: "running"},
	}
	remoteInstances := []providerCore.DiscoveredInstance{
		{UUID: "proxmox-lxc-305", ProviderInstanceID: "305", Name: "after-rename", Status: "stopped"},
	}

	matches := matchDiscoveredAndDBInstances("proxmox", remoteInstances, dbInstances)
	if len(matches.RemoteToDB) != 1 || len(matches.DBToRemote) != 1 {
		t.Fatalf("renamed guest should be paired one-to-one, got remote=%v db=%v", matches.RemoteToDB, matches.DBToRemote)
	}
	if matches.RemoteToDB[0] != 0 {
		t.Fatalf("renamed guest paired with wrong database row: %v", matches.RemoteToDB)
	}
}

func TestDiscoveredInstanceDeleteIDUsesProxmoxVMID(t *testing.T) {
	remote := providerCore.DiscoveredInstance{
		UUID:               "proxmox-vm-999",
		ProviderInstanceID: "999",
		Name:               "guest-999",
	}
	if got := discoveredInstanceDeleteID("proxmox", remote); got != "999" {
		t.Fatalf("expected Proxmox delete identity 999, got %q", got)
	}
}

func TestProviderInstanceIDBackfillsLegacyMatch(t *testing.T) {
	dbInstances := []providerModel.Instance{
		{ID: 42, UUID: "random-local-uuid", Name: "legacy-name"},
	}
	remoteInstances := []providerCore.DiscoveredInstance{
		{UUID: "proxmox-vm-410", ProviderInstanceID: "410", Name: "legacy-name"},
	}
	matches := matchDiscoveredAndDBInstances("proxmox", remoteInstances, dbInstances)

	backfills := providerInstanceIDBackfills("proxmox", remoteInstances, dbInstances, matches)
	if len(backfills) != 1 {
		t.Fatalf("expected one legacy VMID backfill, got %#v", backfills)
	}
	if backfills[0].InstanceID != 42 || backfills[0].ProviderInstanceID != "410" {
		t.Fatalf("unexpected VMID backfill: %#v", backfills[0])
	}
}

func TestProviderInstanceIDBackfillsDoesNotOverwriteExistingVMID(t *testing.T) {
	dbInstances := []providerModel.Instance{
		{ID: 42, UUID: "random-local-uuid", Name: "guest", ProviderVMID: "410"},
	}
	remoteInstances := []providerCore.DiscoveredInstance{
		{UUID: "proxmox-vm-410", ProviderInstanceID: "410", Name: "renamed-guest"},
	}
	matches := matchDiscoveredAndDBInstances("proxmox", remoteInstances, dbInstances)

	if backfills := providerInstanceIDBackfills("proxmox", remoteInstances, dbInstances, matches); len(backfills) != 0 {
		t.Fatalf("existing VMID must not be overwritten: %#v", backfills)
	}
}
