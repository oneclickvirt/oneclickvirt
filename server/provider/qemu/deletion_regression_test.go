package qemu

import (
	"errors"
	"testing"
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
