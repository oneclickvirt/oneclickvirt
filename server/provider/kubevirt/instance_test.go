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
