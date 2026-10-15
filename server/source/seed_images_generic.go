package source

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"oneclickvirt/utils"
)

func parseGenericOneClickVirtImageURL(imageURL string) *ImageInfo {
	cleanURL := strings.Split(strings.TrimSpace(imageURL), "?")[0]
	if cleanURL == "" || !strings.Contains(strings.ToLower(cleanURL), "github.com/oneclickvirt/") {
		return nil
	}
	lowerURL := strings.ToLower(cleanURL)
	filename := path.Base(cleanURL)
	base := trimKnownImageExtension(filename)
	if base == "" || base == filename {
		return nil
	}

	providerType := ""
	instanceType := "container"
	architecture := ""
	switch {
	case strings.Contains(lowerURL, "/lxc_amd64_images/"), strings.Contains(lowerURL, "/pve_lxc_images/"):
		providerType = "proxmox"
		instanceType = "container"
		architecture = "amd64"
	case strings.Contains(lowerURL, "/lxc_arm_images/"):
		providerType = "proxmox"
		instanceType = "container"
		architecture = "arm64"
	case strings.Contains(lowerURL, "/lxd_images/"):
		providerType = "lxd"
	case strings.Contains(lowerURL, "/incus_images/"):
		providerType = "incus"
	case strings.Contains(lowerURL, "/pve_kvm_images/"), strings.Contains(lowerURL, "/kvm_images/"):
		providerType = "proxmox"
		instanceType = "vm"
		architecture = "amd64"
	case strings.Contains(lowerURL, "/docker/"):
		providerType = "docker"
	case strings.Contains(lowerURL, "/podman/"):
		providerType = "podman"
	case strings.Contains(lowerURL, "/containerd/"):
		providerType = "containerd"
	case strings.Contains(lowerURL, "/orbstack/"):
		providerType = "orbstack"
	default:
		return nil
	}
	if strings.Contains(lowerURL, "/kvm_images/") || strings.HasSuffix(base, "_kvm") || strings.HasSuffix(filename, ".qcow2") {
		instanceType = "vm"
	}

	// Keep the architecture alias intact while splitting image fields.
	tokens := strings.Split(strings.ReplaceAll(base, "x86_64", "amd64"), "_")
	if len(tokens) == 0 {
		return nil
	}
	if strings.EqualFold(tokens[0], "spiritlhl") && len(tokens) > 1 {
		tokens = tokens[1:]
	}
	if architecture == "" {
		if arch, _ := detectArchitectureToken(tokens); arch != "" {
			architecture = arch
		}
	}
	if architecture == "" {
		architecture = "amd64"
	}

	archIndex := -1
	if _, idx := detectArchitectureToken(tokens); idx >= 0 {
		archIndex = idx
	}
	osType := utils.NormalizeOSType(extractOSFromFilename(base))
	if osType == "unknown" || osType == "" {
		osType = utils.NormalizeOSType(tokens[0])
	}
	if isUnresolvedSystemImageOS(osType) {
		return nil
	}

	versionTokens := []string{}
	variantTokens := []string{}
	if archIndex > 0 {
		versionTokens = tokens[1:archIndex]
		if archIndex+1 < len(tokens) {
			variantTokens = tokens[archIndex+1:]
		}
	} else if len(tokens) > 1 {
		versionTokens = tokens[1:]
	}
	versionTokens = filterImageTokens(versionTokens, map[string]bool{
		"cloud": true, "default": true, "systemd": true, "openrc": true, "kvm": true,
	})
	variantTokens = filterImageTokens(variantTokens, map[string]bool{"kvm": true})

	osVersion := ""
	// The second field is the version; the following field is a codename.
	if len(versionTokens) > 0 {
		osVersion = versionTokens[0]
	}
	if osVersion == "" {
		if isOCIArchiveProvider(providerType) {
			osVersion = inferDockerOSVersion(osType)
		} else {
			osVersion = extractVersionFromFilename(base)
		}
	}
	if osVersion == "" || osVersion == "unknown" {
		osVersion = "latest"
	}

	nameParts := []string{osType}
	if osVersion != "" && osVersion != "latest" && osVersion != "unknown" {
		nameParts = append(nameParts, osVersion)
	}
	for _, token := range variantTokens {
		if token != "" && token != "default" {
			nameParts = append(nameParts, token)
			break
		}
	}
	name := strings.Join(nameParts, "-")
	if name == "" || name == "unknown" {
		name = base
	}

	return &ImageInfo{
		Name:         name,
		ProviderType: providerType,
		InstanceType: instanceType,
		Architecture: architecture,
		URL:          imageURL,
		OSType:       osType,
		OSVersion:    osVersion,
		Description:  fmt.Sprintf("%s %s %s image", providerDisplayName(providerType), strings.ToUpper(instanceType), name),
	}
}

func trimKnownImageExtension(filename string) string {
	for _, ext := range []string{".tar.xz", ".tar.zst", ".tar.gz", ".qcow2", ".zip"} {
		if strings.HasSuffix(strings.ToLower(filename), ext) {
			return strings.TrimSuffix(filename, ext)
		}
	}
	return filename
}

func detectArchitectureToken(tokens []string) (string, int) {
	for idx, token := range tokens {
		arch := convertArch(strings.ToLower(strings.TrimSpace(token)))
		switch arch {
		case "amd64", "arm64", "s390x":
			return arch, idx
		}
	}
	return "", -1
}

func filterImageTokens(tokens []string, skip map[string]bool) []string {
	filtered := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(strings.ToLower(token))
		if token == "" || skip[token] {
			continue
		}
		filtered = append(filtered, token)
	}
	return filtered
}

// inferDockerOSVersion 根据 Docker 镜像的 OS 类型推断主要版本号。
// Docker 镜像 URL 中不含版本号（版本信息在镜像 tar.gz 内部），此处根据 OS 名称
// 给出当前主推的默认版本，便于 populateImageURLFromSystemImage 按 osVersion 前缀匹配。
func inferDockerOSVersion(osType string) string {
	switch utils.NormalizeOSType(osType) {
	case "debian":
		return "12"
	case "alpine":
		return "3.19"
	case "ubuntu":
		return "24.04"
	case "rockylinux":
		return "9"
	case "almalinux":
		return "9"
	case "openeuler":
		return "24.03"
	case "fedora":
		return "41"
	case "centos":
		return "9"
	case "opensuse":
		return "15.6"
	case "archlinux":
		return "current"
	case "gentoo":
		return "current"
	case "kali":
		return "latest"
	case "oracle":
		return "9"
	case "openwrt":
		return "24.10"
	default:
		return "latest"
	}
}

// convertArch 转换架构名称
func convertArch(arch string) string {
	switch arch {
	case "x86_64", "amd64":
		return "amd64"
	case "arm64", "aarch64", "armv8l", "armv8", "armv7l", "armv7", "armv6l", "armv6", "armv5tel", "armv5te", "armv5t":
		return "arm64"
	case "s390x":
		return "s390x"
	default:
		return arch
	}
}

// extractOSFromFilename 从文件名提取操作系统（确定性顺序匹配，避免 map 随机迭代）
func extractOSFromFilename(filename string) string {
	if osType := utils.DetectOSTypeFromText(filename); osType != "" {
		return osType
	}
	return "unknown"
}

// extractVersionFromFilename 从文件名提取版本
func extractVersionFromFilename(filename string) string {
	versionRe := regexp.MustCompile(`(\d+(?:\.\d+)?)`)
	if matches := versionRe.FindStringSubmatch(filename); matches != nil {
		version := matches[1]
		if strings.HasPrefix(strings.ToLower(filename), "ubuntu") {
			if len(version) == 4 && !strings.Contains(version, ".") {
				return version[:2] + "." + version[2:]
			}
			switch version {
			case "16", "18", "20", "22", "24", "26":
				return version + ".04"
			}
		}
		return version
	}

	lowerName := strings.ToLower(filename)
	if strings.Contains(lowerName, "latest") {
		return "latest"
	}
	if strings.Contains(lowerName, "current") {
		return "current"
	}
	if strings.Contains(lowerName, "stable") {
		return "stable"
	}
	if strings.Contains(lowerName, "edge") {
		return "edge"
	}

	if extractOSFromFilename(filename) == "archlinux" {
		return "current"
	}

	return "unknown"
}

// prioritizeCloudImages 对镜像URL进行排序，cloud镜像优先
func prioritizeCloudImages(imageURLs []string) []string {
	result := append([]string(nil), imageURLs...)
	sort.SliceStable(result, func(i, j int) bool {
		left, right := result[i], result[j]
		// Maintained family releases take precedence over legacy catalogues.
		if l, r := systemImageSourcePriority(left), systemImageSourcePriority(right); l != r {
			return l < r
		}
		if l, r := systemImageVariantPriority(left), systemImageVariantPriority(right); l != r {
			return l < r
		}
		return publishedImageBuildDate(left) > publishedImageBuildDate(right)
	})
	return result
}

var publishedImageDatePattern = regexp.MustCompile(`_(20[0-9]{6})_(?:amd64|arm64)\.tar\.zst$`)

func publishedImageBuildDate(imageURL string) string {
	if !strings.Contains(imageURL, "github.com/oneclickvirt/pve_lxc_images/releases/download/maintained/") {
		return ""
	}
	if matches := publishedImageDatePattern.FindStringSubmatch(imageURL); matches != nil {
		return matches[1]
	}
	return ""
}

func systemImageSourcePriority(imageURL string) int {
	lower := strings.ToLower(imageURL)
	if strings.Contains(lower, "github.com/oneclickvirt/pve_lxc_images/") && !strings.Contains(lower, "/releases/download/maintained/") {
		return 1
	}
	if strings.Contains(lower, "github.com/oneclickvirt/kvm_images/") {
		return 2
	}
	if strings.Contains(lower, "github.com/oneclickvirt/") && strings.Contains(lower, "/releases/download/images/") {
		return 1
	}
	return 0
}

func systemImageVariantPriority(imageURL string) int {
	switch {
	case isCloudImage(imageURL):
		return 0
	case strings.Contains(imageURL, "_openrc"), strings.Contains(imageURL, "_systemd"):
		return 1
	case isDefaultImage(imageURL):
		return 3
	default:
		return 2
	}
}

func isUnpublishedSystemImageURL(imageURL string) bool {
	lower := strings.ToLower(imageURL)
	return strings.Contains(lower, "github.com/oneclickvirt/") &&
		(strings.Contains(lower, "/releases/download/legacy-archived/") ||
			strings.Contains(lower, "/releases/download/processed/") ||
			strings.Contains(lower, ".ci-stage-") || strings.Contains(lower, ".ci-backup-"))
}

// isCloudImage 检查是否为cloud镜像
func isCloudImage(imageURL string) bool {
	return strings.Contains(imageURL, "_cloud.") || strings.Contains(imageURL, "_cloud_")
}

// isDefaultImage 检查是否为default镜像
func isDefaultImage(imageURL string) bool {
	return strings.Contains(imageURL, "_default.") || strings.Contains(imageURL, "_default_")
}

// getImageVariant 从URL中提取镜像变体
func getImageVariant(imageURL string) string {
	if strings.Contains(imageURL, "_cloud") {
		return "cloud"
	} else if strings.Contains(imageURL, "_default") {
		return "default"
	} else if strings.Contains(imageURL, "_openrc") {
		return "openrc"
	} else if strings.Contains(imageURL, "_systemd") {
		return "systemd"
	}
	return "standard"
}

// isGitHubURL 判断URL是否为GitHub链接
// 仅对GitHub链接启用CDN加速，非GitHub链接（如官方上游镜像站）不应使用CDN
func isGitHubURL(url string) bool {
	return strings.Contains(url, "github.com/") || strings.Contains(url, "raw.githubusercontent.com/")
}
