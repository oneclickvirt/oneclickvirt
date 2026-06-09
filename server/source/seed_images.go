package source

import (
	"bufio"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/system"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func SeedSystemImages() {
	global.APP_LOG.Info("开始同步系统镜像列表")

	// 初始化等级配置
	initLevelConfigurations()

	// 初始化等级配置；该操作本身是幂等的，放在镜像同步前确保新库/老库启动口径一致。
	initLevelConfigurations()

	// 先记录当前数量，但不再因为已有数据而直接跳过。
	// 主控从老版本升级时，数据库里可能已有旧初始化镜像，但新版本新增的系统镜像仍需自动补齐。
	var count int64
	if err := global.APP_DB.Model(&system.SystemImage{}).Count(&count).Error; err != nil {
		global.APP_LOG.Warn("统计已有系统镜像失败，仍继续尝试同步", zap.Error(err))
	} else {
		global.APP_LOG.Debug("当前系统镜像数量", zap.Int64("count", count))
	}

	// 收集所有镜像URL
	var imageURLs []string
	useDefaultImages := false

	// 从配置获取基础CDN端点
	baseCDN := utils.GetBaseCDNEndpoint()
	imageURL := baseCDN + "https://raw.githubusercontent.com/oneclickvirt/images_auto_list/refs/heads/main/images.txt"

	// 获取镜像列表，使用带超时的HTTP客户端
	client := &http.Client{
		Timeout: 60 * time.Second, // 获取文本列表，60秒超时
	}
	resp, err := client.Get(imageURL)
	if err != nil {
		global.APP_LOG.Warn("获取远程镜像列表失败，将使用默认镜像列表", zap.Error(err))
		useDefaultImages = true
	} else {
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			global.APP_LOG.Warn("获取远程镜像列表失败，将使用默认镜像列表", zap.Int("status", resp.StatusCode))
			useDefaultImages = true
		} else {
			// 从远程读取镜像URL
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				imageURL := strings.TrimSpace(scanner.Text())
				if imageURL != "" {
					imageURLs = append(imageURLs, imageURL)
				}
			}

			if err := scanner.Err(); err != nil {
				global.APP_LOG.Warn("读取远程镜像列表失败，将使用默认镜像列表", zap.Error(err))
				useDefaultImages = true
				imageURLs = nil // 清空可能部分读取的数据
			}
		}
	}

	// 如果远程获取失败，使用默认镜像列表
	if useDefaultImages {
		global.APP_LOG.Debug("使用默认镜像列表进行初始化/补齐")
		imageURLs = getDefaultImageURLs()
	}

	// 如果仍然没有镜像URL，记录错误并返回
	if len(imageURLs) == 0 {
		global.APP_LOG.Error("无法获取镜像列表，远程和默认列表均为空")
		return
	}

	// 按优先级排序：cloud镜像优先
	sortedURLs := prioritizeCloudImages(imageURLs)

	// 确保 kvm_images 优先级最低：排到最后，pve_kvm_images 等先处理
	{
		primary := make([]string, 0, len(sortedURLs))
		supplement := make([]string, 0)
		for _, u := range sortedURLs {
			if strings.Contains(u, "github.com/oneclickvirt/kvm_images/") {
				supplement = append(supplement, u)
			} else {
				primary = append(primary, u)
			}
		}
		sortedURLs = append(primary, supplement...)
	}

		desiredImages := buildDesiredSystemImages(sortedURLs)
	if len(desiredImages) == 0 {
		global.APP_LOG.Warn("镜像列表解析后没有可导入镜像")
		return
	}

	// 一次性加载已有 provider_type + url 组合，避免逐条查询导致 N+1。
	// 去重口径：同一种 Provider 类型下 URL 不可重复；不同 Provider 类型允许复用同一 URL。
	var existingImages []system.SystemImage
	if err := global.APP_DB.Select("id", "provider_type", "url").Find(&existingImages).Error; err != nil {
		global.APP_LOG.Error("查询已有系统镜像失败", zap.Error(err))
		return
	}
	existingKeys := make(map[string]struct{}, len(existingImages)+len(desiredImages))
	for _, img := range existingImages {
		existingKeys[systemImageUniqueKey(img.ProviderType, img.URL)] = struct{}{}
	}

	missingImages := make([]system.SystemImage, 0)
	for _, img := range desiredImages {
		key := systemImageUniqueKey(img.ProviderType, img.URL)
		if _, exists := existingKeys[key]; exists {
			continue
		}
		missingImages = append(missingImages, img)
		existingKeys[key] = struct{}{}
	}

	if len(missingImages) == 0 {
		global.APP_LOG.Info("系统镜像同步完成，没有发现遗漏镜像", zap.Int("desired", len(desiredImages)))
		return
	}

	if err := global.APP_DB.CreateInBatches(&missingImages, 100).Error; err != nil {
		global.APP_LOG.Error("批量创建遗漏系统镜像失败", zap.Error(err), zap.Int("missing", len(missingImages)))
		return
	}

	global.APP_LOG.Info("系统镜像同步完成", zap.Int("processed", len(missingImages)), zap.Int("desired", len(desiredImages)))
}

func systemImageUniqueKey(providerType, url string) string {
	return strings.ToLower(strings.TrimSpace(providerType)) + "\x00" + strings.TrimSpace(url)
}

func buildDesiredSystemImages(sortedURLs []string) []system.SystemImage {
	importedImages := make(map[string]bool)
	desiredKeys := make(map[string]bool)
	desiredImages := make([]system.SystemImage, 0, len(sortedURLs))

	for _, imageURL := range sortedURLs {
		imageInfo := parseImageURL(imageURL)
		if imageInfo == nil {
			continue
		}

		baseImageKey := fmt.Sprintf("%s-%s-%s-%s-%s",
			imageInfo.ProviderType, imageInfo.InstanceType, imageInfo.Architecture,
			imageInfo.OSType, imageInfo.OSVersion)
		currentVariant := getImageVariant(imageURL)

		// 如果是default镜像且已经导入了优先级更高的镜像（cloud/openrc/systemd），跳过
		if currentVariant == "default" && importedImages[baseImageKey] {
			global.APP_LOG.Debug("跳过default镜像，已有优先级更高的版本",
				zap.String("url", imageURL), zap.String("variant", currentVariant))
			continue
		}

		// 如果当前是openrc/systemd，但已经有cloud版本，跳过
		if (currentVariant == "openrc" || currentVariant == "systemd") && importedImages[baseImageKey+"_cloud"] {
			global.APP_LOG.Debug("跳过openrc/systemd镜像，已有cloud版本",
				zap.String("url", imageURL), zap.String("variant", currentVariant))
			continue
		}

		if isImageBlacklisted(imageInfo.ProviderType, imageInfo.InstanceType, imageInfo.Architecture, imageInfo.OSType, imageInfo.OSVersion) {
			global.APP_LOG.Warn("跳过黑名单镜像",
				zap.String("name", imageInfo.Name),
				zap.String("provider", imageInfo.ProviderType),
				zap.String("type", imageInfo.InstanceType),
				zap.String("arch", imageInfo.Architecture),
				zap.String("os", imageInfo.OSType),
				zap.String("version", imageInfo.OSVersion))
			continue
		}

		imageStatus := defaultSystemImageStatus(imageInfo.OSType)
		minMemoryMB, minDiskMB := getMinHardwareRequirements(imageInfo.OSType, imageInfo.InstanceType)
		useCDN := isGitHubURL(imageInfo.URL)

		baseImage := system.SystemImage{
			Name:         imageInfo.Name,
			ProviderType: imageInfo.ProviderType,
			InstanceType: imageInfo.InstanceType,
			Architecture: imageInfo.Architecture,
			URL:          imageInfo.URL,
			Status:       imageStatus,
			Description:  imageInfo.Description,
			OSType:       imageInfo.OSType,
			OSVersion:    imageInfo.OSVersion,
			MinMemoryMB:  minMemoryMB,
			MinDiskMB:    minDiskMB,
			UseCDN:       useCDN,
			CreatedBy:    nil,
		}
		if appendSystemImageIfNew(&desiredImages, desiredKeys, baseImage) {
			importedImages[baseImageKey] = true
			if currentVariant == "cloud" {
				importedImages[baseImageKey+"_cloud"] = true
			}
			global.APP_LOG.Debug("准备导入镜像",
				zap.String("name", imageInfo.Name),
				zap.String("provider", imageInfo.ProviderType),
				zap.String("url", imageURL),
				zap.String("variant", currentVariant))
		}

		// 对于 PVE/QEMU 通用 qcow2 虚拟机镜像，同时为 QEMU 和 KubeVirt 创建镜像记录。
		if imageInfo.ProviderType == "proxmox" && imageInfo.InstanceType == "vm" && strings.HasSuffix(imageInfo.URL, ".qcow2") {
			for _, extraProvider := range []string{"qemu", "kubevirt"} {
				providerLabel := strings.ToUpper(extraProvider[:1]) + extraProvider[1:]
				extraImage := baseImage
				extraImage.ProviderType = extraProvider
				extraImage.InstanceType = "vm"
				extraImage.Description = fmt.Sprintf("%s KVM %s image", providerLabel, imageInfo.Name)
				appendSystemImageIfNew(&desiredImages, desiredKeys, extraImage)
			}
		}

		// KubeVirt 的容器实例走 K3s/containerd 方式创建，可复用 Docker 家族的容器镜像归档。
		if imageInfo.ProviderType == "docker" && imageInfo.InstanceType == "container" {
			extraImage := baseImage
			extraImage.ProviderType = "kubevirt"
			extraImage.Description = fmt.Sprintf("KubeVirt K3s container %s image", imageInfo.Name)
			appendSystemImageIfNew(&desiredImages, desiredKeys, extraImage)
		}
	}

	return desiredImages
}

func appendSystemImageIfNew(images *[]system.SystemImage, keys map[string]bool, image system.SystemImage) bool {
	key := systemImageUniqueKey(image.ProviderType, image.URL)
	if keys[key] {
		return false
	}
	keys[key] = true
	*images = append(*images, image)
	return true
}

func defaultSystemImageStatus(osType string) string {
	osTypeLower := strings.ToLower(osType)
	if osTypeLower == "debian" || osTypeLower == "alpine" {
		return "active"
	}
	return "inactive"
}



// parseImageURL 解析镜像URL并提取信息
func parseImageURL(imageURL string) *ImageInfo {
	// Proxmox LXC AMD64 镜像
	lxcAmd64Re := regexp.MustCompile(`https://github\.com/oneclickvirt/lxc_amd64_images/releases/download/([^/]+)/([^_]+)_([^_]+)_([^_]+)_([^_]+)_([^.]+)\.tar\.xz`)
	if matches := lxcAmd64Re.FindStringSubmatch(imageURL); matches != nil {
		return &ImageInfo{
			Name:         fmt.Sprintf("%s-%s-%s", matches[2], matches[3], matches[6]),
			ProviderType: "proxmox", // Proxmox VE的LXC镜像
			InstanceType: "container",
			Architecture: "amd64",
			URL:          imageURL,
			OSType:       matches[2],
			OSVersion:    matches[3],
			Description:  fmt.Sprintf("Proxmox LXC %s %s %s image", matches[2], matches[3], matches[6]),
		}
	}

	// Proxmox LXC ARM64 镜像
	lxcArmRe := regexp.MustCompile(`https://github\.com/oneclickvirt/lxc_arm_images/releases/download/([^/]+)/([^_]+)_([^_]+)_([^_]+)_([^_]+)_([^.]+)\.tar\.xz`)
	if matches := lxcArmRe.FindStringSubmatch(imageURL); matches != nil {
		return &ImageInfo{
			Name:         fmt.Sprintf("%s-%s-%s", matches[2], matches[3], matches[6]),
			ProviderType: "proxmox", // Proxmox VE的LXC镜像
			InstanceType: "container",
			Architecture: "arm64",
			URL:          imageURL,
			OSType:       matches[2],
			OSVersion:    matches[3],
			Description:  fmt.Sprintf("Proxmox LXC %s %s %s image", matches[2], matches[3], matches[6]),
		}
	}

	// LXD KVM镜像
	lxdKvmRe := regexp.MustCompile(`https://github\.com/oneclickvirt/lxd_images/releases/download/kvm_images/([^_]+)_([^_]+)_([^_]+)_([^_]+)_([^_]+)_kvm\.zip`)
	if matches := lxdKvmRe.FindStringSubmatch(imageURL); matches != nil {
		return &ImageInfo{
			Name:         fmt.Sprintf("%s-%s-kvm-%s", matches[1], matches[2], matches[5]),
			ProviderType: "lxd",
			InstanceType: "vm",
			Architecture: convertArch(matches[4]),
			URL:          imageURL,
			OSType:       matches[1],
			OSVersion:    matches[2],
			Description:  fmt.Sprintf("LXD KVM %s %s %s image", matches[1], matches[2], matches[5]),
		}
	}

	// LXD 容器镜像
	lxdContainerRe := regexp.MustCompile(`https://github\.com/oneclickvirt/lxd_images/releases/download/([^/]+)/([^_]+)_([^_]+)_([^_]+)_([^_]+)_([^.]+)\.zip`)
	if matches := lxdContainerRe.FindStringSubmatch(imageURL); matches != nil {
		return &ImageInfo{
			Name:         fmt.Sprintf("%s-%s-%s", matches[2], matches[3], matches[6]),
			ProviderType: "lxd",
			InstanceType: "container",
			Architecture: convertArch(matches[5]),
			URL:          imageURL,
			OSType:       matches[2],
			OSVersion:    matches[3],
			Description:  fmt.Sprintf("LXD %s %s %s image", matches[2], matches[3], matches[6]),
		}
	}

	// Incus KVM镜像
	incusKvmRe := regexp.MustCompile(`https://github\.com/oneclickvirt/incus_images/releases/download/kvm_images/([^_]+)_([^_]+)_([^_]+)_((?:x86_64|arm64))_([^_]+)_kvm\.zip`)
	if matches := incusKvmRe.FindStringSubmatch(imageURL); matches != nil {
		return &ImageInfo{
			Name:         fmt.Sprintf("%s-%s-kvm-%s", matches[1], matches[2], matches[5]),
			ProviderType: "incus",
			InstanceType: "vm",
			Architecture: convertArch(matches[4]),
			URL:          imageURL,
			OSType:       matches[1],
			OSVersion:    matches[2],
			Description:  fmt.Sprintf("Incus KVM %s %s %s image", matches[1], matches[2], matches[5]),
		}
	}

	// Incus 容器镜像
	incusContainerRe := regexp.MustCompile(`https://github\.com/oneclickvirt/incus_images/releases/download/([^/]+)/([^_]+)_([^_]+)_([^_]+)_((?:x86_64|arm64))_([^.]+)\.zip`)
	if matches := incusContainerRe.FindStringSubmatch(imageURL); matches != nil {
		return &ImageInfo{
			Name:         fmt.Sprintf("%s-%s-%s", matches[2], matches[3], matches[6]),
			ProviderType: "incus",
			InstanceType: "container",
			Architecture: convertArch(matches[5]),
			URL:          imageURL,
			OSType:       matches[2],
			OSVersion:    matches[3],
			Description:  fmt.Sprintf("Incus %s %s %s image", matches[2], matches[3], matches[6]),
		}
	}

	// Docker镜像
	dockerRe := regexp.MustCompile(`https://github\.com/oneclickvirt/docker/releases/download/([^/]+)/spiritlhl_([^_]+)_([^.]+)\.tar\.gz`)
	if matches := dockerRe.FindStringSubmatch(imageURL); matches != nil {
		osType := matches[2]
		return &ImageInfo{
			Name:         fmt.Sprintf("spiritlhl-%s", osType),
			ProviderType: "docker",
			InstanceType: "container",
			Architecture: convertArch(matches[3]),
			URL:          imageURL,
			OSType:       osType,
			OSVersion:    inferDockerOSVersion(osType),
			Description:  fmt.Sprintf("Docker %s %s image", osType, matches[3]),
		}
	}

	// Podman镜像
	podmanRe := regexp.MustCompile(`https://github\.com/oneclickvirt/podman/releases/download/([^/]+)/spiritlhl_([^_]+)_([^.]+)\.tar\.gz`)
	if matches := podmanRe.FindStringSubmatch(imageURL); matches != nil {
		osType := matches[2]
		return &ImageInfo{
			Name:         fmt.Sprintf("spiritlhl-%s", osType),
			ProviderType: "podman",
			InstanceType: "container",
			Architecture: convertArch(matches[3]),
			URL:          imageURL,
			OSType:       osType,
			OSVersion:    inferDockerOSVersion(osType),
			Description:  fmt.Sprintf("Podman %s %s image", osType, matches[3]),
		}
	}

	// Containerd镜像
	containerdRe := regexp.MustCompile(`https://github\.com/oneclickvirt/containerd/releases/download/([^/]+)/spiritlhl_([^_]+)_([^.]+)\.tar\.gz`)
	if matches := containerdRe.FindStringSubmatch(imageURL); matches != nil {
		osType := matches[2]
		return &ImageInfo{
			Name:         fmt.Sprintf("spiritlhl-%s", osType),
			ProviderType: "containerd",
			InstanceType: "container",
			Architecture: convertArch(matches[3]),
			URL:          imageURL,
			OSType:       osType,
			OSVersion:    inferDockerOSVersion(osType),
			Description:  fmt.Sprintf("Containerd %s %s image", osType, matches[3]),
		}
	}

	// Proxmox KVM镜像（pve_kvm_images）
	proxmoxRe := regexp.MustCompile(`https://github\.com/oneclickvirt/pve_kvm_images/releases/download/([^/]+)/(.+)\.qcow2`)
	if matches := proxmoxRe.FindStringSubmatch(imageURL); matches != nil {
		return &ImageInfo{
			Name:         matches[2],
			ProviderType: "proxmox",
			InstanceType: "vm",
			Architecture: "amd64", // Proxmox默认amd64
			URL:          imageURL,
			OSType:       extractOSFromFilename(matches[2]),
			OSVersion:    extractVersionFromFilename(matches[2]),
			Description:  fmt.Sprintf("Proxmox KVM %s image", matches[2]),
		}
	}

	// KVM镜像（kvm_images仓库）
	kvmImagesRe := regexp.MustCompile(`https://github\.com/oneclickvirt/kvm_images/releases/download/([^/]+)/(.+)\.qcow2`)
	if matches := kvmImagesRe.FindStringSubmatch(imageURL); matches != nil {
		return &ImageInfo{
			Name:         matches[2],
			ProviderType: "proxmox",
			InstanceType: "vm",
			Architecture: "amd64",
			URL:          imageURL,
			OSType:       extractOSFromFilename(matches[2]),
			OSVersion:    extractVersionFromFilename(matches[2]),
			Description:  fmt.Sprintf("KVM %s image", matches[2]),
		}
	}

	return nil
}

// inferDockerOSVersion 根据 Docker 镜像的 OS 类型推断主要版本号。
// Docker 镜像 URL 中不含版本号（版本信息在镜像 tar.gz 内部），此处根据 OS 名称
// 给出当前主推的默认版本，便于 populateImageURLFromSystemImage 按 osVersion 前缀匹配。
func inferDockerOSVersion(osType string) string {
	switch strings.ToLower(osType) {
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
	lowerName := strings.ToLower(filename)

	// 使用确定性顺序的切片而非 map，避免 Go map 随机迭代导致的不确定性匹配。
	// 较长的键优先匹配，确保 "archlinux" 在 "arch" 之前被检查。
	type osPair struct{ key, value string }
	osList := []osPair{
		{"archlinux", "archlinux"},
		{"rockylinux", "rockylinux"},
		{"almalinux", "almalinux"},
		{"openeuler", "openeuler"},
		{"opensuse", "opensuse"},
		{"alpine", "alpine"},
		{"ubuntu", "ubuntu"},
		{"debian", "debian"},
		{"centos", "centos"},
		{"fedora", "fedora"},
		{"oracle", "oracle"},
		{"gentoo", "gentoo"},
		{"kali", "kali"},
		{"openwrt", "openwrt"},
		{"arch", "archlinux"},
	}

	for _, p := range osList {
		if strings.Contains(lowerName, p.key) {
			return p.value
		}
	}

	return "unknown"
}

// extractVersionFromFilename 从文件名提取版本
func extractVersionFromFilename(filename string) string {
	versionRe := regexp.MustCompile(`(\d+(?:\.\d+)?)`)
	if matches := versionRe.FindStringSubmatch(filename); matches != nil {
		return matches[1]
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

	return "unknown"
}

// prioritizeCloudImages 对镜像URL进行排序，cloud镜像优先
func prioritizeCloudImages(imageURLs []string) []string {
	cloudImages := make([]string, 0)
	openrcSystemdImages := make([]string, 0)
	defaultImages := make([]string, 0)
	otherImages := make([]string, 0)

	for _, url := range imageURLs {
		if isCloudImage(url) {
			cloudImages = append(cloudImages, url)
		} else if strings.Contains(url, "_openrc") || strings.Contains(url, "_systemd") {
			openrcSystemdImages = append(openrcSystemdImages, url)
		} else if isDefaultImage(url) {
			defaultImages = append(defaultImages, url)
		} else {
			otherImages = append(otherImages, url)
		}
	}

	// 合并排序：cloud镜像 -> openrc/systemd镜像 -> 其他镜像 -> default镜像
	result := make([]string, 0, len(imageURLs))
	result = append(result, cloudImages...)
	result = append(result, openrcSystemdImages...)
	result = append(result, otherImages...)
	result = append(result, defaultImages...)

	return result
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

// initLevelConfigurations 初始化用户等级与带宽配置
