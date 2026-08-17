package qemu

import (
	"strings"
	"testing"

	"oneclickvirt/provider"
)

func routedQEMUConfig(networkType string) provider.InstanceConfig {
	return provider.InstanceConfig{Metadata: map[string]string{
		"network_type":        networkType,
		"static_ipv6":         "2001:db8::2",
		"static_ipv6_cidr":    "2001:db8::/126",
		"static_ipv6_gateway": "2001:db8::1",
		"static_ipv6_bridge":  "oneclickvirt6",
	}}
}

func TestResolveQEMUIPv6PlanKeepsNATIPv4AndRoutedIPv6(t *testing.T) {
	plan, err := resolveQEMUIPv6Plan(routedQEMUConfig("nat_ipv4_ipv6"))
	if err != nil {
		t.Fatalf("resolveQEMUIPv6Plan() error = %v", err)
	}
	if plan.Routed == nil || plan.IPv6Only {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	args := qemuVirtInstallNetworkArgs(plan, "52:54:00:00:00:01", "52:54:00:00:00:02")
	if !strings.Contains(args, "network=default") || !strings.Contains(args, "bridge=oneclickvirt6") {
		t.Fatalf("network args must contain both interfaces: %s", args)
	}
	data := qemuCloudInitNetworkData(plan, "52:54:00:00:00:01", "52:54:00:00:00:02")
	for _, want := range []string{"dhcp4: true", "2001:db8::2/126", "via: \"2001:db8::1\"", "on-link: true"} {
		if !strings.Contains(data, want) {
			t.Fatalf("cloud-init network data missing %q:\n%s", want, data)
		}
	}
}

func TestResolveQEMUIPv6PlanFailsClosedForBareStaticAddress(t *testing.T) {
	config := provider.InstanceConfig{Metadata: map[string]string{
		"network_type": "nat_ipv4_ipv6",
		"static_ipv6":  "2001:db8::2",
	}}
	if _, err := resolveQEMUIPv6Plan(config); err == nil {
		t.Fatal("bare static IPv6 address should be rejected")
	}
}

func TestResolveQEMUIPv6PlanFailsClosedWithoutAllocatedAddress(t *testing.T) {
	config := provider.InstanceConfig{Metadata: map[string]string{"network_type": "nat_ipv4_ipv6"}}
	if _, err := resolveQEMUIPv6Plan(config); err == nil {
		t.Fatal("QEMU dual-stack request without a routed allocation should be rejected")
	}
}
