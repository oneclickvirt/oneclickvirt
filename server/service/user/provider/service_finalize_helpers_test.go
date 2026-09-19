package provider

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	providerModel "oneclickvirt/model/provider"
)

func finalizedSSHPortDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&providerModel.Port{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPublicIPv6PersistenceDistinguishesNATFromRoutedGuest(t *testing.T) {
	tests := []struct {
		network string
		value   string
		want    string
	}{
		{network: "nat_ipv4_ipv6", value: "2607:9d00:2000:45::35b0:f1cd", want: ""},
		{network: "dedicated_ipv4_ipv6", value: "2001:db8::20", want: "2001:db8::20"},
		{network: "ipv6_only", value: "2001:db8::21/128", want: "2001:db8::21/128"},
		{network: "nat_ipv4", value: "2001:db8::22", want: ""},
	}
	for _, test := range tests {
		t.Run(test.network, func(t *testing.T) {
			updates := publicIPv6Update(test.network, test.value)
			if got := updates["public_ipv6"]; got != test.want {
				t.Fatalf("publicIPv6Update(%q, %q) = %#v, want %q", test.network, test.value, got, test.want)
			}
		})
	}
}

func TestPublicIPv6PersistenceClearsStaleNATValueOnFailedProbe(t *testing.T) {
	updates := publicIPv6Update("nat_ipv4_ipv6", "")
	if got, ok := updates["public_ipv6"]; !ok || got != "" {
		t.Fatalf("NAT update must clear stale public IPv6, got %#v", updates)
	}
}

func TestPublicIPv6PersistenceKeepsRoutedAddressOnTransientProbeFailure(t *testing.T) {
	if updates := publicIPv6Update("dedicated_ipv4_ipv6", ""); len(updates) != 0 {
		t.Fatalf("routed probe failure must not erase a known address, got %#v", updates)
	}
}

func TestFinalizedSSHPortUsesMappingAcrossProviders(t *testing.T) {
	for _, providerType := range []string{"incus", "lxd", "docker", "podman", "containerd", "proxmox", "qemu", "kubevirt"} {
		for _, instanceType := range []string{"container", "vm"} {
			t.Run(providerType+"/"+instanceType, func(t *testing.T) {
				db := finalizedSSHPortDB(t)
				rows := []providerModel.Port{
					{InstanceID: 2, HostPort: 29001, GuestPort: 22, IsSSH: true, Protocol: "tcp", Status: "active"},
					{InstanceID: 1, HostPort: 29002, GuestPort: 22, IsSSH: true, Protocol: "tcp", Status: "deleting"},
					{InstanceID: 1, HostPort: 29003, GuestPort: 22, IsSSH: true, Protocol: "udp", Status: "active"},
					{InstanceID: 1, HostPort: 29900, GuestPort: 22, IsSSH: true, Protocol: "both", Status: "active"},
				}
				if err := db.Create(&rows).Error; err != nil {
					t.Fatal(err)
				}
				updates := map[string]interface{}{"status": "running"}
				instance := providerModel.Instance{ID: 1, InstanceType: instanceType, NetworkType: "nat_ipv4", SSHPort: 22}
				if err := applyFinalizedSSHPort(db, instance, providerModel.Provider{Type: providerType}, updates); err != nil {
					t.Fatal(err)
				}
				if updates["ssh_port"] != 29900 || updates["status"] != "running" {
					t.Fatalf("finalized endpoint ignored active mapping: %#v", updates)
				}
			})
		}
	}
}

func TestFinalizedSSHPortFallbackPreservesReservationsAndDirectNetworks(t *testing.T) {
	for _, tc := range []struct {
		name, network, providerNetwork string
		current, want                  int
	}{
		{"pending NAT reservation", "nat_ipv4", "", 29900, 0},
		{"unallocated NAT", "nat_ipv4", "dedicated_ipv4", 0, 0},
		{"pending dual-stack reservation", "nat_ipv4_ipv6", "", 29900, 0},
		{"custom direct port", "dedicated_ipv4", "", 2222, 0},
		{"direct IPv4", "dedicated_ipv4", "", 0, 22},
		{"direct dual-stack", "dedicated_ipv4_ipv6", "", 0, 22},
		{"direct IPv6", "ipv6_only", "nat_ipv4", 0, 22},
		{"Agent without mapping", "no_port_mapping", "", 0, 22},
		{"legacy provider network", "", "dedicated_ipv4", 0, 22},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := finalizedSSHPortDB(t)
			updates := map[string]interface{}{}
			instance := providerModel.Instance{ID: 1, NetworkType: tc.network, SSHPort: tc.current}
			if err := applyFinalizedSSHPort(db, instance, providerModel.Provider{NetworkType: tc.providerNetwork}, updates); err != nil {
				t.Fatal(err)
			}
			if tc.want == 0 {
				if _, exists := updates["ssh_port"]; exists {
					t.Fatalf("existing reservation/custom port overwritten: %#v", updates)
				}
			} else if updates["ssh_port"] != tc.want {
				t.Fatalf("direct network default = %#v, want %d", updates, tc.want)
			}
		})
	}
}

func TestFinalizedSSHPortFailureDoesNotWriteDefault(t *testing.T) {
	for _, failure := range []string{"database unavailable", "query failed", "invalid mapping"} {
		t.Run(failure, func(t *testing.T) {
			db := finalizedSSHPortDB(t)
			switch failure {
			case "database unavailable":
				db = nil
			case "query failed":
				if err := db.Migrator().DropTable(&providerModel.Port{}); err != nil {
					t.Fatal(err)
				}
			case "invalid mapping":
				if err := db.Create(&providerModel.Port{InstanceID: 1, HostPort: 65536, GuestPort: 22, IsSSH: true, Protocol: "tcp"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			updates := map[string]interface{}{}
			if err := applyFinalizedSSHPort(db, providerModel.Instance{ID: 1}, providerModel.Provider{NetworkType: "dedicated_ipv4"}, updates); err == nil {
				t.Fatal("expected error")
			}
			if len(updates) != 0 {
				t.Fatalf("query failure overwrote endpoint: %#v", updates)
			}
		})
	}
}

func TestProviderCreateSSHWaitTimeout(t *testing.T) {
	tests := []struct {
		name     string
		provider providerModel.Provider
		instance providerModel.Instance
		want     time.Duration
	}{
		{
			name:     "lxd vm post create ssh wait is nonblocking",
			provider: providerModel.Provider{Type: "lxd"},
			instance: providerModel.Instance{InstanceType: "vm"},
			want:     90 * time.Second,
		},
		{
			name:     "incus vm post create ssh wait is nonblocking",
			provider: providerModel.Provider{Type: "incus"},
			instance: providerModel.Instance{InstanceType: "vm"},
			want:     90 * time.Second,
		},
		{
			name:     "container wait remains short",
			provider: providerModel.Provider{Type: "lxd"},
			instance: providerModel.Instance{InstanceType: "container"},
			want:     30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerCreateSSHWaitTimeout(tt.provider, tt.instance); got != tt.want {
				t.Fatalf("providerCreateSSHWaitTimeout() = %s, want %s", got, tt.want)
			}
		})
	}
}
