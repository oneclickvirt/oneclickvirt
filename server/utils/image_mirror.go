package utils

import (
	"net/url"
	"os"
	"path"
	"strings"
)

// ImageMirrorURL maps a oneclickvirt GitHub Release image to the canonical
// public mirror path. Unknown URLs return an empty string so callers retain
// their existing CDN/upstream behavior.
func imageMirrorBaseURL() string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("ONECLICKVIRT_IMAGE_MIRROR_URL")), "/")
	if base == "" {
		base = "https://www.spiritlhl.com"
	}
	return base
}

func ImageMirrorURL(originalURL string) string {
	base := imageMirrorBaseURL()
	if strings.EqualFold(os.Getenv("ONECLICKVIRT_IMAGE_MIRROR_DISABLE"), "true") {
		return ""
	}
	if index := strings.Index(originalURL, "https://cnb.cool/oneclickvirt/template/-/"); index >= 0 {
		parsed, err := url.Parse(originalURL[index:])
		if err != nil {
			return ""
		}
		if strings.Contains(parsed.Path, "/git/raw/") && strings.Contains(parsed.Path, "/macos/") {
			asset := path.Base(parsed.Path)
			if strings.HasSuffix(asset, ".iso.7z") {
				return base + "/pve/kvm/macos/amd64/" + url.PathEscape(asset)
			}
		}
		if strings.Contains(parsed.Path, "/-/lfs/") {
			return base + "/__mirror/resolve?source=" + url.QueryEscape(parsed.String())
		}
		return ""
	}

	githubURL := originalURL
	if index := strings.Index(githubURL, "https://github.com/oneclickvirt/"); index >= 0 {
		githubURL = githubURL[index:]
	}
	parsed, err := url.Parse(githubURL)
	if err != nil || !strings.EqualFold(parsed.Host, "github.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 6 || parts[0] != "oneclickvirt" || parts[2] != "releases" || parts[3] != "download" {
		return ""
	}
	repo, tag, asset := parts[1], parts[4], path.Base(strings.Join(parts[5:], "/"))
	if asset == "." || asset == "/" || asset == "" {
		return ""
	}
	image := false
	for _, suffix := range []string{".qcow2", ".raw", ".img", ".iso", ".iso.7z", ".7z", ".zip", ".tar.xz", ".tar.zst", ".tar.gz"} {
		if strings.HasSuffix(strings.ToLower(asset), suffix) {
			image = true
			break
		}
	}
	if !image {
		return ""
	}

	root := ""
	switch repo {
	case "pve_kvm_images", "kvm_images":
		root = "pve/kvm"
	case "pve_lxc_images", "lxc_amd64_images", "lxc_arm_images":
		root = "pve/lxc"
	case "incus_images", "lxd_images":
		kind := "lxc"
		if strings.Contains(strings.ToLower(tag), "kvm") || strings.Contains(strings.ToLower(asset), "_kvm") {
			kind = "kvm"
		}
		root = strings.TrimSuffix(repo, "_images") + "/" + kind
	case "docker", "containerd", "podman":
		root = "runtime/" + repo
	default:
		return ""
	}

	lower := strings.ToLower(strings.ReplaceAll(asset, "-", "_"))
	system := "misc"
	for _, candidate := range []string{"rockylinux", "almalinux", "openwrt", "openeuler", "opensuse", "archlinux", "debian", "ubuntu", "alpine", "centos", "oracle", "fedora", "gentoo", "kali", "void", "freebsd", "devuan", "windows", "macos"} {
		if strings.Contains(lower, candidate) {
			system = candidate
			break
		}
	}
	architecture := "amd64"
	if strings.Contains(lower, "arm64") || strings.Contains(lower, "aarch64") {
		architecture = "arm64"
	} else if strings.Contains(lower, "armv7") || strings.Contains(lower, "armhf") || strings.Contains(lower, "arm_") {
		architecture = "arm"
	}

	return base + "/" + root + "/" + system + "/" + architecture + "/" + url.PathEscape(asset)
}

// ImageDownloadCandidates returns the node mirror first, followed by caller
// supplied fallbacks and finally the original source. Duplicate URLs are
// removed while preserving order.
func ImageDownloadCandidates(originalURL string, fallbacks ...string) []string {
	result := make([]string, 0, len(fallbacks)+2)
	seen := make(map[string]struct{}, len(fallbacks)+2)
	appendURL := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	appendURL(ImageMirrorURL(originalURL))
	for _, fallback := range fallbacks {
		appendURL(fallback)
	}
	appendURL(originalURL)
	if index := strings.Index(originalURL, "https://github.com/oneclickvirt/"); index > 0 {
		appendURL(originalURL[index:])
	}
	return result
}
