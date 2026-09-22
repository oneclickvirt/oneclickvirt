package provider

import "testing"

func TestNormalizeRequestedPortMappingMethod(t *testing.T) {
	for _, test := range []struct {
		value   string
		want    string
		wantErr bool
	}{
		{value: "", want: ""},
		{value: " Device_Proxy ", want: "device_proxy"},
		{value: "IPTABLES", want: "iptables"},
		{value: " native ", want: "native"},
		{value: "proxy", wantErr: true},
	} {
		got, err := normalizeRequestedPortMappingMethod(test.value, "IPv6")
		if (err != nil) != test.wantErr || got != test.want {
			t.Fatalf("normalizeRequestedPortMappingMethod(%q) = (%q, %v), want (%q, error=%t)", test.value, got, err, test.want, test.wantErr)
		}
	}
}

func TestNormalizeAgentNetworkTypePreservesExplicitControllerMapping(t *testing.T) {
	if got := normalizeAgentNetworkType(""); got != "no_port_mapping" {
		t.Fatalf("empty Agent network type = %q, want no_port_mapping", got)
	}
	if got := normalizeAgentNetworkType("  nat_ipv4  "); got != "nat_ipv4" {
		t.Fatalf("explicit Agent NAT network type = %q, want nat_ipv4", got)
	}
	if got := normalizeAgentNetworkType("no_port_mapping"); got != "no_port_mapping" {
		t.Fatalf("explicit Agent no-port mode = %q, want no_port_mapping", got)
	}
}
