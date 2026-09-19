package utils

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNATProxyListenResolutionHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveNATProxyListenIP(ctx, false, "198.51.100.10"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolution returned %v", err)
	}
}

func TestLXCProxyConnectPreservesWildcardSelectionWithoutHidingConflicts(t *testing.T) {
	for _, tt := range []struct {
		existing, expected string
		want               bool
	}{
		{"tcp:0.0.0.0:22", "tcp:192.0.2.10:22", true},
		{"udp:[::]:8000-8009", "udp:[2001:db8::10]:8000-8009", true},
		{"tcp:192.0.2.99:22", "tcp:192.0.2.10:22", false},
		{"udp:0.0.0.0:22", "tcp:192.0.2.10:22", false},
		{"tcp:0.0.0.0:23", "tcp:192.0.2.10:22", false},
		{"tcp:[::]:22", "tcp:192.0.2.10:22", false},
		{"tcp:0.0.0.0:8000-8008", "tcp:192.0.2.10:8000-8009", false},
		{"unix:/socket", "tcp:192.0.2.10:22", false},
	} {
		if got := LXCProxyConnectMatches(tt.existing, tt.expected); got != tt.want {
			t.Errorf("LXCProxyConnectMatches(%q, %q) = %v", tt.existing, tt.expected, got)
		}
	}
}

func TestNATProxyListenAddressesMatchFamilyAndRejectWildcards(t *testing.T) {
	for _, tt := range []struct {
		name       string
		ipv6       bool
		candidates []string
		want       string
	}{
		{"port IP preferred", false, []string{"198.51.100.10", "192.0.2.2"}, "198.51.100.10"},
		{"IPv6 alongside IPv4 port IP", true, []string{"198.51.100.10", "https://[2001:db8::1]:8443"}, "2001:db8::1"},
		{"address with API port", false, []string{"192.0.2.2:8443"}, "192.0.2.2"},
		{"private routed host", false, []string{"10.0.0.2"}, "10.0.0.2"},
		{"IPv4 wildcard", false, []string{"0.0.0.0"}, ""},
		{"IPv6 wildcard", true, []string{"[::]:8443"}, ""},
		{"wrong family", true, []string{"192.0.2.2"}, ""},
		{"link local", true, []string{"fe80::1"}, ""},
		{"loopback", false, []string{"127.0.0.1"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveNATProxyListenIP(context.Background(), tt.ipv6, tt.candidates...)
			if got != tt.want || (err != nil) != (tt.want == "") {
				t.Fatalf("ResolveNATProxyListenIP() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func proxyNICFixture() (map[string]interface{}, map[string]interface{}) {
	nic := map[string]interface{}{"type": "nic", "network": "custom", "name": "eth0", "mtu": "1400", "limits.ingress": "10Mbit"}
	metadata := map[string]interface{}{
		"expanded_devices": map[string]interface{}{"uplink": nic},
		"expanded_config":  map[string]interface{}{"volatile.uplink.hwaddr": "00:16:3e:11:22:33"},
		"_network_state": map[string]interface{}{
			"network": map[string]interface{}{
				"enp5s0": map[string]interface{}{
					"hwaddr": "00:16:3e:11:22:33",
					"addresses": []interface{}{
						map[string]interface{}{"address": "192.0.2.10", "family": "inet", "scope": "global"},
						map[string]interface{}{"address": "2001:db8::10", "family": "inet6", "scope": "global"},
					},
				},
			},
		},
	}
	devices := map[string]interface{}{
		"root": map[string]interface{}{"type": "disk", "pool": "local", "path": "/"},
		"ssh":  map[string]interface{}{"type": "proxy", "nat": "true", "listen": "tcp:198.51.100.10:22000", "connect": "tcp:192.0.2.10:22"},
	}
	return metadata, devices
}

func TestLXCProxyPinsProfileNICForRenamedVMInterface(t *testing.T) {
	metadata, devices := proxyNICFixture()
	profileNIC := lxcDeviceMap(metadata["expanded_devices"].(map[string]interface{})["uplink"])
	root := devices["root"]
	changed, err := BindLXCProxyNICs(metadata, devices)
	if err != nil || !changed {
		t.Fatalf("BindLXCProxyNICs() = %v, %v", changed, err)
	}
	nic := lxcDeviceMap(devices["uplink"])
	if nic["ipv4.address"] != "192.0.2.10" || nic["network"] != "custom" || nic["mtu"] != "1400" || nic["limits.ingress"] != "10Mbit" {
		t.Fatalf("NIC reservation lost profile settings: %#v", nic)
	}
	if !reflect.DeepEqual(devices["root"], root) || !reflect.DeepEqual(metadata["expanded_devices"].(map[string]interface{})["uplink"], profileNIC) {
		t.Fatal("profile or root disk was modified")
	}
	changed, err = BindLXCProxyNICs(metadata, devices)
	if err != nil || changed {
		t.Fatalf("repeat binding should preserve the existing reservation: %v, %v", changed, err)
	}
}

func TestLXCProxyPinsOnlyAddressFamiliesUsedByMappings(t *testing.T) {
	metadata, devices := proxyNICFixture()
	if _, err := BindLXCProxyNICs(metadata, devices); err != nil {
		t.Fatal(err)
	}
	if lxcDeviceMap(devices["uplink"])["ipv6.address"] != nil {
		t.Fatal("IPv4 mapping changed unused IPv6 configuration")
	}
	devices["v6"] = map[string]string{"type": "proxy", "nat": "true", "listen": "udp:[2001:db8::1]:22000-22009", "connect": "udp:[2001:db8::10]:8000-8009"}
	if changed, err := BindLXCProxyNICs(metadata, devices); err != nil || !changed {
		t.Fatalf("IPv6 reservation failed: %v, %v", changed, err)
	}
	if lxcDeviceMap(devices["uplink"])["ipv6.address"] != "2001:db8::10" {
		t.Fatal("IPv6 NAT target was not reserved")
	}
}

func TestLXCProxyPreservesRoutedNICAndWildcardConnect(t *testing.T) {
	metadata, devices := proxyNICFixture()
	devices["routed"] = map[string]string{"type": "nic", "nictype": "routed", "parent": "wan0", "ipv6.address": "2001:db8:1::10"}
	devices["v6"] = map[string]string{"type": "proxy", "nat": "true", "listen": "udp:[2001:db8::1]:22000", "connect": "udp:[2001:db8:1::10]:8000"}
	devices["ssh"] = map[string]string{"type": "proxy", "nat": "true", "listen": "tcp:198.51.100.10:22000", "connect": "tcp:0.0.0.0:22"}
	if changed, err := BindLXCProxyNICs(metadata, devices); err != nil || changed {
		t.Fatalf("existing routed and auto-selected connect targets must be preserved: %v, %v", changed, err)
	}
}

func TestLXCProxyRefusesAmbiguousOrConflictingNICs(t *testing.T) {
	for _, scenario := range []string{"existing address", "disabled family", "unknown interface", "ambiguous", "macvlan"} {
		t.Run(scenario, func(t *testing.T) {
			metadata, devices := proxyNICFixture()
			expanded := metadata["expanded_devices"].(map[string]interface{})
			nic := expanded["uplink"].(map[string]interface{})
			switch scenario {
			case "existing address":
				nic["ipv4.address"] = "192.0.2.99"
			case "disabled family":
				nic["ipv4.address"] = "none"
			case "unknown interface":
				delete(metadata, "_network_state")
			case "ambiguous":
				other := lxcDeviceMap(nic)
				other["hwaddr"] = "00:16:3e:11:22:33"
				expanded["other"] = other
			case "macvlan":
				delete(nic, "network")
				nic["nictype"] = "macvlan"
				nic["parent"] = "wan0"
			}
			if _, err := BindLXCProxyNICs(metadata, devices); err == nil {
				t.Fatal("unsafe NIC reassignment was accepted")
			}
			if len(devices) != 2 {
				t.Fatal("failed binding modified instance devices")
			}
		})
	}
}

func TestLXCProxyRejectsMalformedConnectBeforeNICMutation(t *testing.T) {
	metadata, devices := proxyNICFixture()
	devices["ssh"].(map[string]interface{})["connect"] = "tcp:not-an-address"
	if _, err := BindLXCProxyNICs(metadata, devices); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("invalid connect accepted: %v", err)
	}
}
