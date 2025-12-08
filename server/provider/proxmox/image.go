package proxmox

import (
	"context"
	"crypto/md5"
	"fmt"
	"path/filepath"
	"strings"

	"oneclickvirt/global"
	systemModel "oneclickvirt/model/system"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *ProxmoxProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if p.shouldUseAPI() {
		images, err := p.apiListImages(ctx)
		if err == nil {
			global.APP_LOG.Info("Proxmox API调用成功 - 获取镜像列表")
			return images, nil
		}
		global.APP_LOG.Warn("Proxmox API失败 - 获取镜像列表", zap.Error(err))

		// 检查是否可以回退到SSH
		if !p.shouldFallbackToSSH() {
			return nil, fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH方式 - 获取镜像列表")
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH")
	}

	return p.sshListImages(ctx)
}

func (p *ProxmoxProvider) PullImage(ctx context.Context, image string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	// 如果image是URL，下载镜像
	if strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
		return p.handleImageDownload(ctx, image)
	}

	// 根据执行规则判断使用哪种方式
	if p.shouldUseAPI() {
		err := p.apiPullImage(ctx, image)
		if err == nil {
			global.APP_LOG.Info("Proxmox API调用成功 - 拉取镜像", zap.String("image", utils.TruncateString(image, 100)))
			return nil
		}
		global.APP_LOG.Warn("Proxmox API失败 - 拉取镜像", zap.String("image", utils.TruncateString(image, 100)), zap.Error(err))

		// 检查是否可以回退到SSH
		if !p.shouldFallbackToSSH() {
			return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH方式 - 拉取镜像", zap.String("image", utils.TruncateString(image, 100)))
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return p.sshPullImage(ctx, image)
}

// handleImageDownload 处理镜像下载
func (p *ProxmoxProvider) handleImageDownload(ctx context.Context, imageURL string) error {
	global.APP_LOG.Info("开始处理Proxmox镜像下载",
		zap.String("imageURL", utils.TruncateString(imageURL, 200)))

	// 从URL中提取镜像名
	imageName := p.extractImageName(imageURL)

	// 检查镜像是否已存在
	if p.imageExists(imageName) {
		global.APP_LOG.Info("Proxmox镜像已存在，跳过下载",
			zap.String("imageName", imageName))
		return nil
	}

	// 下载镜像到远程服务器
	remotePath, err := p.downloadImageToRemote(ctx, imageURL, imageName)
	if err != nil {
		return fmt.Errorf("下载镜像失败: %w", err)
	}

	global.APP_LOG.Info("Proxmox镜像下载完成",
		zap.String("imageName", imageName),
		zap.String("remotePath", remotePath))

	return nil
}

// extractImageName 从URL中提取镜像名
func (p *ProxmoxProvider) extractImageName(imageURL string) string {
	// 从URL中提取文件名
	parts := strings.Split(imageURL, "/")
	if len(parts) > 0 {
		fileName := parts[len(parts)-1]
		// 移除查询参数
		if idx := strings.Index(fileName, "?"); idx != -1 {
			fileName = fileName[:idx]
		}
		return fileName
	}
	return "proxmox_image"
}

// imageExists 检查镜像是否已存在
func (p *ProxmoxProvider) imageExists(imageName string) bool {
	// 检查ISO目录
	checkCmd := fmt.Sprintf("ls /var/lib/vz/template/iso/ | grep -i %s", imageName)
	output, err := p.sshClient.Execute(checkCmd)
	if err == nil && strings.TrimSpace(output) != "" {
		return true
	}

	// 检查cache目录
	checkCmd = fmt.Sprintf("ls /var/lib/vz/template/cache/ | grep -i %s", imageName)
	output, err = p.sshClient.Execute(checkCmd)
	if err == nil && strings.TrimSpace(output) != "" {
		return true
	}

	return false
}

// downloadImageToRemote 在远程服务器上下载镜像
func (p *ProxmoxProvider) downloadImageToRemote(ctx context.Context, imageURL, imageName string) (string, error) {
	// 根据文件类型确定下载目录
	var targetDir string
	if strings.HasSuffix(imageName, ".iso") {
		targetDir = "/var/lib/vz/template/iso"
	} else {
		targetDir = "/var/lib/vz/template/cache"
	}

	// 确保目录存在
	_, err := p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", targetDir))
	if err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	remotePath := fmt.Sprintf("%s/%s", targetDir, imageName)

	// 检查文件是否已存在
	checkCmd := fmt.Sprintf("test -f %s && echo 'exists'", remotePath)
	output, _ := p.sshClient.Execute(checkCmd)
	if strings.TrimSpace(output) == "exists" {
		global.APP_LOG.Info("镜像文件已存在", zap.String("path", remotePath))
		return remotePath, nil
	}

	// 下载文件
	if err := p.downloadFileToRemote(imageURL, remotePath); err != nil {
		return "", err
	}

	global.APP_LOG.Info("镜像下载到远程服务器完成",
		zap.String("imageName", imageName),
		zap.String("remotePath", remotePath))

	return remotePath, nil
}

// downloadFileToRemote 在远程服务器上下载文件
func (p *ProxmoxProvider) downloadFileToRemote(url, remotePath string) error {
	tmpPath := remotePath + ".tmp"

	// 下载文件，支持断点续传，优先使用wget，失败则使用curl
	downloadCmds := []string{
		fmt.Sprintf("wget --no-check-certificate -c -O %s '%s'", tmpPath, url),
		fmt.Sprintf("curl -4 -L -C - --connect-timeout 30 --retry 5 --retry-delay 10 --retry-max-time 0 -o %s '%s'", tmpPath, url),
	}

	var lastErr error
	for _, cmd := range downloadCmds {
		global.APP_LOG.Info("执行下载命令",
			zap.String("url", utils.TruncateString(url, 100)))

		output, err := p.sshClient.Execute(cmd)
		if err == nil {
			// 下载成功，移动文件到最终位置
			mvCmd := fmt.Sprintf("mv %s %s", tmpPath, remotePath)
			_, err = p.sshClient.Execute(mvCmd)
			if err != nil {
				global.APP_LOG.Error("移动文件失败",
					zap.String("tmpPath", tmpPath),
					zap.String("remotePath", remotePath),
					zap.Error(err))
				return fmt.Errorf("移动文件失败: %w", err)
			}

			global.APP_LOG.Info("下载成功",
				zap.String("url", utils.TruncateString(url, 100)),
				zap.String("remotePath", remotePath))
			return nil
		}

		lastErr = err
		global.APP_LOG.Warn("下载命令失败，尝试下一个",
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))

		// 清理临时文件
		p.sshClient.Execute(fmt.Sprintf("rm -f %s", tmpPath))
	}

	return fmt.Errorf("所有下载方式都失败: %w", lastErr)
}

func (p *ProxmoxProvider) DeleteImage(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if p.shouldUseAPI() {
		err := p.apiDeleteImage(ctx, id)
		if err == nil {
			global.APP_LOG.Info("Proxmox API调用成功 - 删除镜像", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		}
		global.APP_LOG.Warn("Proxmox API失败 - 删除镜像", zap.String("id", utils.TruncateString(id, 50)), zap.Error(err))

		// 检查是否可以回退到SSH
		if !p.shouldFallbackToSSH() {
			return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH方式 - 删除镜像", zap.String("id", utils.TruncateString(id, 50)))
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return p.sshDeleteImage(ctx, id)
}

// prepareImage 准备镜像，确保镜像存在且可用
func (p *ProxmoxProvider) prepareImage(ctx context.Context, imageName, instanceType string) error {
	global.APP_LOG.Info("准备Proxmox镜像",
		zap.String("image", imageName),
		zap.String("type", instanceType))

	// 创建配置结构
	config := &provider.InstanceConfig{
		Image:        imageName,
		InstanceType: instanceType,
	}

	// 首先从数据库查询匹配的系统镜像
	if err := p.queryAndSetSystemImage(ctx, config); err != nil {
		global.APP_LOG.Warn("从数据库查询系统镜像失败，使用原有镜像配置",
			zap.String("image", imageName),
			zap.Error(err))
	}

	// 如果有ImageURL，使用下载逻辑
	if config.ImageURL != "" {
		global.APP_LOG.Info("从数据库获取到镜像下载URL，开始下载",
			zap.String("imageURL", utils.TruncateString(config.ImageURL, 100)))

		return p.downloadImageFromURL(ctx, config.ImageURL, imageName, instanceType)
	}

	// 否则使用原有的模板检查逻辑
	if instanceType == "container" {
		global.APP_LOG.Warn("数据库中未找到镜像配置，无法准备容器镜像",
			zap.String("image", imageName))

		return fmt.Errorf("数据库中未找到镜像 %s 的配置，请联系管理员添加镜像", imageName)
	} else {
		// 对于VM，如果没有数据库配置，检查本地ISO文件
		global.APP_LOG.Warn("数据库中未找到VM镜像配置",
			zap.String("image", imageName))

		// 检查VM ISO文件是否存在
		checkCmd := fmt.Sprintf("ls /var/lib/vz/template/iso/ | grep -i %s", imageName)

		output, err := p.sshClient.Execute(checkCmd)
		if err != nil || strings.TrimSpace(output) == "" {
			// 镜像不存在，尝试下载
			return p.downloadImage(ctx, imageName, instanceType)
		}

		global.APP_LOG.Info("Proxmox VM镜像已存在",
			zap.String("image", imageName),
			zap.String("type", instanceType))
		return nil
	}
}

// downloadImage 下载镜像
func (p *ProxmoxProvider) downloadImage(ctx context.Context, imageName, instanceType string) error {
	global.APP_LOG.Info("开始下载Proxmox镜像",
		zap.String("image", imageName),
		zap.String("type", instanceType))

	// 检查是否有ImageURL配置
	config := &provider.InstanceConfig{
		Image:        imageName,
		InstanceType: instanceType,
	}

	// 从数据库查询镜像配置
	if err := p.queryAndSetSystemImage(ctx, config); err != nil {
		global.APP_LOG.Warn("从数据库查询镜像配置失败，回退到默认逻辑",
			zap.String("image", imageName),
			zap.Error(err))

		// 回退到原有的模板映射逻辑
		return p.downloadImageByTemplate(ctx, imageName, instanceType)
	}

	// 如果有ImageURL，使用下载逻辑
	if config.ImageURL != "" {
		return p.downloadImageFromURL(ctx, config.ImageURL, imageName, instanceType)
	}

	// 否则回退到模板逻辑
	return p.downloadImageByTemplate(ctx, imageName, instanceType)
}

// downloadImageFromURL 从URL下载镜像到远程服务器
func (p *ProxmoxProvider) downloadImageFromURL(ctx context.Context, imageURL, imageName, instanceType string) error {
	// 根据provider类型确定远程下载目录
	var downloadDir string
	if instanceType == "container" {
		downloadDir = "/var/lib/vz/template/cache"
	} else {
		downloadDir = "/var/lib/vz/template/iso"
	}

	// 生成远程文件名
	fileName := p.generateRemoteFileName(imageName, imageURL, p.config.Architecture)
	remotePath := filepath.Join(downloadDir, fileName)

	// 检查远程文件是否已存在且完整
	if p.isRemoteFileValid(remotePath) {
		global.APP_LOG.Info("远程镜像文件已存在且完整，跳过下载",
			zap.String("imageName", imageName),
			zap.String("remotePath", remotePath))
		return nil
	}

	global.APP_LOG.Info("开始在远程服务器下载镜像",
		zap.String("imageName", imageName),
		zap.String("downloadURL", imageURL),
		zap.String("remotePath", remotePath))

	// 在远程服务器上下载文件
	if err := p.downloadFileToRemote(imageURL, remotePath); err != nil {
		// 下载失败，删除不完整的文件
		p.removeRemoteFile(remotePath)
		return fmt.Errorf("远程下载镜像失败: %w", err)
	}

	global.APP_LOG.Info("远程镜像下载完成",
		zap.String("imageName", imageName),
		zap.String("remotePath", remotePath))

	return nil
}

// downloadImageByTemplate 使用模板映射下载镜像
func (p *ProxmoxProvider) downloadImageByTemplate(ctx context.Context, imageName, instanceType string) error {
	if instanceType == "container" {
		// 对于容器，先列出可用模板
		availableCmd := "pveam available --section system"
		availableOutput, err := p.sshClient.Execute(availableCmd)
		if err != nil {
			global.APP_LOG.Warn("无法获取可用模板列表", zap.Error(err))
		} else {
			global.APP_LOG.Debug("可用模板列表", zap.String("output", availableOutput))
		}

		global.APP_LOG.Warn("数据库中未找到容器镜像配置，无法下载",
			zap.String("image", imageName),
			zap.String("type", instanceType))

		return fmt.Errorf("数据库中未找到镜像 %s 的配置，请联系管理员添加镜像", imageName)
	} else {
		// 对于VM镜像
		global.APP_LOG.Warn("数据库中未找到VM镜像配置，无法下载",
			zap.String("image", imageName),
			zap.String("type", instanceType))

		return fmt.Errorf("数据库中未找到VM镜像 %s 的配置，请联系管理员添加镜像", imageName)
	}
}

// generateRemoteFileName 生成远程文件名
func (p *ProxmoxProvider) generateRemoteFileName(imageName, imageURL, architecture string) string {
	// 组合字符串
	combined := fmt.Sprintf("%s_%s_%s", imageName, imageURL, architecture)

	// 计算MD5
	hasher := md5.New()
	hasher.Write([]byte(combined))
	md5Hash := fmt.Sprintf("%x", hasher.Sum(nil))

	// 使用镜像名称和MD5的前8位作为文件名，保持可读性
	safeName := strings.ReplaceAll(imageName, "/", "_")
	safeName = strings.ReplaceAll(safeName, ":", "_")

	// 根据URL中的文件扩展名决定下载后的文件扩展名
	if strings.Contains(imageURL, ".qcow2") {
		return fmt.Sprintf("%s_%s.qcow2", safeName, md5Hash[:8])
	} else if strings.Contains(imageURL, ".iso") {
		return fmt.Sprintf("%s_%s.iso", safeName, md5Hash[:8])
	} else if strings.Contains(imageURL, ".tar.xz") {
		return fmt.Sprintf("%s_%s.tar.xz", safeName, md5Hash[:8])
	} else if strings.Contains(imageURL, ".zip") {
		return fmt.Sprintf("%s_%s.zip", safeName, md5Hash[:8])
	} else {
		// 默认使用通用扩展名
		return fmt.Sprintf("%s_%s.img", safeName, md5Hash[:8])
	}
}

// isRemoteFileValid 检查远程文件是否存在且完整
func (p *ProxmoxProvider) isRemoteFileValid(remotePath string) bool {
	// 检查文件是否存在且大小大于0
	cmd := fmt.Sprintf("test -f %s -a -s %s", remotePath, remotePath)
	_, err := p.sshClient.Execute(cmd)
	return err == nil
}

// removeRemoteFile 删除远程文件
func (p *ProxmoxProvider) removeRemoteFile(remotePath string) error {
	_, err := p.sshClient.Execute(fmt.Sprintf("rm -f %s", remotePath))
	return err
}

// queryAndSetSystemImage 从数据库查询匹配的系统镜像记录并设置到配置中
func (p *ProxmoxProvider) queryAndSetSystemImage(ctx context.Context, config *provider.InstanceConfig) error {
	// 构建查询条件
	var systemImage systemModel.SystemImage
	query := global.APP_DB.WithContext(ctx).Where("provider_type = ?", "proxmox")

	// 按实例类型筛选
	if config.InstanceType == "vm" {
		query = query.Where("instance_type = ?", "vm")
	} else {
		query = query.Where("instance_type = ?", "container")
	}

	// 按操作系统匹配（如果配置中有指定）
	if config.Image != "" {
		// 尝试从镜像名中提取操作系统信息
		imageLower := strings.ToLower(config.Image)
		query = query.Where("LOWER(os_type) LIKE ? OR LOWER(name) LIKE ?", "%"+imageLower+"%", "%"+imageLower+"%")
	}

	// 按架构筛选
	if p.config.Architecture != "" {
		query = query.Where("architecture = ?", p.config.Architecture)
	} else {
		// 默认使用amd64
		query = query.Where("architecture = ?", "amd64")
	}

	// 优先获取启用状态的镜像
	query = query.Where("status = ?", "active").Order("created_at DESC")

	err := query.First(&systemImage).Error
	if err != nil {
		return fmt.Errorf("未找到匹配的系统镜像: %w", err)
	}

	// 设置镜像配置，不在这里添加CDN前缀
	// CDN前缀应该在实际下载时根据可用性和UseCDN设置动态添加
	if systemImage.URL != "" {
		config.ImageURL = systemImage.URL
		config.UseCDN = systemImage.UseCDN // 传递UseCDN配置给后续流程
		global.APP_LOG.Info("从数据库获取到系统镜像配置",
			zap.String("imageName", systemImage.Name),
			zap.String("originalURL", utils.TruncateString(systemImage.URL, 100)),
			zap.Bool("useCDN", systemImage.UseCDN),
			zap.String("osType", systemImage.OSType),
			zap.String("osVersion", systemImage.OSVersion),
			zap.String("architecture", systemImage.Architecture),
			zap.String("instanceType", systemImage.InstanceType))
	}

	return nil
}
