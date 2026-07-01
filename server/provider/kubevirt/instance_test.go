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

func TestKubeVirtSSHServiceYAMLUsesLauncherDomainSelector(t *testing.T) {
	yaml := kubeVirtSSHServiceYAML("vm-1", 30122)

	if !strings.Contains(yaml, "name: \"vm-1-ssh\"") {
		t.Fatalf("SSH service YAML should name the service after the VM, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "kubevirt.io/domain: \"vm-1\"") {
		t.Fatalf("SSH service YAML should select KubeVirt launcher pods by domain label, got:\n%s", yaml)
	}
	if strings.Contains(yaml, "kubevirt.io/vm:") {
		t.Fatalf("SSH service YAML should not use the unstable kubevirt.io/vm selector, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "nodePort: 30122") {
		t.Fatalf("SSH service YAML should preserve the allocated nodePort, got:\n%s", yaml)
	}
}
