package source

import (
	"context"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/system"
	"oneclickvirt/service/database"

	"gorm.io/gorm"
)

// InitSeedData 初始化种子数据，确保不重复创建
func InitSeedData() {
	initDefaultRoles()
	initDefaultAnnouncements()
	initLevelConfigurations()
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
			Content:     "欢迎使用虚拟化管理平台，支持Docker、Podman、Containerd、LXD、Incus、Proxmox VE等多种虚拟化技术。本平台提供简单易用的Web界面，让您轻松管理各种虚拟化资源。",
			ContentHTML: "<p>欢迎使用虚拟化管理平台，支持<strong>Docker</strong>、<strong>Podman</strong>、<strong>Containerd</strong>、<strong>LXD</strong>、<strong>Incus</strong>、<strong>Proxmox VE</strong>等多种虚拟化技术。</p><p>本平台提供简单易用的Web界面，让您轻松管理各种虚拟化资源。</p>",
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
