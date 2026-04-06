package qemu

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *QEMUProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	return p.sshListImages(ctx)
}

func (p *QEMUProvider) PullImage(ctx context.Context, imageURL string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	return p.sshPullImage(ctx, imageURL)
}

func (p *QEMUProvider) DeleteImage(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	return p.sshDeleteImage(ctx, id)
}

// sshListImages 列出本地 qcow2 镜像
func (p *QEMUProvider) sshListImages(ctx context.Context) ([]provider.Image, error) {
	// 列出 /var/lib/libvirt/images/ 下的 qcow2 文件
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"ls -lh %s/*.qcow2 2>/dev/null | awk '{print $5, $9}'", ImageDir))
	if err != nil {
		// 目录为空不算错误
		return []provider.Image{}, nil
	}

	var images []provider.Image
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		size := fields[0]
		fullPath := fields[1]
		name := filepath.Base(fullPath)

		images = append(images, provider.Image{
			ID:   name,
			Name: name,
			Tag:  "qcow2",
			Size: size,
		})
	}
	return images, nil
}

// sshPullImage 下载 qcow2 镜像
func (p *QEMUProvider) sshPullImage(ctx context.Context, imageURL string) error {
	fileName := extractFileName(imageURL)
	if fileName == "" {
		return fmt.Errorf("cannot extract filename from URL: %s", imageURL)
	}

	remotePath := fmt.Sprintf("%s/%s", ImageDir, fileName)

	// 使用 singleflight 防止并发下载
	_, err, _ := p.imageImportGroup.Do(remotePath, func() (interface{}, error) {
		// 检查是否已存在
		output, _ := p.sshClient.Execute(fmt.Sprintf("test -f '%s' && echo 'exists'", remotePath))
		if strings.TrimSpace(output) == "exists" {
			global.APP_LOG.Info("镜像已存在，跳过下载",
				zap.String("path", remotePath))
			return nil, nil
		}

		// 确保目录存在
		p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", ImageDir))

		// 下载镜像（使用临时文件+mv模式）
		tmpPath := remotePath + ".tmp"
		downloadCmd := fmt.Sprintf("curl -L --connect-timeout 30 --max-time 360 -o '%s' '%s' 2>&1", tmpPath, imageURL)
		output, err := p.sshClient.ExecuteWithTimeout(downloadCmd, 1*time.Hour)
		if err != nil {
			// 回退到wget
			downloadCmd = fmt.Sprintf("wget --no-check-certificate --timeout=360 -O '%s' '%s' 2>&1", tmpPath, imageURL)
			output, err = p.sshClient.ExecuteWithTimeout(downloadCmd, 1*time.Hour)
			if err != nil {
				global.APP_LOG.Error("镜像下载失败",
					zap.String("url", utils.TruncateString(imageURL, 200)),
					zap.String("output", utils.TruncateString(output, 500)),
					zap.Error(err))
				p.sshClient.Execute(fmt.Sprintf("rm -f '%s'", tmpPath))
				return nil, fmt.Errorf("failed to download image: %w", err)
			}
		}

		// 原子移动
		_, err = p.sshClient.Execute(fmt.Sprintf("mv '%s' '%s'", tmpPath, remotePath))
		if err != nil {
			return nil, fmt.Errorf("failed to move image: %w", err)
		}

		global.APP_LOG.Info("镜像下载完成", zap.String("path", remotePath))
		return nil, nil
	})

	return err
}

// sshDeleteImage 删除镜像
func (p *QEMUProvider) sshDeleteImage(ctx context.Context, id string) error {
	// id 可能是文件名或完整路径
	path := id
	if !strings.HasPrefix(id, "/") {
		path = fmt.Sprintf("%s/%s", ImageDir, id)
	}

	output, err := p.sshClient.Execute(fmt.Sprintf("rm -f '%s' 2>&1", path))
	if err != nil {
		return fmt.Errorf("failed to delete image: %s, %w", utils.TruncateString(output, 200), err)
	}
	return nil
}

// extractFileName 从URL中提取文件名
func extractFileName(url string) string {
	// 移除查询参数
	url = strings.Split(url, "?")[0]
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}
