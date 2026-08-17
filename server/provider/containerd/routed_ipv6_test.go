package containerd

import (
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/provider"
)

type routedContainerdExecutor struct {
	command  string
	commands []string
	outputs  []string
	errors   []error
}

func (e *routedContainerdExecutor) Execute(command string) (string, error) {
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
func (e *routedContainerdExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *routedContainerdExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *routedContainerdExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *routedContainerdExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (e *routedContainerdExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e *routedContainerdExecutor) IsHealthy() bool                                 { return true }
func (e *routedContainerdExecutor) Reconnect() error                                { return nil }
func (e *routedContainerdExecutor) Close() error                                    { return nil }

func TestContainerdRoutedNetworkSelectionUsesTunnelBridgeAsIPv6Attachment(t *testing.T) {
	executor := &routedContainerdExecutor{}
	c := NewContainerdProvider().(*ContainerdProvider)
	c.sshClient.SetExecutor(executor)
	selection, present, err := c.routedNetworkSelection(provider.InstanceConfig{Metadata: map[string]string{
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
	if flags := appendContainerdNetworkOptions("nerdctl run", selection); strings.Contains(flags, "--ip6") {
		t.Fatalf("primary NAT network must not receive the routed IPv6: %q", flags)
	}
	if labels, labelErr := provider.RoutedIPv6RuntimeLabelArgs(selection); labelErr != nil || !strings.Contains(labels, "oneclickvirt.routed-ipv6.address=2001:db8::2") {
		t.Fatalf("routed runtime labels = %q, %v", labels, labelErr)
	}
	if err := c.connectContainerdAdditionalNetworks("instance-a", selection); err != nil {
		t.Fatalf("connectContainerdAdditionalNetworks() error = %v", err)
	}
	if !strings.Contains(executor.command, "ip link add 'oc6h") || !strings.Contains(executor.command, "ip -6 addr add '2001:db8::2/126'") {
		t.Fatalf("unexpected veth attach command: %q", executor.command)
	}
}

func TestContainerdRestoreRoutedIPv6AfterStartReattachesVeth(t *testing.T) {
	executor := &routedContainerdExecutor{outputs: []string{"2001:db8::2\t2001:db8::/126\t2001:db8::1\toneclickvirt6\t17"}}
	c := NewContainerdProvider().(*ContainerdProvider)
	c.sshClient.SetExecutor(executor)
	if err := c.restoreRoutedIPv6AfterStart("instance-a"); err != nil {
		t.Fatalf("restoreRoutedIPv6AfterStart() error = %v", err)
	}
	if len(executor.commands) != 2 || !strings.Contains(executor.commands[0], " inspect ") || !strings.Contains(executor.commands[1], "ip link add 'oc6h") {
		t.Fatalf("restore commands = %#v", executor.commands)
	}
}
