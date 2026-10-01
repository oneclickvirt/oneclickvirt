package utils

import "testing"

func TestParseFirstGlobalIPv6AddressJSONHandlesColorAndLocalizedNoise(t *testing.T) {
	output := "\x1b[32m" + `[{"ifname":"eth0","addr_info":[{"family":"inet6","local":"fe80::1","scope":"link"},{"family":"inet6","local":"fd42:5339:296f:1f00::64","scope":"global","prefixlen":64,"flags":["tentative"]},{"family":"inet6","local":"fd42:5339:296f:1f00::65","scope":"global","prefixlen":64}]}]` + "\x1b[0m"
	address, err := ParseFirstGlobalIPv6AddressJSON(output)
	if err != nil || address != "fd42:5339:296f:1f00::65" {
		t.Fatalf("ParseFirstGlobalIPv6AddressJSON() = %q, %v", address, err)
	}
	if _, err := ParseFirstGlobalIPv6AddressJSON("Adresse IPv6 ungültig"); err == nil {
		t.Fatal("localized diagnostic was accepted as address JSON")
	}
}

func TestSelectPublicIPv6InterfaceNetworkJSONPreservesHostAndVariablePrefixes(t *testing.T) {
	routes := `[{"dst":"default","dev":"vmbr0"}]`
	addresses := "\x1b[32m" + `[{"ifname":"vmbr0","addr_info":[{"family":"inet6","local":"2a14:7c0:1002:10f8::1","prefixlen":128,"scope":"global"}]},{"ifname":"vmbr2","addr_info":[{"family":"inet6","local":"2a14:7c0:1002:10f8::1","prefixlen":38,"scope":"global"}]}]` + "\x1b[0m"
	selected, err := SelectPublicIPv6InterfaceNetworkJSON(addresses, routes, true)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Interface != "vmbr2" || selected.Network.PrefixLen != 38 || selected.Network.Address.String() != "2a14:7c0:1002:10f8::1" {
		t.Fatalf("selected = %#v", selected)
	}
	if _, err := SelectPublicIPv6InterfaceNetworkJSON(`[{"ifname":"vmbr0","addr_info":[{"family":"inet6","local":"2a14:7c0:1002:10f8::1","prefixlen":128,"scope":"global"}]}]`, routes, true); err == nil {
		t.Fatal("host-only /128 was offered as an allocation pool")
	}
	for _, prefix := range []string{"120", "127"} {
		output := `[{"ifname":"eth0","addr_info":[{"family":"inet6","local":"2a01:4f8::1","prefixlen":` + prefix + `,"scope":"global"}]}]`
		selected, err = SelectPublicIPv6InterfaceNetworkJSON(output, `[]`, true)
		if err != nil || selected.Network.CIDR() == "" {
			t.Fatalf("/%s selection = %#v, %v", prefix, selected, err)
		}
	}
}

func TestSelectPublicIPv6InterfaceNetworkJSONRejectsLocalizedDiagnosticsAndTentative(t *testing.T) {
	for _, output := range []string{"Adresse ungültig", "adresse IPv6 invalide", "IPv6 地址无效"} {
		if _, err := SelectPublicIPv6InterfaceNetworkJSON(output, `[]`, false); err == nil {
			t.Fatalf("accepted localized diagnostic %q as address JSON", output)
		}
	}
	addresses := `[{"ifname":"eth0","addr_info":[{"family":"inet6","local":"2a01:4f8::1","prefixlen":64,"scope":"global","flags":["tentative"]}]}]`
	if _, err := SelectPublicIPv6InterfaceNetworkJSON(addresses, `[]`, false); err == nil {
		t.Fatal("tentative IPv6 address was selected")
	}
}

func TestSelectPublicIPv6InterfaceNetworkJSONUsesMultipathDefaultRoute(t *testing.T) {
	addresses := `[{"ifname":"uplink-a","addr_info":[{"family":"inet6","local":"2a01:4f8::1","prefixlen":64,"scope":"global"}]},{"ifname":"uplink-b","addr_info":[{"family":"inet6","local":"2a01:4f8:1::1","prefixlen":64,"scope":"global"}]}]`
	routes := `[{"dst":"default","multipath":[{"dev":"uplink-b","gateway":"fe80::1"},{"dev":"uplink-a","gateway":"fe80::2"}]}]`
	selected, err := SelectPublicIPv6InterfaceNetworkJSON(addresses, routes, true)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Interface != "uplink-b" {
		t.Fatalf("selected interface = %q, want first multipath uplink", selected.Interface)
	}
}

func TestSelectPublicIPv6InterfaceNetworkJSONAcceptsOmittedDefaultRouteDestination(t *testing.T) {
	addresses := `[{"ifname":"eth0","addr_info":[{"family":"inet6","local":"2a01:4f8::1","prefixlen":64,"scope":"global"}]}]`
	selected, err := SelectPublicIPv6InterfaceNetworkJSON(addresses, `[{"dev":"eth0"}]`, true)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Interface != "eth0" {
		t.Fatalf("selected interface = %q, want eth0", selected.Interface)
	}
}
