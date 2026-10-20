package provider

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

type forwardingFixture struct {
	Provider
	kind    string
	command string
	err     error
}

func (p *forwardingFixture) GetType() string { return p.kind }
func (p *forwardingFixture) ExecuteSSHCommand(_ context.Context, command string) (string, error) {
	p.command = command
	return "", p.err
}

func TestDedicatedIPv4RejectsInvalidInputsBeforeSSH(t *testing.T) {
	for _, test := range []struct{ name, address string }{
		{"guest", ""}, {"guest", "10.0.0.1"}, {"guest", "2606:4700::1"}, {"guest;reboot", "8.8.4.4"},
	} {
		fixture := &forwardingFixture{kind: "incus"}
		err := ConfigureDedicatedIPv4(context.Background(), fixture, InstanceConfig{Name: test.name, Metadata: map[string]string{"network_type": "dedicated_ipv4", "static_ipv4": test.address}})
		if err == nil || fixture.command != "" {
			t.Fatalf("unsafe mutation for %#v: %v", test, err)
		}
	}
}

func TestDedicatedIPv4PersistsRecoveryAndPropagatesFailure(t *testing.T) {
	fixture := &forwardingFixture{kind: "incus", err: errors.New("SSH failed")}
	err := ConfigureDedicatedIPv4(context.Background(), fixture, InstanceConfig{Name: "guest", Metadata: map[string]string{"network_type": "dedicated_ipv4_ipv6", "static_ipv4": "8.8.4.4"}})
	if err == nil {
		t.Fatal("reported success without a persistent network")
	}
	if !strings.Contains(fixture.command, "ExecStopPost=") || !strings.Contains(fixture.command, " --apply ") || !strings.Contains(fixture.command, " --watch ") {
		t.Fatal("missing network restore or exact cleanup")
	}
	fixture.command = ""
	if err := ConfigureDedicatedIPv4(context.Background(), fixture, InstanceConfig{Name: "guest", Metadata: map[string]string{"network_type": "nat_ipv4"}}); err != nil || fixture.command != "" {
		t.Fatal("NAT instance was changed")
	}
	command := exec.Command("bash", "-n")
	command.Stdin = strings.NewReader(dedicatedIPv4Helper)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("invalid embedded helper: %v %s", err, out)
	}
}
