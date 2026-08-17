package provider

import (
	"strings"
	"testing"

	"oneclickvirt/utils"
)

func TestResolveRoutedIPv6ValidatesTunnelMetadata(t *testing.T) {
	config := InstanceConfig{Metadata: map[string]string{
		"static_ipv6":                  "2001:db8::2",
		"static_ipv6_cidr":             "2001:db8::/126",
		"static_ipv6_gateway":          "2001:db8::1",
		"static_ipv6_tunnel_id":        "17",
		"static_ipv6_tunnel_interface": "he-ipv6",
	}}
	routed, present, err := ResolveRoutedIPv6(config)
	if err != nil || !present {
		t.Fatalf("ResolveRoutedIPv6() = %#v, %v, present=%v", routed, err, present)
	}
	if routed.Address != "2001:db8::2" || routed.CIDR != "2001:db8::/126" || routed.Gateway != "2001:db8::1" || routed.Prefix != 126 || routed.TunnelID != 17 || routed.TunnelInterface != "he-ipv6" {
		t.Fatalf("unexpected routed metadata: %#v", routed)
	}
	if got := routed.AddressCIDR(); got != "2001:db8::2/126" {
		t.Fatalf("AddressCIDR() = %q", got)
	}
}

func TestResolveRoutedIPv6RejectsGatewayOrAddressOutsidePrefix(t *testing.T) {
	tests := []map[string]string{
		{"static_ipv6": "2001:db8:1::2", "static_ipv6_cidr": "2001:db8::/126", "static_ipv6_gateway": "2001:db8::1"},
		{"static_ipv6": "2001:db8::2", "static_ipv6_cidr": "2001:db8::/127", "static_ipv6_gateway": "2001:db8::1"},
	}
	for _, metadata := range tests {
		_, present, err := ResolveRoutedIPv6(InstanceConfig{Metadata: metadata})
		if !present || err == nil || !strings.Contains(err.Error(), "隧道路由IPv6") {
			t.Fatalf("metadata %#v: present=%v err=%v", metadata, present, err)
		}
	}
}

func TestResolveRoutedIPv6AcceptsPointToPoint127(t *testing.T) {
	routed, present, err := ResolveRoutedIPv6(InstanceConfig{Metadata: map[string]string{
		"static_ipv6":         "2001:db8::1",
		"static_ipv6_cidr":    "2001:db8::/127",
		"static_ipv6_gateway": "2001:db8::",
	}})
	if err != nil || !present {
		t.Fatalf("ResolveRoutedIPv6() = %#v, %v, present=%v", routed, err, present)
	}
	if routed.Prefix != 127 || routed.AddressCIDR() != "2001:db8::1/127" {
		t.Fatalf("unexpected /127 routed config: %#v", routed)
	}
	if command := routed.HostCheckCommand(); !strings.Contains(command, "2001:db8::/127") || !strings.Contains(command, "oneclickvirt6") || !strings.Contains(command, "net.ipv6.conf.all.forwarding") || !strings.Contains(command, "net.ipv6.conf.default.forwarding") || strings.Contains(command, "proxy_ndp") {
		t.Fatalf("HostCheckCommand() missing routed details: %q", command)
	}
}

func TestRoutedIPv6RuntimeNetworkNameIsStableAndScoped(t *testing.T) {
	routed := RoutedIPv6Config{CIDR: "2001:db8::/64", TunnelID: 42}
	if got := RoutedIPv6RuntimeNetworkName("Podman", routed); got != "oneclickvirt6-podman-42" {
		t.Fatalf("network name = %q", got)
	}
	if got := RoutedIPv6RuntimeNetworkName("", RoutedIPv6Config{CIDR: "2001:db8::/64"}); !strings.HasPrefix(got, "oneclickvirt6-runtime-") {
		t.Fatalf("fallback network name = %q", got)
	}
}

func TestRoutedIPv4RuntimeSubnetIsDeterministic(t *testing.T) {
	subnet, gateway := RoutedIPv4RuntimeSubnet(17)
	if subnet != "10.240.17.0/24" || gateway != "10.240.17.1" {
		t.Fatalf("subnet=%q gateway=%q", subnet, gateway)
	}
}

func TestRoutedIPv6VethCommandIsBoundedAndUsesNamespacePeer(t *testing.T) {
	routed := RoutedIPv6Config{Address: "2001:db8::2", CIDR: "2001:db8::/126", Gateway: "2001:db8::1", Bridge: "oneclickvirt6", Prefix: 126, TunnelID: 17, TunnelInterface: "he-ipv6"}
	command, err := routed.RoutedIPv6VethCommand("docker", "instance-a")
	if err != nil {
		t.Fatalf("RoutedIPv6VethCommand() error = %v", err)
	}
	for _, fragment := range []string{
		"docker inspect 'instance-a' --format '{{.State.Pid}}'",
		"ip link add 'oc6h",
		"type veth peer name 'oc6p",
		"ip link set dev 'oc6h",
		"master 'oneclickvirt6'",
		"ip link set dev 'oc6p",
		"netns \"$pid\"",
		"ip -6 addr add '2001:db8::2/126'",
		"ip -6 route replace default via '2001:db8::1'",
		"net.ipv6.conf.all.forwarding",
		"net.ipv6.conf.default.forwarding",
		"net.ipv6.conf.he-ipv6.forwarding",
		"net.ipv6.conf.oneclickvirt6.forwarding",
	} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("command missing %q: %s", fragment, command)
		}
	}
	if strings.Contains(command, "macvlan") || strings.Contains(command, "network create") {
		t.Fatalf("routed veth command unexpectedly creates a runtime network: %s", command)
	}
	host, peer := RoutedIPv6VethNames("docker", "instance-a", 17)
	if len(host) > 15 || len(peer) > 15 || host == peer {
		t.Fatalf("invalid interface names: %q %q", host, peer)
	}
}

func TestRoutedIPv6ConfigFromSelectionRevalidatesMetadata(t *testing.T) {
	selection := utils.ContainerNetworkSelection{
		StaticIPv6:     "2001:db8::2",
		RoutedCIDR:     "2001:db8::/126",
		RoutedGateway:  "2001:db8::1",
		RoutedBridge:   "oneclickvirt6",
		RoutedTunnelID: 17, RoutedTunnelInterface: "he-ipv6",
		RoutedVeth: true,
	}
	routed, err := RoutedIPv6ConfigFromSelection(selection)
	if err != nil || routed.Address != "2001:db8::2" || routed.TunnelID != 17 {
		t.Fatalf("RoutedIPv6ConfigFromSelection() = %#v, %v", routed, err)
	}
	selection.RoutedGateway = "not-an-ip"
	if _, err := RoutedIPv6ConfigFromSelection(selection); err == nil {
		t.Fatal("invalid routed metadata unexpectedly accepted")
	}
}

func TestRoutedIPv6RuntimeLabelsRoundTripAndRejectPartialState(t *testing.T) {
	selection := utils.ContainerNetworkSelection{
		StaticIPv6:     "2001:db8::2",
		RoutedCIDR:     "2001:db8::/126",
		RoutedGateway:  "2001:db8::1",
		RoutedBridge:   "oneclickvirt6",
		RoutedTunnelID: 17, RoutedTunnelInterface: "he-ipv6",
		RoutedVeth: true,
	}
	args, err := RoutedIPv6RuntimeLabelArgs(selection)
	if err != nil {
		t.Fatalf("RoutedIPv6RuntimeLabelArgs() error = %v", err)
	}
	for _, label := range []string{routedIPv6LabelAddress, routedIPv6LabelCIDR, routedIPv6LabelGateway, routedIPv6LabelBridge, routedIPv6LabelTunnelID, routedIPv6LabelTunnelInterface} {
		if !strings.Contains(args, "--label '"+label+"=") {
			t.Fatalf("label args missing %q: %q", label, args)
		}
	}
	inspect, err := RoutedIPv6RuntimeLabelInspectCommand("docker", "instance-a")
	if err != nil {
		t.Fatalf("RoutedIPv6RuntimeLabelInspectCommand() error = %v", err)
	}
	if strings.Count(inspect, " inspect ") != 1 || !strings.Contains(inspect, routedIPv6LabelAddress) || !strings.Contains(inspect, "\t") {
		t.Fatalf("inspect command does not read labels in one call: %q", inspect)
	}
	output := "2001:db8::2\t2001:db8::/126\t2001:db8::1\toneclickvirt6\t17\the-ipv6"
	routed, present, err := RoutedIPv6ConfigFromRuntimeLabelOutput(output)
	if err != nil || !present || routed.Address != "2001:db8::2" || routed.TunnelID != 17 || routed.TunnelInterface != "he-ipv6" {
		t.Fatalf("RoutedIPv6ConfigFromRuntimeLabelOutput() = %#v, %v, present=%v", routed, err, present)
	}
	if _, present, err := RoutedIPv6ConfigFromRuntimeLabelOutput("2001:db8::2\t\t2001:db8::1\toneclickvirt6\t17"); !present || err == nil {
		t.Fatalf("partial labels accepted: present=%v err=%v", present, err)
	}
}
