package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newRuntimeNetworkTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE providers (
		id integer primary key autoincrement,
		name text, type text, status text, deleted_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE ports (
		id integer primary key autoincrement, instance_id integer, provider_id integer,
		host_port integer, guest_port integer, status text, is_ssh boolean,
		mapping_type text, deleted_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func createRuntimeNetworkTestProvider(t *testing.T, db *gorm.DB, name string) providerModel.Provider {
	t.Helper()
	if err := db.Exec("INSERT INTO providers (name, type, status) VALUES (?, ?, ?)", name, "docker", "active").Error; err != nil {
		t.Fatal(err)
	}
	var provider providerModel.Provider
	if err := db.Raw("SELECT id, name, type, status FROM providers WHERE name = ? ORDER BY id DESC LIMIT 1", name).Scan(&provider).Error; err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestProviderRecoveryWaitTracksOnlyActiveStartTasks(t *testing.T) {
	db := newRuntimeNetworkTestDB(t)
	if err := db.Exec(`CREATE TABLE tasks (
		id integer primary key autoincrement,
		provider_id integer,
		instance_id integer,
		task_type text,
		status text,
		deleted_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}
	provider := createRuntimeNetworkTestProvider(t, db, "recovery-wait-node")
	if err := db.Exec(`INSERT INTO tasks (provider_id, instance_id, task_type, status) VALUES (?, ?, ?, ?), (?, ?, ?, ?)`,
		provider.ID, 1, "start", "pending", provider.ID, 1, "stop", "running").Error; err != nil {
		t.Fatal(err)
	}
	active, err := providerHasActiveStartTasks(db, provider.ID)
	if err != nil || !active {
		t.Fatalf("providerHasActiveStartTasks() = (%v, %v), want (true, nil)", active, err)
	}
	if err := db.Exec(`UPDATE tasks SET status = ? WHERE provider_id = ? AND task_type = ?`, "completed", provider.ID, "start").Error; err != nil {
		t.Fatal(err)
	}
	active, err = providerHasActiveStartTasks(db, provider.ID)
	if err != nil || active {
		t.Fatalf("providerHasActiveStartTasks() after completion = (%v, %v), want (false, nil)", active, err)
	}
}

func TestSynchronizeDiscoveredInstanceNetworksUpdatesOnlyAuthoritativeFields(t *testing.T) {
	db := newRuntimeNetworkTestDB(t)
	provider := createRuntimeNetworkTestProvider(t, db, "network-node")
	instances := []providerModel.Instance{
		{
			Name: "updated", Provider: provider.Name, ProviderID: provider.ID, Status: "running",
			PrivateIP: "10.0.0.1", PublicIP: "198.51.100.1", IPv6Address: "2001:db8::1", SSHPort: 22,
			PmacctInterfaceV4: "veth-old", PmacctInterfaceV6: "veth6-old",
		},
		{
			Name: "protected", Provider: provider.Name, ProviderID: provider.ID, Status: "running",
			PrivateIP: "10.0.0.2", PublicIP: "198.51.100.2", IPv6Address: "2001:db8::2", SSHHost: "bastion.internal", SSHPort: 22,
			PmacctInterfaceV4: "veth-protected", PmacctInterfaceV6: "veth6-protected",
		},
	}
	if err := db.Create(&instances).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ports (instance_id, provider_id, host_port, guest_port, status, is_ssh, mapping_type)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, instances[1].ID, provider.ID, 2202, 22, "active", true, "node").Error; err != nil {
		t.Fatal(err)
	}

	result, err := SynchronizeDiscoveredInstanceNetworks(context.Background(), db, provider.ID, []DiscoveredInstanceNetwork{
		{
			InstanceID: instances[0].ID, Status: "up", PrivateIP: "10.0.0.11", PublicIP: "203.0.113.11", IPv6Address: "[2001:db8::11]", SSHPort: 2211,
		},
		{
			InstanceID: instances[1].ID, Status: "running", PrivateIP: "10.0.0.22", PublicIP: "", IPv6Address: "not-an-ip", SSHPort: 2222,
		},
		{InstanceID: 999999, Status: "stopped", PrivateIP: "10.0.0.99"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRuntimeNetworkID(result.PrivateIPChangedInstanceIDs, instances[0].ID) || !hasRuntimeNetworkID(result.PrivateIPChangedInstanceIDs, instances[1].ID) {
		t.Fatalf("private-IP changes = %v, want both instances", result.PrivateIPChangedInstanceIDs)
	}
	if !hasRuntimeNetworkID(result.NetworkChangedInstanceIDs, instances[0].ID) || !hasRuntimeNetworkID(result.NetworkChangedInstanceIDs, instances[1].ID) {
		t.Fatalf("network changes = %v, want both instances", result.NetworkChangedInstanceIDs)
	}

	var updated, protected providerModel.Instance
	if err := db.First(&updated, instances[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&protected, instances[1].ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.PrivateIP != "10.0.0.11" || updated.PublicIP != "203.0.113.11" || updated.IPv6Address != "2001:db8::11" || updated.SSHPort != 2211 {
		t.Fatalf("updated runtime network = %#v", updated)
	}
	if updated.PmacctInterfaceV4 != "" || updated.PmacctInterfaceV6 != "" {
		t.Fatalf("changed address must clear stale interfaces: %#v", updated)
	}
	if protected.PrivateIP != "10.0.0.22" {
		t.Fatalf("protected private IP = %q, want 10.0.0.22", protected.PrivateIP)
	}
	if protected.PublicIP != "198.51.100.2" || protected.IPv6Address != "2001:db8::2" {
		t.Fatalf("empty or invalid discovery values overwrote authoritative values: %#v", protected)
	}
	if protected.SSHHost != "bastion.internal" || protected.SSHPort != 22 {
		t.Fatalf("explicit SSH contract was overwritten: %#v", protected)
	}
}

func TestSynchronizeDiscoveredInstanceNetworksChunksLargeProviderUpdate(t *testing.T) {
	db := newRuntimeNetworkTestDB(t)
	provider := createRuntimeNetworkTestProvider(t, db, "large-network-node")
	// Cross both the bounded read and CASE-update boundaries. A post-reboot
	// discovery must not turn this into an unbounded IN clause or per-row write.
	instances := make([]providerModel.Instance, 0, runtimeNetworkReadBatchSize+1)
	for i := 0; i <= runtimeNetworkReadBatchSize; i++ {
		instances = append(instances, providerModel.Instance{
			Name: fmt.Sprintf("guest-%03d", i), Provider: provider.Name, ProviderID: provider.ID, Status: "running",
			PrivateIP: fmt.Sprintf("10.20.0.%d", i+1),
		})
	}
	if err := db.CreateInBatches(&instances, runtimeNetworkUpdateBatchSize).Error; err != nil {
		t.Fatal(err)
	}
	snapshots := make([]DiscoveredInstanceNetwork, 0, len(instances))
	for i, instance := range instances {
		snapshots = append(snapshots, DiscoveredInstanceNetwork{
			InstanceID: instance.ID, Status: "started", PrivateIP: fmt.Sprintf("10.21.0.%d", i+1),
		})
	}
	result, err := SynchronizeDiscoveredInstanceNetworks(context.Background(), db, provider.ID, snapshots)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ChangedInstanceIDs) != len(instances) || len(result.PrivateIPChangedInstanceIDs) != len(instances) {
		t.Fatalf("result = %#v, want %d changed instances", result, len(instances))
	}
	var changed int64
	if err := db.Model(&providerModel.Instance{}).Where("provider_id = ? AND private_ip LIKE ?", provider.ID, "10.21.0.%").Count(&changed).Error; err != nil {
		t.Fatal(err)
	}
	if changed != int64(len(instances)) {
		t.Fatalf("updated rows = %d, want %d", changed, len(instances))
	}
}

func TestResolveControllerPortTargetPreservesExplicitHostContract(t *testing.T) {
	tests := []struct {
		internalHost string
		privateIP    string
		wantHost     string
		wantUpdate   bool
	}{
		{internalHost: "bastion.internal", privateIP: "10.0.0.8", wantHost: "bastion.internal", wantUpdate: false},
		{internalHost: "10.0.0.7", privateIP: "10.0.0.8", wantHost: "10.0.0.8", wantUpdate: true},
		{internalHost: "", privateIP: "10.0.0.8", wantHost: "10.0.0.8", wantUpdate: true},
		{internalHost: "", privateIP: "", wantHost: "", wantUpdate: false},
	}
	for _, test := range tests {
		host, update := ResolveControllerPortTarget(test.internalHost, test.privateIP)
		if host != test.wantHost || update != test.wantUpdate {
			t.Fatalf("ResolveControllerPortTarget(%q, %q) = (%q, %v), want (%q, %v)", test.internalHost, test.privateIP, host, update, test.wantHost, test.wantUpdate)
		}
	}
}

func TestResolveControllerRecoveryTargetsUsesBoundedAuthoritativeAddressWriteback(t *testing.T) {
	db := newRuntimeNetworkTestDB(t)
	if err := db.Exec("ALTER TABLE ports ADD COLUMN mapping_method text").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE ports ADD COLUMN internal_host text").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE ports ADD COLUMN created_at datetime").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE ports ADD COLUMN updated_at datetime").Error; err != nil {
		t.Fatal(err)
	}
	previousDB := global.APP_DB
	previousLog := global.APP_LOG
	global.APP_DB = db
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB = previousDB
		global.APP_LOG = previousLog
	})

	provider := createRuntimeNetworkTestProvider(t, db, "controller-recovery-node")
	instances := []providerModel.Instance{
		{Name: "changed-ip", Provider: provider.Name, ProviderID: provider.ID, Status: "running", PrivateIP: "10.30.0.11"},
		{Name: "explicit-host", Provider: provider.Name, ProviderID: provider.ID, Status: "running", PrivateIP: "10.30.0.12"},
		{Name: "empty-host", Provider: provider.Name, ProviderID: provider.ID, Status: "running", PrivateIP: "10.30.0.13"},
	}
	if err := db.Create(&instances).Error; err != nil {
		t.Fatal(err)
	}
	ports := []providerModel.Port{
		{ProviderID: provider.ID, InstanceID: instances[0].ID, HostPort: 2301, GuestPort: 22, MappingType: "controller", MappingMethod: "controller", Status: "active", InternalHost: "10.30.0.1"},
		{ProviderID: provider.ID, InstanceID: instances[1].ID, HostPort: 2302, GuestPort: 22, MappingType: "controller", MappingMethod: "controller", Status: "active", InternalHost: "bastion.internal"},
		{ProviderID: provider.ID, InstanceID: instances[2].ID, HostPort: 2303, GuestPort: 22, MappingType: "controller", MappingMethod: "controller", Status: "active"},
	}
	for i := range ports {
		if err := db.Exec(`INSERT INTO ports (instance_id, provider_id, host_port, guest_port, status, mapping_type, mapping_method, internal_host)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			ports[i].InstanceID, ports[i].ProviderID, ports[i].HostPort, ports[i].GuestPort, ports[i].Status,
			ports[i].MappingType, ports[i].MappingMethod, ports[i].InternalHost).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Raw("SELECT id FROM ports WHERE host_port = ?", ports[i].HostPort).Scan(&ports[i].ID).Error; err != nil {
			t.Fatal(err)
		}
	}

	targets, err := resolveControllerRecoveryTargets(ports)
	if err != nil {
		t.Fatal(err)
	}
	want := map[uint]string{
		ports[0].ID: "10.30.0.11",
		ports[1].ID: "bastion.internal",
		ports[2].ID: "10.30.0.13",
	}
	for portID, expected := range want {
		if got := targets[portID]; got != expected {
			t.Fatalf("target[%d] = %q, want %q", portID, got, expected)
		}
	}

	var updated []struct {
		ID           uint
		InternalHost string
	}
	if err := db.Raw("SELECT id, internal_host FROM ports ORDER BY id ASC").Scan(&updated).Error; err != nil {
		t.Fatal(err)
	}
	if updated[0].InternalHost != "10.30.0.11" || updated[1].InternalHost != "bastion.internal" || updated[2].InternalHost != "10.30.0.13" {
		t.Fatalf("persisted recovery targets = %#v", updated)
	}
}

func TestRuntimeNetworkInstanceIDForRemotePrefersStableProviderIdentity(t *testing.T) {
	byProviderID := map[string]uint{"vm-101": 1}
	byUUID := map[string]uint{"controller-uuid": 2}
	byName := map[string]uint{"guest": 3}
	tests := []struct {
		remote providerCore.DiscoveredInstance
		want   uint
		ok     bool
	}{
		{remote: providerCore.DiscoveredInstance{ProviderInstanceID: "vm-101", UUID: "controller-uuid", Name: "guest"}, want: 1, ok: true},
		{remote: providerCore.DiscoveredInstance{UUID: "controller-uuid", Name: "guest"}, want: 2, ok: true},
		{remote: providerCore.DiscoveredInstance{Name: "guest"}, want: 3, ok: true},
		{remote: providerCore.DiscoveredInstance{Name: "unknown"}, ok: false},
	}
	for _, test := range tests {
		got, ok := runtimeNetworkInstanceIDForRemote(test.remote, byProviderID, byUUID, byName)
		if got != test.want || ok != test.ok {
			t.Fatalf("runtimeNetworkInstanceIDForRemote(%#v) = (%d, %v), want (%d, %v)", test.remote, got, ok, test.want, test.ok)
		}
	}
}

func hasRuntimeNetworkID(ids []uint, wanted uint) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}
