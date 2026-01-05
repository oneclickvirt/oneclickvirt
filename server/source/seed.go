package source

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"oneclickvirt/service/database"
	"regexp"
	"strings"
	"time"

	"oneclickvirt/config"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/system"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// InitSeedData 初始化种子数据，确保不重复创建
func InitSeedData() {
	initDefaultRoles()
	initDefaultAnnouncements()
	initLevelConfigurations()
	initOtherConfigurations()
	// OAuth2 providers are not automatically initialized
	// Admin should configure them manually based on their needs
}

func initDefaultRoles() {
	roles := []auth.Role{
		{Name: "admin", Code: "admin", Description: "系统管理员角色", Status: 1},
		{Name: "user", Code: "user", Description: "普通用户角色", Status: 1},
	}

	for _, role := range roles {
		var count int64
		global.APP_DB.Model(&auth.Role{}).Where("name = ? OR code = ?", role.Name, role.Code).Count(&count)
		if count == 0 {
			// 使用数据库抽象层创建
			dbService := database.GetDatabaseService()
			dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Create(&role).Error
			})
		}
	}
}

func initDefaultAnnouncements() {
	announcements := []system.Announcement{
		{
			Title:       "欢迎使用虚拟化管理平台",
			Content:     "欢迎使用虚拟化管理平台，支持Docker、LXD、Incus、Proxmox VE等多种虚拟化技术。本平台提供简单易用的Web界面，让您轻松管理各种虚拟化资源。",
			ContentHTML: "<p>欢迎使用虚拟化管理平台，支持<strong>Docker</strong>、<strong>LXD</strong>、<strong>Incus</strong>、<strong>Proxmox VE</strong>等多种虚拟化技术。</p><p>本平台提供简单易用的Web界面，让您轻松管理各种虚拟化资源。</p>",
			Type:        "homepage",
			Status:      1,
			Priority:    10,
			IsSticky:    true,
		},
		{
			Title:       "系统维护通知",
			Content:     "为了提供更好的服务质量，会定期进行系统维护。维护期间可能会影响部分功能的使用，请您谅解。",
			ContentHTML: "<p>为了提供更好的服务质量，会定期进行系统维护。</p>",
			Type:        "topbar",
			Status:      1,
			Priority:    5,
			IsSticky:    false,
		},
		{
			Title:       "新手使用指南",
			Content:     "如果您是第一次使用本平台，建议先阅读使用文档。您可以在右上角的帮助菜单中找到详细的操作指南。",
			ContentHTML: "<p>如果您是第一次使用本平台，建议先阅读使用文档。</p>",
			Type:        "homepage",
			Status:      1,
			Priority:    8,
			IsSticky:    false,
		},
	}

	for _, announcement := range announcements {
		var count int64
		global.APP_DB.Model(&system.Announcement{}).Where("title = ? AND type = ?", announcement.Title, announcement.Type).Count(&count)
		if count == 0 {
			dbService := database.GetDatabaseService()
			dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Create(&announcement).Error
			})
		}
	}
}

// ImageInfo 镜像解析信息
type ImageInfo struct {
	Name         string
	ProviderType string
	InstanceType string
	Architecture string
	URL          string
	OSType       string
	OSVersion    string
	Description  string
}

// getMinHardwareRequirements 根据操作系统类型和实例类型获取最低硬件要求
// 返回值：minMemoryMB, minDiskMB
func getMinHardwareRequirements(osType string, instanceType string) (int, int) {
	osTypeLower := strings.ToLower(osType)

	// 容器的最低要求
	containerRequirements := map[string]struct{ memory, disk int }{
		"centos":     {512, 2048}, // 512MB, 2GB
		"almalinux":  {350, 1536}, // 350MB, 1.5GB
		"debian":     {128, 1024}, // 128MB, 1GB
		"kali":       {256, 1024}, // 256MB, 1GB
		"rockylinux": {350, 1536}, // 350MB, 1.5GB
		"alpine":     {64, 200},   // 64MB, 200MB
	}

	// 虚拟机的最低要求（取容器要求和当前硬编码的最大值）
	// 当前硬编码：VM 512MB内存，3GB硬盘
	vmRequirements := map[string]struct{ memory, disk int }{
		"centos":     {512, 3072}, // max(512, 512)=512MB, max(2048, 3072)=3072MB
		"almalinux":  {512, 3072}, // max(350, 512)=512MB, max(1536, 3072)=3072MB
		"debian":     {326, 3072}, // max(128, 326)=326MB, max(1024, 3072)=3072MB
		"kali":       {326, 3072}, // max(256, 326)=326MB, max(1024, 3072)=3072MB
		"rockylinux": {512, 3072}, // max(350, 512)=512MB, max(1536, 3072)=3072MB
		"alpine":     {64, 3072},  // max(64, 326)=326MB, max(200, 3072)=3072MB
	}

	if instanceType == "vm" {
		if req, ok := vmRequirements[osTypeLower]; ok {
			return req.memory, req.disk
		}
		// 其他系统默认值：512MB, 3GB
		return 326, 3072
	} else {
		// container
		if req, ok := containerRequirements[osTypeLower]; ok {
			return req.memory, req.disk
		}
		// 其他系统默认值：128MB, 1GB
		return 128, 1024
	}
}

// imageBlacklist 黑名单配置 - 禁用特定镜像
// 硬编码黑名单，用于暂时禁用有问题的镜像
type ImageBlacklistEntry struct {
	ProviderType string
	InstanceType string
	Architecture string
	OSType       string
	OSVersion    string
}

// isImageBlacklisted 检查镜像是否在黑名单中
func isImageBlacklisted(providerType, instanceType, architecture, osType, osVersion string) bool {
	// 硬编码黑名单：Debian 12 和 Debian 13 的 Proxmox VE AMD64 容器镜像，暂时不可用
	blacklist := []ImageBlacklistEntry{
		{
			ProviderType: "proxmox",
			InstanceType: "container",
			Architecture: "amd64",
			OSType:       "debian",
			OSVersion:    "12",
		},
		{
			ProviderType: "proxmox",
			InstanceType: "container",
			Architecture: "amd64",
			OSType:       "debian",
			OSVersion:    "13",
		},
	}

	osTypeLower := strings.ToLower(osType)
	osVersionLower := strings.ToLower(osVersion)

	for _, entry := range blacklist {
		if strings.EqualFold(entry.ProviderType, providerType) &&
			strings.EqualFold(entry.InstanceType, instanceType) &&
			strings.EqualFold(entry.Architecture, architecture) &&
			strings.EqualFold(entry.OSType, osTypeLower) &&
			strings.EqualFold(entry.OSVersion, osVersionLower) {
			return true
		}
	}

	return false
}

// SeedSystemImages 从远程URL获取镜像列表并添加到数据库
func SeedSystemImages() {
	global.APP_LOG.Info("开始同步系统镜像列表")

	// 初始化等级配置
	initLevelConfigurations()

	// 检查是否已经有镜像数据
	var count int64
	global.APP_DB.Model(&system.SystemImage{}).Count(&count)
	if count > 0 {
		global.APP_LOG.Info("镜像数据已存在，跳过同步", zap.Int64("count", count))
		return
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
		global.APP_LOG.Info("使用默认镜像列表进行初始化")
		imageURLs = getDefaultImageURLs()
	}

	// 如果仍然没有镜像URL，记录错误并返回
	if len(imageURLs) == 0 {
		global.APP_LOG.Error("无法获取镜像列表，远程和默认列表均为空")
		return
	}

	// 按优先级排序：cloud镜像优先
	sortedURLs := prioritizeCloudImages(imageURLs)

	processedCount := 0
	importedImages := make(map[string]bool) // 用于跟踪已导入的镜像基础信息

	for _, imageURL := range sortedURLs {
		imageInfo := parseImageURL(imageURL)
		if imageInfo != nil {
			// 生成基础镜像标识（不包含变体信息）
			baseImageKey := fmt.Sprintf("%s-%s-%s-%s-%s",
				imageInfo.ProviderType, imageInfo.InstanceType, imageInfo.Architecture,
				imageInfo.OSType, imageInfo.OSVersion)

			// 获取当前镜像的变体
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

			// 检查镜像是否在黑名单中
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

			// 检查是否已存在
			var existingImage system.SystemImage
			result := global.APP_DB.Where("name = ? AND provider_type = ? AND instance_type = ? AND architecture = ?",
				imageInfo.Name, imageInfo.ProviderType, imageInfo.InstanceType, imageInfo.Architecture).First(&existingImage)

			if result.Error != nil {
				// 确定镜像状态：默认仅启用 Debian 和 Alpine 镜像
				imageStatus := "inactive"
				osTypeLower := strings.ToLower(imageInfo.OSType)
				if osTypeLower == "debian" || osTypeLower == "alpine" {
					imageStatus = "active"
				}

				// 获取最低硬件要求
				minMemoryMB, minDiskMB := getMinHardwareRequirements(imageInfo.OSType, imageInfo.InstanceType)

				// 创建新镜像记录
				systemImage := system.SystemImage{
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
					UseCDN:       true, // 系统镜像默认使用CDN加速
					CreatedBy:    nil,  // 系统创建，设为nil
				}

				dbService := database.GetDatabaseService()
				if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
					return tx.Create(&systemImage).Error
				}); err != nil {
					global.APP_LOG.Error("创建镜像记录失败", zap.Error(err), zap.String("name", imageInfo.Name))
				} else {
					processedCount++
					// 标记该基础镜像已导入
					importedImages[baseImageKey] = true
					// 如果是cloud镜像，单独标记
					if currentVariant == "cloud" {
						importedImages[baseImageKey+"_cloud"] = true
					}
					global.APP_LOG.Debug("导入镜像成功",
						zap.String("name", imageInfo.Name),
						zap.String("url", imageURL),
						zap.String("variant", currentVariant))
				}
			}
		}
	}

	global.APP_LOG.Info("系统镜像同步完成", zap.Int("processed", processedCount))
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
		return &ImageInfo{
			Name:         fmt.Sprintf("spiritlhl-%s", matches[2]),
			ProviderType: "docker",
			InstanceType: "container",
			Architecture: convertArch(matches[3]),
			URL:          imageURL,
			OSType:       matches[2],
			OSVersion:    "latest",
			Description:  fmt.Sprintf("Docker %s %s image", matches[2], matches[3]),
		}
	}

	// Proxmox KVM镜像
	proxmoxRe := regexp.MustCompile(`https://github\.com/oneclickvirt/pve_kvm_images/releases/download/([^/]+)/([^.]+)\.qcow2`)
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

	return nil
}

// convertArch 转换架构名称
func convertArch(arch string) string {
	switch arch {
	case "x86_64", "amd64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	case "s390x":
		return "s390x"
	default:
		return arch
	}
}

// extractOSFromFilename 从文件名提取操作系统
func extractOSFromFilename(filename string) string {
	lowerName := strings.ToLower(filename)

	osMap := map[string]string{
		"ubuntu":    "ubuntu",
		"debian":    "debian",
		"centos":    "centos",
		"rocky":     "rockylinux",
		"alma":      "almalinux",
		"fedora":    "fedora",
		"alpine":    "alpine",
		"arch":      "archlinux",
		"opensuse":  "opensuse",
		"openeuler": "openeuler",
		"oracle":    "oracle",
		"gentoo":    "gentoo",
		"kali":      "kali",
	}

	for key, value := range osMap {
		if strings.Contains(lowerName, key) {
			return value
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

	if strings.Contains(filename, "latest") {
		return "latest"
	}
	if strings.Contains(filename, "current") {
		return "current"
	}
	if strings.Contains(filename, "edge") {
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

// initLevelConfigurations 初始化用户等级与带宽配置
func initLevelConfigurations() {
	global.APP_LOG.Info("开始初始化等级与带宽配置")

	// 检查配置是否已经初始化
	if len(global.APP_CONFIG.Quota.LevelLimits) > 0 {
		global.APP_LOG.Info("等级配置已存在，跳过初始化")
		return
	}

	// 创建默认的等级配置（如果配置为空）
	if global.APP_CONFIG.Quota.LevelLimits == nil {
		global.APP_CONFIG.Quota.LevelLimits = make(map[int]config.LevelLimitInfo)
	}

	// 设置默认等级配置
	// 等级1: 最低档次
	global.APP_CONFIG.Quota.LevelLimits[1] = config.LevelLimitInfo{
		MaxInstances: 1,
		MaxResources: map[string]interface{}{
			"cpu":       1,
			"memory":    350,  // 350MB
			"disk":      1024, // 1GB
			"bandwidth": 100,  // 100Mbps
		},
		MaxTraffic: 102400, // 100GB
		ExpiryDays: 0,      // 0表示不过期
	}

	// 等级2: 中级档次
	global.APP_CONFIG.Quota.LevelLimits[2] = config.LevelLimitInfo{
		MaxInstances: 3,
		MaxResources: map[string]interface{}{
			"cpu":       2,
			"memory":    1024,  // 1GB
			"disk":      20480, // 20GB
			"bandwidth": 200,   // 200Mbps
		},
		MaxTraffic: 204800, // 200GB
		ExpiryDays: 0,      // 0表示不过期
	}

	// 等级3: 高级档次
	global.APP_CONFIG.Quota.LevelLimits[3] = config.LevelLimitInfo{
		MaxInstances: 5,
		MaxResources: map[string]interface{}{
			"cpu":       4,
			"memory":    2048,  // 2GB
			"disk":      40960, // 40GB
			"bandwidth": 500,   // 500Mbps
		},
		MaxTraffic: 307200, // 300GB
		ExpiryDays: 0,      // 0表示不过期
	}

	// 等级4: 超级档次
	global.APP_CONFIG.Quota.LevelLimits[4] = config.LevelLimitInfo{
		MaxInstances: 10,
		MaxResources: map[string]interface{}{
			"cpu":       8,
			"memory":    4096,  // 4GB
			"disk":      81920, // 80GB
			"bandwidth": 1000,  // 1000Mbps
		},
		MaxTraffic: 409600, // 400GB
		ExpiryDays: 0,      // 0表示不过期
	}

	// 等级5: 管理员档次
	global.APP_CONFIG.Quota.LevelLimits[5] = config.LevelLimitInfo{
		MaxInstances: 20,
		MaxResources: map[string]interface{}{
			"cpu":       16,
			"memory":    8192,   // 8GB
			"disk":      163840, // 160GB
			"bandwidth": 2000,   // 2000Mbps
		},
		MaxTraffic: 512000, // 500GB
		ExpiryDays: 0,      // 0表示不过期
	}

	global.APP_LOG.Info("等级与带宽配置初始化完成")

	// 初始化实例类型权限配置
	initInstanceTypePermissions()
}

// initInstanceTypePermissions 初始化实例类型权限配置
func initInstanceTypePermissions() {
	global.APP_LOG.Info("开始初始化实例类型权限配置")

	// 检查配置是否已经设置
	permissions := global.APP_CONFIG.Quota.InstanceTypePermissions
	if permissions.MinLevelForContainer != 0 || permissions.MinLevelForVM != 0 ||
		permissions.MinLevelForDeleteContainer != 0 || permissions.MinLevelForDeleteVM != 0 ||
		permissions.MinLevelForResetContainer != 0 || permissions.MinLevelForResetVM != 0 {
		global.APP_LOG.Info("实例类型权限配置已存在，跳过初始化")
		return
	}

	// 设置默认权限配置
	global.APP_CONFIG.Quota.InstanceTypePermissions = config.InstanceTypePermissions{
		MinLevelForContainer:       1, // 所有等级用户都可以创建容器
		MinLevelForVM:              3, // 等级3及以上可以创建虚拟机
		MinLevelForDeleteContainer: 1, // 等级1及以上可以删除容器
		MinLevelForDeleteVM:        2, // 等级2及以上可以删除虚拟机
		MinLevelForResetContainer:  1, // 等级1及以上可以重置容器系统
		MinLevelForResetVM:         2, // 等级2及以上可以重置虚拟机系统
	}

	global.APP_LOG.Info("实例类型权限配置初始化完成",
		zap.Int("minLevelForContainer", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer),
		zap.Int("minLevelForVM", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM),
		zap.Int("minLevelForDeleteContainer", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteContainer),
		zap.Int("minLevelForDeleteVM", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteVM),
		zap.Int("minLevelForResetContainer", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetContainer),
		zap.Int("minLevelForResetVM", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetVM))
}

// initOtherConfigurations 初始化其他配置
func initOtherConfigurations() {
	global.APP_LOG.Info("开始初始化其他配置")

	// 定义需要初始化的配置项
	configs := []admin.SystemConfig{
		{
			Key:         "max_avatar_size",
			Value:       "2", // 默认2MB
			Description: "用户头像上传的最大文件大小限制（单位：MB），仅支持PNG和JPEG格式",
			Category:    "other",
			Type:        "number",
			IsPublic:    false,
		},
		{
			Key:         "default_language",
			Value:       "", // 空字符串表示使用浏览器语言
			Description: "系统默认语言设置，支持zh-CN（中文）和en-US（英文）。留空则根据浏览器语言自动选择，非中文时显示英文，检测不到时默认显示中文",
			Category:    "other",
			Type:        "string",
			IsPublic:    true, // 公开配置，登录前也可访问
		},
	}

	dbService := database.GetDatabaseService()

	// 遍历配置项，检查并创建
	for _, config := range configs {
		var existingConfig admin.SystemConfig
		result := global.APP_DB.Where("key = ?", config.Key).First(&existingConfig)

		if result.Error != nil {
			// 配置不存在，创建默认配置
			err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Create(&config).Error
			})

			if err != nil {
				global.APP_LOG.Error(fmt.Sprintf("创建%s配置失败", config.Key), zap.Error(err))
			} else {
				global.APP_LOG.Info(fmt.Sprintf("已创建%s默认配置", config.Key), zap.String("value", config.Value))
			}
		} else {
			global.APP_LOG.Info(fmt.Sprintf("%s配置已存在，跳过初始化", config.Key))
		}
	}
}
