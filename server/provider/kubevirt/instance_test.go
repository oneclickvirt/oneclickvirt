package kubevirt

import (
	"strings"
	"testing"
)

func TestKubeVirtDataVolumeYAMLUsesDefaultStorageClass(t *testing.T) {
	yaml := kubeVirtDataVolumeYAML("vm-1-dv", "vm-1", "https://example.com/image.qcow2", 20, "")

	if !strings.Contains(yaml, "storageClassName: \"local-path\"") {
		t.Fatalf("DataVolume YAML should use default local-path storage class, got:\n%s", yaml)
	}
	if strings.Contains(yaml, "storage.bind.immediate.requested") {
		t.Fatalf("DataVolume YAML should not request immediate binding for local-path:\n%s", yaml)
	}
}

func TestKubeVirtDataVolumeYAMLUsesExplicitStorageClass(t *testing.T) {
	yaml := kubeVirtDataVolumeYAML("vm-1-dv", "vm-1", "https://example.com/image.qcow2", 20, "fast-local")

	if !strings.Contains(yaml, "storageClassName: \"fast-local\"") {
		t.Fatalf("DataVolume YAML should use explicit storage class, got:\n%s", yaml)
	}
}

func TestKubeVirtSSHServiceYAMLUsesStableVMNameSelector(t *testing.T) {
	yaml := kubeVirtSSHServiceYAML("vm-1", 30122)

	if !strings.Contains(yaml, "name: \"vm-1-ssh\"") {
		t.Fatalf("SSH service YAML should name the service after the VM, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "vm.kubevirt.io/name: \"vm-1\"") {
		t.Fatalf("SSH service YAML should select KubeVirt launcher pods by VM name label, got:\n%s", yaml)
	}
	if strings.Contains(yaml, "kubevirt.io/domain:") || strings.Contains(yaml, "kubevirt.io/vm:") {
		t.Fatalf("SSH service YAML should not use KubeVirt version-dependent selector labels, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "nodePort: 30122") {
		t.Fatalf("SSH service YAML should preserve the allocated nodePort, got:\n%s", yaml)
	}
}

func TestKubeVirtVMCloudInitUserDataUnlocksRootPasswordLogin(t *testing.T) {
	userData := kubeVirtVMCloudInitUserData("vm-1", "Passw0rd!\n")

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
