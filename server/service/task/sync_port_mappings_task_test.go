package task

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRemoteInstanceIdentityMatchesRenamedRuntime(t *testing.T) {
	remoteIdentities := buildRemoteInstanceIdentitySet([]provider.Instance{{
		ID:   "container-runtime-id",
		Name: "renamed-container",
	}})

	instance := providerModel.Instance{
		Name:         "renamed-container",
		ProviderVMID: "container-runtime-id",
	}
	if !remoteInstanceMatchesDBInstance("containerd", remoteIdentities, instance) {
		t.Fatal("remote runtime ID/name should match the managed instance")
	}

	legacyInstance := providerModel.Instance{Name: "renamed-container"}
	if !remoteInstanceMatchesDBInstance("containerd", remoteIdentities, legacyInstance) {
		t.Fatal("legacy instance rows should retain name matching")
	}
}

func TestRemoteInstanceIdentityUsesProxmoxVMID(t *testing.T) {
	remoteIdentities := buildRemoteInstanceIdentitySet([]provider.Instance{{ID: "121", Name: "reused-name"}})
	managed := providerModel.Instance{Name: "reused-name", ProviderVMID: "120"}
	if remoteInstanceMatchesDBInstance("proxmox", remoteIdentities, managed) {
		t.Fatal("a reused Proxmox name must not override a conflicting VMID")
	}
	legacy := providerModel.Instance{Name: "reused-name"}
	if !remoteInstanceMatchesDBInstance("proxmox", remoteIdentities, legacy) {
		t.Fatal("legacy Proxmox rows without provider_vm_id should retain name fallback")
	}
}

func TestIsSafeSyncOrphanCandidateDefersRecentAndBusyInstances(t *testing.T) {
	now := time.Now()
	for name, instance := range map[string]providerModel.Instance{
		"recent":     {Status: "running", CreatedAt: now.Add(-time.Minute)},
		"creating":   {Status: "creating", CreatedAt: now.Add(-time.Hour)},
		"rebuilding": {Status: "rebuilding", CreatedAt: now.Add(-time.Hour)},
	} {
		if isSafeSyncOrphanCandidate(instance, now) {
			t.Fatalf("%s instance should be deferred", name)
		}
	}
	if !isSafeSyncOrphanCandidate(providerModel.Instance{Status: "running", CreatedAt: now.Add(-time.Hour)}, now) {
		t.Fatal("an old stable instance should remain eligible for explicit cleanup")
	}
}

func TestCreateSyncPortMappingsTaskRequiresPreviewSelection(t *testing.T) {
	service := &TaskService{}
	_, err := service.CreateSyncPortMappingsTask(1, &adminModel.SyncPortMappingsTaskRequest{
		ProviderIDs: []uint{1},
	}, 0)
	if err == nil || !strings.Contains(err.Error(), "预览") {
		t.Fatalf("empty selection error = %v, want preview-selection validation", err)
	}
}

func TestDeleteSyncPortsKeepsInstanceRecord(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:sync_ports_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	previousDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previousDB })

	if err := db.Exec(`CREATE TABLE providers (
		id integer primary key,
		port_range_start integer,
		port_range_end integer,
		next_available_port integer
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE ports (
		id integer primary key,
		provider_id integer,
		instance_id integer,
		host_port integer,
		guest_port integer,
		protocol text,
		is_automatic numeric,
		is_ssh numeric
	)`).Error; err != nil {
		t.Fatal(err)
	}
	providerRow := providerModel.Provider{ID: 1, Name: "node-a"}
	if err := db.Exec("INSERT INTO providers (id, port_range_start, port_range_end, next_available_port) VALUES (?, ?, ?, ?)",
		providerRow.ID, 20000, 30000, 25000).Error; err != nil {
		t.Fatal(err)
	}
	instance := providerModel.Instance{
		Name: "instance-a", Provider: providerRow.Name, ProviderID: providerRow.ID,
		Status: "running", SSHPort: 26000,
	}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	port := providerModel.Port{
		ProviderID: providerRow.ID, InstanceID: instance.ID, HostPort: 26000,
		GuestPort: 22, Protocol: "tcp", IsAutomatic: true, IsSSH: true,
	}
	if err := db.Exec("INSERT INTO ports (id, provider_id, instance_id, host_port, guest_port, protocol, is_automatic, is_ssh) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		1, port.ProviderID, port.InstanceID, port.HostPort, port.GuestPort, port.Protocol, port.IsAutomatic, port.IsSSH).Error; err != nil {
		t.Fatal(err)
	}
	port.ID = 1

	service := &TaskService{}
	deleted, err := service.deleteSyncPortsInShortTransaction(context.Background(), providerRow.ID, []providerModel.Port{port})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted ports = %d, want 1", deleted)
	}
	var kept providerModel.Instance
	if err := db.First(&kept, instance.ID).Error; err != nil {
		t.Fatalf("instance row was deleted: %v", err)
	}
	if kept.SSHPort != 0 {
		t.Fatalf("stale SSH port = %d, want 0", kept.SSHPort)
	}
	if err := db.Unscoped().First(&providerModel.Port{}, port.ID).Error; err == nil {
		t.Fatal("selected port row still exists")
	}
}
