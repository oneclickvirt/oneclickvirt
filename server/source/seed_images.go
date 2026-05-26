package source

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/system"
	"oneclickvirt/service/database"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

func SeedSystemImages() {
	global.APP_LOG.Info("开始同步系统镜像列表")

	// 初始化等级配置
	initLevelConfigurations()

	// 检查是否已经有镜像数据
	var count int64
	global.APP_DB.Model(&system.SystemImage{}).Count(&count)
	if count > 0 {
		global.APP_LOG.Debug("镜像数据已存在，跳过同步", zap.Int64("count", count))
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
		global.APP_LOG.Debug("使用默认镜像列表进行初始化")
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

				// 判断是否使用CDN：仅对GitHub链接启用CDN加速，非GitHub链接（如官方上游）不启用
				useCDN := isGitHubURL(imageInfo.URL)

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
					UseCDN:       useCDN,
					CreatedBy:    nil, // 系统创建，设为nil
				}

				dbService := database.GetDatabaseService()
				if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
					return tx.Create(&systemImage).Error
				}); err != nil {
					global.APP_LOG.Warn("创建镜像记录失败", zap.Error(err), zap.String("name", imageInfo.Name))
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

				// 对于 KVM qcow2 虚拟机镜像，同时为 QEMU 和 KubeVirt 创建镜像记录
				// 因为 pve_kvm_images/kvm_images 的 qcow2 镜像是通用的 cloud image，
				// 可以被 Proxmox、QEMU(libvirt) 和 KubeVirt(CDI) 三种 provider 共用
				if imageInfo.InstanceType == "vm" && strings.HasSuffix(imageInfo.URL, ".qcow2") {
					for _, extraProvider := range []string{"qemu", "kubevirt"} {
						// 使用镜像名称作为去重key，避免同osType+osVersion的不同镜像（如centos8 vs centos8-stream）互相阻塞
						extraKey := fmt.Sprintf("%s-%s-%s-%s",
							extraProvider, imageInfo.InstanceType, imageInfo.Architecture,
							imageInfo.Name)

						if importedImages[extraKey] {
							continue
						}

						var existingExtra system.SystemImage
						checkResult := global.APP_DB.Where("name = ? AND provider_type = ? AND instance_type = ? AND architecture = ?",
							imageInfo.Name, extraProvider, imageInfo.InstanceType, imageInfo.Architecture).First(&existingExtra)
						if checkResult.Error == nil {
							importedImages[extraKey] = true
							continue
						}

						providerLabel := strings.ToUpper(extraProvider[:1]) + extraProvider[1:]
						extraImage := system.SystemImage{
							Name:         imageInfo.Name,
							ProviderType: extraProvider,
							InstanceType: "vm",
							Architecture: imageInfo.Architecture,
							URL:          imageInfo.URL,
							Status:       imageStatus,
							Description:  fmt.Sprintf("%s KVM %s image", providerLabel, imageInfo.Name),
							OSType:       imageInfo.OSType,
							OSVersion:    imageInfo.OSVersion,
							MinMemoryMB:  minMemoryMB,
							MinDiskMB:    minDiskMB,
							UseCDN:       useCDN,
							CreatedBy:    nil,
						}

						if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
							return tx.Create(&extraImage).Error
						}); err != nil {
							global.APP_LOG.Warn("创建额外provider镜像记录失败",
								zap.Error(err),
								zap.String("name", imageInfo.Name),
								zap.String("provider", extraProvider))
						} else {
							processedCount++
							importedImages[extraKey] = true
							global.APP_LOG.Debug("导入额外provider镜像成功",
								zap.String("name", imageInfo.Name),
								zap.String("provider", extraProvider))
						}
					}
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

	// Podman镜像
	podmanRe := regexp.MustCompile(`https://github\.com/oneclickvirt/podman/releases/download/([^/]+)/spiritlhl_([^_]+)_([^.]+)\.tar\.gz`)
	if matches := podmanRe.FindStringSubmatch(imageURL); matches != nil {
		return &ImageInfo{
			Name:         fmt.Sprintf("spiritlhl-%s", matches[2]),
			ProviderType: "podman",
			InstanceType: "container",
			Architecture: convertArch(matches[3]),
			URL:          imageURL,
			OSType:       matches[2],
			OSVersion:    "latest",
			Description:  fmt.Sprintf("Podman %s %s image", matches[2], matches[3]),
		}
	}

	// Containerd镜像
	containerdRe := regexp.MustCompile(`https://github\.com/oneclickvirt/containerd/releases/download/([^/]+)/spiritlhl_([^_]+)_([^.]+)\.tar\.gz`)
	if matches := containerdRe.FindStringSubmatch(imageURL); matches != nil {
		return &ImageInfo{
			Name:         fmt.Sprintf("spiritlhl-%s", matches[2]),
			ProviderType: "containerd",
			InstanceType: "container",
			Architecture: convertArch(matches[3]),
			URL:          imageURL,
			OSType:       matches[2],
			OSVersion:    "latest",
			Description:  fmt.Sprintf("Containerd %s %s image", matches[2], matches[3]),
		}
	}

	// Proxmox KVM镜像（pve_kvm_images）
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

	// KVM镜像（kvm_images仓库）
	kvmImagesRe := regexp.MustCompile(`https://github\.com/oneclickvirt/kvm_images/releases/download/([^/]+)/([^.]+)\.qcow2`)
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

// isGitHubURL 判断URL是否为GitHub链接
// 仅对GitHub链接启用CDN加速，非GitHub链接（如官方上游镜像站）不应使用CDN
func isGitHubURL(url string) bool {
	return strings.Contains(url, "github.com/") || strings.Contains(url, "raw.githubusercontent.com/")
}

// initLevelConfigurations 初始化用户等级与带宽配置
