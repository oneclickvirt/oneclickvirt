package lxd

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"oneclickvirt/global"
	systemModel "oneclickvirt/model/system"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// handleImageDownloadAndImport 处理镜像下载和导入的通用逻辑
func (l *LXDProvider) handleImageDownloadAndImport(ctx context.Context, config *provider.InstanceConfig) error {
	// 首先从数据库查询匹配的系统镜像
	if err := l.queryAndSetSystemImage(ctx, config); err != nil {
		global.APP_LOG.Warn("从数据库查询系统镜像失败，使用原有镜像配置",
			zap.String("image", config.Image),
			zap.Error(err))
	}

	// 为镜像名称添加前缀
	originalImageName := config.Image
	imageNameWithPrefix := "oneclickvirt_" + config.Image

	// 根据实例类型确定镜像类型
	var imageTypeStr string
	if config.InstanceType == "vm" {
		imageTypeStr = "虚拟机"
	} else {
		imageTypeStr = "容器"
	}

	// 如果有镜像URL，先在远程服务器下载镜像
	if config.ImageURL != "" {
		global.APP_LOG.Info("开始在远程服务器下载LXD"+imageTypeStr+"镜像",
			zap.String("imageURL", utils.TruncateString(config.ImageURL, 200)),
			zap.String("type", config.InstanceType),
			zap.Bool("useCDN", config.UseCDN))

		// 直接在远程服务器上下载镜像
		imagePath, err := l.downloadImageToRemote(config.ImageURL, originalImageName, l.config.Country, l.config.Architecture, config.InstanceType, config.UseCDN)
		if err != nil {
			return fmt.Errorf("下载%s镜像失败: %w", imageTypeStr, err)
		}
		config.ImagePath = imagePath
		global.APP_LOG.Info("LXD"+imageTypeStr+"镜像下载成功",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("type", config.InstanceType))

		// 生成基于URL、架构和实例类型的唯一别名，避免重复
		config.Image = imageNameWithPrefix + "_" + config.InstanceType + "_" + l.generateImageAlias(config.ImageURL, originalImageName, l.config.Architecture)[len(originalImageName)+1:]
	} else {
		config.Image = imageNameWithPrefix + "_" + config.InstanceType
	}

	// 如果有镜像文件路径，先导入镜像
	if config.ImagePath != "" {
		// 检查镜像是否已存在
		if !l.imageExists(config.Image) {
			global.APP_LOG.Info("开始导入LXD"+imageTypeStr+"镜像",
				zap.String("imagePath", utils.TruncateString(config.ImagePath, 200)),
				zap.String("alias", utils.TruncateString(config.Image, 100)),
				zap.String("type", config.InstanceType))

			var importCmd string
			// 根据实例类型确定导入方式
			if config.InstanceType == "vm" {
				// 虚拟机镜像导入
				if strings.HasSuffix(config.ImagePath, ".zip") {
					extractDir := strings.TrimSuffix(config.ImagePath, ".zip")
					unzipCmd := fmt.Sprintf("unzip -o %s -d %s", config.ImagePath, extractDir)
					_, err := l.sshClient.Execute(unzipCmd)
					if err != nil {
						return fmt.Errorf("解压LXD虚拟机镜像失败: %w", err)
					}

					// 查找解压后的VM镜像文件（可能是img、qcow2等格式）
					findCmd := fmt.Sprintf("find %s -name '*.img' -o -name '*.qcow2' -o -name '*.vmdk' | head -1", extractDir)
					vmImagePath, err := l.sshClient.Execute(findCmd)
					if err != nil || strings.TrimSpace(vmImagePath) == "" {
						// 如果没找到VM镜像，尝试查找tar.xz
						findCmd = fmt.Sprintf("find %s -name '*.tar.xz' | head -1", extractDir)
						vmImagePath, err = l.sshClient.Execute(findCmd)
						if err != nil || utils.CleanCommandOutput(vmImagePath) == "" {
							return fmt.Errorf("未找到解压后的LXD虚拟机镜像文件")
						}
					}

					vmImagePath = utils.CleanCommandOutput(vmImagePath)
					// 检查是否需要导入lxd.tar.xz和disk文件
					lxdTarPath := fmt.Sprintf("%s/lxd.tar.xz", extractDir)
					diskPath := fmt.Sprintf("%s/disk.qcow2", extractDir)
					if l.isRemoteFileValid(lxdTarPath) && l.isRemoteFileValid(diskPath) {
						importCmd = fmt.Sprintf("lxc image import %s %s --alias %s", lxdTarPath, diskPath, config.Image)
					} else {
						importCmd = fmt.Sprintf("lxc image import %s --alias %s --vm", vmImagePath, config.Image)
					}

					// 清理解压后的临时目录
					defer l.sshClient.Execute(fmt.Sprintf("rm -rf %s", extractDir))
				} else {
					importCmd = fmt.Sprintf("lxc image import %s --alias %s --vm", config.ImagePath, config.Image)
				}
			} else {
				// 容器镜像导入
				if strings.HasSuffix(config.ImagePath, ".zip") {
					extractDir := strings.TrimSuffix(config.ImagePath, ".zip")
					unzipCmd := fmt.Sprintf("unzip -o %s -d %s", config.ImagePath, extractDir)
					_, err := l.sshClient.Execute(unzipCmd)
					if err != nil {
						return fmt.Errorf("解压LXD容器镜像失败: %w", err)
					}
					// 查找解压后的文件
					lxdTarPath := fmt.Sprintf("%s/lxd.tar.xz", extractDir)
					rootfsPath := fmt.Sprintf("%s/rootfs.squashfs", extractDir)

					if l.isRemoteFileValid(lxdTarPath) && l.isRemoteFileValid(rootfsPath) {
						importCmd = fmt.Sprintf("lxc image import %s %s --alias %s", lxdTarPath, rootfsPath, config.Image)
					} else {
						// 查找任何tar.xz文件
						findCmd := fmt.Sprintf("find %s -name '*.tar.xz' | head -1", extractDir)
						tarPath, err := l.sshClient.Execute(findCmd)
						if err != nil || utils.CleanCommandOutput(tarPath) == "" {
							return fmt.Errorf("未找到解压后的LXD容器镜像文件")
						}
						tarPath = utils.CleanCommandOutput(tarPath)
						importCmd = fmt.Sprintf("lxc image import %s --alias %s", tarPath, config.Image)
					}

					// 清理解压后的临时目录
					defer l.sshClient.Execute(fmt.Sprintf("rm -rf %s", extractDir))
				} else {
					importCmd = fmt.Sprintf("lxc image import %s --alias %s", config.ImagePath, config.Image)
				}
			}

			_, err := l.sshClient.Execute(importCmd)
			if err != nil {
				return fmt.Errorf("LXD%s镜像导入失败: %w", imageTypeStr, err)
			}

			global.APP_LOG.Info("LXD"+imageTypeStr+"镜像导入成功",
				zap.String("imagePath", utils.TruncateString(config.ImagePath, 200)),
				zap.String("alias", utils.TruncateString(config.Image, 100)),
				zap.String("type", config.InstanceType))

			// 导入成功后删除远程镜像文件
			if config.ImageURL != "" {
				if err := l.cleanupRemoteImage(originalImageName, config.ImageURL, l.config.Architecture, config.InstanceType); err != nil {
					global.APP_LOG.Warn("删除LXD远程"+imageTypeStr+"镜像文件失败",
						zap.String("imagePath", utils.TruncateString(config.ImagePath, 100)),
						zap.String("type", config.InstanceType),
						zap.Error(err))
				} else {
					global.APP_LOG.Info("LXD远程"+imageTypeStr+"镜像文件已删除",
						zap.String("imagePath", utils.TruncateString(config.ImagePath, 100)),
						zap.String("type", config.InstanceType))
				}
			}
		} else {
			global.APP_LOG.Info("LXD"+imageTypeStr+"镜像已存在，跳过导入",
				zap.String("alias", utils.TruncateString(config.Image, 100)),
				zap.String("type", config.InstanceType))
		}
	}

	return nil
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
		global.APP_LOG.Info("从数据库获取到系统镜像配置",
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
	output, err := l.sshClient.Execute(fmt.Sprintf("lxc image list %s --format csv", alias))
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
	cmd := fmt.Sprintf("mkdir -p %s", downloadDir)
	_, err := l.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("创建远程下载目录失败: %w", err)
	}

	// 生成文件名
	fileName := l.generateRemoteFileName(imageName, imageURL, architecture, instanceType)
	remotePath := filepath.Join(downloadDir, fileName)

	// 检查远程文件是否已存在
	if l.isRemoteFileValid(remotePath) {
		global.APP_LOG.Info("远程LXD镜像文件已存在且完整，跳过下载",
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
	cmd := fmt.Sprintf("test -f %s -a -s %s", remotePath, remotePath)
	_, err := l.sshClient.Execute(cmd)
	return err == nil
}

// removeRemoteFile 删除远程文件
func (l *LXDProvider) removeRemoteFile(remotePath string) error {
	cmd := fmt.Sprintf("rm -f %s", remotePath)
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
		"curl -4 -L -C - --connect-timeout 30 --retry 5 --retry-delay 10 --retry-max-time 0 -o %s '%s'",
		tmpPath, url,
	)

	global.APP_LOG.Info("执行远程下载命令",
		zap.String("url", utils.TruncateString(url, 100)))

	output, err := l.sshClient.Execute(curlCmd)
	if err != nil {
		// 清理临时文件
		l.sshClient.Execute(fmt.Sprintf("rm -f %s", tmpPath))

		global.APP_LOG.Error("远程下载失败",
			zap.String("url", utils.TruncateString(url, 100)),
			zap.String("remotePath", remotePath),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("远程下载失败: %w", err)
	}

	// 移动文件到最终位置
	mvCmd := fmt.Sprintf("mv %s %s", tmpPath, remotePath)
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
			global.APP_LOG.Info("SSH脚本文件不存在或无效",
				zap.String("scriptPath", scriptPath))
			break
		}
	}

	if allExist {
		global.APP_LOG.Info("SSH脚本文件都已存在且有效")
		return nil
	}

	// 下载缺失的脚本
	global.APP_LOG.Info("开始下载SSH脚本文件")

	for _, script := range scripts {
		scriptPath := filepath.Join(scriptsDir, script)

		// 如果脚本已存在且有效，跳过
		if l.isRemoteFileValid(scriptPath) {
			global.APP_LOG.Info("SSH脚本已存在，跳过下载",
				zap.String("script", script))
			continue
		}

		// 构建下载URL - 使用LXD仓库路径
		baseURL := "https://raw.githubusercontent.com/oneclickvirt/lxd/main/scripts/" + script
		downloadURL := l.getSSHScriptDownloadURL(baseURL, providerCountry)

		global.APP_LOG.Info("开始下载SSH脚本",
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
		chmodCmd := fmt.Sprintf("chmod +x %s", scriptPath)
		if _, err := l.sshClient.Execute(chmodCmd); err != nil {
			global.APP_LOG.Error("设置SSH脚本执行权限失败",
				zap.String("script", script),
				zap.Error(err))
			return fmt.Errorf("设置SSH脚本 %s 执行权限失败: %w", script, err)
		}

		// 使用dos2unix处理脚本格式（如果可用）
		dos2unixCmd := fmt.Sprintf("command -v dos2unix >/dev/null 2>&1 && dos2unix %s || true", scriptPath)
		l.sshClient.Execute(dos2unixCmd)

		global.APP_LOG.Info("SSH脚本下载并设置完成",
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
			testCmd := fmt.Sprintf("curl -s -I --max-time 5 '%s' | head -n 1 | grep -q '200'", cdnURL)
			if _, err := l.sshClient.Execute(testCmd); err == nil {
				global.APP_LOG.Info("使用CDN下载SSH脚本",
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
		testCmd := fmt.Sprintf("curl -s -I --max-time 5 '%s' | head -n 1 | grep -q '200'", cdnURL)
		if _, err := l.sshClient.Execute(testCmd); err == nil {
			return cdnURL
		}
	}
	return ""
}
