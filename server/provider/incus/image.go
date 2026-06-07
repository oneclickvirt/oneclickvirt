package incus

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// handleImageDownloadAndImport 处理镜像下载和导入的通用逻辑
func (i *IncusProvider) handleImageDownloadAndImport(ctx context.Context, config *provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	// 首先从数据库查询匹配的系统镜像（单次读查询，无长事务）
	if err := i.queryAndSetSystemImage(ctx, config); err != nil {
		global.APP_LOG.Warn("从数据库查询系统镜像失败，使用原有镜像配置",
			zap.String("image", config.Image),
			zap.Error(err))
	}

	// 为镜像名称添加前缀
	originalImageName := config.Image

	// 根据实例类型确定镜像类型
	var imageTypeStr string
	if config.InstanceType == "vm" {
		imageTypeStr = "虚拟机"
	} else {
		imageTypeStr = "容器"
	}

	// 有 URL：下载 → 导入 → 用本地别名
	if config.ImageURL != "" {
		imageNameWithPrefix := "oneclickvirt_" + originalImageName
		// 始终使用数据库中的最新架构值（可能已被 detectAndUpdateArchitecture 自动纠正）
		architecture := i.getCurrentArchitecture()
		config.Image = imageNameWithPrefix + "_" + config.InstanceType + "_" + i.generateImageAlias(config.ImageURL, originalImageName, architecture)[len(originalImageName)+1:]

		// 快速路径：镜像已存在，直接跳过（避免进入 singleflight）
		if i.imageExists(config.Image) {
			global.APP_LOG.Debug("Incus"+imageTypeStr+"镜像已存在，跳过导入",
				zap.String("alias", utils.TruncateString(config.Image, 100)),
				zap.String("type", config.InstanceType))
			if progressCallback != nil {
				progressCallback(28, "镜像已缓存，跳过下载")
			}
			return nil
		}

		if progressCallback != nil {
			progressCallback(17, "开始下载镜像...")
		}
		return i.downloadAndImportImage(ctx, config, originalImageName, imageTypeStr, progressCallback)
	}

	// 无 URL（数据库无匹配的系统镜像）：尝试解析为 Incus 远程镜像引用
	// Docker 风格名称 "debian:12" → Incus 远程 "images:debian/12/cloud"
	incusRemoteImage := resolveIncusRemoteImage(originalImageName)
	if incusRemoteImage != "" {
		config.Image = incusRemoteImage
		global.APP_LOG.Info("将镜像名解析为Incus远程镜像引用",
			zap.String("original", originalImageName),
			zap.String("resolved", incusRemoteImage),
			zap.String("type", config.InstanceType))
		return nil
	}

	// 兜底：保留原始名称让 Incus 自行解析
	config.Image = originalImageName
	global.APP_LOG.Warn("无法解析镜像为Incus远程引用，使用原始名称",
		zap.String("image", originalImageName),
		zap.String("type", config.InstanceType))
	return nil
}

// downloadAndImportImage 下载镜像并导入到 Incus。
// progressCallback 用于报告下载/导入进度；可为 nil。
func (i *IncusProvider) downloadAndImportImage(ctx context.Context, config *provider.InstanceConfig, originalImageName, imageTypeStr string, progressCallback provider.ProgressCallback) error {

	// 使用 singleflight 确保同一别名只有一个协程执行下载+导入，
	// 其余协程阻塞等待，完成后共享同一结果，彻底消除并发解压/导入冲突。
	aliasKey := config.Image
	imageURL := config.ImageURL
	useCDN := config.UseCDN
	_, err, _ := i.imageImportGroup.Do(aliasKey, func() (interface{}, error) {
		// 等待期间镜像可能已由其他协程导入完毕，再次检查
		if i.imageExists(aliasKey) {
			global.APP_LOG.Debug("Incus"+imageTypeStr+"镜像已由并发协程完成导入，跳过",
				zap.String("alias", utils.TruncateString(aliasKey, 100)))
			return nil, nil
		}

		global.APP_LOG.Info("开始在远程服务器下载Incus"+imageTypeStr+"镜像",
			zap.String("imageURL", utils.TruncateString(imageURL, 200)),
			zap.String("type", config.InstanceType),
			zap.Bool("useCDN", useCDN))

		imagePath, err := i.downloadImageToRemote(imageURL, originalImageName, i.getCurrentArchitecture(), config.InstanceType, useCDN)
		if err != nil {
			return nil, fmt.Errorf("下载%s镜像失败: %w", imageTypeStr, err)
		}

		global.APP_LOG.Info("Incus"+imageTypeStr+"镜像下载成功",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("type", config.InstanceType))

		global.APP_LOG.Info("开始导入Incus"+imageTypeStr+"镜像",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("alias", utils.TruncateString(aliasKey, 100)),
			zap.String("type", config.InstanceType))

		var importErr error
		var importOutput string
		if config.InstanceType == "vm" {
			if strings.HasSuffix(imagePath, ".zip") {
				extractDir := strings.TrimSuffix(imagePath, ".zip")
				if _, err := i.sshClient.Execute(fmt.Sprintf("unzip -o %s -d %s", shellSingleQuote(imagePath), shellSingleQuote(extractDir))); err != nil {
					return nil, fmt.Errorf("解压Incus虚拟机镜像失败: %w", err)
				}
				var importCmd string
				findCmd := fmt.Sprintf("find %s -name '*.img' -o -name '*.qcow2' -o -name '*.vmdk' | head -1", shellSingleQuote(extractDir))
				vmImagePath, err := i.sshClient.Execute(findCmd)
				if err != nil || strings.TrimSpace(vmImagePath) == "" {
					findCmd = fmt.Sprintf("find %s -name '*.tar.xz' | head -1", shellSingleQuote(extractDir))
					vmImagePath, err = i.sshClient.Execute(findCmd)
					if err != nil || utils.CleanCommandOutput(vmImagePath) == "" {
						i.sshClient.Execute(fmt.Sprintf("rm -rf %s", shellSingleQuote(extractDir)))
						return nil, fmt.Errorf("未找到解压后的Incus虚拟机镜像文件")
					}
				}
				vmImagePath = utils.CleanCommandOutput(vmImagePath)
				incusTarPath := fmt.Sprintf("%s/incus.tar.xz", extractDir)
				diskPath := fmt.Sprintf("%s/disk.qcow2", extractDir)
				if i.isRemoteFileValid(incusTarPath) && i.isRemoteFileValid(diskPath) {
					importCmd = fmt.Sprintf("incus image import %s %s --alias %s", shellSingleQuote(incusTarPath), shellSingleQuote(diskPath), shellSingleQuote(aliasKey))
				} else {
					importCmd = fmt.Sprintf("incus image import %s --alias %s --vm", shellSingleQuote(vmImagePath), shellSingleQuote(aliasKey))
				}
				importOutput, importErr = i.sshClient.Execute(importCmd)
				i.sshClient.Execute(fmt.Sprintf("rm -rf %s", shellSingleQuote(extractDir)))
			} else {
				importOutput, importErr = i.sshClient.Execute(fmt.Sprintf("incus image import %s --alias %s --vm", shellSingleQuote(imagePath), shellSingleQuote(aliasKey)))
			}
		} else {
			if strings.HasSuffix(imagePath, ".zip") {
				extractDir := strings.TrimSuffix(imagePath, ".zip")
				if _, err := i.sshClient.Execute(fmt.Sprintf("unzip -o %s -d %s", shellSingleQuote(imagePath), shellSingleQuote(extractDir))); err != nil {
					return nil, fmt.Errorf("解压Incus容器镜像失败: %w", err)
				}
				var importCmd string
				incusTarPath := fmt.Sprintf("%s/incus.tar.xz", extractDir)
				rootfsPath := fmt.Sprintf("%s/rootfs.squashfs", extractDir)
				if i.isRemoteFileValid(incusTarPath) && i.isRemoteFileValid(rootfsPath) {
					importCmd = fmt.Sprintf("incus image import %s %s --alias %s", shellSingleQuote(incusTarPath), shellSingleQuote(rootfsPath), shellSingleQuote(aliasKey))
				} else {
					findCmd := fmt.Sprintf("find %s -name '*.tar.xz' | head -1", shellSingleQuote(extractDir))
					tarPath, err := i.sshClient.Execute(findCmd)
					if err != nil || utils.CleanCommandOutput(tarPath) == "" {
						i.sshClient.Execute(fmt.Sprintf("rm -rf %s", shellSingleQuote(extractDir)))
						return nil, fmt.Errorf("未找到解压后的Incus容器镜像文件")
					}
					importCmd = fmt.Sprintf("incus image import %s --alias %s", shellSingleQuote(utils.CleanCommandOutput(tarPath)), shellSingleQuote(aliasKey))
				}
				importOutput, importErr = i.sshClient.Execute(importCmd)
				i.sshClient.Execute(fmt.Sprintf("rm -rf %s", shellSingleQuote(extractDir)))
			} else {
				importOutput, importErr = i.sshClient.Execute(fmt.Sprintf("incus image import %s --alias %s", shellSingleQuote(imagePath), shellSingleQuote(aliasKey)))
			}
		}

		if importErr != nil {
			// 保留 Incus 原始错误输出，帮助排查镜像格式、存储池空间等问题
			if importOutput == "" {
				importOutput = importErr.Error()
			}
			global.APP_LOG.Error("Incus镜像导入命令失败",
				zap.String("alias", utils.TruncateString(aliasKey, 100)),
				zap.String("imagePath", utils.TruncateString(imagePath, 200)),
				zap.String("incusOutput", utils.TruncateString(importOutput, 1000)),
				zap.Error(importErr))
			return nil, fmt.Errorf("Incus%s镜像导入失败: %s (incus output: %s)", imageTypeStr, importErr.Error(), utils.TruncateString(importOutput, 500))
		}

		global.APP_LOG.Info("Incus"+imageTypeStr+"镜像导入成功",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("alias", utils.TruncateString(aliasKey, 100)),
			zap.String("type", config.InstanceType))

		// 导入成功后删除远程镜像 zip 文件
		if err := i.cleanupRemoteImage(originalImageName, imageURL, i.getCurrentArchitecture(), config.InstanceType); err != nil {
			global.APP_LOG.Warn("删除Incus远程"+imageTypeStr+"镜像文件失败",
				zap.String("imagePath", utils.TruncateString(imagePath, 100)),
				zap.String("type", config.InstanceType),
				zap.Error(err))
		} else {
			global.APP_LOG.Info("Incus远程"+imageTypeStr+"镜像文件已删除",
				zap.String("imagePath", utils.TruncateString(imagePath, 100)),
				zap.String("type", config.InstanceType))
		}

		return nil, nil
	})

	return err
}

// queryAndSetSystemImage 从数据库查询匹配的系统镜像记录并设置到配置中。
// 如果 config.ImageURL 已由上层设置（例如用户选择了特定系统镜像），则跳过查询，
// 避免模糊匹配返回不同的镜像导致 URL/别名不一致。
func (i *IncusProvider) queryAndSetSystemImage(ctx context.Context, config *provider.InstanceConfig) error {
	// 若上层已设置 ImageURL，直接信任该值，避免二次查询覆盖
	if config.ImageURL != "" {
		global.APP_LOG.Debug("ImageURL已由上层设置，跳过queryAndSetSystemImage",
			zap.String("image", config.Image),
			zap.String("imageURL", utils.TruncateString(config.ImageURL, 100)))
		return nil
	}

	// 构建查询条件
	var systemImage systemModel.SystemImage
	query := global.APP_DB.WithContext(ctx).Where("provider_type = ?", "incus")

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
	if i.config.Architecture != "" {
		// 使用数据库最新架构值（可能已被 auto-detect 纠正）
		query = query.Where("architecture = ?", i.getCurrentArchitecture())
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
		global.APP_LOG.Debug("从数据库获取到系统镜像配置",
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

// generateImageAlias 生成基于URL、镜像名和架构的唯一别名
func (i *IncusProvider) generateImageAlias(imageURL, imageName, architecture string) string {
	// 使用URL和架构的哈希值来生成唯一标识
	hashInput := fmt.Sprintf("%s_%s", imageURL, architecture)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))
	// 取前8位哈希值，组合镜像名和架构
	return fmt.Sprintf("%s-%s-%s", imageName, architecture, hash[:8])
}

// imageExists 检查镜像是否已存在
// getCurrentArchitecture 从数据库读取最新的 Provider 架构值。
// 用于镜像别名生成，确保自动架构检测纠正后立即生效，避免使用 i.config.Architecture 中的缓存旧值。
func (i *IncusProvider) getCurrentArchitecture() string {
	var p providerModel.Provider
	if err := global.APP_DB.Select("architecture").Where("id = ?", i.config.ID).First(&p).Error; err == nil && p.Architecture != "" {
		return p.Architecture
	}
	return i.config.Architecture
}

func (i *IncusProvider) imageExists(alias string) bool {
	output, err := i.sshClient.Execute(fmt.Sprintf("incus image list %s --format csv", shellSingleQuote(alias)))
	if err != nil {
		return false
	}
	return strings.TrimSpace(output) != ""
}

// isRemoteFileValid 检查远程文件是否存在
func (i *IncusProvider) isRemoteFileValid(remotePath string) bool {
	// 检查文件是否存在且大小大于 0
	output, err := i.sshClient.Execute(fmt.Sprintf("test -f %s -a -s %s && echo 'exists'", shellSingleQuote(remotePath), shellSingleQuote(remotePath)))
	if err != nil || strings.TrimSpace(output) != "exists" {
		return false
	}
	return true
}

// downloadImageToRemote 在远程服务器上下载镜像
func (i *IncusProvider) downloadImageToRemote(imageURL, imageName, architecture, instanceType string, useCDN bool) (string, error) {
	// 根据实例类型确定远程下载目录
	var downloadDir string
	if instanceType == "vm" {
		downloadDir = "/usr/local/bin/incus_vm_images"
	} else {
		downloadDir = "/usr/local/bin/incus_ct_images"
	}

	// 在远程服务器上创建下载目录
	cmd := fmt.Sprintf("mkdir -p %s", shellSingleQuote(downloadDir))
	_, err := i.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("创建远程下载目录失败: %w", err)
	}

	// 生成文件名
	fileName := i.generateRemoteFileName(imageName, imageURL, architecture, instanceType)
	remotePath := filepath.Join(downloadDir, fileName)

	// 检查远程文件是否已存在
	if i.isRemoteFileValid(remotePath) {
		global.APP_LOG.Debug("远程镜像文件已存在且完整，跳过下载",
			zap.String("imageName", imageName),
			zap.String("remotePath", remotePath))
		return remotePath, nil
	}

	// 如果文件存在但无效，先删除它
	i.sshClient.Execute(fmt.Sprintf("test -f %s && rm -f %s || true", shellSingleQuote(remotePath), shellSingleQuote(remotePath)))

	// 确定下载URL，传递 useCDN 参数
	downloadURL := i.getDownloadURL(imageURL, useCDN)

	global.APP_LOG.Info("开始在远程服务器下载镜像",
		zap.String("imageName", imageName),
		zap.String("downloadURL", downloadURL),
		zap.String("remotePath", remotePath),
		zap.Bool("useCDN", useCDN))

	// 在远程服务器上下载文件
	if err := i.downloadFileToRemote(downloadURL, remotePath); err != nil {
		// 下载失败，删除不完整的文件
		i.removeRemoteFile(remotePath)
		return "", fmt.Errorf("远程下载镜像失败: %w", err)
	}

	global.APP_LOG.Info("远程镜像下载完成",
		zap.String("imageName", imageName),
		zap.String("remotePath", remotePath))

	return remotePath, nil
}

// downloadFileToRemote 在远程服务器上下载文件

// generateRemoteFileName 生成远程文件名
func (i *IncusProvider) generateRemoteFileName(imageName, imageURL, architecture, instanceType string) string {
	// 组合字符串，包含实例类型以区分容器和虚拟机
	combined := fmt.Sprintf("%s_%s_%s_%s", imageName, imageURL, architecture, instanceType)

	// 计算MD5
	hasher := md5.New()
	hasher.Write([]byte(combined))
	md5Hash := fmt.Sprintf("%x", hasher.Sum(nil))

	// 使用镜像名称和MD5的前8位作为文件名，保持可读性
	safeName := strings.ReplaceAll(imageName, "/", "_")
	safeName = strings.ReplaceAll(safeName, ":", "_")

	return fmt.Sprintf("%s_%s.zip", safeName, md5Hash[:8])
}

// removeRemoteFile 删除远程文件
func (i *IncusProvider) removeRemoteFile(remotePath string) error {
	cmd := fmt.Sprintf("rm -f %s", shellSingleQuote(remotePath))
	_, err := i.sshClient.Execute(cmd)
	return err
}

// downloadFileToRemote 在远程服务器上下载文件
func (i *IncusProvider) downloadFileToRemote(url, remotePath string) error {
	// 使用curl在远程服务器上下载文件
	tmpPath := remotePath + ".tmp"

	// 下载文件，支持断点续传
	curlCmd := fmt.Sprintf(
		"curl -4 -L -C - --connect-timeout 30 --max-time 360 --retry 5 --retry-delay 10 --retry-max-time 0 -o %s %s",
		shellSingleQuote(tmpPath), shellSingleQuote(url),
	)

	global.APP_LOG.Debug("执行远程下载命令",
		zap.String("url", utils.TruncateString(url, 100)))

	output, err := i.sshClient.ExecuteWithTimeout(curlCmd, 1*time.Hour)
	if err != nil {
		// 清理临时文件
		i.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(tmpPath)))

		global.APP_LOG.Error("远程下载失败",
			zap.String("url", utils.TruncateString(url, 100)),
			zap.String("remotePath", remotePath),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("远程下载失败: %w", err)
	}

	// 移动文件到最终位置
	mvCmd := fmt.Sprintf("mv %s %s", shellSingleQuote(tmpPath), shellSingleQuote(remotePath))
	_, err = i.sshClient.Execute(mvCmd)
	if err != nil {
		global.APP_LOG.Error("移动文件失败",
			zap.String("tmpPath", tmpPath),
			zap.String("remotePath", remotePath),
			zap.Error(err))
		return fmt.Errorf("移动文件失败: %w", err)
	}

	global.APP_LOG.Info("远程下载成功",
		zap.String("url", utils.TruncateString(url, 100)),
		zap.String("remotePath", remotePath))

	return nil
}

// cleanupRemoteImage 清理远程镜像文件
func (i *IncusProvider) cleanupRemoteImage(imageName, imageURL, architecture, instanceType string) error {
	// 根据实例类型确定目录
	var downloadDir string
	if instanceType == "vm" {
		downloadDir = "/usr/local/bin/incus_vm_images"
	} else {
		downloadDir = "/usr/local/bin/incus_ct_images"
	}

	fileName := i.generateRemoteFileName(imageName, imageURL, architecture, instanceType)
	remotePath := filepath.Join(downloadDir, fileName)

	return i.removeRemoteFile(remotePath)
}

// ListImages 获取Incus镜像列表（优先API，自动回退SSH）
func (i *IncusProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	if !i.connected {
		return nil, fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		images, err := i.apiListImages(ctx)
		if err == nil {
			global.APP_LOG.Debug("Incus API调用成功 - 列出镜像")
			return images, nil
		}
		if fallbackErr := i.ensureSSHBeforeFallback(err, "列出镜像"); fallbackErr != nil {
			return nil, fallbackErr
		}
	}

	// 使用SSH方式
	if !i.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH")
	}

	return i.sshListImages()
}

// PullImage 拉取Incus镜像（优先API，自动回退SSH）
func (i *IncusProvider) PullImage(ctx context.Context, image string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiPullImage(ctx, image); err == nil {
			global.APP_LOG.Debug("Incus API调用成功 - 拉取镜像", zap.String("image", utils.TruncateString(image, 50)))
			return nil
		} else {
			if fallbackErr := i.ensureSSHBeforeFallback(err, "拉取镜像"); fallbackErr != nil {
				return fallbackErr
			}
		}
	}

	// 使用SSH方式
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return i.sshPullImage(image)
}

// DeleteImage 删除Incus镜像（优先API，自动回退SSH）
func (i *IncusProvider) DeleteImage(ctx context.Context, id string) error {
	if !i.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiDeleteImage(ctx, id); err == nil {
			global.APP_LOG.Debug("Incus API调用成功 - 删除镜像", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			if fallbackErr := i.ensureSSHBeforeFallback(err, "删除镜像"); fallbackErr != nil {
				return fallbackErr
			}
		}
	}

	// 使用SSH方式
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return i.sshDeleteImage(id)
}

// resolveIncusRemoteImage 将 Docker 风格的镜像名转换为 Incus 远程镜像引用
// 复用 LXD 的解析逻辑（两者使用相同的镜像服务器）
// "debian:12"    → "images:debian/12/cloud"
// "ubuntu:22.04" → "ubuntu:22.04"
func resolveIncusRemoteImage(imageName string) string {
	// 已经是远程格式（含 /）则直接返回
	if strings.Contains(imageName, "/") {
		return imageName
	}

	// 解析 Docker 风格 "os:version" 格式
	parts := strings.SplitN(imageName, ":", 2)
	if len(parts) != 2 {
		return imageName
	}

	osName := strings.ToLower(strings.TrimSpace(parts[0]))
	version := strings.TrimSpace(parts[1])

	switch osName {
	case "ubuntu":
		return imageName
	case "debian":
		return fmt.Sprintf("images:debian/%s/cloud", version)
	case "alpine":
		return fmt.Sprintf("images:alpine/%s/cloud", version)
	case "centos":
		return fmt.Sprintf("images:centos/%s/cloud", version)
	case "fedora":
		return fmt.Sprintf("images:fedora/%s/cloud", version)
	case "archlinux", "arch":
		return "images:archlinux/cloud"
	case "opensuse", "opensuse-leap":
		return fmt.Sprintf("images:opensuse/%s/cloud", version)
	case "rockylinux":
		return fmt.Sprintf("images:rockylinux/%s/cloud", version)
	case "oracle", "oraclelinux":
		return fmt.Sprintf("images:oracle/%s/cloud", version)
	case "kali":
		return fmt.Sprintf("images:kali/%s/cloud", version)
	case "gentoo":
		return fmt.Sprintf("images:gentoo/%s/cloud", version)
	default:
		return fmt.Sprintf("images:%s/%s/cloud", osName, version)
	}
}

// cleanupCachedImageOnFailure 在实例创建失败时清理可能不兼容/损坏的镜像缓存。
// 典型场景：ARM 节点上误下载了 amd64 镜像，或镜像文件下载不完整。
// 清理后下次创建会重新下载正确的镜像。
func (i *IncusProvider) cleanupCachedImageOnFailure(imageName, instanceType string) {
	if imageName == "" {
		return
	}
	if !strings.HasPrefix(imageName, "oneclickvirt_") {
		return
	}

	global.APP_LOG.Info("Incus实例创建失败，清理可能不兼容的镜像缓存",
		zap.String("imageName", imageName),
		zap.String("instanceType", instanceType))

	// 1. 删除 Incus 中的镜像别名
	deleteAliasCmd := fmt.Sprintf("incus image alias delete %s 2>/dev/null || true", shellSingleQuote(imageName))
	i.sshClient.Execute(deleteAliasCmd)

	// 2. 删除远程下载的镜像文件缓存
	var downloadDir string
	if instanceType == "vm" {
		downloadDir = "/usr/local/bin/incus_vm_images"
	} else {
		downloadDir = "/usr/local/bin/incus_ct_images"
	}
	cleanupCmd := fmt.Sprintf("find %s -name 'oneclickvirt_*' -mtime +0 -delete 2>/dev/null || true; "+
		"find %s -name 'oneclickvirt_*' -delete 2>/dev/null || true",
		shellSingleQuote(downloadDir), shellSingleQuote(downloadDir))
	i.sshClient.Execute(cleanupCmd)

	global.APP_LOG.Info("Incus镜像缓存清理完成",
		zap.String("imageName", imageName))
}
