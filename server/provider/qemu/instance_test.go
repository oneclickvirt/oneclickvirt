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
		"root:Passw0rd!",
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
