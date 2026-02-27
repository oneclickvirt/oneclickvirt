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

func (p *ProxmoxProvider) sshListImages(ctx context.Context) ([]provider.Image, error) {
	output, err := p.sshClient.Execute(fmt.Sprintf("pvesh get /nodes/%s/storage/local/content --content iso", p.node))
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	var images []provider.Image

	for _, line := range lines {
		if strings.Contains(line, ".iso") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				image := provider.Image{
					ID:   fields[0],
					Name: fields[0],
					Tag:  "iso",
					Size: fields[1],
				}
				images = append(images, image)
			}
		}
	}

	global.APP_LOG.Info("通过 SSH 成功获取 Proxmox 镜像列表", zap.Int("count", len(images)))
	return images, nil
}

func (p *ProxmoxProvider) sshPullImage(ctx context.Context, imageURL string) error {
	_, err := p.sshPullImageToPath(ctx, imageURL, "")
	return err
}

func (p *ProxmoxProvider) sshPullImageToPath(ctx context.Context, imageURL, imageName string) (string, error) {
	// 确定镜像下载目录
	downloadDir := "/usr/local/bin/proxmox_images"

	// 创建下载目录
	_, err := p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", downloadDir))
	if err != nil {
		return "", fmt.Errorf("创建下载目录失败: %w", err)
	}

	// 从URL中提取文件名
	fileName := p.extractFileName(imageURL)
	if imageName != "" {
		fileName = imageName
	}

	remotePath := fmt.Sprintf("%s/%s", downloadDir, fileName)

	global.APP_LOG.Debug("开始下载Proxmox镜像",
		zap.String("imageURL", utils.TruncateString(imageURL, 200)),
		zap.String("remotePath", remotePath))

	// 检查文件是否已存在
	checkCmd := fmt.Sprintf("test -f %s && echo 'exists'", remotePath)
	output, _ := p.sshClient.Execute(checkCmd)
	if strings.TrimSpace(output) == "exists" {
		global.APP_LOG.Debug("镜像已存在，跳过下载", zap.String("path", remotePath))
		return remotePath, nil
	}

	// 下载镜像
	downloadCmd := fmt.Sprintf("wget --no-check-certificate -O %s %s", remotePath, imageURL)
	_, err = p.sshClient.Execute(downloadCmd)
	if err != nil {
		// 尝试使用curl下载
		downloadCmd = fmt.Sprintf("curl -L -k -o %s %s", remotePath, imageURL)
		_, err = p.sshClient.Execute(downloadCmd)
		if err != nil {
			return "", fmt.Errorf("下载镜像失败: %w", err)
		}
	}

	global.APP_LOG.Debug("Proxmox镜像下载完成", zap.String("remotePath", remotePath))

	// 根据文件类型移动到相应目录
	if strings.HasSuffix(fileName, ".iso") {
		// ISO文件移动到ISO目录
		isoPath := fmt.Sprintf("/var/lib/vz/template/iso/%s", fileName)
		moveCmd := fmt.Sprintf("mv %s %s", remotePath, isoPath)
		_, err = p.sshClient.Execute(moveCmd)
		if err != nil {
			global.APP_LOG.Warn("移动ISO文件失败", zap.Error(err))
			return remotePath, nil
		}
		return isoPath, nil
	} else {
		// 其他文件可能是LXC模板，移动到cache目录
		cachePath := fmt.Sprintf("/var/lib/vz/template/cache/%s", fileName)
		moveCmd := fmt.Sprintf("mv %s %s", remotePath, cachePath)
		_, err = p.sshClient.Execute(moveCmd)
		if err != nil {
			global.APP_LOG.Warn("移动模板文件失败", zap.Error(err))
			return remotePath, nil
		}
		return cachePath, nil
	}
}

// extractFileName 从URL中提取文件名
func (p *ProxmoxProvider) extractFileName(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "downloaded_image"
}

func (p *ProxmoxProvider) sshDeleteImage(ctx context.Context, id string) error {
	_, err := p.sshClient.Execute(fmt.Sprintf("rm -f /var/lib/vz/template/iso/%s", id))
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}

	global.APP_LOG.Info("通过 SSH 成功删除 Proxmox 镜像", zap.String("id", id))
	return nil
}
