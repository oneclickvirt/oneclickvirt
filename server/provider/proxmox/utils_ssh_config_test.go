package proxmox

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestRepairSSHDropInsCommandCoversRootAndPasswordAuthentication(t *testing.T) {
	for _, want := range []string{
		"PermitRootLogin",
		"without-password",
		"prohibit-password",
		"PasswordAuthentication",
		"PasswordAuthentication yes",
	} {
		if !strings.Contains(repairSSHDropInsCommand, want) {
			t.Fatalf("repairSSHDropInsCommand = %q, want %q", repairSSHDropInsCommand, want)
		}
	}
}

func TestDebianSSHDependenciesAreBoundedNoninteractiveAndFailClosed(t *testing.T) {
	for _, failure := range []bool{false, true} {
		executor := &ipv6CommandExecutor{fail: func(command string) error {
			if failure && strings.Contains(command, "240s sh -c") {
				return fmt.Errorf("package failure")
			}
			return nil
		}}
		p := NewProxmoxProvider().(*ProxmoxProvider)
		p.sshClient.SetExecutor(executor)
		if err := p.configureDebianBasedSSH(context.Background(), 110); (err != nil) != failure {
			t.Fatalf("failure=%t: %v", failure, err)
		}
		first := executor.commands[0]
		for _, required := range []string{"timeout --kill-after=15s 240s", "DEBIAN_FRONTEND=noninteractive", "UCF_FORCE_CONFFOLD=1", "--force-confold", "dpkg-query", "DPkg::Lock::Timeout=45", "APT::Update::Error-Mode=any"} {
			if !strings.Contains(first, required) {
				t.Fatalf("missing %s in provisioning command", required)
			}
		}
		if strings.Contains(first, "apt-key") {
			t.Fatal("imports an untrusted key from package diagnostics")
		}
		if failure && len(executor.commands) != 1 {
			t.Fatal("continued setup after package failure")
		}
	}
}
