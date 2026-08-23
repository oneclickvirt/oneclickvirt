package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequiredAssetsRequireChecksumManifest(t *testing.T) {
	release := githubRelease{Assets: []githubAsset{
		{Name: "server-allinone-linux-amd64.tar.gz", BrowserDownloadURL: "https://github.com/example/server"},
	}}
	cfg := runtimeConfig{Flavor: FlavorAllInOne, UpdateWeb: false}
	if _, err := requiredAssetsFor(release, cfg, "linux", "amd64"); err == nil {
		t.Fatal("release without SHA256SUMS was accepted")
	}
	release.Assets = append(release.Assets, githubAsset{Name: checksumAssetName, BrowserDownloadURL: "https://github.com/example/checksums"})
	assets, err := requiredAssetsFor(release, cfg, "linux", "amd64")
	if err != nil {
		t.Fatalf("release with checksum manifest rejected: %v", err)
	}
	if assets["checksums"].Name != checksumAssetName {
		t.Fatalf("checksum asset = %q", assets["checksums"].Name)
	}
}

func TestParseChecksumManifest(t *testing.T) {
	serverDigest := strings.Repeat("a", 64)
	webDigest := strings.Repeat("b", 64)
	manifest := []byte(serverDigest + "  server-linux-amd64.tar.gz\n" + webDigest + " *web-dist.zip\n")
	got, err := parseChecksumManifest(manifest, "server-linux-amd64.tar.gz", "web-dist.zip")
	if err != nil {
		t.Fatalf("parse checksum manifest: %v", err)
	}
	if got["server-linux-amd64.tar.gz"] != serverDigest || got["web-dist.zip"] != webDigest {
		t.Fatalf("unexpected manifest entries: %#v", got)
	}
	if _, err := parseChecksumManifest([]byte(serverDigest+"  ../server-linux-amd64.tar.gz\n"), "server-linux-amd64.tar.gz"); err == nil {
		t.Fatal("unsafe checksum filename was accepted")
	}
}

func TestVerifyDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	const digest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if err := verifyDigest(path, digest, false); err != nil {
		t.Fatalf("verify digest: %v", err)
	}
	if err := verifyDigest(path, strings.Repeat("0", 64), false); err == nil {
		t.Fatal("incorrect digest was accepted")
	}
}

func TestVersionOrderingAndStableSelection(t *testing.T) {
	if compareVersions("v20260822-120000", "v20260821-235959") <= 0 {
		t.Fatal("date-style release versions were not ordered")
	}
	if compareVersions("v1.10.0", "v1.9.9") <= 0 {
		t.Fatal("semantic versions were not ordered")
	}
	release, ok := latestStableRelease([]githubRelease{
		{TagName: "v2.0.0-rc1", Prerelease: true},
		{TagName: "v1.9.0"},
	})
	if !ok || release.TagName != "v1.9.0" {
		t.Fatalf("stable release selection = %#v, %t", release, ok)
	}
}

func TestRequiredAssetsRejectUnsupportedArchitecture(t *testing.T) {
	release := githubRelease{Assets: []githubAsset{
		{Name: "server-linux-riscv64.tar.gz", BrowserDownloadURL: "https://github.com/example/server"},
	}}
	if _, err := requiredAssetsFor(release, runtimeConfig{Flavor: FlavorStandalone}, "linux", "riscv64"); err == nil {
		t.Fatal("unsupported architecture was accepted")
	}
}

func TestAllowedRemoteURL(t *testing.T) {
	cfg := runtimeConfig{CDNEndpoints: []string{"https://cdn.example.invalid"}}
	if !isAllowedRemoteURL("https://github.com/oneclickvirt/oneclickvirt/releases", cfg) {
		t.Fatal("known GitHub host was rejected")
	}
	if !isAllowedRemoteURL("https://cdn.example.invalid/https://github.com/release", cfg) {
		t.Fatal("configured CDN host was rejected")
	}
	for _, value := range []string{"https://untrusted.example.invalid/release", "https://token@github.com/release", "http://github.com/release"} {
		if isAllowedRemoteURL(value, cfg) {
			t.Fatalf("unsafe remote URL accepted: %s", value)
		}
	}
}
