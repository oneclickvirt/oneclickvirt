package provider

import (
	"reflect"
	"testing"
)

func TestExpandPortMappingFamiliesPreservesDefaultAndExplicitContracts(t *testing.T) {
	automatic := Port{ID: 7, HostPort: 22000, GuestPort: 22, PortCount: 1, IsAutomatic: true, PortType: "range_mapped", IPv6Enabled: true, MappingMethod: "iptables", MappingType: "node"}
	for _, tt := range []struct {
		name, network string
		port          Port
		families      []bool
		methods       []string
	}{
		{"automatic dual stack", "nat_ipv4_ipv6", automatic, []bool{false, true}, []string{"iptables", "device_proxy"}},
		{"IPv6 only", "ipv6_only", automatic, []bool{true}, []string{"iptables"}},
		{"manual dual-stack", "nat_ipv4_ipv6", Port{IPv6Enabled: true, PortType: "manual", MappingMethod: "iptables"}, []bool{false, true}, []string{"iptables", "device_proxy"}},
		{"manual dual-stack opt-in", "nat_ipv4_ipv6", Port{IPv6Enabled: true, PortType: "range_mapped", MappingMethod: "iptables"}, []bool{false, true}, []string{"iptables", "device_proxy"}},
		{"explicit IPv6 address", "nat_ipv4_ipv6", Port{IsAutomatic: true, PortType: "range_mapped", IPv6Address: "2001:db8::10"}, []bool{true}, []string{"device_proxy"}},
		{"manual IPv4", "nat_ipv4_ipv6", Port{PortType: "manual"}, []bool{false}, []string{"iptables"}},
		{"controller default", "nat_ipv4_ipv6", Port{MappingType: "controller", PortType: "range_mapped", IsAutomatic: true, IPv6Enabled: true}, []bool{false}, []string{""}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original := tt.port
			mappings := ExpandPortMappingFamilies(tt.port, tt.network, "iptables", "device_proxy")
			if len(mappings) != len(tt.families) {
				t.Fatalf("families = %#v", mappings)
			}
			for index, mapping := range mappings {
				if (mapping.IPv6Enabled || mapping.IPv6Address != "") != tt.families[index] || mapping.MappingMethod != tt.methods[index] {
					t.Fatalf("family %d = %#v", index, mapping)
				}
				if mapping.ID != original.ID || mapping.HostPort != original.HostPort || mapping.GuestPort != original.GuestPort {
					t.Fatalf("family expansion changed the port allocation: %#v", mapping)
				}
			}
			if !reflect.DeepEqual(tt.port, original) {
				t.Fatal("persisted row was modified")
			}
		})
	}
}
