package containerd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func containerdPlatformFromArch(arch string) string {
	a := strings.ToLower(strings.TrimSpace(arch))
	switch a {
	case "amd64", "x86_64":
		return "linux/amd64"
	case "arm64", "aarch64":
		return "linux/arm64"
	case "arm", "armv7", "armv7l":
		return "linux/arm/v7"
	default:
		return "linux/amd64"
	}
}

func (c *ContainerdProvider) containerdPlatform() string {
	return containerdPlatformFromArch(c.config.Architecture)
}

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
	pullCmd := fmt.Sprintf("%s pull --platform=%s %s", cliName, shellSingleQuote(c.containerdPlatform()), shellSingleQuote(image))
	output, err := c.sshClient.Execute(pullCmd)
	if err != nil {
		global.APP_LOG.Error("Containerd镜像拉取失败",
			zap.String("image", utils.TruncateString(image, 64)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to pull image %s: %w; output: %s", image, err, output)
	}
	global.APP_LOG.Info("Containerd镜像拉取成功", zap.String("image", utils.TruncateString(image, 64)))
	return nil
}

// sshDeleteImage 删除镜像
func (c *ContainerdProvider) sshDeleteImage(ctx context.Context, id string) error {
	output, err := c.sshClient.Execute(fmt.Sprintf("%s rmi -f %s", cliName, shellSingleQuote(id)))
	if err != nil {
		return fmt.Errorf("failed to delete image %s: %w; output: %s", id, err, output)
	}
	global.APP_LOG.Info("Containerd镜像删除成功", zap.String("id", utils.TruncateString(id, 32)))
	return nil
}

// loadImageToContainerd 加载镜像到Containerd
// containerd/nerdctl 2.x 对多架构 archive 必须显式指定 platform，否则可能报
// "unable to initialize unpacker: no unpack platforms defined"。
func (c *ContainerdProvider) loadImageToContainerd(imagePath, targetImageName string) error {
	platform := c.containerdPlatform()
	loadCmd := fmt.Sprintf("%s load --platform=%s --input=%s", cliName, shellSingleQuote(platform), shellSingleQuote(imagePath))
	output, err := c.sshClient.ExecuteWithTimeout(loadCmd, 30*time.Minute)
	if err != nil {
		global.APP_LOG.Warn("Containerd nerdctl load失败，尝试ctr --local导入",
			zap.String("imagePath", utils.TruncateString(imagePath, 64)),
			zap.String("platform", platform),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))

		ctrCmd := fmt.Sprintf("command -v ctr >/dev/null 2>&1 && ctr images import --local --platform %s %s", shellSingleQuote(platform), shellSingleQuote(imagePath))
		ctrOutput, ctrErr := c.sshClient.ExecuteWithTimeout(ctrCmd, 30*time.Minute)
		if ctrErr != nil {
			global.APP_LOG.Error("Containerd镜像加载失败",
				zap.String("imagePath", utils.TruncateString(imagePath, 64)),
				zap.String("platform", platform),
				zap.String("nerdctlOutput", utils.TruncateString(output, 500)),
				zap.String("ctrOutput", utils.TruncateString(ctrOutput, 500)),
				zap.Error(ctrErr))
			return fmt.Errorf("failed to load image from %s: nerdctl: %v; nerdctl output: %s; ctr: %w; ctr output: %s", imagePath, err, output, ctrErr, ctrOutput)
		}
		output = ctrOutput
	}

	loadedImageNames := containerdLoadedImageCandidates(output)
	for _, loadedImageName := range loadedImageNames {
		if err := c.ensureContainerdImageTag(loadedImageName, targetImageName); err == nil {
			if c.imageExists(targetImageName) {
				global.APP_LOG.Debug("Containerd镜像打标成功",
					zap.String("loadedImageName", utils.TruncateString(loadedImageName, 64)),
					zap.String("targetImageName", utils.TruncateString(targetImageName, 64)))
				return nil
			}
		} else {
			global.APP_LOG.Warn("Containerd镜像候选打标失败",
				zap.String("loadedImageName", utils.TruncateString(loadedImageName, 64)),
				zap.String("targetImageName", utils.TruncateString(targetImageName, 64)),
				zap.Error(err))
		}
	}
	if !c.imageExists(targetImageName) {
		return fmt.Errorf("loaded containerd image did not create expected tag %s; load output: %s", targetImageName, output)
	}

	global.APP_LOG.Debug("Containerd镜像加载成功",
		zap.String("imagePath", utils.TruncateString(imagePath, 64)),
		zap.Strings("loadedImageNames", loadedImageNames),
		zap.String("targetImageName", utils.TruncateString(targetImageName, 64)))
	return nil
}

func containerdLoadedImageCandidates(output string) []string {
	seen := make(map[string]struct{})
	candidates := make([]string, 0, 4)
	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		candidate = strings.TrimSuffix(candidate, "...")
		candidate = strings.TrimSuffix(candidate, ",")
		if candidate == "" {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Loaded image:") {
			parts := strings.SplitN(line, "Loaded image:", 2)
			if len(parts) == 2 {
				add(parts[1])
			}
			continue
		}
		if strings.HasPrefix(line, "unpacking ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				add(parts[1])
			}
		}
	}
	return candidates
}

func containerdRefWithImplicitLatest(imageName string) string {
	if imageName == "" || strings.Contains(imageName, "@") {
		return imageName
	}
	lastSlash := strings.LastIndex(imageName, "/")
	lastColon := strings.LastIndex(imageName, ":")
	if lastColon > lastSlash {
		return imageName
	}
	return imageName + ":latest"
}

func containerdTrimDockerHubPrefix(imageName string) string {
	imageName = strings.TrimSpace(imageName)
	imageName = strings.TrimPrefix(imageName, "docker.io/library/")
	imageName = strings.TrimPrefix(imageName, "docker.io/")
	return imageName
}

func containerdRefPathWithoutTag(imageName string) string {
	if at := strings.Index(imageName, "@"); at >= 0 {
		imageName = imageName[:at]
	}
	lastSlash := strings.LastIndex(imageName, "/")
	lastColon := strings.LastIndex(imageName, ":")
	if lastColon > lastSlash {
		return imageName[:lastColon]
	}
	return imageName
}

func containerdRuntimeImageRef(imageName string) string {
	raw := strings.TrimSpace(imageName)
	if strings.HasPrefix(raw, "docker.io/") && !strings.HasPrefix(raw, "docker.io/library/") {
		return containerdRefWithImplicitLatest(raw)
	}
	ref := containerdRefWithImplicitLatest(containerdTrimDockerHubPrefix(raw))
	if ref == "" || strings.Contains(ref, "@") {
		return ref
	}
	if strings.Contains(containerdRefPathWithoutTag(ref), "/") {
		return ref
	}
	return "docker.io/library/" + ref
}

func containerdImageReferenceVariants(imageName string) []string {
	seen := make(map[string]struct{})
	var refs []string
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}

	add(imageName)
	add(containerdRefWithImplicitLatest(imageName))

	trimmed := containerdTrimDockerHubPrefix(imageName)
	add(trimmed)
	add(containerdRefWithImplicitLatest(trimmed))

	taggedTrimmed := containerdRefWithImplicitLatest(trimmed)
	add("docker.io/" + taggedTrimmed)
	if !strings.Contains(containerdRefPathWithoutTag(taggedTrimmed), "/") {
		add("docker.io/library/" + taggedTrimmed)
	}
	add(containerdRuntimeImageRef(imageName))

	return refs
}

func containerdTagSources(loadedImageName string) []string {
	seen := make(map[string]struct{})
	sources := make([]string, 0, 4)
	add := func(source string) {
		source = strings.TrimSpace(source)
		if source == "" {
			return
		}
		if _, ok := seen[source]; ok {
			return
		}
		seen[source] = struct{}{}
		sources = append(sources, source)
	}
	add(loadedImageName)
	add(strings.TrimPrefix(loadedImageName, "docker.io/"))
	add(containerdTrimDockerHubPrefix(loadedImageName))
	for _, ref := range containerdImageReferenceVariants(loadedImageName) {
		add(ref)
	}
	if strings.HasPrefix(loadedImageName, "import@sha256:") {
		add("sha256:" + strings.TrimPrefix(loadedImageName, "import@sha256:"))
	}
	return sources
}

func (c *ContainerdProvider) ensureContainerdImageTag(loadedImageName, targetImageName string) error {
	if loadedImageName == "" {
		return fmt.Errorf("loaded containerd image name is empty")
	}
	if c.imageExists(targetImageName) {
		return nil
	}
	var attempts []string
	for _, targetRef := range containerdImageReferenceVariants(targetImageName) {
		if c.imageExists(targetRef) {
			return nil
		}
		for _, source := range containerdTagSources(loadedImageName) {
			if source == targetRef {
				continue
			}
			nerdctlCmd := fmt.Sprintf("%s tag %s %s 2>&1", cliName, shellSingleQuote(source), shellSingleQuote(targetRef))
			nerdctlOutput, nerdctlErr := c.sshClient.Execute(nerdctlCmd)
			if nerdctlErr == nil {
				if c.imageExists(targetRef) {
					return nil
				}
				attempts = append(attempts, fmt.Sprintf("nerdctl tag %s -> %s succeeded but target was not inspectable", source, targetRef))
				continue
			}
			attempts = append(attempts, fmt.Sprintf("nerdctl tag %s -> %s failed: %v; output: %s", source, targetRef, nerdctlErr, nerdctlOutput))

			for _, namespace := range []string{"default", "k8s.io", "moby"} {
				ctrCmd := fmt.Sprintf("command -v ctr >/dev/null 2>&1 && ctr -n %s images tag %s %s 2>&1",
					shellSingleQuote(namespace), shellSingleQuote(source), shellSingleQuote(targetRef))
				ctrOutput, ctrErr := c.sshClient.Execute(ctrCmd)
				if ctrErr == nil {
					if c.imageExists(targetRef) {
						return nil
					}
					attempts = append(attempts, fmt.Sprintf("ctr -n %s images tag %s -> %s succeeded but target was not inspectable", namespace, source, targetRef))
					continue
				}
				attempts = append(attempts, fmt.Sprintf("ctr -n %s images tag %s -> %s failed: %v; output: %s", namespace, source, targetRef, ctrErr, ctrOutput))
			}
		}
	}
	return fmt.Errorf("failed to tag image from %s to %s:\n%s", loadedImageName, targetImageName, strings.Join(attempts, "\n"))
}

// cleanupContainerdImage 清理Containerd镜像
func (c *ContainerdProvider) cleanupContainerdImage(imageName string) {
	for _, ref := range containerdImageReferenceVariants(imageName) {
		c.sshClient.Execute(fmt.Sprintf("%s rmi -f %s", cliName, shellSingleQuote(ref)))
	}
	c.sshClient.Execute(fmt.Sprintf("%s image prune -f", cliName))
}

// imageExists 检查Containerd镜像是否已存在
// nerdctl 会自动为镜像名添加 docker.io/ 前缀，所以需要同时检查带前缀和不带前缀的情况
func (c *ContainerdProvider) imageExists(imageName string) bool {
	for _, name := range containerdImageReferenceVariants(imageName) {
		if _, err := c.sshClient.Execute(fmt.Sprintf("%s image inspect %s >/dev/null 2>&1", cliName, shellSingleQuote(name))); err == nil {
			return true
		}
		output, err := c.sshClient.Execute(fmt.Sprintf("%s images --format '{{.Repository}}:{{.Tag}}' | grep -Fx %s", cliName, shellSingleQuote(name)))
		if err == nil && strings.TrimSpace(output) != "" {
			return true
		}
	}
	return false
}
