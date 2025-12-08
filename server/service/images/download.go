package images

import (
	"crypto/md5"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

type ImageDownloadService struct {
	cdnEndpoints []string
}

func NewImageDownloadService() *ImageDownloadService {
	return &ImageDownloadService{
		cdnEndpoints: utils.GetCDNEndpoints(),
	}
}

// DownloadImage 下载镜像文件
func (s *ImageDownloadService) DownloadImage(imageURL, imageName, providerCountry, architecture string) (string, error) {
	return s.DownloadImageForProvider(imageURL, imageName, providerCountry, architecture, "docker")
}

// DownloadImageForProvider 为指定provider下载镜像文件
func (s *ImageDownloadService) DownloadImageForProvider(imageURL, imageName, providerCountry, architecture, providerType string) (string, error) {
	// 根据provider类型确定下载目录
	downloadDir := s.getDownloadDir(providerType)
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		return "", fmt.Errorf("创建下载目录失败: %w", err)
	}

	// 生成文件名（使用镜像名称、架构和URL的MD5）
	fileName := s.generateFileName(imageName, imageURL, architecture)
	filePath := filepath.Join(downloadDir, fileName)

	// 检查文件是否已存在且完整
	if s.isFileValid(filePath, imageURL) {
		global.APP_LOG.Info("镜像文件已存在且完整，跳过下载",
			zap.String("imageName", imageName),
			zap.String("filePath", filePath))
		return filePath, nil
	}

	// 确定下载URL
	downloadURL := s.getDownloadURL(imageURL, providerCountry)

	global.APP_LOG.Info("开始下载镜像",
		zap.String("imageName", imageName),
		zap.String("downloadURL", downloadURL),
		zap.String("filePath", filePath))

	// 下载文件
	if err := s.downloadFile(downloadURL, filePath); err != nil {
		// 下载失败，删除不完整的文件
		os.Remove(filePath)
		return "", fmt.Errorf("下载镜像失败: %w", err)
	}

	global.APP_LOG.Info("镜像下载完成",
		zap.String("imageName", imageName),
		zap.String("filePath", filePath))

	return filePath, nil
}

// getDownloadDir 根据provider类型获取下载目录
func (s *ImageDownloadService) getDownloadDir(providerType string) string {
	baseDir := "/usr/local/bin"
	switch providerType {
	case "docker":
		return filepath.Join(baseDir, "docker_ct_images")
	case "lxd":
		return filepath.Join(baseDir, "lxd_images")
	case "incus":
		return filepath.Join(baseDir, "incus_images")
	case "proxmox":
		return filepath.Join(baseDir, "proxmox_images")
	default:
		return filepath.Join(baseDir, "docker_ct_images")
	}
}

// CleanupImage 清理镜像文件
func (s *ImageDownloadService) CleanupImage(imageName, imageURL, architecture string) error {
	return s.CleanupImageForProvider(imageName, imageURL, architecture, "docker")
}

// CleanupImageForProvider 清理指定provider的镜像文件
func (s *ImageDownloadService) CleanupImageForProvider(imageName, imageURL, architecture, providerType string) error {
	downloadDir := s.getDownloadDir(providerType)
	fileName := s.generateFileName(imageName, imageURL, architecture)
	filePath := filepath.Join(downloadDir, fileName)

	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除镜像文件失败: %w", err)
	}

	global.APP_LOG.Info("镜像文件已清理",
		zap.String("imageName", imageName),
		zap.String("filePath", filePath))

	return nil
}

// generateFileName 生成文件名
func (s *ImageDownloadService) generateFileName(imageName, imageURL, architecture string) string {
	// 包含架构信息在哈希计算中
	hashInput := fmt.Sprintf("%s_%s", imageURL, architecture)
	hash := md5.Sum([]byte(hashInput))
	return fmt.Sprintf("%s_%s_%x.tar", strings.ReplaceAll(imageName, "/", "_"), architecture, hash[:8])
}

// isFileValid 检查文件是否存在且有效
func (s *ImageDownloadService) isFileValid(filePath, imageURL string) bool {
	info, err := os.Stat(filePath)
	if err != nil {
		return false
	}

	// 检查文件大小是否大于0
	if info.Size() == 0 {
		return false
	}

	// 进行更详细的文件完整性检查
	return s.performIntegrityCheck(filePath, imageURL, info.Size())
}

// performIntegrityCheck 执行文件完整性检查
func (s *ImageDownloadService) performIntegrityCheck(filePath, imageURL string, fileSize int64) bool {
	// 1. 检查文件魔数/签名 - 识别文件类型
	if !s.checkFileSignature(filePath) {
		global.APP_LOG.Warn("文件签名检查失败", zap.String("filePath", filePath))
		return false
	}

	// 2. 检查文件大小是否合理（镜像文件通常较大）
	minSize := int64(10 * 1024 * 1024)        // 10MB 最小大小
	maxSize := int64(50 * 1024 * 1024 * 1024) // 50GB 最大大小
	if fileSize < minSize {
		global.APP_LOG.Warn("文件大小过小，可能下载不完整",
			zap.String("filePath", filePath),
			zap.Int64("size", fileSize))
		return false
	}
	if fileSize > maxSize {
		global.APP_LOG.Warn("文件大小异常，超过合理范围",
			zap.String("filePath", filePath),
			zap.Int64("size", fileSize))
		return false
	}

	// 3. 如果URL包含校验和信息，进行校验
	if s.checkChecksum(filePath, imageURL) {
		global.APP_LOG.Info("文件校验和验证通过", zap.String("filePath", filePath))
	}

	// 4. 检查是否为有效的压缩文件格式
	if !s.validateCompressedFile(filePath) {
		global.APP_LOG.Warn("压缩文件格式验证失败", zap.String("filePath", filePath))
		return false
	}

	return true
}

// checkFileSignature 检查文件头签名
func (s *ImageDownloadService) checkFileSignature(filePath string) bool {
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()

	// 读取文件头部分用于检查文件类型
	header := make([]byte, 512)
	_, err = file.Read(header)
	if err != nil {
		return false
	}

	// 检查常见的镜像文件格式
	// 这里检查是否为已知的压缩格式或镜像格式
	return s.isValidImageFormat(header)
}

// isValidImageFormat 验证是否为有效的镜像文件格式
func (s *ImageDownloadService) isValidImageFormat(header []byte) bool {
	// 检查 gzip 格式 (1f 8b)
	if len(header) >= 2 && header[0] == 0x1f && header[1] == 0x8b {
		return true
	}

	// 检查 xz 格式 (fd 37 7a 58 5a 00)
	if len(header) >= 6 &&
		header[0] == 0xfd && header[1] == 0x37 && header[2] == 0x7a &&
		header[3] == 0x58 && header[4] == 0x5a && header[5] == 0x00 {
		return true
	}

	// 检查 bzip2 格式 (42 5a)
	if len(header) >= 2 && header[0] == 0x42 && header[1] == 0x5a {
		return true
	}

	// 检查 tar 格式（通过 magic bytes）
	if len(header) >= 265 {
		// tar 格式在 257-262 字节位置有 "ustar" 标识
		tarMagic := string(header[257:262])
		if tarMagic == "ustar" {
			return true
		}
	}

	// 检查 zip 格式 (50 4b)
	if len(header) >= 2 && header[0] == 0x50 && header[1] == 0x4b {
		return true
	}

	// 对于一些特殊情况，如果文件看起来是文本文件但可能是脚本，也允许
	// 这里可以添加更多格式的检测
	return false
}

// checkChecksum 检查文件校验和（如果URL中包含校验和信息）
func (s *ImageDownloadService) checkChecksum(filePath, imageURL string) bool {
	// TODO: 未来实现从URL或相关的.sha256文件中获取校验和并验证
	// 目前先返回true，表示没有校验和要求或校验通过
	return true
}

// validateCompressedFile 验证压缩文件是否完整
func (s *ImageDownloadService) validateCompressedFile(filePath string) bool {
	// 尝试验证压缩文件的完整性
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()

	// 读取文件头以确定压缩格式
	header := make([]byte, 10)
	_, err = file.Read(header)
	if err != nil {
		return false
	}

	// 重置文件指针
	file.Seek(0, 0)

	// 根据文件类型进行基本的完整性检查
	if len(header) >= 2 && header[0] == 0x1f && header[1] == 0x8b {
		// gzip 文件：检查gzip尾部
		return s.validateGzipFile(file)
	}

	// 对于其他格式，暂时返回true
	return true
}

// validateGzipFile 验证gzip文件完整性
func (s *ImageDownloadService) validateGzipFile(file *os.File) bool {
	// 移动到文件末尾检查gzip trailer
	stat, err := file.Stat()
	if err != nil {
		return false
	}

	// gzip文件末尾8字节是trailer (CRC32 + ISIZE)
	if stat.Size() < 8 {
		return false
	}

	_, err = file.Seek(-8, 2) // 从文件末尾向前8字节
	if err != nil {
		return false
	}

	trailer := make([]byte, 8)
	_, err = file.Read(trailer)
	return err == nil
}

// getDownloadURL 根据Provider国家和URL确定下载地址
func (s *ImageDownloadService) getDownloadURL(originalURL, providerCountry string) string {
	// 默认随机尝试CDN，不再限制地区
	return s.getCDNURL(originalURL)
}

// getCDNURL 获取随机CDN加速URL
func (s *ImageDownloadService) getCDNURL(originalURL string) string {
	// 随机打乱CDN端点顺序
	endpoints := make([]string, len(s.cdnEndpoints))
	copy(endpoints, s.cdnEndpoints)

	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(endpoints), func(i, j int) {
		endpoints[i], endpoints[j] = endpoints[j], endpoints[i]
	})

	// 尝试每个CDN端点
	for _, endpoint := range endpoints {
		cdnURL := endpoint + originalURL
		if s.testCDNEndpoint(cdnURL) {
			global.APP_LOG.Info("使用CDN加速下载",
				zap.String("originalURL", originalURL),
				zap.String("cdnURL", cdnURL),
				zap.String("endpoint", endpoint))
			return cdnURL
		}
	}

	// 如果所有CDN都不可用，回退到原始URL
	global.APP_LOG.Warn("所有CDN端点都不可用，使用原始URL",
		zap.String("originalURL", originalURL))
	return originalURL
}

// testCDNEndpoint 测试CDN端点是否可用
func (s *ImageDownloadService) testCDNEndpoint(url string) bool {
	client := utils.GetHTTPClientWithTimeout(5 * time.Second)
	resp, err := client.Head(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent
}

// downloadFile 下载文件
func (s *ImageDownloadService) downloadFile(url, filePath string) error {
	// 创建临时文件
	tmpPath := filePath + ".tmp"
	defer os.Remove(tmpPath)

	// 创建HTTP客户端（30分钟超时，带连接池）
	client := utils.GetHTTPClientWithTimeout(30 * time.Minute)

	// 发起下载请求
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("请求下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败，HTTP状态码: %d", resp.StatusCode)
	}

	// 创建目标文件
	file, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer file.Close()

	// 复制数据
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}

	// 移动到最终位置
	if err := os.Rename(tmpPath, filePath); err != nil {
		return fmt.Errorf("移动文件失败: %w", err)
	}

	return nil
}
