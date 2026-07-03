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

func TestValidateImageURLDockerRuntimeRefs(t *testing.T) {
	for _, url := range []string{
		"docker://spiritlhl/wds:2022",
		"docker://redroid/redroid:12.0.0-latest",
		"docker://dockurr/macos:15",
	} {
		if err := validateImageURL("docker", "container", url); err != nil {
			t.Fatalf("docker runtime ref %q should be accepted: %v", url, err)
		}
	}
	if err := validateImageURL("podman", "container", "docker://spiritlhl/wds:2022"); err == nil {
		t.Fatalf("podman should reject docker runtime refs")
	}
}

func TestValidateImageURLInstallerVMs(t *testing.T) {
	if err := validateImageURL("proxmox", "vm", "https://example.com/windows.iso"); err != nil {
		t.Fatalf("proxmox vm should accept ISO installers: %v", err)
	}
	if err := validateImageURL("proxmox", "vm", "https://example.com/sonoma.iso.7z"); err != nil {
		t.Fatalf("proxmox vm should accept compressed ISO installers: %v", err)
	}
	if err := validateImageURL("lxd", "vm", "https://example.com/windows.iso?token=1"); err != nil {
		t.Fatalf("lxd vm should accept Windows ISO installers: %v", err)
	}
	if err := validateImageURL("incus", "vm", "https://example.com/windows.iso/download"); err != nil {
		t.Fatalf("incus vm should accept Windows ISO installers from redirect-style URLs: %v", err)
	}
	if err := validateImageURL("lxd", "container", "https://example.com/rootfs.iso"); err == nil {
		t.Fatalf("lxd container should not accept ISO installers")
	}
}
