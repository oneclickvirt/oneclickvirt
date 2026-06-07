package lxd

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"oneclickvirt/global"
	systemModel "oneclickvirt/model/system"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// handleImageDownloadAndImport 处理镜像下载和导入的通用逻辑
func (l *LXDProvider) handleImageDownloadAndImport(ctx context.Context, config *provider.InstanceConfig) error {
	// 首先从数据库查询匹配的系统镜像（单次读查询，无长事务）
	if err := l.queryAndSetSystemImage(ctx, config); err != nil {
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
		config.Image = imageNameWithPrefix + "_" + config.InstanceType + "_" + l.generateImageAlias(config.ImageURL, originalImageName, l.config.Architecture)[len(originalImageName)+1:]

		// 快速路径：镜像已存在，直接跳过（避免进入 singleflight）
		if l.imageExists(config.Image) {
			global.APP_LOG.Debug("LXD"+imageTypeStr+"镜像已存在，跳过导入",
				zap.String("alias", utils.TruncateString(config.Image, 100)),
				zap.String("type", config.InstanceType))
			return nil
		}

		return l.downloadAndImportImage(ctx, config, originalImageName, imageTypeStr)
	}

	// 无 URL（数据库无匹配的系统镜像）：尝试解析为 LXD 远程镜像引用
	// Docker 风格名称 "debian:12" → LXD 远程 "images:debian/12/cloud"
	// 如果已经是 LXD 远程格式（含 / 或 :），直接使用
	lxdRemoteImage := l.resolveLxdRemoteImage(originalImageName)
	if lxdRemoteImage != "" {
		config.Image = lxdRemoteImage
		global.APP_LOG.Info("将镜像名解析为LXD远程镜像引用",
			zap.String("original", originalImageName),
			zap.String("resolved", lxdRemoteImage),
			zap.String("type", config.InstanceType))
		return nil
	}

	// 兜底：保留原始名称让 LXD 自行解析
	config.Image = originalImageName
	global.APP_LOG.Warn("无法解析镜像为LXD远程引用，使用原始名称",
		zap.String("image", originalImageName),
		zap.String("type", config.InstanceType))
	return nil
}

// downloadAndImportImage 下载镜像并导入到 LXD（原 handleImageDownloadAndImport 的下载导入逻辑）
func (l *LXDProvider) downloadAndImportImage(ctx context.Context, config *provider.InstanceConfig, originalImageName, imageTypeStr string) error {
	// 使用 singleflight 确保同一别名只有一个协程执行下载+导入，
	// 其余协程阻塞等待，完成后共享同一结果，彻底消除并发解压/导入冲突。
	aliasKey := config.Image
	imageURL := config.ImageURL
	useCDN := config.UseCDN
	_, err, _ := l.imageImportGroup.Do(aliasKey, func() (interface{}, error) {
		// 等待期间镜像可能已由其他协程导入完毕，再次检查
		if l.imageExists(aliasKey) {
			global.APP_LOG.Debug("LXD"+imageTypeStr+"镜像已由并发协程完成导入，跳过",
				zap.String("alias", utils.TruncateString(aliasKey, 100)))
			return nil, nil
		}

		global.APP_LOG.Info("开始在远程服务器下载LXD"+imageTypeStr+"镜像",
			zap.String("imageURL", utils.TruncateString(imageURL, 200)),
			zap.String("type", config.InstanceType),
			zap.Bool("useCDN", useCDN))

		imagePath, err := l.downloadImageToRemote(imageURL, originalImageName, l.config.Country, l.config.Architecture, config.InstanceType, useCDN)
		if err != nil {
			return nil, fmt.Errorf("下载%s镜像失败: %w", imageTypeStr, err)
		}

		global.APP_LOG.Info("LXD"+imageTypeStr+"镜像下载成功",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("type", config.InstanceType))

		global.APP_LOG.Info("开始导入LXD"+imageTypeStr+"镜像",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("alias", utils.TruncateString(aliasKey, 100)),
			zap.String("type", config.InstanceType))

		var importErr error
		if config.InstanceType == "vm" {
			if strings.HasSuffix(imagePath, ".zip") {
				extractDir := strings.TrimSuffix(imagePath, ".zip")
				if _, err := l.sshClient.Execute(fmt.Sprintf("unzip -o %s -d %s", shellSingleQuote(imagePath), shellSingleQuote(extractDir))); err != nil {
					return nil, fmt.Errorf("解压LXD虚拟机镜像失败: %w", err)
				}
				var importCmd string
				findCmd := fmt.Sprintf("find %s -name '*.img' -o -name '*.qcow2' -o -name '*.vmdk' | head -1", shellSingleQuote(extractDir))
				vmImagePath, err := l.sshClient.Execute(findCmd)
				if err != nil || strings.TrimSpace(vmImagePath) == "" {
					findCmd = fmt.Sprintf("find %s -name '*.tar.xz' | head -1", shellSingleQuote(extractDir))
					vmImagePath, err = l.sshClient.Execute(findCmd)
					if err != nil || utils.CleanCommandOutput(vmImagePath) == "" {
						l.sshClient.Execute(fmt.Sprintf("rm -rf %s", shellSingleQuote(extractDir)))
						return nil, fmt.Errorf("未找到解压后的LXD虚拟机镜像文件")
					}
				}
				vmImagePath = utils.CleanCommandOutput(vmImagePath)
				lxdTarPath := fmt.Sprintf("%s/lxd.tar.xz", extractDir)
				diskPath := fmt.Sprintf("%s/disk.qcow2", extractDir)
				if l.isRemoteFileValid(lxdTarPath) && l.isRemoteFileValid(diskPath) {
					importCmd = fmt.Sprintf("lxc image import %s %s --alias %s", shellSingleQuote(lxdTarPath), shellSingleQuote(diskPath), shellSingleQuote(aliasKey))
				} else {
					importCmd = fmt.Sprintf("lxc image import %s --alias %s --vm", shellSingleQuote(vmImagePath), shellSingleQuote(aliasKey))
				}
				_, importErr = l.sshClient.Execute(importCmd)
				l.sshClient.Execute(fmt.Sprintf("rm -rf %s", shellSingleQuote(extractDir))) // 显式清理，避免 defer 被并发协程复用
			} else {
				_, importErr = l.sshClient.Execute(fmt.Sprintf("lxc image import %s --alias %s --vm", shellSingleQuote(imagePath), shellSingleQuote(aliasKey)))
			}
		} else {
			if strings.HasSuffix(imagePath, ".zip") {
				extractDir := strings.TrimSuffix(imagePath, ".zip")
				if _, err := l.sshClient.Execute(fmt.Sprintf("unzip -o %s -d %s", shellSingleQuote(imagePath), shellSingleQuote(extractDir))); err != nil {
					return nil, fmt.Errorf("解压LXD容器镜像失败: %w", err)
				}
				var importCmd string
				lxdTarPath := fmt.Sprintf("%s/lxd.tar.xz", extractDir)
				rootfsPath := fmt.Sprintf("%s/rootfs.squashfs", extractDir)
				if l.isRemoteFileValid(lxdTarPath) && l.isRemoteFileValid(rootfsPath) {
					importCmd = fmt.Sprintf("lxc image import %s %s --alias %s", shellSingleQuote(lxdTarPath), shellSingleQuote(rootfsPath), shellSingleQuote(aliasKey))
				} else {
					findCmd := fmt.Sprintf("find %s -name '*.tar.xz' | head -1", shellSingleQuote(extractDir))
					tarPath, err := l.sshClient.Execute(findCmd)
					if err != nil || utils.CleanCommandOutput(tarPath) == "" {
						l.sshClient.Execute(fmt.Sprintf("rm -rf %s", shellSingleQuote(extractDir)))
						return nil, fmt.Errorf("未找到解压后的LXD容器镜像文件")
					}
					importCmd = fmt.Sprintf("lxc image import %s --alias %s", shellSingleQuote(utils.CleanCommandOutput(tarPath)), shellSingleQuote(aliasKey))
				}
				_, importErr = l.sshClient.Execute(importCmd)
				l.sshClient.Execute(fmt.Sprintf("rm -rf %s", shellSingleQuote(extractDir))) // 显式清理，避免 defer 被并发协程复用
			} else {
				_, importErr = l.sshClient.Execute(fmt.Sprintf("lxc image import %s --alias %s", shellSingleQuote(imagePath), shellSingleQuote(aliasKey)))
			}
		}

		if importErr != nil {
			return nil, fmt.Errorf("LXD%s镜像导入失败: %w", imageTypeStr, importErr)
		}

		global.APP_LOG.Info("LXD"+imageTypeStr+"镜像导入成功",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("alias", utils.TruncateString(aliasKey, 100)),
			zap.String("type", config.InstanceType))

		// 导入成功后删除远程镜像 zip 文件
		if err := l.cleanupRemoteImage(originalImageName, imageURL, l.config.Architecture, config.InstanceType); err != nil {
			global.APP_LOG.Warn("删除LXD远程"+imageTypeStr+"镜像文件失败",
				zap.String("imagePath", utils.TruncateString(imagePath, 100)),
				zap.String("type", config.InstanceType),
				zap.Error(err))
		} else {
			global.APP_LOG.Info("LXD远程"+imageTypeStr+"镜像文件已删除",
				zap.String("imagePath", utils.TruncateString(imagePath, 100)),
				zap.String("type", config.InstanceType))
		}

		return nil, nil
	})

	return err
}

// resolveLxdRemoteImage 将 Docker 风格的镜像名转换为 LXD 远程镜像引用
// "debian:12"    → "images:debian/12/cloud"
// "ubuntu:22.04" → "ubuntu:22.04"
// "alpine:3.19"  → "images:alpine/3.19/cloud"
// "centos:7"     → "images:centos/7/cloud"
// 如果镜像名已经包含 "/"（如 "images:debian/12"），直接返回原值
func (l *LXDProvider) resolveLxdRemoteImage(imageName string) string {
	// 已经是 LXD 远程格式（含 /）则直接返回
	if strings.Contains(imageName, "/") {
		return imageName
	}

	// 解析 Docker 风格 "os:version" 格式
	parts := strings.SplitN(imageName, ":", 2)
	if len(parts) != 2 {
		// 无冒号：可能是别名，直接返回让 LXD 自行解析
		return imageName
	}

	osName := strings.ToLower(strings.TrimSpace(parts[0]))
	version := strings.TrimSpace(parts[1])

	// 已知的 LXD 官方远程：ubuntu、debian 等有专属镜像服务器
	// 其他的统一走 images: 远程
	switch osName {
	case "ubuntu":
		return imageName // ubuntu:22.04 可直接用
	case "debian":
		return fmt.Sprintf("images:debian/%s/cloud", version)
	case "alpine":
		if version == "latest" || version == "edge" {
			return fmt.Sprintf("images:alpine/%s/cloud", version)
		}
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
		// 未知 OS：尝试 images: 远程，用 os/version/cloud 格式
		return fmt.Sprintf("images:%s/%s/cloud", osName, version)
	}
}

// queryAndSetSystemImage 从数据库查询匹配的系统镜像记录并设置到配置中
func (l *LXDProvider) queryAndSetSystemImage(ctx context.Context, config *provider.InstanceConfig) error {
	// 构建查询条件
	var systemImage systemModel.SystemImage
	query := global.APP_DB.WithContext(ctx).Where("provider_type = ?", "lxd")

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
	if l.config.Architecture != "" {
		query = query.Where("architecture = ?", l.config.Architecture)
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
func (l *LXDProvider) generateImageAlias(imageURL, imageName, architecture string) string {
	// 使用URL和架构的哈希值来生成唯一标识
	hashInput := fmt.Sprintf("%s_%s", imageURL, architecture)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))
	// 取前8位哈希值，组合镜像名和架构
	return fmt.Sprintf("%s-%s-%s", imageName, architecture, hash[:8])
}

// imageExists 检查镜像是否已存在
func (l *LXDProvider) imageExists(alias string) bool {
	output, err := l.sshClient.Execute(fmt.Sprintf("lxc image list %s --format csv", shellSingleQuote(alias)))
	if err != nil {
		return false
	}
	return strings.TrimSpace(output) != ""
}

// downloadImageToRemote 在远程服务器上下载LXD镜像
func (l *LXDProvider) downloadImageToRemote(imageURL, imageName, providerCountry, architecture, instanceType string, useCDN bool) (string, error) {
	// 根据实例类型确定远程下载目录
	var downloadDir string
	if instanceType == "vm" {
		downloadDir = "/usr/local/bin/lxd_vm_images"
	} else {
		downloadDir = "/usr/local/bin/lxd_ct_images"
	}

	// 在远程服务器上创建下载目录
	cmd := fmt.Sprintf("mkdir -p %s", shellSingleQuote(downloadDir))
	_, err := l.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("创建远程下载目录失败: %w", err)
	}

	// 生成文件名
	fileName := l.generateRemoteFileName(imageName, imageURL, architecture, instanceType)
	remotePath := filepath.Join(downloadDir, fileName)

	// 检查远程文件是否已存在
	if l.isRemoteFileValid(remotePath) {
		global.APP_LOG.Debug("远程LXD镜像文件已存在且完整，跳过下载",
			zap.String("imageName", imageName),
			zap.String("remotePath", remotePath),
			zap.String("instanceType", instanceType))
		return remotePath, nil
	}

	// 确定下载URL，传递 useCDN 参数
	downloadURL := l.getDownloadURL(imageURL, providerCountry, useCDN)

	global.APP_LOG.Info("开始在远程服务器下载LXD镜像",
		zap.String("imageName", imageName),
		zap.String("downloadURL", downloadURL),
		zap.String("remotePath", remotePath),
		zap.String("instanceType", instanceType),
		zap.Bool("useCDN", useCDN))

	// 在远程服务器上下载文件
	if err := l.downloadFileToRemote(downloadURL, remotePath); err != nil {
		// 下载失败，删除不完整的文件
		l.removeRemoteFile(remotePath)
		return "", fmt.Errorf("远程下载LXD镜像失败: %w", err)
	}

	global.APP_LOG.Info("远程LXD镜像下载完成",
		zap.String("imageName", imageName),
		zap.String("remotePath", remotePath),
		zap.String("instanceType", instanceType))

	return remotePath, nil
}

// cleanupRemoteImage 清理远程LXD镜像文件
func (l *LXDProvider) cleanupRemoteImage(imageName, imageURL, architecture, instanceType string) error {
	var downloadDir string
	if instanceType == "vm" {
		downloadDir = "/usr/local/bin/lxd_vm_images"
	} else {
		downloadDir = "/usr/local/bin/lxd_ct_images"
	}

	fileName := l.generateRemoteFileName(imageName, imageURL, architecture, instanceType)
	remotePath := filepath.Join(downloadDir, fileName)

	return l.removeRemoteFile(remotePath)
}

// generateRemoteFileName 生成远程文件名
func (l *LXDProvider) generateRemoteFileName(imageName, imageURL, architecture, instanceType string) string {
	// 组合字符串
	combined := fmt.Sprintf("%s_%s_%s_%s", imageName, imageURL, architecture, instanceType)

	// 计算MD5
	hasher := md5.New()
	hasher.Write([]byte(combined))
	md5Hash := fmt.Sprintf("%x", hasher.Sum(nil))

	// 使用镜像名称和MD5的前8位作为文件名，保持可读性
	safeName := strings.ReplaceAll(imageName, "/", "_")
	safeName = strings.ReplaceAll(safeName, ":", "_")

	// LXD镜像通常是压缩包格式
	var extension string
	if strings.Contains(imageURL, ".zip") {
		extension = ".zip"
	} else if strings.Contains(imageURL, ".tar.xz") {
		extension = ".tar.xz"
	} else {
		extension = ".tar"
	}

	return fmt.Sprintf("%s_%s_%s%s", safeName, instanceType, md5Hash[:8], extension)
}

// isRemoteFileValid 检查远程文件是否存在且完整
// isRemoteFileValid 检查远程文件是否有效
func (l *LXDProvider) isRemoteFileValid(remotePath string) bool {
	// 检查文件是否存在且大小大于0
	cmd := fmt.Sprintf("test -f %s -a -s %s", shellSingleQuote(remotePath), shellSingleQuote(remotePath))
	_, err := l.sshClient.Execute(cmd)
	return err == nil
}

// removeRemoteFile 删除远程文件
func (l *LXDProvider) removeRemoteFile(remotePath string) error {
	cmd := fmt.Sprintf("rm -f %s", shellSingleQuote(remotePath))
	_, err := l.sshClient.Execute(cmd)
	return err
}

// downloadFileToRemote 在远程服务器上下载文件
// downloadFileToRemote 在远程服务器上下载文件
func (l *LXDProvider) downloadFileToRemote(url, remotePath string) error {
	// 使用curl在远程服务器上下载文件
	tmpPath := remotePath + ".tmp"

	// 下载文件，支持断点续传
	curlCmd := fmt.Sprintf(
		"curl -4 -L -C - --connect-timeout 30 --max-time 360 --retry 5 --retry-delay 10 --retry-max-time 0 -o %s %s",
		shellSingleQuote(tmpPath), shellSingleQuote(url),
	)

	global.APP_LOG.Debug("执行远程下载命令",
		zap.String("url", utils.TruncateString(url, 100)))

	output, err := l.sshClient.ExecuteWithTimeout(curlCmd, 1*time.Hour)
	if err != nil {
		// 清理临时文件
		l.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(tmpPath)))

		global.APP_LOG.Error("远程下载失败",
			zap.String("url", utils.TruncateString(url, 100)),
			zap.String("remotePath", remotePath),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("远程下载失败: %w", err)
	}

	// 移动文件到最终位置
	mvCmd := fmt.Sprintf("mv %s %s", shellSingleQuote(tmpPath), shellSingleQuote(remotePath))
	_, err = l.sshClient.Execute(mvCmd)
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

// ensureSSHScriptsAvailable 确保SSH脚本文件在远程服务器上可用
func (l *LXDProvider) ensureSSHScriptsAvailable(providerCountry string) error {
	scriptsDir := "/usr/local/bin"
	scripts := []string{"ssh_bash.sh", "ssh_sh.sh"}

	// 检查脚本是否都存在
	allExist := true
	for _, script := range scripts {
		scriptPath := filepath.Join(scriptsDir, script)
		if !l.isRemoteFileValid(scriptPath) {
			allExist = false
			global.APP_LOG.Debug("SSH脚本文件不存在或无效",
				zap.String("scriptPath", scriptPath))
			break
		}
	}

	if allExist {
		global.APP_LOG.Debug("SSH脚本文件都已存在且有效")
		return nil
	}

	// 下载缺失的脚本
	global.APP_LOG.Debug("开始下载SSH脚本文件")

	for _, script := range scripts {
		scriptPath := filepath.Join(scriptsDir, script)

		// 如果脚本已存在且有效，跳过
		if l.isRemoteFileValid(scriptPath) {
			global.APP_LOG.Debug("SSH脚本已存在，跳过下载",
				zap.String("script", script))
			continue
		}

		// 构建下载URL - 使用LXD仓库路径
		baseURL := "https://raw.githubusercontent.com/oneclickvirt/lxd/main/scripts/" + script
		downloadURL := l.getSSHScriptDownloadURL(baseURL, providerCountry)

		global.APP_LOG.Debug("开始下载SSH脚本",
			zap.String("script", script),
			zap.String("downloadURL", downloadURL),
			zap.String("scriptPath", scriptPath))

		// 下载脚本文件
		if err := l.downloadFileToRemote(downloadURL, scriptPath); err != nil {
			global.APP_LOG.Error("下载SSH脚本失败",
				zap.String("script", script),
				zap.Error(err))
			return fmt.Errorf("下载SSH脚本 %s 失败: %w", script, err)
		}

		// 设置执行权限
		chmodCmd := fmt.Sprintf("chmod +x %s", shellSingleQuote(scriptPath))
		if _, err := l.sshClient.Execute(chmodCmd); err != nil {
			global.APP_LOG.Error("设置SSH脚本执行权限失败",
				zap.String("script", script),
				zap.Error(err))
			return fmt.Errorf("设置SSH脚本 %s 执行权限失败: %w", script, err)
		}

		// 使用dos2unix处理脚本格式（如果可用）
		dos2unixCmd := fmt.Sprintf("command -v dos2unix >/dev/null 2>&1 && dos2unix %s || true", shellSingleQuote(scriptPath))
		l.sshClient.Execute(dos2unixCmd)

		global.APP_LOG.Debug("SSH脚本下载并设置完成",
			zap.String("script", script),
			zap.String("scriptPath", scriptPath))
	}

	global.APP_LOG.Info("所有SSH脚本文件下载完成")
	return nil
}

// getSSHScriptDownloadURL 获取SSH脚本下载URL，支持CDN
func (l *LXDProvider) getSSHScriptDownloadURL(originalURL, providerCountry string) string {
	// 如果是中国地区，尝试使用CDN
	if providerCountry == "CN" || providerCountry == "cn" {
		if cdnURL := l.getSSHScriptCDNURL(originalURL); cdnURL != "" {
			// 测试CDN可用性
			testCmd := fmt.Sprintf("curl -s -I --max-time 5 %s | head -n 1 | grep -q '200'", shellSingleQuote(cdnURL))
			if _, err := l.sshClient.Execute(testCmd); err == nil {
				global.APP_LOG.Debug("使用CDN下载SSH脚本",
					zap.String("cdnURL", cdnURL))
				return cdnURL
			}
		}
	}
	return originalURL
}

// getSSHScriptCDNURL 获取SSH脚本CDN URL
func (l *LXDProvider) getSSHScriptCDNURL(originalURL string) string {
	cdnEndpoints := utils.GetCDNEndpoints()

	// 直接在原始URL前加CDN前缀
	// 原始URL格式: https://raw.githubusercontent.com/oneclickvirt/lxd/main/scripts/ssh_bash.sh
	// CDN URL格式: https://cdn0.spiritlhl.top/https://raw.githubusercontent.com/oneclickvirt/lxd/main/scripts/ssh_bash.sh
	for _, endpoint := range cdnEndpoints {
		cdnURL := endpoint + originalURL
		// 测试CDN可用性
		testCmd := fmt.Sprintf("curl -s -I --max-time 5 %s | head -n 1 | grep -q '200'", shellSingleQuote(cdnURL))
		if _, err := l.sshClient.Execute(testCmd); err == nil {
			return cdnURL
		}
	}
	return ""
}

// ListImages 获取LXD镜像列表（优先API，自动回退SSH）
func (l *LXDProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	if !l.connected {
		return nil, fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		images, err := l.apiListImages(ctx)
		if err == nil {
			global.APP_LOG.Debug("LXD API调用成功 - 获取镜像列表")
			return images, nil
		}
		if fallbackErr := l.ensureSSHBeforeFallback(err, "获取镜像列表"); fallbackErr != nil {
			return nil, fallbackErr
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshListImages(ctx)
}

// PullImage 拉取LXD镜像（优先API，自动回退SSH）
func (l *LXDProvider) PullImage(ctx context.Context, image string) error {
	if !l.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiPullImage(ctx, image); err == nil {
			global.APP_LOG.Debug("LXD API调用成功 - 拉取镜像", zap.String("image", utils.TruncateString(image, 100)))
			return nil
		} else {
			if fallbackErr := l.ensureSSHBeforeFallback(err, "拉取镜像"); fallbackErr != nil {
				return fallbackErr
			}
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshPullImage(ctx, image)
}

// DeleteImage 删除LXD镜像（优先API，自动回退SSH）
func (l *LXDProvider) DeleteImage(ctx context.Context, id string) error {
	if !l.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiDeleteImage(ctx, id); err == nil {
			global.APP_LOG.Debug("LXD API调用成功 - 删除镜像", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		} else {
			if fallbackErr := l.ensureSSHBeforeFallback(err, "删除镜像"); fallbackErr != nil {
				return fallbackErr
			}
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH 方式
	return l.sshDeleteImage(ctx, id)
}

// cleanupCachedImageOnFailure 在实例创建失败时清理可能不兼容/损坏的镜像缓存。
// 典型场景：ARM 节点上误下载了 amd64 镜像，或镜像文件下载不完整。
// 清理后下次创建会重新下载正确的镜像。
func (l *LXDProvider) cleanupCachedImageOnFailure(imageName, instanceType string) {
	if imageName == "" {
		return
	}
	// 仅清理 oneclickvirt_ 前缀的本地镜像别名（非远程镜像引用）
	if !strings.HasPrefix(imageName, "oneclickvirt_") {
		return
	}

	global.APP_LOG.Info("实例创建失败，清理可能不兼容的镜像缓存",
		zap.String("imageName", imageName),
		zap.String("instanceType", instanceType))

	// 1. 删除 LXD 中的镜像别名
	deleteAliasCmd := fmt.Sprintf("lxc image alias delete %s 2>/dev/null || true", shellSingleQuote(imageName))
	l.sshClient.Execute(deleteAliasCmd)

	// 2. 删除远程下载的镜像文件缓存
	var downloadDir string
	if instanceType == "vm" {
		downloadDir = "/usr/local/bin/lxd_vm_images"
	} else {
		downloadDir = "/usr/local/bin/lxd_ct_images"
	}
	// 删除 oneclickvirt_ 开头的相关缓存文件
	cleanupCmd := fmt.Sprintf("find %s -name 'oneclickvirt_*' -mtime +0 -delete 2>/dev/null || true; "+
		"find %s -name 'oneclickvirt_*' -delete 2>/dev/null || true",
		shellSingleQuote(downloadDir), shellSingleQuote(downloadDir))
	l.sshClient.Execute(cleanupCmd)

	global.APP_LOG.Info("镜像缓存清理完成",
		zap.String("imageName", imageName))
}
