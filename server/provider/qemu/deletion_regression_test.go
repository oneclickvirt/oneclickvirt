package qemu

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"oneclickvirt/utils"
)

func TestQemuDeletionVerificationDoesNotTreatConnectionFailureAsSuccess(t *testing.T) {
	if qemuDomainAlreadyGone("connection refused", errors.New("ssh transport unavailable")) {
		t.Fatal("connection failure was classified as an absent domain")
	}
	if !qemuDomainAlreadyGone("error: failed to get domain 'guest'", errors.New("virsh exited with status 1")) {
		t.Fatal("missing domain was not classified as absent")
	}
}

func TestQemuDeletionStopAcceptsOnlyInactiveOrMissingDomain(t *testing.T) {
	if qemuDomainNotRunning("permission denied", errors.New("permission denied")) {
		t.Fatal("permission failure was classified as an inactive domain")
	}
	if !qemuDomainNotRunning("error: domain is not running", errors.New("virsh exited with status 1")) {
		t.Fatal("inactive domain was not classified as acceptable")
	}
}

type undefineExecutor struct {
	utils.ShellExecutor
	commands []string
	message  string
}

func (e *undefineExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	if e.message != "" && !strings.Contains(command, "--nvram") {
		return e.message, errors.New("virsh exit status 1")
	}
	return "Domain has been undefined", nil
}

func TestQemuUndefineRetriesWithNVRAMOnlyForUEFI(t *testing.T) {
	command := "LC_ALL=C virsh undefine 'guest' --remove-all-storage"
	for _, tc := range []struct {
		name, message string
		retry, fail   bool
	}{
		{name: "bios domain"},
		{name: "uefi domain", message: "error: Requested operation is not valid: cannot undefine domain with nvram", retry: true},
		{name: "provider error", message: "permission denied", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &undefineExecutor{message: tc.message}
			p := &QEMUProvider{sshClient: utils.NewSafeShellExecutor(executor)}
			if _, err := p.undefineDomain(context.Background(), command); (err != nil) != tc.fail {
				t.Fatalf("undefine error = %v", err)
			}
			expected := []string{command + " 2>&1"}
			if tc.retry {
				expected = append(expected, command+" --nvram 2>&1")
			}
			if !slices.Equal(executor.commands, expected) {
				t.Fatalf("undefine commands = %v, want %v", executor.commands, expected)
			}
		})
	}
}
