package source

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"oneclickvirt/service/database"
	"regexp"
	"strings"

	"oneclickvirt/config"
	"oneclickvirt/global"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/system"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// InitSeedData 初始化种子数据，确保不重复创建
func InitSeedData() {
	initDefaultRoles()
	initDefaultMenus()
	initDefaultAPIs()
	initDefaultAnnouncements()
	initLevelConfigurations()
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

func initDefaultMenus() {
	menus := []auth.Menu{
		{Name: "仪表盘", Title: "仪表盘", Path: "/dashboard", Component: "dashboard/index", Icon: "dashboard", Sort: 1, Status: 1},
		{Name: "虚拟化管理", Title: "虚拟化管理", Path: "/virtualization", Component: "virtualization/index", Icon: "server", Sort: 2, Status: 1},
		{Name: "系统管理", Title: "系统管理", Path: "/system", Component: "system/index", Icon: "setting", Sort: 3, Status: 1},
	}

	for _, menu := range menus {
		var count int64
		global.APP_DB.Model(&auth.Menu{}).Where("name = ? AND path = ?", menu.Name, menu.Path).Count(&count)
		if count == 0 {
			dbService := database.GetDatabaseService()
			dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Create(&menu).Error
			})
		}
	}
}

func initDefaultAPIs() {
	apis := []auth.Api{
		{Path: "/api/v1/auth/login", Method: "POST", Description: "用户登录", Group: "认证"},
		{Path: "/api/v1/auth/logout", Method: "POST", Description: "用户退出", Group: "认证"},
		{Path: "/api/v1/virtualization/provider", Method: "GET", Description: "获取提供商列表", Group: "虚拟化"},
		{Path: "/api/v1/virtualization/instances", Method: "GET", Description: "获取实例列表", Group: "虚拟化"},
		{Path: "/api/v1/virtualization/instances", Method: "POST", Description: "创建实例", Group: "虚拟化"},
	}

	for _, api := range apis {
		var count int64
		global.APP_DB.Model(&auth.Api{}).Where("path = ? AND method = ?", api.Path, api.Method).Count(&count)
		if count == 0 {
			dbService := database.GetDatabaseService()
			dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Create(&api).Error
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
			Content:     "为了提供更好的服务质量，我们会定期进行系统维护。维护期间可能会影响部分功能的使用，请您谅解。",
			ContentHTML: "<p>为了提供更好的服务质量，我们会定期进行系统维护。</p>",
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

	// 从配置获取基础CDN端点
	baseCDN := utils.GetBaseCDNEndpoint()
	imageURL := baseCDN + "https://raw.githubusercontent.com/oneclickvirt/images_auto_list/refs/heads/main/images.txt"

	// 获取镜像列表
	resp, err := http.Get(imageURL)
	if err != nil {
		global.APP_LOG.Error("获取镜像列表失败", zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		global.APP_LOG.Error("获取镜像列表失败", zap.Int("status", resp.StatusCode))
		return
	}

	// 收集所有镜像URL
	var imageURLs []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		imageURL := strings.TrimSpace(scanner.Text())
		if imageURL != "" {
			imageURLs = append(imageURLs, imageURL)
		}
	}

	if err := scanner.Err(); err != nil {
		global.APP_LOG.Error("读取镜像列表失败", zap.Error(err))
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
			} // 检查是否已存在
			var existingImage system.SystemImage
			result := global.APP_DB.Where("name = ? AND provider_type = ? AND instance_type = ? AND architecture = ?",
				imageInfo.Name, imageInfo.ProviderType, imageInfo.InstanceType, imageInfo.Architecture).First(&existingImage)

			if result.Error != nil {
				// 创建新镜像记录
				systemImage := system.SystemImage{
					Name:         imageInfo.Name,
					ProviderType: imageInfo.ProviderType,
					InstanceType: imageInfo.InstanceType,
					Architecture: imageInfo.Architecture,
					URL:          imageInfo.URL,
					Status:       "active",
					Description:  imageInfo.Description,
					OSType:       imageInfo.OSType,
					OSVersion:    imageInfo.OSVersion,
					CreatedBy:    nil, // 系统创建，设为nil
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
			"memory":    512,   // 512MB
			"disk":      10240, // 10GB
			"bandwidth": 10,    // 10Mbps
		},
		MaxTraffic: 102400, // 100GB
	}

	// 等级2: 中级档次
	global.APP_CONFIG.Quota.LevelLimits[2] = config.LevelLimitInfo{
		MaxInstances: 3,
		MaxResources: map[string]interface{}{
			"cpu":       2,
			"memory":    1024,  // 1GB
			"disk":      20480, // 20GB
			"bandwidth": 20,    // 20Mbps
		},
		MaxTraffic: 204800, // 200GB
	}

	// 等级3: 高级档次
	global.APP_CONFIG.Quota.LevelLimits[3] = config.LevelLimitInfo{
		MaxInstances: 5,
		MaxResources: map[string]interface{}{
			"cpu":       4,
			"memory":    2048,  // 2GB
			"disk":      40960, // 40GB
			"bandwidth": 50,    // 50Mbps
		},
		MaxTraffic: 307200, // 300GB
	}

	// 等级4: 超级档次
	global.APP_CONFIG.Quota.LevelLimits[4] = config.LevelLimitInfo{
		MaxInstances: 10,
		MaxResources: map[string]interface{}{
			"cpu":       8,
			"memory":    4096,  // 4GB
			"disk":      81920, // 80GB
			"bandwidth": 100,   // 100Mbps
		},
		MaxTraffic: 409600, // 400GB
	}

	// 等级5: 管理员档次
	global.APP_CONFIG.Quota.LevelLimits[5] = config.LevelLimitInfo{
		MaxInstances: 20,
		MaxResources: map[string]interface{}{
			"cpu":       16,
			"memory":    8192,   // 8GB
			"disk":      163840, // 160GB
			"bandwidth": 200,    // 200Mbps
		},
		MaxTraffic: 512000, // 500GB
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
	if permissions.MinLevelForContainer != 0 || permissions.MinLevelForVM != 0 || permissions.MinLevelForDelete != 0 {
		global.APP_LOG.Info("实例类型权限配置已存在，跳过初始化")
		return
	}

	// 设置默认权限配置
	global.APP_CONFIG.Quota.InstanceTypePermissions = config.InstanceTypePermissions{
		MinLevelForContainer: 1, // 所有等级用户都可以创建容器
		MinLevelForVM:        3, // 等级3及以上可以创建虚拟机
		MinLevelForDelete:    2, // 等级2及以上可以自行删除实例
	}

	global.APP_LOG.Info("实例类型权限配置初始化完成",
		zap.Int("minLevelForContainer", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer),
		zap.Int("minLevelForVM", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM),
		zap.Int("minLevelForDelete", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDelete))
}
