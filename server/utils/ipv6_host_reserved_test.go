package utils

import "testing"

func TestParseHostIPv6ReservationsKeepsHostAndGateway(t *testing.T) {
	addresses := "\x1b[32m" + `[{"ifname":"eth0","addr_info":[{"family":"inet6","local":"2a01:4f8:c014:1a63::1","prefixlen":64},{}]}]` + "\x1b[0m"
	routes := "\x1b[33m" + `[{"dst":"2a01:4f8:c014:1a63::/64","dev":"eth0"},{"dst":"default","gateway":"fe80::1","dev":"eth0","multipath":[{"gateway":"fe80::2","dev":"eth1"}],"nexthops":[{"gateway":"2001:db8::123","dev":"eth2"}]},{"type":"local","dst":"2a01:4f8:c014:1a63::1","dev":"eth0"},{"type":"multicast","dst":"multicast","dev":"eth0"},{"dst":"2a01:4f8:c014:1a63::4/128","dev":"veth0"}]` + "\x1b[0m"
	reserved, err := ParseHostIPv6Reservations(addresses, routes)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"2a01:4f8:c014:1a63::1", "fe80::1", "fe80::2", "2001:db8::123", "2a01:4f8:c014:1a63::4"} {
		if !HostIPv6AddressReserved(address, reserved) {
			t.Fatalf("host-owned address %s was available", address)
		}
	}
	if HostIPv6AddressReserved("2a01:4f8:c014:1a63::5", reserved) {
		t.Fatal("free address was reserved merely because it is inside the host /64")
	}
	for _, test := range []struct {
		prefix  string
		address string
		gateway string
		want    string
	}{
		{prefix: "2a01:4f8::/38", address: "2a01:4f8::1", gateway: "2a01:4f8::2", want: "2a01:4f8::3"},
		{prefix: "2a01:4f8::/120", address: "2a01:4f8::1", gateway: "2a01:4f8::2", want: "2a01:4f8::3"},
	} {
		network, err := ParseIPv6Network(test.prefix, 64)
		if err != nil {
			t.Fatal(err)
		}
		available, err := FirstAvailableIPv6(network, []string{test.address, test.gateway}, 1, 256)
		if err != nil || available != test.want {
			t.Fatalf("FirstAvailableIPv6(%s) = %q, %v, want %q", test.prefix, available, err, test.want)
		}
	}
	if _, err := ParseHostIPv6Reservations("Adresse ungültig", routes); err == nil {
		t.Fatal("localized diagnostic text was accepted as machine-readable addresses")
	}
}
