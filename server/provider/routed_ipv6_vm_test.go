package provider

import (
	"strings"
	"testing"
)

func routedVMConfig(networkType string) InstanceConfig {
	return InstanceConfig{Metadata: map[string]string{
		"network_type":          networkType,
		"static_ipv6":           "2001:db8::2",
		"static_ipv6_cidr":      "2001:db8::/126",
		"static_ipv6_gateway":   "2001:db8::1",
		"static_ipv6_bridge":    "oneclickvirt6",
		"static_ipv6_tunnel_id": "17",
	}}
}

func TestResolveRoutedIPv6VMPlanKeepsNATIPv4AndAddsRoutedNIC(t *testing.T) {
	plan, err := ResolveRoutedIPv6VMPlan(routedVMConfig("nat_ipv4_ipv6"), "virtualbox")
	if err != nil {
		t.Fatalf("ResolveRoutedIPv6VMPlan() error = %v", err)
	}
	if plan.Routed == nil || plan.IPv6Only || !strings.HasPrefix(plan.MAC, "02:") {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	networkData, err := plan.NoCloudNetworkData()
	if err != nil {
		t.Fatalf("NoCloudNetworkData() error = %v", err)
	}
	for _, want := range []string{"oneclickvirt6", plan.MAC, "2001:db8::2/126", "via: \"2001:db8::1\"", "on-link: true"} {
		if !strings.Contains(networkData, want) {
			t.Fatalf("network data missing %q:\n%s", want, networkData)
		}
	}
	command, err := plan.NoCloudISOCommand("/var/lib/oneclickvirt/seed.iso", "vm-a", "Passw0rd!")
	if err != nil {
		t.Fatalf("NoCloudISOCommand() error = %v", err)
	}
	for _, want := range []string{"cloud-localds", "genisoimage", "network-config", "/var/lib/oneclickvirt/seed.iso"} {
		if !strings.Contains(command, want) {
			t.Fatalf("seed command missing %q:\n%s", want, command)
		}
	}
	if strings.Contains(command, "Passw0rd!") {
		t.Fatalf("seed command should not expose the raw password: %s", command)
	}
}

func TestResolveRoutedIPv6VMPlanRejectsUnallocatedIPv6(t *testing.T) {
	config := InstanceConfig{Metadata: map[string]string{"network_type": "nat_ipv4_ipv6"}}
	if _, err := ResolveRoutedIPv6VMPlan(config, "virtualbox"); err == nil {
		t.Fatal("dual-stack VM request without an allocation should be rejected")
	}
}

func TestResolveRoutedIPv6VMPlanRejectsUnsupportedIPv6OnlyBackends(t *testing.T) {
	for _, backend := range []string{"multipass", "vagrant"} {
		if _, err := ResolveRoutedIPv6VMPlan(routedVMConfig("ipv6_only"), backend); err == nil {
			t.Fatalf("%s ipv6_only request should be rejected", backend)
		}
	}
}

func TestRoutedIPv6VMGuestMACUsesVMwareRange(t *testing.T) {
	mac := RoutedIPv6VMGuestMAC("vmware", "2001:db8::2")
	if !strings.HasPrefix(mac, "00:50:56:") {
		t.Fatalf("VMware MAC = %q", mac)
	}
	if RoutedIPv6VMSeedFileName("vmware", "vm-a") != RoutedIPv6VMSeedFileName("vmware", "vm-a") {
		t.Fatal("seed filename must be deterministic")
	}
}
