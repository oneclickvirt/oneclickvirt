package qemu

import (
	"reflect"
	"strings"
	"testing"
)

func TestQEMUPasswordCandidatesIncludesDesiredThenDefault(t *testing.T) {
	got := qemuPasswordCandidates("NewPass123!")
	want := []string{"NewPass123!", qemuDefaultGuestPassword}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qemuPasswordCandidates() = %#v, want %#v", got, want)
	}
}

func TestQEMUPasswordCandidatesDeduplicatesDefault(t *testing.T) {
	got := qemuPasswordCandidates(qemuDefaultGuestPassword)
	want := []string{qemuDefaultGuestPassword}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qemuPasswordCandidates() = %#v, want %#v", got, want)
	}
}

func TestQEMULXCPasswordCommandQuotesCredentialAndRootfs(t *testing.T) {
	command := qemuLXCPasswordCommand("/var/lib/oneclickvirt/root fs", "p'a ss$(touch /tmp/pwned)")
	if !strings.Contains(command, "chroot '/var/lib/oneclickvirt/root fs'") {
		t.Fatalf("rootfs was not shell quoted: %q", command)
	}
	if !strings.Contains(command, "'root:p'\\''a ss$(touch /tmp/pwned)'") {
		t.Fatalf("credential was not passed as a literal stdin value: %q", command)
	}
	if strings.Contains(command, "echo root:") {
		t.Fatalf("password command still exposes the old interpolated echo form: %q", command)
	}
}
