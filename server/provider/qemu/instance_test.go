package qemu

import (
	"strings"
	"testing"
)

func TestQEMUCloudInitUserDataUnlocksRootPasswordLogin(t *testing.T) {
	userData := qemuCloudInitUserData("vm-1", "Passw0rd!\n")

	required := []string{
		"disable_root: false",
		"ssh_pwauth: true",
		"users:",
		"- name: root",
		"lock_passwd: false",
		"PermitRootLogin yes",
		"PasswordAuthentication yes",
		"Passw0rd!",
	}
	for _, item := range required {
		if !strings.Contains(userData, item) {
			t.Fatalf("cloud-init user-data missing %q:\n%s", item, userData)
		}
	}
	if strings.Contains(userData, "\nPassw0rd!\n") {
		t.Fatalf("cloud-init user-data should strip password newlines:\n%s", userData)
	}
}

func TestQEMUCloudInitAvoidsDeprecatedPasswordSchema(t *testing.T) {
	data := qemuCloudInitUserData("vm", "a:b'c")
	if strings.Contains(data, "chpasswd:\n") {
		t.Fatal("deprecated password list must not be emitted")
	}
	if !strings.Contains(data, "| chpasswd") {
		t.Fatal("password initialization missing")
	}
}

func TestQEMUBIOSBootArgs(t *testing.T) {
	for _, arch := range []string{"x86_64\n", "AMD64", "i686"} {
		if got := qemuBIOSBootArgs(arch); got != "--boot uefi=off --features smm=off" {
			t.Fatalf("unexpected BIOS args for %q: %q", arch, got)
		}
	}
	for _, arch := range []string{"aarch64", "arm64", ""} {
		if got := qemuBIOSBootArgs(arch); got != "" {
			t.Fatalf("unexpected BIOS override for %q", arch)
		}
	}
}
