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
		{"docker://spiritlhl/wds:2022", "windows-2022", "windows", "2022"},
		{"docker://redroid/redroid:11.0.0-latest", "android-11.0.0-latest", "android", "11.0.0-latest"},
		{"docker://dockurr/macos:sonoma", "macos-sonoma", "macos", "sonoma"},
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
		"docker://redroid/redroid:11.0.0-latest",
		"docker://dockurr/macos:sonoma",
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

func TestDefaultImageURLsIncludeDockerRuntimeRefs(t *testing.T) {
	urls := getDefaultImageURLs()
	seen := map[string]bool{}
	for _, url := range urls {
		seen[url] = true
	}
	for _, want := range []string{
		"docker://spiritlhl/wds:2022",
		"docker://redroid/redroid:11.0.0-latest",
		"docker://dockurr/macos:sonoma",
	} {
		if !seen[want] {
			t.Fatalf("default image URL %q not found", want)
		}
	}
}
