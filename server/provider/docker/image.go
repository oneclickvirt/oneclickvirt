package docker

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
func (d *DockerProvider) sshListImages(ctx context.Context) ([]provider.Image, error) {
	output, err := d.sshClient.ExecuteWithLogging(d.runtime.CLI+" images --format 'table {{.Repository}}\\t{{.Tag}}\\t{{.ID}}\\t{{.Size}}\\t{{.CreatedAt}}'", "DOCKER_IMAGES")
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

		image := provider.Image{
			ID:   fields[2],
			Name: fields[0],
			Tag:  fields[1],
			Size: fields[3],
		}
		images = append(images, image)
	}

	global.APP_LOG.Info("获取Docker镜像列表成功", zap.Int("count", len(images)))
	return images, nil
}

// sshPullImage 拉取镜像
func (d *DockerProvider) sshPullImage(ctx context.Context, image string) error {
	pullCmd := fmt.Sprintf("%s pull %s", d.runtime.CLI, image)
	global.APP_LOG.Info("开始拉取Docker镜像",
		zap.String("image", utils.TruncateString(image, 64)),
		zap.String("command", pullCmd))

	output, err := d.sshClient.Execute(pullCmd)
	if err != nil {
		global.APP_LOG.Error("Docker镜像拉取失败",
			zap.String("image", utils.TruncateString(image, 64)),
			zap.String("command", pullCmd),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to pull image: %w", err)
	}

	global.APP_LOG.Info("Docker镜像拉取成功", zap.String("image", utils.TruncateString(image, 64)))
	return nil
}

// sshDeleteImage 删除镜像
func (d *DockerProvider) sshDeleteImage(ctx context.Context, id string) error {
	_, err := d.sshClient.Execute(fmt.Sprintf("%s rmi -f %s", d.runtime.CLI, id))
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}

	global.APP_LOG.Info("Docker镜像删除成功", zap.String("id", utils.TruncateString(id, 32)))
	return nil
}

// loadImageToDocker 加载镜像到Docker
func (d *DockerProvider) loadImageToDocker(imagePath, targetImageName string) error {
	loadCmd := fmt.Sprintf("%s load -i %s", d.runtime.CLI, imagePath)

	global.APP_LOG.Debug("开始加载Docker镜像",
		zap.String("imagePath", utils.TruncateString(imagePath, 64)),
		zap.String("targetImageName", utils.TruncateString(targetImageName, 64)),
		zap.String("command", utils.TruncateString(loadCmd, 200)))

	output, err := d.sshClient.Execute(loadCmd)
	if err != nil {
		global.APP_LOG.Error("Docker镜像加载失败",
			zap.String("imagePath", utils.TruncateString(imagePath, 64)),
			zap.String("command", utils.TruncateString(loadCmd, 200)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to load image from %s: %w", imagePath, err)
	}
	// 从docker load的输出中提取加载的镜像名称
	// 输出格式通常是: "Loaded image: <image_name>:<tag>"
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
		tagCmd := fmt.Sprintf("%s tag %s %s", d.runtime.CLI, loadedImageName, targetImageName)
		global.APP_LOG.Debug("重新标记Docker镜像",
			zap.String("sourceImage", utils.TruncateString(loadedImageName, 64)),
			zap.String("targetImage", utils.TruncateString(targetImageName, 64)),
			zap.String("command", utils.TruncateString(tagCmd, 200)))

		_, err = d.sshClient.Execute(tagCmd)
		if err != nil {
			global.APP_LOG.Error("Docker镜像重新标记失败",
				zap.String("sourceImage", utils.TruncateString(loadedImageName, 64)),
				zap.String("targetImage", utils.TruncateString(targetImageName, 64)),
				zap.String("command", utils.TruncateString(tagCmd, 200)),
				zap.Error(err))
			return fmt.Errorf("failed to tag image from %s to %s: %w", loadedImageName, targetImageName, err)
		}
		global.APP_LOG.Debug("Docker镜像重新标记成功",
			zap.String("sourceImage", utils.TruncateString(loadedImageName, 64)),
			zap.String("targetImage", utils.TruncateString(targetImageName, 64)))
	}
	global.APP_LOG.Debug("Docker镜像加载成功",
		zap.String("imagePath", utils.TruncateString(imagePath, 64)),
		zap.String("targetImageName", utils.TruncateString(targetImageName, 64)))
	return nil
}

// cleanupDockerImage 清理Docker镜像
func (d *DockerProvider) cleanupDockerImage(imageName string) {
	// 删除损坏的镜像（忽略错误）
	d.sshClient.Execute(fmt.Sprintf("%s rmi -f %s", d.runtime.CLI, imageName))
	// 清理未使用的镜像
	d.sshClient.Execute(fmt.Sprintf("%s image prune -f", d.runtime.CLI))
	global.APP_LOG.Debug("清理Docker镜像", zap.String("imageName", utils.TruncateString(imageName, 64)))
}

// imageExists 检查Docker镜像是否已存在
func (d *DockerProvider) imageExists(imageName string) bool {
	output, err := d.sshClient.Execute(fmt.Sprintf("%s images --format '{{.Repository}}:{{.Tag}}' | grep -E '^%s($|:)'", d.runtime.CLI, imageName))
	if err != nil {
		global.APP_LOG.Debug("检查Docker镜像存在性失败",
			zap.String("imageName", utils.TruncateString(imageName, 64)),
			zap.Error(err))
		return false
	}

	exists := strings.TrimSpace(output) != ""
	global.APP_LOG.Debug("Docker镜像存在性检查",
		zap.String("imageName", utils.TruncateString(imageName, 64)),
		zap.Bool("exists", exists))
	return exists
}
