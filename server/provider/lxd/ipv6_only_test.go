package lxd

import (
	"strings"
	"testing"
)

func TestLXDIPv6OnlyIsolationMasksInheritedIPv4NIC(t *testing.T) {
	command := lxdIPv6OnlyIsolationCommand("guest", NetworkConfig{InSpeed: 120, OutSpeed: 80})
	for _, fragment := range []string{
		"eth1 type", "eth1 nictype", "eth0 none", "device remove \"$name\" eth0",
		"eth1 limits.egress '80Mbit'", "eth1 limits.ingress '120Mbit'", "eth1 limits.max '120Mbit'",
	} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("isolation command missing %q: %s", fragment, command)
		}
	}
	if strings.Contains(command, "eth0 limits.") || strings.Contains(command, "ipv4.address") {
		t.Fatalf("isolation command retained IPv4 configuration: %s", command)
	}
}

func TestLXDIPv6OnlyDNSUsesOnlyIPv6Resolvers(t *testing.T) {
	command := lxdIPv6OnlyDNSCommand("guest")
	for _, fragment := range []string{"2606:4700:4700::1111", "2001:4860:4860::8888", "mktemp", "mv -f"} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("DNS command missing %q: %s", fragment, command)
		}
	}
	if strings.Contains(command, "8.8.8.8") || strings.Contains(command, "1.1.1.1") {
		t.Fatalf("DNS command contains an IPv4 resolver: %s", command)
	}
}
