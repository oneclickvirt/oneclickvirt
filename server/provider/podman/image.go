package podman

import (
	"context"
	"fmt"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// sshListImages 列出所有镜像
func (p *PodmanProvider) sshListImages(ctx context.Context) ([]provider.Image, error) {
	output, err := p.sshClient.ExecuteWithLogging(cliName+" images --format 'table {{.Repository}}\\t{{.Tag}}\\t{{.ID}}\\t{{.Size}}\\t{{.CreatedAt}}'", "PODMAN_IMAGES")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) <= 1 {
		return []provider.Image{}, nil
	}

	var images []provider.Image
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		images = append(images, provider.Image{
			ID:   fields[2],
			Name: fields[0],
			Tag:  fields[1],
			Size: fields[3],
		})
	}

	global.APP_LOG.Info("获取Podman镜像列表成功", zap.Int("count", len(images)))
	return images, nil
}

// sshPullImage 拉取镜像
func (p *PodmanProvider) sshPullImage(ctx context.Context, image string) error {
	pullCmd := fmt.Sprintf("%s pull %s", cliName, image)
	output, err := p.sshClient.Execute(pullCmd)
	if err != nil {
		global.APP_LOG.Error("Podman镜像拉取失败",
			zap.String("image", utils.TruncateString(image, 64)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to pull image: %w", err)
	}
	global.APP_LOG.Info("Podman镜像拉取成功", zap.String("image", utils.TruncateString(image, 64)))
	return nil
}

// sshDeleteImage 删除镜像
func (p *PodmanProvider) sshDeleteImage(ctx context.Context, id string) error {
	_, err := p.sshClient.Execute(fmt.Sprintf("%s rmi -f %s", cliName, id))
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}
	global.APP_LOG.Info("Podman镜像删除成功", zap.String("id", utils.TruncateString(id, 32)))
	return nil
}

// loadImageToPodman 加载镜像到Podman
func (p *PodmanProvider) loadImageToPodman(imagePath, targetImageName string) error {
	loadCmd := fmt.Sprintf("%s load -i %s", cliName, imagePath)
	output, err := p.sshClient.Execute(loadCmd)
	if err != nil {
		global.APP_LOG.Error("Podman镜像加载失败",
			zap.String("imagePath", utils.TruncateString(imagePath, 64)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to load image from %s: %w", imagePath, err)
	}

	var loadedImageName string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Loaded image:") {
			parts := strings.Split(line, "Loaded image:")
			if len(parts) > 1 {
				loadedImageName = strings.TrimSpace(parts[1])
				break
			}
		}
	}

	if loadedImageName != "" && loadedImageName != targetImageName {
		tagCmd := fmt.Sprintf("%s tag %s %s", cliName, loadedImageName, targetImageName)
		_, err = p.sshClient.Execute(tagCmd)
		if err != nil {
			return fmt.Errorf("failed to tag image from %s to %s: %w", loadedImageName, targetImageName, err)
		}
	}

	global.APP_LOG.Debug("Podman镜像加载成功",
		zap.String("imagePath", utils.TruncateString(imagePath, 64)),
		zap.String("targetImageName", utils.TruncateString(targetImageName, 64)))
	return nil
}

// cleanupPodmanImage 清理Podman镜像
func (p *PodmanProvider) cleanupPodmanImage(imageName string) {
	p.sshClient.Execute(fmt.Sprintf("%s rmi -f %s", cliName, imageName))
	p.sshClient.Execute(fmt.Sprintf("%s image prune -f", cliName))
}

// imageExists 检查Podman镜像是否已存在
func (p *PodmanProvider) imageExists(imageName string) bool {
	output, err := p.sshClient.Execute(fmt.Sprintf("%s images --format '{{.Repository}}:{{.Tag}}' | grep -E '^%s($|:)'", cliName, imageName))
	if err != nil {
		return false
	}
	return strings.TrimSpace(output) != ""
}
