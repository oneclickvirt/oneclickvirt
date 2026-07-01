package qemu

import (
	"errors"
	"strings"
	"testing"
)

func TestQEMUCloudInitMissingStartError(t *testing.T) {
	output := "error: Cannot access storage file '/var/lib/libvirt/images/vm-test-cloudinit.iso': No such file or directory"
	if !qemuCloudInitMissingStartError(output, nil) {
		t.Fatalf("expected missing cloud-init ISO error to be detected")
	}
	if qemuCloudInitMissingStartError("generic virsh start failure", errors.New("exit 1")) {
		t.Fatalf("generic start failure should not be treated as cloud-init ISO error")
	}
}

func TestQEMUDetachCloudInitISOCommand(t *testing.T) {
	cmd := qemuDetachCloudInitISOCommand("qemu:///system", "vm-one", "/var/lib/libvirt/images/vm-one-cloudinit.iso")
	for _, want := range []string{
		"domblklist",
		"-cloudinit\\.iso",
		"detach-disk",
		"--config",
		"/var/lib/libvirt/images/vm-one-cloudinit.iso",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("detach command missing %q: %s", want, cmd)
		}
	}
}

func TestQEMUListDomainNamesCommandAllowsEmptyList(t *testing.T) {
	cmd := qemuListDomainNamesCommand("qemu:///system")
	for _, want := range []string{
		"virsh -c 'qemu:///system' list --all --name",
		"|| exit $?",
		"awk 'NF {print}'",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("list command missing %q: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "grep -v") {
		t.Fatalf("list command should not use grep -v because empty input exits non-zero: %s", cmd)
	}
}
