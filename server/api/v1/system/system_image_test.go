package system

import "testing"

func TestValidateImageURLQEMUContainerRootfs(t *testing.T) {
	if err := validateImageURL("qemu", "container", "https://example.com/rootfs.tar.xz"); err != nil {
		t.Fatalf("qemu container rootfs tar.xz should be accepted: %v", err)
	}
	if err := validateImageURL("qemu", "container", "https://example.com/docker.tar.gz"); err == nil {
		t.Fatalf("qemu container docker tar.gz should be rejected")
	}
}

func TestValidateImageURLKubeVirtContainerArchive(t *testing.T) {
	if err := validateImageURL("kubevirt", "container", "https://example.com/docker.tar.gz"); err != nil {
		t.Fatalf("kubevirt container tar.gz should be accepted: %v", err)
	}
	if err := validateImageURL("kubevirt", "container", "https://example.com/rootfs.tar.xz"); err == nil {
		t.Fatalf("kubevirt container tar.xz should be rejected")
	}
}
