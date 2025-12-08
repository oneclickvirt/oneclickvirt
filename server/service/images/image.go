package images

import (
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/image"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/model/system"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

type ImageService struct{}

// DownloadImage 下载镜像
func (s *ImageService) DownloadImage(req image.DownloadImageRequest) error {
	// 获取系统镜像信息
	var systemImage system.SystemImage
	if err := global.APP_DB.First(&systemImage, req.ImageID).Error; err != nil {
		return fmt.Errorf("系统镜像不存在: %w", err)
	}

	// 检查镜像是否支持指定的provider类型和架构
	if !s.isImageCompatible(systemImage, req.ProviderType, req.InstanceType, req.Architecture) {
		return fmt.Errorf("镜像不支持指定的provider类型或架构")
	}

	// 获取镜像下载URL
	downloadURL := s.getImageDownloadURL(systemImage, req.Architecture)
	if downloadURL == "" {
		return fmt.Errorf("无法获取镜像下载URL")
	}

	// 创建下载服务
	downloadService := NewImageDownloadService()

	// 执行下载
	_, err := downloadService.DownloadImageForProvider(
		downloadURL,
		systemImage.Name,
		"", // provider country，这里可以根据需要添加
		req.Architecture,
		req.ProviderType,
	)

	if err != nil {
		global.APP_LOG.Error("镜像下载失败",
			zap.Uint("imageId", req.ImageID),
			zap.String("imageName", systemImage.Name),
			zap.String("providerType", req.ProviderType),
			zap.String("architecture", req.Architecture),
			zap.Error(err))
		return fmt.Errorf("镜像下载失败: %w", err)
	}

	global.APP_LOG.Info("镜像下载成功",
		zap.Uint("imageId", req.ImageID),
		zap.String("imageName", systemImage.Name),
		zap.String("providerType", req.ProviderType),
		zap.String("architecture", req.Architecture))

	return nil
}

// isImageCompatible 检查镜像是否与指定的provider类型和架构兼容
func (s *ImageService) isImageCompatible(systemImage system.SystemImage, providerType, instanceType, architecture string) bool {
	// 检查Provider类型兼容性
	if systemImage.ProviderType != providerType {
		return false
	}

	// 检查实例类型兼容性
	if systemImage.InstanceType != instanceType {
		return false
	}

	// 检查架构兼容性
	if systemImage.Architecture != architecture {
		return false
	}

	// 检查镜像状态
	if systemImage.Status != "active" {
		return false
	}

	return true
}

// getImageDownloadURL 获取镜像下载URL
func (s *ImageService) getImageDownloadURL(systemImage system.SystemImage, architecture string) string {
	// 检查架构是否匹配
	if systemImage.Architecture != architecture {
		return ""
	}

	// 处理CDN加速
	imageURL := systemImage.URL
	if systemImage.UseCDN {
		baseCDN := utils.GetBaseCDNEndpoint()
		if baseCDN != "" {
			imageURL = baseCDN + systemImage.URL
		}
	}

	return imageURL
}

// GetImageDownloadPath 获取镜像下载路径
func (s *ImageService) GetImageDownloadPath(providerType, instanceType string) string {
	baseDir := "/usr/local/bin"

	switch providerType {
	case "proxmox":
		if instanceType == "vm" {
			return filepath.Join(baseDir, "proxmox_vm_images")
		}
		return filepath.Join(baseDir, "proxmox_images")
	case "lxd":
		if instanceType == "vm" {
			return filepath.Join(baseDir, "lxd_vm_images")
		}
		return filepath.Join(baseDir, "lxd_container_images")
	case "incus":
		if instanceType == "vm" {
			return filepath.Join(baseDir, "incus_vm_images")
		}
		return filepath.Join(baseDir, "incus_container_images")
	case "docker":
		return filepath.Join(baseDir, "docker_images")
	default:
		return filepath.Join(baseDir, "images")
	}
}

// GetImageFileName 根据URL获取文件名
func (s *ImageService) GetImageFileName(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "image"
}

// FileExists 检查文件是否存在
func (s *ImageService) FileExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return err == nil
}

// CreateDirectory 创建目录
func (s *ImageService) CreateDirectory(dirPath string) error {
	return os.MkdirAll(dirPath, 0755)
}

// CalculateFileMD5 计算文件MD5
func (s *ImageService) CalculateFileMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// DownloadFile 下载文件
func (s *ImageService) DownloadFile(url, filePath string) error {
	// 创建目录（使用全局工具函数）
	dir := filepath.Dir(filePath)
	if err := utils.EnsureDir(dir); err != nil {
		return fmt.Errorf("创建目录失败: %v", err)
	}

	// 创建文件
	out, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer out.Close()

	// 下载文件，使用HTTP客户端（带连接池）
	client := utils.GetHTTPClientWithTimeout(10 * time.Minute)
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}

	_, err = io.Copy(out, resp.Body)
	return err
}

// PrepareImageForInstance 为实例准备镜像信息（不再进行本地下载）
func (s *ImageService) PrepareImageForInstance(req image.DownloadImageRequest) (string, error) {
	global.APP_LOG.Debug("开始准备镜像信息",
		zap.Uint("imageId", req.ImageID),
		zap.String("providerType", req.ProviderType),
		zap.String("instanceType", req.InstanceType),
		zap.String("architecture", req.Architecture))

	// 根据镜像ID查询镜像信息
	var systemImage system.SystemImage
	if err := global.APP_DB.First(&systemImage, req.ImageID).Error; err != nil {
		global.APP_LOG.Error("查询系统镜像失败",
			zap.Uint("imageId", req.ImageID),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return "", fmt.Errorf("未找到系统镜像: %v", err)
	}

	// 验证参数匹配
	if systemImage.ProviderType != req.ProviderType ||
		systemImage.InstanceType != req.InstanceType ||
		systemImage.Architecture != req.Architecture {
		global.APP_LOG.Warn("镜像参数不匹配",
			zap.Uint("imageId", req.ImageID),
			zap.String("reqProviderType", req.ProviderType),
			zap.String("imageProviderType", systemImage.ProviderType),
			zap.String("reqInstanceType", req.InstanceType),
			zap.String("imageInstanceType", systemImage.InstanceType),
			zap.String("reqArchitecture", req.Architecture),
			zap.String("imageArchitecture", systemImage.Architecture))
		return "", fmt.Errorf("镜像参数不匹配")
	}

	// 检查镜像状态
	if systemImage.Status != "active" {
		global.APP_LOG.Warn("镜像未激活",
			zap.Uint("imageId", req.ImageID),
			zap.String("status", systemImage.Status))
		return "", fmt.Errorf("镜像未激活")
	}

	// 处理镜像URL，根据UseCDN字段决定是否使用CDN加速
	imageURL := systemImage.URL
	if systemImage.UseCDN {
		// 如果启用CDN，添加CDN前缀
		baseCDN := utils.GetBaseCDNEndpoint()
		if baseCDN != "" {
			imageURL = baseCDN + systemImage.URL
			global.APP_LOG.Info("使用CDN加速镜像下载",
				zap.Uint("imageId", req.ImageID),
				zap.String("originalURL", utils.TruncateString(systemImage.URL, 100)),
				zap.String("cdnURL", utils.TruncateString(imageURL, 100)))
		}
	}

	global.APP_LOG.Info("镜像信息准备完成",
		zap.Uint("imageId", req.ImageID),
		zap.String("imageURL", utils.TruncateString(imageURL, 200)),
		zap.Bool("useCDN", systemImage.UseCDN))

	// 返回处理后的镜像URL，让各个Provider自己处理下载
	return imageURL, nil
}

// GetAvailableImages 获取可用镜像列表
func (s *ImageService) GetAvailableImages(providerType, instanceType, architecture string) ([]system.SystemImage, error) {
	return s.GetAvailableImagesWithOS(providerType, instanceType, architecture, "")
}

// GetAvailableImagesWithOS 获取可用的系统镜像（带操作系统过滤）
func (s *ImageService) GetAvailableImagesWithOS(providerType, instanceType, architecture, osType string) ([]system.SystemImage, error) {
	var images []system.SystemImage

	global.APP_LOG.Debug("查询可用镜像",
		zap.String("providerType", providerType),
		zap.String("instanceType", instanceType),
		zap.String("architecture", architecture),
		zap.String("osType", osType))

	query := global.APP_DB.Where("status = ?", "active")

	if providerType != "" {
		query = query.Where("provider_type = ?", providerType)
	}
	if instanceType != "" {
		query = query.Where("instance_type = ?", instanceType)
	}
	if architecture != "" {
		query = query.Where("architecture = ?", architecture)
	}
	if osType != "" {
		// 使用小写匹配，支持主流Linux系统
		query = query.Where("LOWER(os_type) = LOWER(?)", osType)
	}

	if err := query.Order("created_at DESC").Find(&images).Error; err != nil {
		global.APP_LOG.Error("查询可用镜像失败",
			zap.String("providerType", providerType),
			zap.String("instanceType", instanceType),
			zap.String("architecture", architecture),
			zap.String("osType", osType),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, err
	}

	global.APP_LOG.Debug("查询可用镜像成功",
		zap.Int("imageCount", len(images)),
		zap.String("providerType", providerType),
		zap.String("instanceType", instanceType),
		zap.String("architecture", architecture),
		zap.String("osType", osType))

	return images, nil
}

// GetFilteredImages 根据Provider和实例类型获取过滤后的镜像列表
func (s *ImageService) GetFilteredImages(providerID uint, instanceType string) ([]system.SystemImage, error) {
	// 获取Provider信息
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在: %v", err)
	}

	// 验证Provider是否支持该实例类型
	if instanceType == "container" && !provider.ContainerEnabled {
		return nil, fmt.Errorf("该Provider不支持容器类型")
	}
	if instanceType == "vm" && !provider.VirtualMachineEnabled {
		return nil, fmt.Errorf("该Provider不支持虚拟机类型")
	}

	// 设置默认架构
	architecture := provider.Architecture
	if architecture == "" {
		architecture = "amd64" // 默认amd64架构
	}

	// 根据Provider类型、实例类型和架构过滤镜像
	return s.GetAvailableImages(provider.Type, instanceType, architecture)
}
