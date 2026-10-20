package utils

import "testing"

func TestNormalizePVEPortMappingMethods(t *testing.T) {
	for _, mode := range []string{"nat_ipv4", "nat_ipv4_ipv6", "dedicated_ipv4", "dedicated_ipv4_ipv6", "ipv6_only"} {
		v4, v6 := NormalizeProviderPortMappingMethods("proxmox", mode, "device_proxy", "device_proxy")
		want4 := "native"
		if mode == "nat_ipv4" || mode == "nat_ipv4_ipv6" {
			want4 = "iptables"
		}
		if v4 != want4 || v6 != "native" {
			t.Fatalf("%s: got %s/%s", mode, v4, v6)
		}
	}
	if v4, v6 := NormalizeProviderPortMappingMethods("proxmox", "nat_ipv4_ipv6", "native", "iptables"); v4 != "iptables" || v6 != "iptables" {
		t.Fatal("explicit IPv6 NAT choice lost")
	}
	if v4, v6 := NormalizeProviderPortMappingMethods("incus", "nat_ipv4_ipv6", "device_proxy", "device_proxy"); v4 != "device_proxy" || v6 != "device_proxy" {
		t.Fatal("Incus proxy mode changed")
	}
}
