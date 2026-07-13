package source

import (
	"testing"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

func init() {
	global.APP_LOG = zap.NewNop()
}

func TestParseDockerRuntimeImageURL(t *testing.T) {
	tests := []struct {
		url       string
		name      string
		osType    string
		osVersion string
	}{
		{"docker://spiritlhl/wds:10", "windows-10", "windows", "10"},
		{"docker://spiritlhl/wds:2022", "windows-2022", "windows", "2022"},
		{"docker://redroid/redroid:12.0.0-latest", "android-12.0.0-latest", "android", "12.0.0-latest"},
		{"docker://dockurr/macos:15", "macos-15", "macos", "15"},
	}

	for _, tt := range tests {
		got := parseImageURL(tt.url)
		if got == nil {
			t.Fatalf("parseImageURL(%q) returned nil", tt.url)
		}
		if got.ProviderType != "docker" || got.InstanceType != "container" || got.Architecture != "amd64" {
			t.Fatalf("parseImageURL(%q) provider/type/arch = %s/%s/%s", tt.url, got.ProviderType, got.InstanceType, got.Architecture)
		}
		if got.Name != tt.name || got.OSType != tt.osType || got.OSVersion != tt.osVersion {
			t.Fatalf("parseImageURL(%q) = name=%s os=%s version=%s", tt.url, got.Name, got.OSType, got.OSVersion)
		}
	}
}

func TestDockerRuntimeImagesDefaultInactiveAndHighRequirement(t *testing.T) {
	images := buildDesiredSystemImages([]string{
		"docker://spiritlhl/wds:2022",
		"docker://redroid/redroid:12.0.0-latest",
		"docker://dockurr/macos:15",
	})
	if len(images) != 3 {
		t.Fatalf("len(images) = %d, want 3", len(images))
	}

	for _, img := range images {
		if img.Status != "inactive" {
			t.Fatalf("%s status = %s, want inactive", img.Name, img.Status)
		}
		switch img.OSType {
		case "windows", "macos":
			if img.MinMemoryMB < 6144 || img.MinDiskMB < 40960 {
				t.Fatalf("%s requirement = %d/%d, want high desktop-class limits", img.Name, img.MinMemoryMB, img.MinDiskMB)
			}
		case "android":
			if img.MinMemoryMB < 2048 || img.MinDiskMB < 15360 {
				t.Fatalf("%s requirement = %d/%d, want Android runtime limits", img.Name, img.MinMemoryMB, img.MinDiskMB)
			}
		default:
			t.Fatalf("unexpected OS type: %s", img.OSType)
		}
	}
}

func TestLXDAlpineVMDefaultsInactiveWhileSupportedImagesStayActive(t *testing.T) {
	images := buildDesiredSystemImages([]string{
		"https://github.com/oneclickvirt/lxd_images/releases/download/kvm_images/alpine_3.19_3.19_amd64_cloud_kvm.zip",
		"https://github.com/oneclickvirt/lxd_images/releases/download/alpine/alpine_3.21_3.21_amd64_cloud.zip",
		"https://github.com/oneclickvirt/lxd_images/releases/download/kvm_images/debian_12_bookworm_amd64_cloud_kvm.zip",
	})

	statuses := map[string]string{}
	for _, img := range images {
		statuses[img.ProviderType+"/"+img.InstanceType+"/"+img.OSType] = img.Status
	}

	if got := statuses["lxd/vm/alpine"]; got != "inactive" {
		t.Fatalf("lxd/vm/alpine status = %q, want inactive", got)
	}
	if got := statuses["lxd/container/alpine"]; got != "active" {
		t.Fatalf("lxd/container/alpine status = %q, want active", got)
	}
	if got := statuses["lxd/vm/debian"]; got != "active" {
		t.Fatalf("lxd/vm/debian status = %q, want active", got)
	}
}

func TestParseInstallerImageURLs(t *testing.T) {
	tests := []struct {
		url          string
		providerType string
		instanceType string
		osType       string
		osVersion    string
	}{
		{
			url:          "https://download.testip.xyz/Windows-VirtIO/virtio_zh-cn_windows_server_2019_x64_dvd_19d65722.iso",
			providerType: "proxmox",
			instanceType: "vm",
			osType:       "windows",
			osVersion:    "2019",
		},
		{
			url:          "https://github.com/oneclickvirt/macos/releases/download/images/sonoma.iso.7z",
			providerType: "proxmox",
			instanceType: "vm",
			osType:       "macos",
			osVersion:    "sonoma",
		},
		{
			url:          "https://mirrors.tuna.tsinghua.edu.cn/osdn/android-x86/71931/android-x86_64-9.0-r2.iso",
			providerType: "proxmox",
			instanceType: "vm",
			osType:       "android",
			osVersion:    "9.0-r2",
		},
		{
			url:          "https://sourceforge.net/projects/blissos-x86/files/Official/BlissOS15/Gapps/Generic/Bliss-v15.9.2-x86_64-OFFICIAL-gapps-20241012.iso/download",
			providerType: "proxmox",
			instanceType: "vm",
			osType:       "android",
			osVersion:    "15.9.2",
		},
	}

	for _, tt := range tests {
		got := parseImageURL(tt.url)
		if got == nil {
			t.Fatalf("parseImageURL(%q) returned nil", tt.url)
		}
		if got.ProviderType != tt.providerType || got.InstanceType != tt.instanceType || got.OSType != tt.osType || got.OSVersion != tt.osVersion {
			t.Fatalf("parseImageURL(%q) = provider=%s type=%s os=%s version=%s", tt.url, got.ProviderType, got.InstanceType, got.OSType, got.OSVersion)
		}
	}
}

func TestOrdinaryWindowsInstallerAddsLXDAndIncusVMImages(t *testing.T) {
	images := buildDesiredSystemImages([]string{
		"https://download.testip.xyz/windows/zh-cn_windows_server_2019_x64_dvd_19d65722.iso",
	})
	seen := map[string]bool{}
	for _, img := range images {
		seen[img.ProviderType+"/"+img.InstanceType+"/"+img.OSType] = true
	}
	for _, want := range []string{
		"proxmox/vm/windows",
		"lxd/vm/windows",
		"incus/vm/windows",
	} {
		if !seen[want] {
			t.Fatalf("expected derived image %s, got %#v", want, seen)
		}
	}
}

func TestBuildDesiredSystemImagesDeduplicatesSameURLWithinNodeType(t *testing.T) {
	url := "https://github.com/oneclickvirt/incus_images/releases/download/images/debian_12_rootfs_x86_64_cloud.zip"
	images := buildDesiredSystemImages([]string{url, url})
	if len(images) != 1 {
		t.Fatalf("len(images) = %d, want 1; images=%#v", len(images), images)
	}
	if images[0].ProviderType != "incus" || images[0].InstanceType != "container" || images[0].Architecture != "amd64" {
		t.Fatalf("unexpected image identity: %s/%s/%s", images[0].ProviderType, images[0].InstanceType, images[0].Architecture)
	}
}

func TestBuildDesiredSystemImagesDeduplicatesSameSystemVersionWithinNodeType(t *testing.T) {
	images := buildDesiredSystemImages([]string{
		"https://github.com/oneclickvirt/incus_images/releases/download/images/debian_12_rootfs_x86_64_cloud.zip",
		"https://github.com/oneclickvirt/incus_images/releases/download/images2/debian_12_rootfs_x86_64_cloud2.zip",
	})
	if len(images) != 1 {
		t.Fatalf("len(images) = %d, want 1; images=%#v", len(images), images)
	}
	if images[0].OSType != "debian" || images[0].OSVersion != "12" {
		t.Fatalf("unexpected OS identity: %s/%s", images[0].OSType, images[0].OSVersion)
	}
}

func TestDefaultImageURLsIncludeDockerRuntimeRefs(t *testing.T) {
	urls := getDefaultImageURLs()
	seen := map[string]bool{}
	for _, url := range urls {
		seen[url] = true
	}
	for _, want := range []string{
		"https://download.testip.xyz/Windows-VirtIO/virtio_zh-cn_windows_server_2019_x64_dvd_19d65722.iso",
		"https://github.com/oneclickvirt/macos/releases/download/images/sonoma.iso.7z",
		"https://mirrors.tuna.tsinghua.edu.cn/osdn/android-x86/71931/android-x86_64-9.0-r2.iso",
		"https://sourceforge.net/projects/blissos-x86/files/Official/BlissOS15/Gapps/Generic/Bliss-v15.9.2-x86_64-OFFICIAL-gapps-20241012.iso/download",
		"docker://spiritlhl/wds:10",
		"docker://spiritlhl/wds:2019",
		"docker://spiritlhl/wds:2022",
		"docker://redroid/redroid:8.1.0-latest",
		"docker://redroid/redroid:9.0.0-latest",
		"docker://redroid/redroid:10.0.0-latest",
		"docker://redroid/redroid:11.0.0-latest",
		"docker://redroid/redroid:12.0.0-latest",
		"docker://dockurr/macos:11",
		"docker://dockurr/macos:12",
		"docker://dockurr/macos:13",
		"docker://dockurr/macos:14",
		"docker://dockurr/macos:15",
	} {
		if !seen[want] {
			t.Fatalf("default image URL %q not found", want)
		}
	}
}

func TestMergeDefaultImageURLsBackfillsNewBuiltInTypes(t *testing.T) {
	remoteURL := "https://github.com/oneclickvirt/pve_kvm_images/releases/download/images/debian-12.qcow2"
	urls := mergeDefaultImageURLs([]string{remoteURL})

	if len(urls) == 0 || urls[0] != remoteURL {
		t.Fatalf("remote URL order not preserved: %#v", urls)
	}

	seen := map[string]bool{}
	for _, url := range urls {
		seen[url] = true
	}
	for _, want := range []string{
		"docker://spiritlhl/wds:2022",
		"docker://redroid/redroid:12.0.0-latest",
		"docker://dockurr/macos:15",
		"https://download.testip.xyz/Windows-VirtIO/virtio_zh-cn_windows_server_2019_x64_dvd_19d65722.iso",
	} {
		if !seen[want] {
			t.Fatalf("merged default image URL %q not found", want)
		}
	}
}

func TestMergeDefaultImageURLsDeduplicatesExactURLs(t *testing.T) {
	defaultURL := "docker://spiritlhl/wds:2022"
	urls := mergeDefaultImageURLs([]string{defaultURL, defaultURL})
	count := 0
	for _, url := range urls {
		if url == defaultURL {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("URL %q count = %d, want 1", defaultURL, count)
	}
}
