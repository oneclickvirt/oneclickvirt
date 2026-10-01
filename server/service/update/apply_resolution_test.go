package update

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func resolutionTestConfig(t *testing.T, current string) runtimeConfig {
	t.Helper()
	cfg := updateStateTestConfig(t)
	cfg.Repo = "example/repo"
	cfg.Flavor = FlavorAllInOne
	cfg.UpdateWeb = false
	cfg.AllowUnverified = false
	if err := os.WriteFile(currentVersionFile(cfg), []byte(current+"\n"), 0640); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func resolutionTestRelease(tag string) githubRelease {
	base := "https://github.com/example/repo/releases/download/" + tag + "/"
	return githubRelease{
		TagName: tag,
		Assets: []githubAsset{
			{Name: "server-allinone-linux-amd64.tar.gz", BrowserDownloadURL: base + "server-allinone-linux-amd64.tar.gz"},
			{Name: checksumAssetName, BrowserDownloadURL: base + checksumAssetName},
		},
	}
}

func cachedResolutionService(cfg runtimeConfig, releases []githubRelease) *Service {
	service := &Service{now: time.Now}
	service.storeReleases(cfg.Repo, releases)
	return service
}

func TestResolveReleaseTargetSelectsLatestCompatibleRelease(t *testing.T) {
	cfg := resolutionTestConfig(t, "v1.0.0")
	service := cachedResolutionService(cfg, []githubRelease{
		resolutionTestRelease("v1.1.0"),
		resolutionTestRelease("v1.0.5"),
	})

	got, err := service.resolveReleaseTargetForPlatform(context.Background(), cfg, "", false, "linux", "amd64")
	if err != nil {
		t.Fatalf("resolve latest release: %v", err)
	}
	if got != "v1.1.0" {
		t.Fatalf("resolved release = %q, want v1.1.0", got)
	}
}

func TestResolveReleaseTargetRejectsCurrentOrOlderUpgrade(t *testing.T) {
	cfg := resolutionTestConfig(t, "v1.1.0")
	service := cachedResolutionService(cfg, []githubRelease{resolutionTestRelease("v1.1.0")})

	if _, err := service.resolveReleaseTargetForPlatform(context.Background(), cfg, "", false, "linux", "amd64"); err == nil {
		t.Fatal("latest release equal to current version was accepted")
	}

	cfg = resolutionTestConfig(t, "v1.2.0")
	service = cachedResolutionService(cfg, []githubRelease{resolutionTestRelease("v1.1.0")})
	if _, err := service.resolveReleaseTargetForPlatform(context.Background(), cfg, "v1.1.0", false, "linux", "amd64"); err == nil {
		t.Fatal("older explicit upgrade target was accepted")
	}
}

func TestResolveReleaseTargetRejectsMissingOrIncompatibleRelease(t *testing.T) {
	cfg := resolutionTestConfig(t, "v1.0.0")
	service := cachedResolutionService(cfg, []githubRelease{resolutionTestRelease("v1.1.0")})

	if _, err := service.resolveReleaseTargetForPlatform(context.Background(), cfg, "v9.9.9", false, "linux", "amd64"); err == nil {
		t.Fatal("nonexistent upgrade target was accepted")
	}

	bad := resolutionTestRelease("v1.2.0")
	bad.Assets[0].Name = "server-allinone-linux-arm64.tar.gz"
	service = cachedResolutionService(cfg, []githubRelease{bad})
	if _, err := service.resolveReleaseTargetForPlatform(context.Background(), cfg, "v1.2.0", false, "linux", "amd64"); err == nil {
		t.Fatal("release without a compatible server asset was accepted")
	}

	cfg = resolutionTestConfig(t, "v2.0.0")
	service = cachedResolutionService(cfg, []githubRelease{resolutionTestRelease("v1.9.0")})
	if _, err := service.resolveReleaseTargetForPlatform(context.Background(), cfg, "invalid target", true, "linux", "amd64"); err == nil {
		t.Fatal("invalid rollback target was accepted")
	}

	badRollback := resolutionTestRelease("v1.8.0")
	badRollback.Assets[0].Name = "server-allinone-linux-arm64.tar.gz"
	service = cachedResolutionService(cfg, []githubRelease{badRollback})
	if _, err := service.resolveReleaseTargetForPlatform(context.Background(), cfg, "v1.8.0", true, "linux", "amd64"); err == nil {
		t.Fatal("rollback without a compatible server asset was accepted")
	}
}

func TestResolveReleaseTargetRejectsReleaseWithoutChecksumManifest(t *testing.T) {
	cfg := resolutionTestConfig(t, "v2.0.0")
	release := resolutionTestRelease("v1.9.0")
	release.Assets = release.Assets[:1]
	service := cachedResolutionService(cfg, []githubRelease{release})

	_, err := service.resolveReleaseTargetForPlatform(context.Background(), cfg, "v1.9.0", true, "linux", "amd64")
	if err == nil || !strings.Contains(err.Error(), checksumAssetName) {
		t.Fatalf("release without checksum manifest error = %v", err)
	}
}
