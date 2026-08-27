package docker

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/provider"
)

type routedDockerExecutor struct {
	commands []string
	outputs  []string
	errors   []error
}

func (e *routedDockerExecutor) Execute(command string) (string, error) {
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
func (e *routedDockerExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *routedDockerExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *routedDockerExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *routedDockerExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (e *routedDockerExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e *routedDockerExecutor) IsHealthy() bool                                 { return true }
func (e *routedDockerExecutor) Reconnect() error                                { return nil }
func (e *routedDockerExecutor) Close() error                                    { return nil }

func TestDockerRoutedNetworkSelectionKeepsNATIPv4Primary(t *testing.T) {
	executor := &routedDockerExecutor{}
	d := NewDockerProvider().(*DockerProvider)
	d.sshClient.SetExecutor(executor)
	selection, present, err := d.routedNetworkSelection(provider.InstanceConfig{Metadata: map[string]string{
		"static_ipv6":                  "2001:db8::2",
		"static_ipv6_cidr":             "2001:db8::/126",
		"static_ipv6_gateway":          "2001:db8::1",
		"static_ipv6_tunnel_id":        "17",
		"static_ipv6_tunnel_interface": "he-ipv6",
	}}, "nat_ipv4_ipv6")
	if err != nil || !present {
		t.Fatalf("selection=%#v present=%v err=%v", selection, present, err)
	}
	if len(executor.commands) != 1 {
		t.Fatalf("remote commands = %d, want 1", len(executor.commands))
	}
	for _, fragment := range []string{"ip link show dev 'oneclickvirt6'", "net.ipv6.conf.he-ipv6.forwarding", "net.ipv6.conf.oneclickvirt6.forwarding"} {
		if !strings.Contains(executor.commands[0], fragment) {
			t.Fatalf("command %q missing %q", executor.commands[0], fragment)
		}
	}
	if selection.Network != defaultDockerRuntime.IPv4Network || len(selection.AdditionalNetworks) != 0 || selection.RoutedCIDR != "2001:db8::/126" || selection.RoutedGateway != "2001:db8::1" || selection.RoutedBridge != "oneclickvirt6" || selection.StaticIPv6 != "2001:db8::2" || !selection.IPv6 || !selection.RoutedVeth {
		t.Fatalf("unexpected selection: %#v", selection)
	}
	if flags := dockerNetworkOptionFlags(selection); strings.Contains(flags, "--ip6") {
		t.Fatalf("primary NAT network must not receive the routed IPv6: %q", flags)
	}
	if labels, labelErr := provider.RoutedIPv6RuntimeLabelArgs(selection); labelErr != nil || !strings.Contains(labels, "oneclickvirt.routed-ipv6.address=2001:db8::2") {
		t.Fatalf("routed runtime labels = %q, %v", labels, labelErr)
	}
	if err := d.connectDockerAdditionalNetworks("instance-a", selection); err != nil {
		t.Fatalf("connectDockerAdditionalNetworks() error = %v", err)
	}
	if got := executor.commands[len(executor.commands)-1]; !strings.Contains(got, "ip link add 'oc6h") || !strings.Contains(got, "nsenter -t \"$pid\" -n ip -6 addr add '2001:db8::2/126'") {
		t.Fatalf("unexpected veth attach command: %q", got)
	}
}

func TestDockerRestoreRoutedIPv6AfterStartReattachesVeth(t *testing.T) {
	executor := &routedDockerExecutor{outputs: []string{"2001:db8::2\t2001:db8::/126\t2001:db8::1\toneclickvirt6\t17"}}
	d := NewDockerProvider().(*DockerProvider)
	d.sshClient.SetExecutor(executor)
	if err := d.restoreRoutedIPv6AfterStart("instance-a"); err != nil {
		t.Fatalf("restoreRoutedIPv6AfterStart() error = %v", err)
	}
	if len(executor.commands) != 2 || !strings.Contains(executor.commands[0], " inspect ") || !strings.Contains(executor.commands[1], "ip link add 'oc6h") {
		t.Fatalf("restore commands = %#v", executor.commands)
	}
}

func TestDockerRoutedIPv6OnlyUsesRoutedNetworkAtCreate(t *testing.T) {
	executor := &routedDockerExecutor{}
	d := NewDockerProvider().(*DockerProvider)
	d.sshClient.SetExecutor(executor)
	selection, present, err := d.routedNetworkSelection(provider.InstanceConfig{Metadata: map[string]string{
		"static_ipv6":                  "2001:db8::1",
		"static_ipv6_cidr":             "2001:db8::/127",
		"static_ipv6_gateway":          "2001:db8::",
		"static_ipv6_tunnel_interface": "he-ipv6",
	}}, "ipv6_only")
	if err != nil || !present {
		t.Fatalf("selection=%#v present=%v err=%v", selection, present, err)
	}
	if selection.Network != "none" || len(selection.AdditionalNetworks) != 0 || !selection.RoutedVeth {
		t.Fatalf("unexpected IPv6-only selection: %#v", selection)
	}
	flags := dockerNetworkOptionFlags(selection)
	if !strings.Contains(flags, "--network='none'") || strings.Contains(flags, "--ip6") {
		t.Fatalf("unexpected IPv6-only flags %q", flags)
	}
	if _, err := provider.RoutedIPv6VethAttachCommand("docker", "instance-a", selection); err != nil {
		t.Fatalf("RoutedIPv6VethAttachCommand() error = %v", err)
	}
}

func TestOrbstackRoutedIPv6RejectsMacOSHostBeforeContainerCreation(t *testing.T) {
	executor := &routedDockerExecutor{
		outputs: []string{"OrbStack on macOS cannot provide host-routed public IPv6 through the Docker CLI"},
		errors:  []error{errors.New("remote preflight failed")},
	}
	d := NewOrbstackProvider().(*DockerProvider)
	d.sshClient.SetExecutor(executor)
	_, present, err := d.routedNetworkSelection(provider.InstanceConfig{Metadata: map[string]string{
		"static_ipv6":         "2001:db8::2",
		"static_ipv6_cidr":    "2001:db8::/126",
		"static_ipv6_gateway": "2001:db8::1",
	}}, "nat_ipv4_ipv6")
	if !present || err == nil || !strings.Contains(err.Error(), "OrbStack on macOS") {
		t.Fatalf("routedNetworkSelection() = present=%v err=%v, want explicit macOS capability error", present, err)
	}
	if len(executor.commands) != 1 || !strings.Contains(executor.commands[0], "$(uname -s") {
		t.Fatalf("OrbStack preflight command = %#v, want Darwin capability guard", executor.commands)
	}
}
