package proxmox

import (
	"context"
	"errors"
	"testing"

	"oneclickvirt/utils"
)

func TestNativePoolProbeUsesGuestPrefixAndPreservesHostReservations(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.bridgeDedicatedV6 = "vmbr2"
	addresses := `[{"ifname":"vmbr0","addr_info":[{"family":"inet6","scope":"global","local":"2a14:7c0:1002:10f8::2","prefixlen":64}]},{"ifname":"vmbr2","addr_info":[{"family":"inet6","scope":"global","local":"2a14:7c0:1002:10f8:100::1","prefixlen":72}]}]`
	state := "2a14:7c0:1002:10f8:100::/72\t2a14:7c0:1002:10f8:100::1\tndp\tfe80::1\tvmbr2\tbridge"
	p.sshClient.SetExecutor(directIPv6Executor(addresses, state))
	network, err := p.GetNativeIPv6PoolNetwork(context.Background())
	if err != nil || network.CIDR != "2a14:7c0:1002:10f8:100::/72" || network.Bridge != "vmbr2" {
		t.Fatalf("network = %#v, %v", network, err)
	}
	for _, address := range []string{network.Gateway, "2a14:7c0:1002:10f8::2", "fe80::1"} {
		if !utils.HostIPv6AddressReserved(address, network.Reserved) {
			t.Fatalf("reservation missing: %s", address)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.GetNativeIPv6PoolNetwork(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
