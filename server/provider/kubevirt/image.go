package kubevirt

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

func (p *KubeVirtProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	return p.sshListImages(ctx)
}

func (p *KubeVirtProvider) PullImage(ctx context.Context, imageURL string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	return p.sshPullImage(ctx, imageURL)
}

func (p *KubeVirtProvider) DeleteImage(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	return p.sshDeleteImage(ctx, id)
}

// sshListImages 列出本地镜像文件
func (p *KubeVirtProvider) sshListImages(ctx context.Context) ([]provider.Image, error) {
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"ls -lh %s/*.{qcow2,img,raw} 2>/dev/null | awk '{print $5, $9}'", shellSingleQuote(ImageDir)))
	if err != nil {
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
			Tag:  "disk",
			Size: size,
		})
	}
	return images, nil
}

// sshPullImage 下载镜像文件
func (p *KubeVirtProvider) sshPullImage(ctx context.Context, imageURL string) error {
	fileName := extractKubeVirtFileName(imageURL)
	if fileName == "" {
		return fmt.Errorf("cannot extract filename from URL: %s", imageURL)
	}

	remotePath := fmt.Sprintf("%s/%s", ImageDir, fileName)

	_, err, _ := p.imageImportGroup.Do(remotePath, func() (interface{}, error) {
		output, _ := p.sshClient.Execute(fmt.Sprintf("test -f %s && echo 'exists'", shellSingleQuote(remotePath)))
		if strings.TrimSpace(output) == "exists" {
			global.APP_LOG.Info("镜像已存在，跳过下载", zap.String("path", remotePath))
			return nil, nil
		}

		p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", shellSingleQuote(ImageDir)))

		tmpPath := remotePath + ".tmp"
		downloadScript := utils.BuildRemoteDownloadScript(imageURL, tmpPath, remotePath)
		output, err := p.sshClient.ExecuteViaTempScript(downloadScript, nil, 30*time.Minute)
		if err != nil {
			global.APP_LOG.Error("镜像下载失败",
				zap.String("url", utils.TruncateString(imageURL, 200)),
				zap.String("output", utils.TruncateString(output, 1000)),
				zap.Error(err))
			p.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(tmpPath)))
			return nil, fmt.Errorf("failed to download image: %w", err)
		}

		global.APP_LOG.Info("镜像下载完成", zap.String("path", remotePath))
		return nil, nil
	})

	return err
}

// sshDeleteImage 删除镜像
func (p *KubeVirtProvider) sshDeleteImage(ctx context.Context, id string) error {
	path := id
	if !strings.HasPrefix(id, "/") {
		path = fmt.Sprintf("%s/%s", ImageDir, id)
	}

	output, err := p.sshClient.Execute(fmt.Sprintf("rm -f %s 2>&1", shellSingleQuote(path)))
	if err != nil {
		return fmt.Errorf("failed to delete image: %s, %w", utils.TruncateString(output, 200), err)
	}
	return nil
}

// extractKubeVirtFileName 从URL提取文件名
func extractKubeVirtFileName(url string) string {
	url = strings.Split(url, "?")[0]
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}
