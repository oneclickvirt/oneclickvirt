package docker

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDockerInstallerManualIPv6DoesNotPassPublicAddressToIPAM(t *testing.T) {
	setupDockerTestLogger(t)
	for _, network := range []string{"nat_ipv4_ipv6", "dedicated_ipv4_ipv6", "ipv6_only"} {
		t.Run(network, func(t *testing.T) {
			executor := &routedDockerExecutor{outputs: []string{"", "manual", "false", "valid", "manual", "manual", ""}}
			d := NewDockerProvider().(*DockerProvider)
			d.connected = true
			d.sshClient.SetExecutor(executor)
			selection, err := d.resolveDockerContainerNetwork(network, "2001:db8::200")
			if err != nil {
				t.Fatal(err)
			}
			if !selection.ManualIPv6 || selection.StaticIPv6 != "2001:db8::200" {
				t.Fatalf("lost manual routed allocation: %#v", selection)
			}
			if flags := dockerNetworkOptionFlags(selection); strings.Contains(flags, "--ip6=") {
				t.Fatalf("public /128 was passed to ULA IPAM: %s", flags)
			}
			if err := d.connectDockerAdditionalNetworks("guest", selection, context.Background()); err != nil {
				t.Fatal(err)
			}
			commands := strings.Join(executor.commands, "\n")
			if !strings.Contains(commands, "docker-ipv6-attach.sh' 'guest' '2001:db8::200'") {
				t.Fatalf("public /128 was not applied by the installer: %s", commands)
			}
			if network == "ipv6_only" {
				if !selection.IPv6Only || !strings.Contains(commands, "ip -4 address flush scope global") {
					t.Fatal("IPv6-only retained implicit bridge IPv4")
				}
				if !strings.Contains(dockerNetworkOptionFlags(selection), "--dns=2606:4700:4700::1111") {
					t.Fatal("IPv6-only inherited IPv4-only DNS")
				}
			}
		})
	}
}

func TestDockerManualIPv6RestoreFailureStopsContainer(t *testing.T) {
	executor := &routedDockerExecutor{
		outputs: []string{"", `{"oneclickvirt.network.docker-manual-ipv6":"2001:db8::200"}`, "failed", ""},
		errors:  []error{nil, nil, errors.New("installer failed"), nil},
	}
	d := NewDockerProvider().(*DockerProvider)
	d.sshClient.SetExecutor(executor)
	if err := d.restoreRoutedIPv6AfterStart("guest", context.Background()); err == nil {
		t.Fatal("reported success with a missing public IPv6")
	}
	if got := executor.commands[len(executor.commands)-1]; !strings.Contains(got, "docker stop 'guest'") {
		t.Fatalf("container continued running with incomplete network: %s", got)
	}
}

func TestDockerManualIPv6RegistryRejectsMalformedAndPrivateRows(t *testing.T) {
	executor := &routedDockerExecutor{outputs: []string{"guest 2606:4700::200\nprivate fd42::2\nbad not-an-ip\npolluted 2606:4700::3 trailing\n"}}
	d := NewDockerProvider().(*DockerProvider)
	d.sshClient.SetExecutor(executor)
	got := d.dockerInstallerIPv6Allocations()
	if len(got) != 1 || got["guest"] != "2606:4700::200" {
		t.Fatalf("accepted invalid public registry values: %#v", got)
	}
}
