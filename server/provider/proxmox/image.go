package proxmox

import (
	"context"
	"fmt"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *ProxmoxProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}

	// 尝试 API 调用
	if p.hasAPIAccess() {
		images, err := p.apiListImages(ctx)
		if err == nil {
			global.APP_LOG.Info("Proxmox API调用成功 - 获取镜像列表")
			return images, nil
		}
		global.APP_LOG.Warn("Proxmox API失败，回退到SSH - 获取镜像列表", zap.Error(err))
	}

	// SSH 方式
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

	// 尝试 API 调用
	if p.hasAPIAccess() {
		err := p.apiPullImage(ctx, image)
		if err == nil {
			global.APP_LOG.Info("Proxmox API调用成功 - 拉取镜像", zap.String("image", utils.TruncateString(image, 100)))
			return nil
		}
		global.APP_LOG.Warn("Proxmox API失败，回退到SSH - 拉取镜像", zap.String("image", utils.TruncateString(image, 100)), zap.Error(err))
	}

	// SSH 方式
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

	// 尝试 API 调用
	if p.hasAPIAccess() {
		err := p.apiDeleteImage(ctx, id)
		if err == nil {
			global.APP_LOG.Info("Proxmox API调用成功 - 删除镜像", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		}
		global.APP_LOG.Warn("Proxmox API失败，回退到SSH - 删除镜像", zap.String("id", utils.TruncateString(id, 50)), zap.Error(err))
	}

	// SSH 方式
	return p.sshDeleteImage(ctx, id)
}
