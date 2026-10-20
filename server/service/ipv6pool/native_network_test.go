package ipv6pool

import (
	"context"
	"errors"
	"strings"
	"testing"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
)

func nativePoolFixture(t *testing.T) (*Service, *provider.NativeIPv6PoolNetwork, *int) {
	t.Helper()
	db := setupIPv6PoolTestDB(t)
	if err := db.Exec("UPDATE providers SET type = 'proxmox' WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	network := &provider.NativeIPv6PoolNetwork{CIDR: "2001:db8:1:2:100::/72", Gateway: "2001:db8:1:2:100::1", Bridge: "vmbr2", Reserved: []string{"2001:db8:1:2:100::1", "2001:db8:1:2:100::2"}}
	calls := new(int)
	s := NewServiceWithDB(db)
	s.readPoolNetwork = func(ctx context.Context, id uint) (*provider.NativeIPv6PoolNetwork, error) {
		*calls++
		// MaxOpenConns=1: this query would deadlock if the probe held a transaction.
		var count int64
		if err := db.Model(&providerModel.Provider{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return network, nil
	}
	return s, network, calls
}

func TestPVEPoolRejectsOutsidePrefixAndReservedAddressesBeforeSaving(t *testing.T) {
	s, _, calls := nativePoolFixture(t)
	for _, input := range []string{"2001:db8:1:2::/64", "2001:db8:1:2:200::5", "2001:db8:1:2:100::", "2001:db8:1:2:100::1", "2001:db8:1:2:100::2"} {
		if _, _, err := s.SetIPv6Pool(1, input); err == nil || !strings.Contains(err.Error(), "vmbr2") {
			t.Fatalf("%s: %v", input, err)
		}
	}
	if *calls != 5 {
		t.Fatalf("probes = %d", *calls)
	}
	var count int64
	s.database().Model(&providerModel.ProviderIPv6Pool{}).Count(&count)
	if count != 0 {
		t.Fatal("invalid pool partially saved")
	}
}

func TestPVEPoolRangeSkipsReservationsAndReusesReleasedAddress(t *testing.T) {
	s, _, calls := nativePoolFixture(t)
	if _, _, err := s.SetIPv6Pool(1, "2001:db8:1:2:100::/120"); err != nil {
		t.Fatal(err)
	}
	address, err := s.AllocateIPv6Address(1, 1)
	if err != nil || address != "2001:db8:1:2:100::3" {
		t.Fatalf("allocated %s, %v", address, err)
	}
	if *calls != 2 {
		t.Fatalf("probe must run once per operation, got %d", *calls)
	}
	if again, err := s.AllocateIPv6Address(1, 1); err != nil || again != address {
		t.Fatalf("retry: %s, %v", again, err)
	}
	if err := s.ReleaseIPv6(1); err != nil {
		t.Fatal(err)
	}
	if reused, err := s.AllocateIPv6Address(1, 2); err != nil || reused != address {
		t.Fatalf("reuse: %s, %v", reused, err)
	}
}

func TestPVEPoolNodeFileAndLegacyRowsCannotBypassLiveTopology(t *testing.T) {
	s, network, _ := nativePoolFixture(t)
	s.readNodeFile = func(context.Context, uint, string) (string, error) { return "2001:db8:1:2::/64\n", nil }
	if _, err := s.SyncProviderFile(context.Background(), 1); err == nil {
		t.Fatal("node file bypassed guest prefix")
	}
	if _, _, err := s.SetIPv6Pool(1, "2001:db8:1:2:100::5"); err != nil {
		t.Fatal(err)
	}
	network.CIDR, network.Gateway = "2001:db8:1:2:200::/72", "2001:db8:1:2:200::1"
	if _, err := s.AllocateIPv6Address(1, 3); err == nil {
		t.Fatal("stale pool after bridge change was allocated")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.SetIPv6PoolContext(ctx, 1, "2001:db8:1:2:200::5"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
