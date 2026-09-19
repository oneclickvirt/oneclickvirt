package task

import (
	"context"
	"fmt"
	"strings"
	"testing"

	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"
)

type mappingFamilyProvider struct {
	providerCore.Provider
	commands []string
	natRules map[string][]string
}

func (p *mappingFamilyProvider) ExecuteSSHCommand(ctx context.Context, command string) (string, error) {
	return p.ExecuteCommand(ctx, command)
}

func (p *mappingFamilyProvider) ExecuteCommand(_ context.Context, command string) (string, error) {
	p.commands = append(p.commands, command)
	if strings.HasPrefix(command, "cat ") {
		return "iptables", nil
	}
	if strings.HasPrefix(command, "command -v nft") {
		return "", fmt.Errorf("nft is not installed")
	}
	if p.natRules == nil {
		p.natRules = make(map[string][]string)
	}
	for _, tool := range []string{"iptables", "ip6tables"} {
		if command == tool+" -w 5 -t nat -S" {
			return strings.Join(p.natRules[tool], "\n"), nil
		}
		if strings.HasPrefix(command, tool+" -t nat -A PREROUTING") {
			p.natRules[tool] = append(p.natRules[tool], strings.ReplaceAll(strings.TrimPrefix(command, tool+" -t nat "), "'", ""))
		}
		plain := strings.ReplaceAll(command, "'", "")
		if strings.HasPrefix(plain, tool+" -w 5 -t nat -D PREROUTING") {
			want := strings.Replace(strings.TrimPrefix(plain, tool+" -w 5 -t nat "), "-D", "-A", 1)
			for index, rule := range p.natRules[tool] {
				if rule == want {
					p.natRules[tool] = append(p.natRules[tool][:index], p.natRules[tool][index+1:]...)
					return "", nil
				}
			}
			return "", fmt.Errorf("rule does not exist: %s", want)
		}
	}
	return "", nil
}

func TestAutomaticDualStackRepairAndRemovalKeepBothFamilies(t *testing.T) {
	p := &mappingFamilyProvider{natRules: map[string][]string{
		"iptables":  {"-A PREROUTING -p tcp --dport 22000 -m comment --comment pm:guest:22000:22 -j DNAT --to-destination 192.0.2.10:22"},
		"ip6tables": {"-A PREROUTING -p tcp --dport 22000 -m comment --comment pm:guest:22000:22 -j DNAT --to-destination [2001:db8::10]:22"},
	}}
	applier := newPortMappingApplier(context.Background(), p, &providerModel.Provider{Type: "qemu", NetworkType: "nat_ipv4_ipv6", IPv4PortMappingMethod: "iptables", IPv6PortMappingMethod: "iptables"})
	instance := &providerModel.Instance{Name: "guest", PrivateIP: "192.0.2.10", IPv6Address: "2001:db8::10"}
	port := &providerModel.Port{HostPort: 22000, GuestPort: 22, Protocol: "tcp", IsAutomatic: true, PortType: "range_mapped", IPv6Enabled: true, MappingMethod: "iptables"}
	if err := applier.Apply(instance, port, true); err != nil {
		t.Fatal(err)
	}
	ipv4Added, ipv6Added, firstAdd, lastDelete := false, false, -1, -1
	for index, command := range p.commands {
		command = strings.ReplaceAll(command, "'", "")
		if strings.Contains(command, "-A PREROUTING") && strings.Contains(command, "--dport 22000") {
			if firstAdd < 0 {
				firstAdd = index
			}
			ipv4Added = ipv4Added || strings.Contains(command, "192.0.2.10:22")
			ipv6Added = ipv6Added || strings.Contains(command, "[2001:db8::10]:22")
		}
		if strings.Contains(command, "-D PREROUTING") && strings.Contains(command, "--dport 22000") {
			lastDelete = index
		}
	}
	if !ipv4Added || !ipv6Added || firstAdd <= lastDelete || lastDelete < 0 {
		t.Fatalf("dual-stack repair lost a family or deleted a newly created mapping: %v", p.commands)
	}
	p.commands = nil
	if err := applier.Remove(instance, port); err != nil {
		t.Fatal(err)
	}
	deleted4, deleted6 := false, false
	for _, command := range p.commands {
		command = strings.ReplaceAll(command, "'", "")
		if strings.Contains(command, "-D PREROUTING") {
			deleted4 = deleted4 || strings.Contains(command, "192.0.2.10:22")
			deleted6 = deleted6 || strings.Contains(command, "[2001:db8::10]:22")
		}
	}
	if !deleted4 || !deleted6 || len(p.natRules["iptables"]) != 0 || len(p.natRules["ip6tables"]) != 0 {
		t.Fatalf("dual-stack cleanup omitted a family: %v", p.commands)
	}
}

func TestPortCleanupWithoutSavedGuestAddressUsesRowFamily(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		t.Run(fmt.Sprint(ipv6), func(t *testing.T) {
			p := &mappingFamilyProvider{natRules: map[string][]string{
				"iptables":  {"-A PREROUTING -p tcp --dport 22000 -m comment --comment pm:guest:22000:22 -j DNAT --to-destination 192.0.2.10:22"},
				"ip6tables": {"-A PREROUTING -p tcp --dport 22000 -m comment --comment pm:guest:22000:22 -j DNAT --to-destination [2001:db8::10]:22"},
			}}
			applier := newPortMappingApplier(context.Background(), p, &providerModel.Provider{Type: "qemu", NetworkType: "nat_ipv4_ipv6", IPv4PortMappingMethod: "iptables", IPv6PortMappingMethod: "iptables"})
			port := &providerModel.Port{HostPort: 22000, GuestPort: 22, Protocol: "tcp", IPv6Enabled: ipv6, MappingMethod: "iptables"}
			if ipv6 {
				// An explicit guest address denotes a single-family mapping. A
				// NAT dual-stack row without IPv6Address intentionally expands to
				// both families and is covered by the test above.
				port.IPv6Address = "2001:db8::10"
			}
			if err := applier.Remove(&providerModel.Instance{Name: "guest"}, port); err != nil {
				t.Fatal(err)
			}
			target, other := "iptables", "ip6tables"
			if ipv6 {
				target, other = other, target
			}
			if len(p.natRules[target]) != 0 || len(p.natRules[other]) != 1 {
				t.Fatalf("cleanup crossed address families: %v", p.natRules)
			}
		})
	}
}

func TestAutomaticIPv4MappingDoesNotRequireNATForNativeIPv6(t *testing.T) {
	p := &mappingFamilyProvider{}
	applier := newPortMappingApplier(context.Background(), p, &providerModel.Provider{Type: "qemu", NetworkType: "nat_ipv4_ipv6", IPv4PortMappingMethod: "iptables", IPv6PortMappingMethod: "native"})
	instance := &providerModel.Instance{Name: "guest", PrivateIP: "192.0.2.10"}
	port := &providerModel.Port{HostPort: 22000, GuestPort: 22, Protocol: "tcp", IsAutomatic: true, PortType: "range_mapped", IPv6Enabled: true, MappingMethod: "iptables"}
	if err := applier.Apply(instance, port, false); err != nil {
		t.Fatal(err)
	}
	for _, command := range p.commands {
		if strings.Contains(command, "ip6tables") {
			t.Fatalf("native IPv6 unexpectedly changed firewall rules: %s", command)
		}
	}
}
