package containerd

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
func (c *ContainerdProvider) sshListImages(ctx context.Context) ([]provider.Image, error) {
	output, err := c.sshClient.ExecuteWithLogging(cliName+" images --format 'table {{.Repository}}\\t{{.Tag}}\\t{{.ID}}\\t{{.Size}}\\t{{.CreatedAt}}'", "CONTAINERD_IMAGES")
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

	global.APP_LOG.Info("获取Containerd镜像列表成功", zap.Int("count", len(images)))
	return images, nil
}

// sshPullImage 拉取镜像
func (c *ContainerdProvider) sshPullImage(ctx context.Context, image string) error {
	pullCmd := fmt.Sprintf("%s pull %s", cliName, image)
	output, err := c.sshClient.Execute(pullCmd)
	if err != nil {
		global.APP_LOG.Error("Containerd镜像拉取失败",
			zap.String("image", utils.TruncateString(image, 64)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to pull image: %w", err)
	}
	global.APP_LOG.Info("Containerd镜像拉取成功", zap.String("image", utils.TruncateString(image, 64)))
	return nil
}

// sshDeleteImage 删除镜像
func (c *ContainerdProvider) sshDeleteImage(ctx context.Context, id string) error {
	_, err := c.sshClient.Execute(fmt.Sprintf("%s rmi -f %s", cliName, id))
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}
	global.APP_LOG.Info("Containerd镜像删除成功", zap.String("id", utils.TruncateString(id, 32)))
	return nil
}

// loadImageToContainerd 加载镜像到Containerd
// nerdctl load 不支持 -i 参数，需使用 --input=<path> 或 stdin 重定向
func (c *ContainerdProvider) loadImageToContainerd(imagePath, targetImageName string) error {
	// 使用 nerdctl load --input=<path>，这是 nerdctl 正确的语法（不同于 docker/podman 的 -i）
	loadCmd := fmt.Sprintf("%s load --input=%s", cliName, imagePath)
	output, err := c.sshClient.Execute(loadCmd)
	if err != nil {
		global.APP_LOG.Error("Containerd镜像加载失败",
			zap.String("imagePath", utils.TruncateString(imagePath, 64)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to load image from %s: %w", imagePath, err)
	}

	// nerdctl load 输出格式为“unpacking <image>...” 或 "Loaded image: <image>"
	// 且 nerdctl 会自动添加 docker.io/ 前缀，如 docker.io/spiritlhl/debian:latest
	var loadedImageName string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Loaded image:") {
			parts := strings.SplitN(line, "Loaded image:", 2)
			if len(parts) == 2 {
				loadedImageName = strings.TrimSpace(parts[1])
				break
			}
		} else if strings.HasPrefix(line, "unpacking ") {
			// nerdctl v2 输出格式: "unpacking docker.io/spiritlhl/debian:latest..."
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				candidate := strings.TrimSuffix(parts[1], "...")
				candidate = strings.TrimSuffix(candidate, ",")
				if strings.Contains(candidate, "/") || strings.Contains(candidate, ":") {
					loadedImageName = candidate
				}
			}
		}
	}

	// 如果加载的镜像名与目标名不同，进行打标操作
	if loadedImageName != "" && loadedImageName != targetImageName {
		// nerdctl 会自动添加 docker.io/ 前缀，这里需要同时处理带和不带前缀的情况
		tagCmd := fmt.Sprintf("%s tag %s %s", cliName, loadedImageName, targetImageName)
		_, err = c.sshClient.Execute(tagCmd)
		if err != nil {
			// 尝试不带 docker.io/ 前缀的名字来打标
			shortName := strings.TrimPrefix(loadedImageName, "docker.io/")
			tagCmd2 := fmt.Sprintf("%s tag %s %s", cliName, shortName, targetImageName)
			_, err2 := c.sshClient.Execute(tagCmd2)
			if err2 != nil {
				return fmt.Errorf("failed to tag image from %s to %s: %w", loadedImageName, targetImageName, err)
			}
		}
	}

	global.APP_LOG.Debug("Containerd镜像加载成功",
		zap.String("imagePath", utils.TruncateString(imagePath, 64)),
		zap.String("loadedImageName", utils.TruncateString(loadedImageName, 64)),
		zap.String("targetImageName", utils.TruncateString(targetImageName, 64)))
	return nil
}

// cleanupContainerdImage 清理Containerd镜像
func (c *ContainerdProvider) cleanupContainerdImage(imageName string) {
	c.sshClient.Execute(fmt.Sprintf("%s rmi -f %s", cliName, imageName))
	c.sshClient.Execute(fmt.Sprintf("%s image prune -f", cliName))
}

// imageExists 检查Containerd镜像是否已存在
// nerdctl 会自动为镜像名添加 docker.io/ 前缀，所以需要同时检查带前缀和不带前缀的情况
func (c *ContainerdProvider) imageExists(imageName string) bool {
	// 同时检查原始名称和带 docker.io/ 前缀的名称
	shortName := strings.TrimPrefix(imageName, "docker.io/")
	// 检查 Repository:Tag 格式 (不带标签的名字添加 :latest)
	checkName := shortName
	if !strings.Contains(shortName, ":") {
		checkName = shortName + ":latest"
	}
	// nerdctl images --format 输出格式: docker.io/<repo>:<tag>
	// 同时尝试匹配带和不带 docker.io/ 前缀的名字
	checks := []string{
		checkName,
		"docker.io/" + checkName,
		shortName,
		imageName,
	}
	for _, name := range checks {
		output, err := c.sshClient.Execute(fmt.Sprintf("%s images --format '{{.Repository}}:{{.Tag}}' | grep -Fx '%s'", cliName, name))
		if err == nil && strings.TrimSpace(output) != "" {
			return true
		}
	}
	return false
}
