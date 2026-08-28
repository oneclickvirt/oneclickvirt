package podman

import (
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/provider"
)

type routedPodmanExecutor struct {
	command     string
	commands    []string
	tempScripts []string
	outputs     []string
	errors      []error
}

func (e *routedPodmanExecutor) Execute(command string) (string, error) {
	e.command = command
	e.commands = append(e.commands, command)
	index := len(e.commands) - 1
	var output string
	var err error
	if index < len(e.outputs) {
		output = e.outputs[index]
	}
	if index < len(e.errors) {
		err = e.errors[index]
	}
	return output, err
}
func (e *routedPodmanExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *routedPodmanExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *routedPodmanExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *routedPodmanExecutor) ExecuteViaTempScript(script string, _ []string, _ time.Duration) (string, error) {
	e.tempScripts = append(e.tempScripts, script)
	return "", nil
}
func (e *routedPodmanExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e *routedPodmanExecutor) IsHealthy() bool                                 { return true }
func (e *routedPodmanExecutor) Reconnect() error                                { return nil }
func (e *routedPodmanExecutor) Close() error                                    { return nil }

func TestPodmanRoutedNetworkSelectionUsesTunnelBridgeAsIPv6Attachment(t *testing.T) {
	executor := &routedPodmanExecutor{}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(executor)
	selection, present, err := p.routedNetworkSelection(provider.InstanceConfig{Metadata: map[string]string{
		"static_ipv6":                  "2001:db8::2",
		"static_ipv6_cidr":             "2001:db8::/126",
		"static_ipv6_gateway":          "2001:db8::1",
		"static_ipv6_tunnel_id":        "17",
		"static_ipv6_tunnel_interface": "he-ipv6",
	}}, "nat_ipv4_ipv6")
	if err != nil || !present {
		t.Fatalf("selection=%#v present=%v err=%v", selection, present, err)
	}
	for _, fragment := range []string{"ip link show dev 'oneclickvirt6'", "net.ipv6.conf.he-ipv6.forwarding", "net.ipv6.conf.oneclickvirt6.forwarding"} {
		if !strings.Contains(executor.command, fragment) {
			t.Fatalf("command %q missing %q", executor.command, fragment)
		}
	}
	if selection.Network != ipv4Network || len(selection.AdditionalNetworks) != 0 || selection.RoutedCIDR != "2001:db8::/126" || selection.RoutedGateway != "2001:db8::1" || selection.RoutedBridge != "oneclickvirt6" || selection.StaticIPv6 != "2001:db8::2" || !selection.IPv6 || !selection.RoutedVeth {
		t.Fatalf("unexpected selection: %#v", selection)
	}
	if flags := appendPodmanNetworkOptions("podman run", selection); strings.Contains(flags, "--ip6") {
		t.Fatalf("primary NAT network must not receive the routed IPv6: %q", flags)
	}
	if labels, labelErr := provider.RoutedIPv6RuntimeLabelArgs(selection); labelErr != nil || !strings.Contains(labels, "oneclickvirt.routed-ipv6.address=2001:db8::2") {
		t.Fatalf("routed runtime labels = %q, %v", labels, labelErr)
	}
	if err := p.connectPodmanAdditionalNetworks("instance-a", selection); err != nil {
		t.Fatalf("connectPodmanAdditionalNetworks() error = %v", err)
	}
	if !strings.Contains(executor.command, "ip link add 'oc6h") || !strings.Contains(executor.command, "ip -6 route replace default via '2001:db8::1'") {
		t.Fatalf("unexpected veth attach command: %q", executor.command)
	}
}

func TestPodmanRestoreRoutedIPv6AfterStartReattachesVeth(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"2001:db8::2\t2001:db8::/126\t2001:db8::1\toneclickvirt6\t17"}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(executor)
	if err := p.restoreRoutedIPv6AfterStart("instance-a"); err != nil {
		t.Fatalf("restoreRoutedIPv6AfterStart() error = %v", err)
	}
	if len(executor.commands) != 2 || !strings.Contains(executor.commands[0], " inspect ") || !strings.Contains(executor.commands[1], "ip link add 'oc6h") {
		t.Fatalf("restore commands = %#v", executor.commands)
	}
}

func TestPodmanRestoreIPv6AfterStartReattachesManualAddress(t *testing.T) {
	executor := &routedPodmanExecutor{outputs: []string{"", "", ""}}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(executor)
	if err := p.restoreIPv6AfterStart("instance-a"); err != nil {
		t.Fatalf("restoreIPv6AfterStart() error = %v", err)
	}
	if len(executor.commands) != 3 {
		t.Fatalf("restore commands = %#v, want manual check, helper, and routed label check", executor.commands)
	}
	if !strings.Contains(executor.commands[0], "podman_ipv6_allocations") || !strings.Contains(executor.commands[1], "podman-ipv6-attach.sh") {
		t.Fatalf("manual restore commands = %#v", executor.commands)
	}
}
