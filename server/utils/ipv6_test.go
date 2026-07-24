package utils

import "testing"

func TestParseIPv6NetworkSupportsBareAndAllPrefixLengths(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		def    int
		prefix int
	}{
		{name: "bare address", value: "2001:db8::1", def: 128, prefix: 128},
		{name: "slash zero", value: "2001:db8::1/0", def: 64, prefix: 0},
		{name: "slash sixty four", value: "2001:db8:1:2::1/64", def: 128, prefix: 64},
		{name: "slash eighty", value: "2001:db8:1:2:3::1/80", def: 64, prefix: 80},
		{name: "slash ninety six", value: "2001:db8:1:2:3:4::1/96", def: 64, prefix: 96},
		{name: "slash one hundred twelve", value: "2001:db8:1:2:3:4:5:6/112", def: 64, prefix: 112},
		{name: "slash one hundred twenty eight", value: "2001:db8::1/128", def: 64, prefix: 128},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			network, err := ParseIPv6Network(test.value, test.def)
			if err != nil {
				t.Fatalf("ParseIPv6Network() error = %v", err)
			}
			if network.PrefixLen != test.prefix {
				t.Fatalf("prefix = %d, want %d", network.PrefixLen, test.prefix)
			}
		})
	}
}

func TestResolveContainerNetworkIsFailClosedForStaticIPv6(t *testing.T) {
	selection, err := ResolveContainerNetwork("nat_ipv4_ipv6", "2001:0db8::42/80", "ipv4-net", "ipv6-net", true)
	if err != nil {
		t.Fatalf("ResolveContainerNetwork() error = %v", err)
	}
	if !selection.IPv6 || selection.Network != "ipv6-net" || selection.StaticIPv6 != "2001:db8::42" {
		t.Fatalf("selection = %#v", selection)
	}

	for _, test := range []struct {
		name        string
		networkType string
		staticIPv6  string
		available   bool
	}{
		{name: "invalid address", networkType: "nat_ipv4_ipv6", staticIPv6: "bad", available: true},
		{name: "IPv4-only type", networkType: "nat_ipv4", staticIPv6: "2001:db8::42", available: true},
		{name: "unavailable IPv6 network", networkType: "nat_ipv4_ipv6", staticIPv6: "2001:db8::42"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ResolveContainerNetwork(test.networkType, test.staticIPv6, "ipv4-net", "ipv6-net", test.available); err == nil {
				t.Fatal("ResolveContainerNetwork() should reject an unusable static IPv6 assignment")
			}
		})
	}
}

func TestResolveContainerNetworkAllowsUnassignedIPv4Fallback(t *testing.T) {
	selection, err := ResolveContainerNetwork("nat_ipv4_ipv6", "", "ipv4-net", "ipv6-net", false)
	if err != nil {
		t.Fatalf("ResolveContainerNetwork() error = %v", err)
	}
	if selection.IPv6 || selection.Network != "ipv4-net" || selection.StaticIPv6 != "" {
		t.Fatalf("selection = %#v", selection)
	}
}

func TestExtractIPv6NetworksIgnoresMultilineDiagnostics(t *testing.T) {
	output := `尝试从路由器通告中获取真实的 IPv6 前缀...
Found real IPv6 prefix length from router advertisement: /64
64' is invalid, resetting to 64
address="[2001:db8:1234:5678:9abc:def0:1111:2222/80]"
Attempting to get real IPv6 prefix from router advertisement...
2001:db8:1234:5678:9abc:def0:1111:3333/80,2001:db8:1234:5678:9abc:def0:1111:4444/112`

	networks := ExtractIPv6Networks(output, 64)
	if len(networks) != 3 {
		t.Fatalf("found %d IPv6 values, want 3", len(networks))
	}
	if networks[0].PrefixLen != 80 || networks[1].PrefixLen != 80 || networks[2].PrefixLen != 112 {
		t.Fatalf("unexpected prefix lengths: %d, %d, %d", networks[0].PrefixLen, networks[1].PrefixLen, networks[2].PrefixLen)
	}
}

func TestExtractIPv6AddressesPreservesHostBits(t *testing.T) {
	output := "inet6 2001:db8:abcd:1::42/80 scope global\n" +
		"2001:db8:abcd:1::99 dev eth0 lladdr 00:11:22:33:44:55 REACHABLE\n" +
		"ip6 daddr 2001:db8:abcd:1::42 counter"
	got := ExtractIPv6Addresses(output)
	want := []string{"2001:db8:abcd:1::42", "2001:db8:abcd:1::99"}
	if len(got) != len(want) {
		t.Fatalf("ExtractIPv6Addresses() = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ExtractIPv6Addresses()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestIPv6AddressWithSuffixHonorsPrefixLength(t *testing.T) {
	tests := []struct {
		name string
		cidr string
		want string
	}{
		{name: "slash zero", cidr: "2001:db8:1:2:3:4:5:6/0", want: "::abcd"},
		{name: "slash sixty four", cidr: "2001:db8:1:2:3:4:5:6/64", want: "2001:db8:1:2::abcd"},
		{name: "non byte aligned", cidr: "ffff:ffff:ffff:ffff:8000::/65", want: "ffff:ffff:ffff:ffff:8000::abcd"},
		{name: "slash eighty", cidr: "2001:db8:1:2:3:4:5:6/80", want: "2001:db8:1:2:3::abcd"},
		{name: "slash ninety six", cidr: "2001:db8:1:2:3:4:5:6/96", want: "2001:db8:1:2:3:4:0:abcd"},
		{name: "slash one hundred twelve", cidr: "2001:db8:1:2:3:4:5:6/112", want: "2001:db8:1:2:3:4:5:abcd"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			network, err := ParseIPv6Network(test.cidr, 64)
			if err != nil {
				t.Fatalf("ParseIPv6Network() error = %v", err)
			}
			address, err := IPv6AddressWithSuffix(network, 0xabcd)
			if err != nil {
				t.Fatalf("IPv6AddressWithSuffix() error = %v", err)
			}
			if address != test.want {
				t.Fatalf("address = %s, want %s", address, test.want)
			}
		})
	}

	network, err := ParseIPv6Network("2001:db8::1/128", 64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := IPv6AddressWithSuffix(network, 1); err == nil {
		t.Fatal("expected /128 allocation with non-zero suffix to fail")
	}
	address, err := IPv6AddressWithSuffix(network, 0)
	if err != nil || address != "2001:db8::1" {
		t.Fatalf("/128 zero suffix = %q, %v", address, err)
	}
}

func TestFirstAvailableIPv6UsesSnapshotAndStopsOnSmallPrefix(t *testing.T) {
	network, err := ParseIPv6Network("2001:db8::/126", 64)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FirstAvailableIPv6(network, []string{"2001:db8::3"}, 3, 65535)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2001:db8::" {
		t.Fatalf("FirstAvailableIPv6() = %q, want %q", got, "2001:db8::")
	}

	_, err = FirstAvailableIPv6(network, []string{"2001:db8::", "2001:db8::1", "2001:db8::2", "2001:db8::3"}, 3, 65535)
	if err == nil {
		t.Fatal("FirstAvailableIPv6() should report exhausted /126")
	}
}

func TestParseHexUint64RejectsPollutedOutput(t *testing.T) {
	value, err := ParseHexUint64("0x0aBc")
	if err != nil || value != 0x0abc {
		t.Fatalf("ParseHexUint64() = %#x, %v", value, err)
	}
	if _, err := ParseHexUint64("Attempting...\n0abc"); err == nil {
		t.Fatal("expected multiline diagnostic output to be rejected")
	}
	for _, output := range []string{"'0abc'", "[0abc]", "0abc trailing", "0abc\nwarning"} {
		if _, err := ParseHexUint64(output); err == nil {
			t.Fatalf("ParseHexUint64(%q) accepted polluted output", output)
		}
	}
}

func TestParseSingleIPv6NetworkOutputRejectsPollution(t *testing.T) {
	network, err := ParseSingleIPv6NetworkOutput(" 2001:db8:1::42/80\n", 64)
	if err != nil {
		t.Fatalf("ParseSingleIPv6NetworkOutput() error = %v", err)
	}
	if network.Address.String() != "2001:db8:1::42" || network.PrefixLen != 80 {
		t.Fatalf("network = %#v", network)
	}

	for _, output := range []string{
		"warning: fallback route\n2001:db8::1/64",
		"2001:db8::1/64\n2001:db8::2/64",
		"address=2001:db8::1/64",
		"[2001:db8::1/64]",
		"2001:db8::1/64,",
	} {
		if _, err := ParseSingleIPv6NetworkOutput(output, 64); err == nil {
			t.Fatalf("ParseSingleIPv6NetworkOutput(%q) accepted polluted output", output)
		}
	}
}

func TestParseSingleIPv6AddressOutputRequiresBareAddress(t *testing.T) {
	address, err := ParseSingleIPv6AddressOutput("2001:0db8::1\r\n")
	if err != nil || address != "2001:db8::1" {
		t.Fatalf("ParseSingleIPv6AddressOutput() = %q, %v", address, err)
	}
	for _, output := range []string{
		"2001:db8::1/64",
		"gateway:2001:db8::1",
		"2001:db8::1 ok",
		"diagnostic\n2001:db8::1",
	} {
		if _, err := ParseSingleIPv6AddressOutput(output); err == nil {
			t.Fatalf("ParseSingleIPv6AddressOutput(%q) accepted polluted output", output)
		}
	}
}

func TestParseSingleIPv4AddressOutputRejectsPollution(t *testing.T) {
	address, err := ParseSingleIPv4AddressOutput("192.0.2.10\r\n")
	if err != nil || address != "192.0.2.10" {
		t.Fatalf("ParseSingleIPv4AddressOutput() = %q, %v", address, err)
	}
	for _, output := range []string{
		"192.0.2.10/24",
		"address=192.0.2.10",
		"192.0.2.10 192.0.2.11",
		"warning\n192.0.2.10",
		"999.0.2.10",
		"2001:db8::1",
	} {
		if _, err := ParseSingleIPv4AddressOutput(output); err == nil {
			t.Fatalf("ParseSingleIPv4AddressOutput(%q) accepted polluted output", output)
		}
	}
}

func TestParseIPv6LinesRejectsAnyDiagnosticLine(t *testing.T) {
	networks, err := ParseIPv6NetworkLines("fe80::1/64\nfe80::2/64\n", 128)
	if err != nil || len(networks) != 2 {
		t.Fatalf("ParseIPv6NetworkLines() = %#v, %v", networks, err)
	}
	if _, err := ParseIPv6NetworkLines("warning: route changed\nfe80::1/64", 128); err == nil {
		t.Fatal("ParseIPv6NetworkLines() accepted a diagnostic line")
	}

	addresses, err := ParseIPv6AddressLines("2001:db8::1\n2001:db8::2\n")
	if err != nil || len(addresses) != 2 {
		t.Fatalf("ParseIPv6AddressLines() = %#v, %v", addresses, err)
	}
	if _, err := ParseIPv6AddressLines("2001:db8::1\nfailed to read next address"); err == nil {
		t.Fatal("ParseIPv6AddressLines() accepted a diagnostic line")
	}
}

func TestStrictCommandMetadataParsersRejectPollution(t *testing.T) {
	if prefix, err := ParseIPv6PrefixLengthOutput("80\n"); err != nil || prefix != 80 {
		t.Fatalf("ParseIPv6PrefixLengthOutput() = %d, %v", prefix, err)
	}
	for _, output := range []string{"64' is invalid", "64\nwarning", "+64", "129"} {
		if _, err := ParseIPv6PrefixLengthOutput(output); err == nil {
			t.Fatalf("ParseIPv6PrefixLengthOutput(%q) accepted polluted output", output)
		}
	}

	if name, err := ParseNetworkInterfaceOutput("enp1s0.100\n"); err != nil || name != "enp1s0.100" {
		t.Fatalf("ParseNetworkInterfaceOutput() = %q, %v", name, err)
	}
	for _, output := range []string{
		"warning\neth0",
		"eth0 extra",
		"eth0;reboot",
		"interface=eth0",
		"-eth0",
		"..",
		"this-interface-name-is-too-long",
	} {
		if _, err := ParseNetworkInterfaceOutput(output); err == nil {
			t.Fatalf("ParseNetworkInterfaceOutput(%q) accepted polluted output", output)
		}
	}
}
