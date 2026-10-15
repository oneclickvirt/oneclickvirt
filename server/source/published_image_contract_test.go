package source

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oneclickvirt/global"
	"oneclickvirt/model/system"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPublishedImageNamesAndArchitectureAliasesPreserveIdentity(t *testing.T) {
	cases := []struct{ repo, tag, file, provider, kind, arch, os, version string }{
		{"lxd_images", "ubuntu", "ubuntu_26.04_resolute_amd64_cloud.zip", "lxd", "container", "amd64", "ubuntu", "26.04"},
		{"lxd_images", "kvm_images", "ubuntu_24.04_noble_amd64_cloud_kvm.zip", "lxd", "vm", "amd64", "ubuntu", "24.04"},
		{"lxd_images", "kvm_images", "alpine_3.24_3.24_arm64_default_kvm.zip", "lxd", "vm", "arm64", "alpine", "3.24"},
		{"lxc_arm_images", "opensuse", "opensuse_16.0_16.0_arm64_cloud.tar.xz", "proxmox", "container", "arm64", "opensuse", "16.0"},
		// Accept architecture aliases used by other builders as well.
		{"lxd_images", "ubuntu", "ubuntu_26.04_resolute_x86_64_cloud.zip", "lxd", "container", "amd64", "ubuntu", "26.04"},
		{"lxd_images", "kvm_images", "ubuntu_24.04_noble_x86_64_cloud_kvm.zip", "lxd", "vm", "amd64", "ubuntu", "24.04"},
		{"lxd_images", "kvm_images", "alpine_3.24_3.24_aarch64_default_kvm.zip", "lxd", "vm", "arm64", "alpine", "3.24"},
		{"incus_images", "ubuntu", "ubuntu_26.04_resolute_arm64_cloud.zip", "incus", "container", "arm64", "ubuntu", "26.04"},
		{"incus_images", "kvm_images", "rockylinux_9_9_x86_64_default_kvm.zip", "incus", "vm", "amd64", "rockylinux", "9"},
		{"lxc_amd64_images", "almalinux", "almalinux_10_10_x86_64_cloud.tar.xz", "proxmox", "container", "amd64", "almalinux", "10"},
		{"lxc_arm_images", "opensuse", "opensuse_16.0_16.0_aarch64_cloud.tar.xz", "proxmox", "container", "arm64", "opensuse", "16.0"},
		{"pve_lxc_images", "maintained", "debian_13_trixie_x86_64_default_20261015_amd64.tar.zst", "proxmox", "container", "amd64", "debian", "13"},
		{"pve_kvm_images", "ubuntu", "ubuntu2404.qcow2", "proxmox", "vm", "amd64", "ubuntu", "24.04"},
		{"pve_kvm_images", "images", "ubuntu24.qcow2", "proxmox", "vm", "amd64", "ubuntu", "24.04"},
		{"kvm_images", "v2.0", "debian12.qcow2", "proxmox", "vm", "amd64", "debian", "12"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			url := "https://github.com/oneclickvirt/" + tc.repo + "/releases/download/" + tc.tag + "/" + tc.file
			got := parseImageURL(url)
			if got == nil {
				t.Fatalf("published filename or supported architecture alias not recognized: %s", url)
			}
			if got.ProviderType != tc.provider || got.InstanceType != tc.kind || got.Architecture != tc.arch || got.OSType != tc.os || got.OSVersion != tc.version {
				t.Fatalf("got %s/%s/%s/%s/%s, want %s/%s/%s/%s/%s", got.ProviderType, got.InstanceType, got.Architecture, got.OSType, got.OSVersion, tc.provider, tc.kind, tc.arch, tc.os, tc.version)
			}
		})
	}
}

func TestPublishedImagesPreferMaintainedFamilyRelease(t *testing.T) {
	old := "https://github.com/oneclickvirt/pve_kvm_images/releases/download/images/rockylinux9.qcow2"
	current := "https://github.com/oneclickvirt/pve_kvm_images/releases/download/rockylinux/rockylinux9.qcow2"
	images := buildDesiredSystemImages(prioritizeCloudImages([]string{old, current}))
	if len(images) != 3 {
		t.Fatalf("expected proxmox/qemu/kubevirt images, got %d", len(images))
	}
	for _, image := range images {
		if image.URL != current {
			t.Errorf("%s selected historical release %s over maintained %s", image.ProviderType, image.URL, current)
		}
	}
}

func TestPublishedImagesExcludeArchiveAndUnpromotedAssets(t *testing.T) {
	for _, url := range []string{
		"https://github.com/oneclickvirt/kvm_images/releases/download/legacy-archived/rockylinux8-legacy-119744446.qcow2",
		"https://github.com/oneclickvirt/incus_images/releases/download/ubuntu/ubuntu_24.04_noble_x86_64_cloud.zip.ci-stage-123",
		"https://github.com/oneclickvirt/pve_kvm_images/releases/download/debian/debian12.qcow2.ci-backup-456",
	} {
		if images := buildDesiredSystemImages([]string{url}); len(images) != 0 {
			t.Errorf("archived or unpublished asset advertised: %s", url)
		}
	}
}

func TestSeededImageURLRepairPreservesUserData(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "images.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&system.SystemImage{}); err != nil {
		t.Fatal(err)
	}
	old := "https://github.com/oneclickvirt/kvm_images/releases/download/ubuntu24/ubuntu24.qcow2"
	broken := "https://github.com/oneclickvirt/kvm_images/releases/download/centos8-stream/centos8-stream.qcow2"
	user := uint(42)
	rows := []system.SystemImage{
		{Name: "builtin", URL: old, ProviderType: "proxmox", InstanceType: "vm", Architecture: "amd64", OSType: "ubuntu", OSVersion: "24", Status: "inactive", MinMemoryMB: 8192, Checksum: "old-digest", Size: 123},
		{Name: "custom", URL: old, ProviderType: "qemu", InstanceType: "vm", Architecture: "amd64", OSType: "ubuntu", OSVersion: "24", Status: "active", CreatedBy: &user},
		{Name: "unavailable", URL: broken, ProviderType: "proxmox", InstanceType: "vm", Architecture: "amd64", OSType: "centos", OSVersion: "8", Status: "active"},
		{Name: "historical", URL: "https://github.com/oneclickvirt/pve_kvm_images/releases/download/images/ubuntu22.qcow2", ProviderType: "kubevirt", InstanceType: "vm", Architecture: "amd64", OSType: "ubuntu", OSVersion: "22", Status: "active"},
		{Name: "split-architecture", URL: "https://github.com/oneclickvirt/lxd_images/releases/download/kvm_images/ubuntu_24.04_noble_x86_64_cloud_kvm.zip", ProviderType: "lxd", InstanceType: "container", Architecture: "x86", OSType: "ubuntu", OSVersion: "24.04.noble.x86.64", Status: "active"},
		{Name: "compact-version", URL: "https://github.com/oneclickvirt/pve_kvm_images/releases/download/ubuntu/ubuntu2404.qcow2", ProviderType: "qemu", InstanceType: "vm", Architecture: "amd64", OSType: "ubuntu", OSVersion: "2404", Status: "active"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := repairSeededImageURLs(db, nil); err != nil {
			t.Fatal(err)
		}
	}
	var got []system.SystemImage
	if err := db.Order("id").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 || got[0].ID != rows[0].ID || got[0].UUID != rows[0].UUID || got[0].Status != "inactive" || got[0].MinMemoryMB != 8192 || got[0].OSVersion != "24.04" || got[0].Checksum != "" || got[0].Size != 0 || got[0].URL != "https://github.com/oneclickvirt/pve_kvm_images/releases/download/ubuntu/ubuntu2404.qcow2" {
		t.Fatalf("builtin migration changed identity or settings: %#v", got)
	}
	if got[1].URL != old || got[1].OSVersion != "24" || got[1].Status != "active" {
		t.Fatalf("custom image changed: %#v", got[1])
	}
	if got[2].URL != broken || got[2].Status != "inactive" {
		t.Fatalf("missing release was not disabled: %#v", got[2])
	}
	if got[3].URL != "https://github.com/oneclickvirt/pve_kvm_images/releases/download/ubuntu/ubuntu2204.qcow2" || got[3].Status != "active" || got[3].OSVersion != "22.04" {
		t.Fatalf("maintained release migration failed: %#v", got[3])
	}
	if got[4].Architecture != "amd64" || got[4].InstanceType != "vm" || got[4].OSVersion != "24.04" || got[4].Status != "active" {
		t.Fatalf("old architecture metadata not repaired: %#v", got[4])
	}
	if got[5].OSVersion != "24.04" {
		t.Fatalf("compact version not normalized: %#v", got[5])
	}

}

func TestPublishedUbuntuVersionsDeduplicateAcrossReleaseNames(t *testing.T) {
	old := "https://github.com/oneclickvirt/pve_kvm_images/releases/download/images/ubuntu24.qcow2"
	current := "https://github.com/oneclickvirt/pve_kvm_images/releases/download/ubuntu/ubuntu2404.qcow2"
	images := buildDesiredSystemImages([]string{old, current})
	if len(images) != 3 {
		t.Fatalf("expected one image for each VM provider, got %d", len(images))
	}
	for _, image := range images {
		if image.URL != current || image.OSVersion != "24.04" {
			t.Fatalf("incorrect Ubuntu selection: %#v", image)
		}
	}
}

// CI supplies a freshly downloaded public catalogue. Ordinary unit tests use
// fixed fixtures and do not require the network.
func TestPublishedImageCatalogContract(t *testing.T) {
	filename := os.Getenv("ONECLICKVIRT_IMAGE_CATALOG_FILE")
	if filename == "" {
		t.Skip("live catalogue supplied by the image catalogue workflow")
	}
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	repos := map[string]int{"incus_images": 0, "lxd_images": 0, "lxc_amd64_images": 0, "lxc_arm_images": 0, "kvm_images": 0, "pve_kvm_images": 0, "pve_lxc_images": 0}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		imageURL := strings.TrimSpace(scanner.Text())
		for repo := range repos {
			if !strings.HasPrefix(imageURL, "https://github.com/oneclickvirt/"+repo+"/releases/download/") {
				continue
			}
			imageAsset := false
			for _, ext := range []string{".qcow2", ".zip", ".tar.xz", ".tar.zst"} {
				if strings.HasSuffix(imageURL, ext) {
					imageAsset = true
				}
			}
			if isUnpublishedSystemImageURL(imageURL) {
				t.Errorf("unpromoted or archived asset in catalogue: %s", imageURL)
				continue
			}
			if !imageAsset {
				continue
			}
			repos[repo]++
			got := parseImageURL(imageURL)
			if got == nil {
				t.Errorf("published asset not recognized: %s", imageURL)
				continue
			}
			if got.Architecture != "amd64" && got.Architecture != "arm64" {
				t.Errorf("invalid architecture %s: %s", got.Architecture, imageURL)
			}
			if isUnresolvedSystemImageOS(got.OSType) || got.OSVersion == "unknown" || got.OSVersion == "" {
				t.Errorf("invalid OS identity: %#v", got)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for repo, count := range repos {
		if count == 0 {
			t.Errorf("catalogue has no images from %s", repo)
		}
		t.Logf("%s: %d image assets", repo, count)
	}
}

func TestMaintainedPVETemplatesPreferNewestAndPreserveSettings(t *testing.T) {
	prefix := "https://github.com/oneclickvirt/pve_lxc_images/releases/download/maintained/"
	old := prefix + "ubuntu_24.04_noble_x86_64_default_20261014_amd64.tar.zst"
	current := prefix + "ubuntu_24.04_noble_x86_64_default_20261015_amd64.tar.zst"
	images := buildDesiredSystemImages([]string{old, current})
	if len(images) != 2 || images[0].URL != current || images[1].URL != current {
		t.Fatalf("old daily template selected: %#v", images)
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "templates.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&system.SystemImage{}); err != nil {
		t.Fatal(err)
	}
	row := images[0]
	row.URL = old
	row.Status = "inactive"
	row.Name = "administrator label"
	row.MinMemoryMB = 4096
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := repairSeededImageURLs(db, images); err != nil {
		t.Fatal(err)
	}
	var got system.SystemImage
	if err := db.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.URL != current || got.Status != "inactive" || got.Name != row.Name || got.ID != row.ID || got.MinMemoryMB != 4096 {
		t.Fatalf("template settings changed: %#v", got)
	}
}

func TestSyncRepairsExistingSeedBeforeDeduplication(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "sync.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&system.SystemImage{}); err != nil {
		t.Fatal(err)
	}
	previousDB, previousConfig := global.APP_DB, global.GetAppConfig()
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previousDB; global.SetAppConfig(previousConfig) })
	current := "https://github.com/oneclickvirt/pve_kvm_images/releases/download/ubuntu/ubuntu2404.qcow2"
	row := system.SystemImage{Name: "saved", URL: "https://github.com/oneclickvirt/kvm_images/releases/download/ubuntu24/ubuntu24.qcow2", ProviderType: "proxmox", InstanceType: "vm", Architecture: "amd64", OSType: "ubuntu", OSVersion: "24", Status: "inactive", MinMemoryMB: 8192}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(current + "\n")) }))
	defer server.Close()
	for i := 0; i < 2; i++ {
		result, err := SyncSystemImagesFromURL(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		want := 2
		if i == 1 {
			want = 0
		}
		if result.Processed != want || result.Desired != 3 {
			t.Fatalf("non-idempotent synchronization: %#v", result)
		}
	}
	var got system.SystemImage
	if err := db.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.URL != current || got.Status != "inactive" || got.MinMemoryMB != 8192 || got.OSVersion != "24.04" {
		t.Fatalf("existing seed not safely repaired: %#v", got)
	}
}

func TestSeededAndroidMirrorRepair(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "android.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&system.SystemImage{}); err != nil {
		t.Fatal(err)
	}
	old := "https://mirrors.tuna.tsinghua.edu.cn/osdn/android-x86/71931/android-x86_64-9.0-r2.iso"
	row := system.SystemImage{Name: "android", URL: old, ProviderType: "proxmox", InstanceType: "vm", Architecture: "amd64", OSType: "android", OSVersion: "9.0-r2", Status: "inactive"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := repairSeededImageURLs(db, nil); err != nil {
		t.Fatal(err)
	}
	var got system.SystemImage
	if err := db.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.URL == old || !strings.HasPrefix(got.URL, "https://sourceforge.net/projects/android-x86/") || got.OSVersion != "9.0-r2" || got.Status != "inactive" {
		t.Fatalf("retired mirror not repaired: %#v", got)
	}
}
