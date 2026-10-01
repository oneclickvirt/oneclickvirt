package proxmox

import (
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
