package proxmox

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	coreprovider "oneclickvirt/provider"
)

func dedicatedIPv4Snapshot(bridge string, prefix int) string {
	return fmt.Sprintf(`{"addresses":[{"ifname":%q,"addr_info":[{"family":"inet","local":"198.51.100.194","prefixlen":%d,"scope":"global"}]}],"routes":[{"dst":"default","dev":%q,"gateway":"198.51.100.193"}]}`, bridge, prefix, bridge)
}

func TestDedicatedIPv4PreservesRealPublicSubnetAndRejectsReservedAddresses(t *testing.T) {
	for _, prefix := range []int{24, 27, 28, 30} {
		got, err := parseProxmoxDedicatedIPv4("\x1b[32m"+dedicatedIPv4Snapshot("vmbr0", prefix)+"\x1b[0m", netip.MustParseAddr("198.51.100.195"), "vmbr0")
		if prefix == 30 {
			if err == nil {
				t.Fatal("accepted broadcast address in a /30")
			}
			continue
		}
		if err != nil || got.Prefix != prefix || got.Gateway.String() != "198.51.100.193" {
			t.Fatalf("/%d: %#v, %v", prefix, got, err)
		}
	}
	for _, address := range []string{"198.51.100.192", "198.51.100.193", "198.51.100.194", "198.51.100.223", "198.51.100.224"} {
		if _, err := parseProxmoxDedicatedIPv4(dedicatedIPv4Snapshot("vmbr0", 27), netip.MustParseAddr(address), "vmbr0"); err == nil {
			t.Fatalf("accepted reserved or out-of-subnet address %s", address)
		}
	}
	if _, err := parseProxmoxDedicatedIPv4(dedicatedIPv4Snapshot("management0", 27), netip.MustParseAddr("198.51.100.195"), "vmbr0"); err == nil {
		t.Fatal("used the gateway of an unrelated interface")
	}
	for _, output := range []string{"", "network ready\n" + dedicatedIPv4Snapshot("vmbr0", 27), "[]", `{"addresses":[],"routes":[]}`} {
		if _, err := parseProxmoxDedicatedIPv4(output, netip.MustParseAddr("198.51.100.195"), "vmbr0"); err == nil {
			t.Fatalf("accepted missing or polluted live network state: %q", output)
		}
	}
}

func TestDedicatedIPv4ChoosesMostSpecificBridgeSubnet(t *testing.T) {
	output := strings.Replace(dedicatedIPv4Snapshot("vmbr0", 24), `"scope":"global"}]`, `"scope":"global"},{"family":"inet","local":"198.51.100.196","prefixlen":27,"scope":"global"}]`, 1)
	got, err := parseProxmoxDedicatedIPv4(output, netip.MustParseAddr("198.51.100.195"), "vmbr0")
	if err != nil || got.Prefix != 27 {
		t.Fatalf("expected the /27 bridge subnet: %#v, %v", got, err)
	}
}

func TestDedicatedIPv4RejectsMalformedNICBeforeMutation(t *testing.T) {
	for _, original := range []string{
		"virtio=02:00:00:00:00:02,bridge=vmbr1,bridge=vmbr9",
		"virtio=02:00:00:00:00:02,bridge",
	} {
		executor := &ipv6CommandExecutor{output: func(command string) string { return "net0: " + original + "\n" }}
		p := NewProxmoxProvider().(*ProxmoxProvider)
		p.sshClient.SetExecutor(executor)
		network := &proxmoxDedicatedIPv4{Address: netip.MustParseAddr("198.51.100.195"), Prefix: 27, Gateway: netip.MustParseAddr("198.51.100.193"), Bridge: "vmbr0"}
		if err := p.configureDedicatedIPv4(context.Background(), 501, "vm", network); err == nil {
			t.Fatal("accepted malformed guest NIC")
		}
		if len(executor.commands) != 1 || executor.commands[0] != "qm config 501" {
			t.Fatalf("mutated malformed guest NIC: %v", executor.commands)
		}
	}
}

func TestDedicatedIPv4AppliesAddressToGuestAndPreservesNICOptions(t *testing.T) {
	for _, instanceType := range []string{"container", "vm"} {
		t.Run(instanceType, func(t *testing.T) {
			tool, original := "pct", "name=eth0,ip=172.16.1.2/24,bridge=vmbr1,gw=172.16.1.1,hwaddr=02:00:00:00:00:02,rate=12,firewall=1"
			if instanceType == "vm" {
				tool, original = "qm", "virtio=02:00:00:00:00:02,bridge=vmbr1,rate=12,firewall=1"
			}
			executor := &ipv6CommandExecutor{output: func(command string) string {
				if strings.Contains(command, "ip -j -4 addr show") {
					return dedicatedIPv4Snapshot("vmbr0", 27)
				}
				if command == tool+" config 501" {
					return "net0: " + original + "\nnet1: bridge=vmbr2\n"
				}
				return ""
			}}
			p := NewProxmoxProvider().(*ProxmoxProvider)
			p.bridgeDedicatedV4 = "vmbr0"
			p.sshClient.SetExecutor(executor)
			config := coreprovider.InstanceConfig{Metadata: map[string]string{"static_ipv4": "198.51.100.195"}}
			network, err := p.dedicatedIPv4Config(context.Background(), config, "dedicated_ipv4")
			if err != nil {
				t.Fatal(err)
			}
			if err := p.configureDedicatedIPv4(context.Background(), 501, instanceType, network); err != nil {
				t.Fatal(err)
			}
			last := executor.commands[len(executor.commands)-1]
			for _, want := range []string{"bridge=vmbr0", "198.51.100.195/27", "gw=198.51.100.193", "02:00:00:00:00:02", "rate=12", "firewall=1"} {
				if !strings.Contains(last, want) {
					t.Fatalf("lost %q in guest configuration: %s", want, last)
				}
			}
			if strings.Contains(last, "--net1") || strings.Contains(last, "172.16.1.") {
				t.Fatalf("dedicated address retained NAT or changed IPv6 NIC: %s", last)
			}
			for _, command := range executor.commands {
				if strings.Contains(command, "ip addr add") || strings.Contains(command, "ip addr del") {
					t.Fatalf("guest address was stolen by the host: %s", command)
				}
			}
		})
	}
}

func TestDedicatedIPv4RejectsMissingAllocationAndUnavailableNIC(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.bridgeDedicatedV4 = "vmbr0"
	executor := &ipv6CommandExecutor{}
	p.sshClient.SetExecutor(executor)
	if _, err := p.dedicatedIPv4Config(context.Background(), coreprovider.InstanceConfig{}, "dedicated_ipv4_ipv6"); err == nil {
		t.Fatal("accepted a dedicated guest without a pool allocation")
	}
	if len(executor.commands) != 0 {
		t.Fatal("missing allocation caused a remote operation")
	}
	if network, err := p.dedicatedIPv4Config(context.Background(), coreprovider.InstanceConfig{}, "nat_ipv4"); network != nil || err != nil {
		t.Fatalf("NAT-only guest was affected: %#v, %v", network, err)
	}
	network, err := parseProxmoxDedicatedIPv4(dedicatedIPv4Snapshot("vmbr0", 27), netip.MustParseAddr("198.51.100.195"), "vmbr0")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.configureDedicatedIPv4(context.Background(), 501, "vm", network); err == nil {
		t.Fatal("silently fabricated a missing VM NIC")
	}
}

func TestRequestedIPv6CannotSilentlyBecomeIPv4Only(t *testing.T) {
	setupProxmoxIPv6CommandTestDB(t)
	for _, instanceType := range []string{"container", "vm"} {
		for _, networkType := range []string{"nat_ipv4_ipv6", "ipv6_only"} {
			p := NewProxmoxProvider().(*ProxmoxProvider)
			executor := &ipv6CommandExecutor{}
			p.sshClient.SetExecutor(executor)
			config := coreprovider.InstanceConfig{InstanceType: instanceType, Metadata: map[string]string{"network_type": networkType}}
			if err := p.configureInstanceNetwork(context.Background(), 501, config); err == nil {
				t.Fatalf("%s %s succeeded despite missing IPv6 state", instanceType, networkType)
			}
			for _, command := range executor.commands {
				if strings.Contains(command, "--net0") || strings.Contains(command, "--ipconfig0") {
					t.Fatalf("%s %s silently changed to IPv4: %s", instanceType, networkType, command)
				}
			}
		}
	}
}
