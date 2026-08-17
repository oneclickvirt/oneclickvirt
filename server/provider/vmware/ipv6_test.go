package vmware

import (
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/model/provider"
	rootProvider "oneclickvirt/provider"
)

type vmwareIPv6Executor struct{ commands []string }

func (e *vmwareIPv6Executor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	return "", nil
}
func (e *vmwareIPv6Executor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	e.commands = append(e.commands, command)
	return "", nil
}
func (e *vmwareIPv6Executor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *vmwareIPv6Executor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *vmwareIPv6Executor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (e *vmwareIPv6Executor) UploadContent(string, string, os.FileMode) error { return nil }
func (e *vmwareIPv6Executor) IsHealthy() bool                                 { return true }
func (e *vmwareIPv6Executor) Reconnect() error                                { return nil }
func (e *vmwareIPv6Executor) Close() error                                    { return nil }

func TestVMwareRoutedIPv6PreflightAndConfigurationUseVMNetAndNoCloud(t *testing.T) {
	config := rootProvider.InstanceConfig{
		Name: "vm-a",
		Metadata: map[string]string{
			"network_type":        "nat_ipv4_ipv6",
			"static_ipv6":         "2001:db8::2",
			"static_ipv6_cidr":    "2001:db8::/126",
			"static_ipv6_gateway": "2001:db8::1",
			"static_ipv6_bridge":  "oneclickvirt6",
			"password":            "Passw0rd!",
		},
	}
	plan, err := rootProvider.ResolveRoutedIPv6VMPlan(config, "vmware")
	if err != nil {
		t.Fatal(err)
	}
	executor := &vmwareIPv6Executor{}
	p := &VMwareProvider{
		config:    provider.ProviderNodeConfig{StoragePoolPath: "/var/lib/oneclickvirt/vmware"},
		executor:  executor,
		connected: true,
	}
	if err := p.preflightRoutedIPv6(plan); err != nil {
		t.Fatal(err)
	}
	if err := p.configureRoutedIPv6(executor, "/var/lib/oneclickvirt/vmware/instances/vm-a/vm-a.vmx", config, plan); err != nil {
		t.Fatal(err)
	}
	if len(executor.commands) != 3 {
		t.Fatalf("commands = %#v, want preflight + seed + VMX write", executor.commands)
	}
	for _, want := range []string{"VNET_0_INTERFACE", "oneclickvirt6", "cloud-localds", "ethernet1.vnet = \"vmnet0\"", "ethernet1.address = \"" + plan.MAC + "\"", "ide1:1.fileName"} {
		found := false
		for _, command := range executor.commands {
			if strings.Contains(command, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("VMware routed IPv6 commands missing %q: %#v", want, executor.commands)
		}
	}
	if strings.Contains(strings.Join(executor.commands, "\n"), "Passw0rd!") {
		t.Fatal("VMware seed command should not expose the raw password")
	}
}
