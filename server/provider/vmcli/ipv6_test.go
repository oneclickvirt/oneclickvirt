package vmcli

import (
	"strings"
	"testing"

	rootProvider "oneclickvirt/provider"
)

func routedVMCLIConfig(networkType string) rootProvider.InstanceConfig {
	return rootProvider.InstanceConfig{
		Name:   "vm-a",
		CPU:    "2",
		Memory: "1024m",
		Disk:   "10g",
		Metadata: map[string]string{
			"network_type":        networkType,
			"static_ipv6":         "2001:db8::2",
			"static_ipv6_cidr":    "2001:db8::/126",
			"static_ipv6_gateway": "2001:db8::1",
			"static_ipv6_bridge":  "oneclickvirt6",
			"password":            "Passw0rd!",
		},
	}
}

func TestVirtualBoxRoutedIPv6CreateScriptAddsSecondBridgeNICAndSeed(t *testing.T) {
	p := New(VirtualBoxSpec()).(*Provider)
	p.config.StoragePoolPath = "/var/lib/oneclickvirt/virtualbox"
	config := routedVMCLIConfig("nat_ipv4_ipv6")
	plan, err := rootProvider.ResolveRoutedIPv6VMPlan(config, "virtualbox")
	if err != nil {
		t.Fatal(err)
	}
	script, _, err := p.virtualBoxCreateScript(config.Name, "template", p.basePath(), 2, 1024, 10, config, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--nic1 nat", "--nic2 bridged", "--bridgeadapter2 'oneclickvirt6'", "oneclickvirt-ipv6-seed", "cloud-localds", "bridgeadapter2=\"oneclickvirt6\""} {
		if !strings.Contains(script, want) {
			t.Fatalf("VirtualBox script missing %q:\n%s", want, script)
		}
	}
}

func TestMultipassRoutedIPv6CreateScriptUsesCloudInitAndVerifiesGuest(t *testing.T) {
	p := New(MultipassSpec()).(*Provider)
	config := routedVMCLIConfig("nat_ipv4_ipv6")
	plan, err := rootProvider.ResolveRoutedIPv6VMPlan(config, "multipass")
	if err != nil {
		t.Fatal(err)
	}
	script, _, err := p.multipassCreateScript(config.Name, "lts", 2, 1024, 10, config, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--cloud-init $work/cloud-init.yaml", "--network 'name=oneclickvirt6,mode=manual,mac=", "multipass exec \"$name\"", "oneclickvirt6"} {
		if !strings.Contains(script, want) {
			t.Fatalf("Multipass script missing %q:\n%s", want, script)
		}
	}
}

func TestVagrantRoutedIPv6CreateScriptAttachesPublicBridgeAndPersistsGuestRoute(t *testing.T) {
	p := New(VagrantSpec()).(*Provider)
	p.config.StoragePoolPath = "/var/lib/oneclickvirt/vagrant"
	config := routedVMCLIConfig("nat_ipv4_ipv6")
	plan, err := rootProvider.ResolveRoutedIPv6VMPlan(config, "vagrant")
	if err != nil {
		t.Fatal(err)
	}
	script, _, err := p.vagrantCreateScript(config.Name, "generic/ubuntu2204", p.basePath(), 2, 1024, config, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"config.vm.network \"public_network\"", "bridge: \"oneclickvirt6\"", "vagrant ssh -c"} {
		if !strings.Contains(script, want) {
			t.Fatalf("Vagrant script missing %q:\n%s", want, script)
		}
	}
	guestScript := vagrantRoutedIPv6GuestScript(plan)
	for _, want := range []string{"oneclickvirt-routed-ipv6.service", "ip -6 route replace default", "oneclickvirt6"} {
		if !strings.Contains(guestScript, want) {
			t.Fatalf("Vagrant guest script missing %q:\n%s", want, guestScript)
		}
	}
}
