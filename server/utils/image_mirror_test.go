package utils

import (
	"os"
	"testing"
)

func TestImageMirrorURLCanonicalPaths(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"pve-kvm", "https://github.com/oneclickvirt/pve_kvm_images/releases/download/images/debian12.qcow2", "https://www.spiritlhl.com/pve/kvm/debian/amd64/debian12.qcow2"},
		{"pve-lxc-arm", "https://github.com/oneclickvirt/lxc_arm_images/releases/download/debian/debian_12_bookworm_arm64_cloud.tar.xz", "https://www.spiritlhl.com/pve/lxc/debian/arm64/debian_12_bookworm_arm64_cloud.tar.xz"},
		{"incus-kvm", "https://github.com/oneclickvirt/incus_images/releases/download/kvm_images/debian_12_bookworm_x86_64_cloud_kvm.zip", "https://www.spiritlhl.com/incus/kvm/debian/amd64/debian_12_bookworm_x86_64_cloud_kvm.zip"},
		{"runtime", "https://github.com/oneclickvirt/docker/releases/download/debian/spiritlhl_debian_arm64.tar.gz", "https://www.spiritlhl.com/runtime/docker/debian/arm64/spiritlhl_debian_arm64.tar.gz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ImageMirrorURL(tt.url); got != tt.want {
				t.Fatalf("ImageMirrorURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestImageMirrorURLCanBeDisabledAndConfigured(t *testing.T) {
	t.Setenv("ONECLICKVIRT_IMAGE_MIRROR_URL", "https://mirror.example.test/base/")
	if got := ImageMirrorURL("https://github.com/oneclickvirt/kvm_images/releases/download/v2.0/debian12.qcow2"); got != "https://mirror.example.test/base/pve/kvm/debian/amd64/debian12.qcow2" {
		t.Fatalf("configured mirror URL = %q", got)
	}
	t.Setenv("ONECLICKVIRT_IMAGE_MIRROR_DISABLE", "true")
	if got := ImageMirrorURL("https://github.com/oneclickvirt/kvm_images/releases/download/v2.0/debian12.qcow2"); got != "" {
		t.Fatalf("disabled mirror URL = %q", got)
	}
	_ = os.Unsetenv("ONECLICKVIRT_IMAGE_MIRROR_DISABLE")
}

func TestImageDownloadCandidatesPreserveFallbackOrder(t *testing.T) {
	got := ImageDownloadCandidates("https://github.com/oneclickvirt/kvm_images/releases/download/v2.0/debian12.qcow2", "https://cdn.example.test/original", "https://github.com/oneclickvirt/kvm_images/releases/download/v2.0/debian12.qcow2")
	want := []string{
		"https://www.spiritlhl.com/pve/kvm/debian/amd64/debian12.qcow2",
		"https://cdn.example.test/original",
		"https://github.com/oneclickvirt/kvm_images/releases/download/v2.0/debian12.qcow2",
	}
	if len(got) != len(want) {
		t.Fatalf("candidate count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
