package kubevirt

import (
	"context"
	"fmt"
	"oneclickvirt/utils"
	"strings"
	"testing"
	"time"
)

type serviceFailureExecutor struct {
	utils.ShellExecutor
	commands []string
}

func (e *serviceFailureExecutor) Execute(command string) (string, error) {
	return e.ExecuteWithTimeout(command, 0)
}
func (e *serviceFailureExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	e.commands = append(e.commands, command)
	return "port already allocated", fmt.Errorf("injected service apply failure")
}

func TestStablePortMappingPropagatesServiceFailureWithoutRemovingLegacyAccess(t *testing.T) {
	e := &serviceFailureExecutor{}
	p := &KubeVirtProvider{connected: true, sshClient: utils.NewSafeShellExecutor(e)}
	if err := p.SetupStablePortMapping(context.Background(), "vm-test", 25001, 22, "tcp"); err == nil {
		t.Fatal("service failure accepted")
	}
	if len(e.commands) != 1 {
		t.Fatalf("legacy access was mutated after failed apply: %v", e.commands)
	}
	for _, required := range []string{"PreferDualStack", `"kubevirt.io/vm":$name`, "ownerReferences", "--ignore-not-found=true", "vm-test-ssh"} {
		if !strings.Contains(e.commands[0], required) {
			t.Errorf("missing %q", required)
		}
	}
}
func TestStablePortValidationRejectsInvalidInputBeforeSSH(t *testing.T) {
	e := &serviceFailureExecutor{}
	p := &KubeVirtProvider{sshClient: utils.NewSafeShellExecutor(e)}
	for _, proto := range []string{"icmp", "tcp; reboot"} {
		if p.SetupStablePortMapping(context.Background(), "vm", 25001, 22, proto) == nil {
			t.Fatal("invalid protocol accepted")
		}
	}
	if p.SetupStablePortMapping(context.Background(), "bad name", 25001, 22, "tcp") == nil {
		t.Fatal("invalid name accepted")
	}
	if p.RemoveStablePortMapping(context.Background(), "vm", 0, 22, "tcp") == nil {
		t.Fatal("invalid port accepted")
	}
	if len(e.commands) != 0 {
		t.Fatal("invalid input reached SSH")
	}
}
