package incus

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
func (i *IncusProvider) handleImageDownloadAndImport(ctx context.Context, config *provider.InstanceConfig) error {
	// 首先从数据库查询匹配的系统镜像（单次读查询，无长事务）
	if err := i.queryAndSetSystemImage(ctx, config); err != nil {
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

	// 提前计算确定性别名（纯计算，无 I/O），确保并发时 key 一致
	if config.ImageURL != "" {
		config.Image = imageNameWithPrefix + "_" + config.InstanceType + "_" + i.generateImageAlias(config.ImageURL, originalImageName, i.config.Architecture)[len(originalImageName)+1:]
	} else {
		config.Image = imageNameWithPrefix + "_" + config.InstanceType
	}

	// 没有 URL 说明不需要下载/导入
	if config.ImageURL == "" {
		return nil
	}

	// 快速路径：镜像已存在，直接跳过（避免进入 singleflight）
	if i.imageExists(config.Image) {
		global.APP_LOG.Debug("Incus"+imageTypeStr+"镜像已存在，跳过导入",
			zap.String("alias", utils.TruncateString(config.Image, 100)),
			zap.String("type", config.InstanceType))
		return nil
	}

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

		imagePath, err := i.downloadImageToRemote(imageURL, originalImageName, i.config.Architecture, config.InstanceType, useCDN)
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
		if config.InstanceType == "vm" {
			if strings.HasSuffix(imagePath, ".zip") {
				extractDir := strings.TrimSuffix(imagePath, ".zip")
				if _, err := i.sshClient.Execute(fmt.Sprintf("unzip -o %s -d %s", imagePath, extractDir)); err != nil {
					return nil, fmt.Errorf("解压Incus虚拟机镜像失败: %w", err)
				}
				var importCmd string
				findCmd := fmt.Sprintf("find %s -name '*.img' -o -name '*.qcow2' -o -name '*.vmdk' | head -1", extractDir)
				vmImagePath, err := i.sshClient.Execute(findCmd)
				if err != nil || strings.TrimSpace(vmImagePath) == "" {
					findCmd = fmt.Sprintf("find %s -name '*.tar.xz' | head -1", extractDir)
					vmImagePath, err = i.sshClient.Execute(findCmd)
					if err != nil || utils.CleanCommandOutput(vmImagePath) == "" {
						i.sshClient.Execute(fmt.Sprintf("rm -rf %s", extractDir))
						return nil, fmt.Errorf("未找到解压后的Incus虚拟机镜像文件")
					}
				}
				vmImagePath = utils.CleanCommandOutput(vmImagePath)
				incusTarPath := fmt.Sprintf("%s/incus.tar.xz", extractDir)
				diskPath := fmt.Sprintf("%s/disk.qcow2", extractDir)
				if i.isRemoteFileValid(incusTarPath) && i.isRemoteFileValid(diskPath) {
					importCmd = fmt.Sprintf("incus image import %s %s --alias %s", incusTarPath, diskPath, aliasKey)
				} else {
					importCmd = fmt.Sprintf("incus image import %s --alias %s --vm", vmImagePath, aliasKey)
				}
				_, importErr = i.sshClient.Execute(importCmd)
				i.sshClient.Execute(fmt.Sprintf("rm -rf %s", extractDir)) // 显式清理，避免 defer 被并发协程复用
			} else {
				_, importErr = i.sshClient.Execute(fmt.Sprintf("incus image import %s --alias %s --vm", imagePath, aliasKey))
			}
		} else {
			if strings.HasSuffix(imagePath, ".zip") {
				extractDir := strings.TrimSuffix(imagePath, ".zip")
				if _, err := i.sshClient.Execute(fmt.Sprintf("unzip -o %s -d %s", imagePath, extractDir)); err != nil {
					return nil, fmt.Errorf("解压Incus容器镜像失败: %w", err)
				}
				var importCmd string
				incusTarPath := fmt.Sprintf("%s/incus.tar.xz", extractDir)
				rootfsPath := fmt.Sprintf("%s/rootfs.squashfs", extractDir)
				if i.isRemoteFileValid(incusTarPath) && i.isRemoteFileValid(rootfsPath) {
					importCmd = fmt.Sprintf("incus image import %s %s --alias %s", incusTarPath, rootfsPath, aliasKey)
				} else {
					findCmd := fmt.Sprintf("find %s -name '*.tar.xz' | head -1", extractDir)
					tarPath, err := i.sshClient.Execute(findCmd)
					if err != nil || utils.CleanCommandOutput(tarPath) == "" {
						i.sshClient.Execute(fmt.Sprintf("rm -rf %s", extractDir))
						return nil, fmt.Errorf("未找到解压后的Incus容器镜像文件")
					}
					importCmd = fmt.Sprintf("incus image import %s --alias %s", utils.CleanCommandOutput(tarPath), aliasKey)
				}
				_, importErr = i.sshClient.Execute(importCmd)
				i.sshClient.Execute(fmt.Sprintf("rm -rf %s", extractDir)) // 显式清理，避免 defer 被并发协程复用
			} else {
				_, importErr = i.sshClient.Execute(fmt.Sprintf("incus image import %s --alias %s", imagePath, aliasKey))
			}
		}

		if importErr != nil {
			return nil, fmt.Errorf("Incus%s镜像导入失败: %w", imageTypeStr, importErr)
		}

		global.APP_LOG.Info("Incus"+imageTypeStr+"镜像导入成功",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("alias", utils.TruncateString(aliasKey, 100)),
			zap.String("type", config.InstanceType))

		// 导入成功后删除远程镜像 zip 文件
		if err := i.cleanupRemoteImage(originalImageName, imageURL, i.config.Architecture, config.InstanceType); err != nil {
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

// queryAndSetSystemImage 从数据库查询匹配的系统镜像记录并设置到配置中
func (i *IncusProvider) queryAndSetSystemImage(ctx context.Context, config *provider.InstanceConfig) error {
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
		query = query.Where("architecture = ?", i.config.Architecture)
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
func (i *IncusProvider) imageExists(alias string) bool {
	output, err := i.sshClient.Execute(fmt.Sprintf("incus image list %s --format csv", alias))
	if err != nil {
		return false
	}
	return strings.TrimSpace(output) != ""
}

// isRemoteFileValid 检查远程文件是否存在
func (i *IncusProvider) isRemoteFileValid(remotePath string) bool {
	// 检查文件是否存在且大小大于 0
	output, err := i.sshClient.Execute(fmt.Sprintf("test -f %s -a -s %s && echo 'exists'", remotePath, remotePath))
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
	cmd := fmt.Sprintf("mkdir -p %s", downloadDir)
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
	i.sshClient.Execute(fmt.Sprintf("test -f %s && rm -f %s || true", remotePath, remotePath))

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
	cmd := fmt.Sprintf("rm -f %s", remotePath)
	_, err := i.sshClient.Execute(cmd)
	return err
}

// downloadFileToRemote 在远程服务器上下载文件
func (i *IncusProvider) downloadFileToRemote(url, remotePath string) error {
	// 使用curl在远程服务器上下载文件
	tmpPath := remotePath + ".tmp"

	// 下载文件，支持断点续传
	curlCmd := fmt.Sprintf(
		"curl -4 -L -C - --connect-timeout 30 --retry 5 --retry-delay 10 --retry-max-time 0 -o %s '%s'",
		tmpPath, url,
	)

	global.APP_LOG.Debug("执行远程下载命令",
		zap.String("url", utils.TruncateString(url, 100)))

	output, err := i.sshClient.Execute(curlCmd)
	if err != nil {
		// 清理临时文件
		i.sshClient.Execute(fmt.Sprintf("rm -f %s", tmpPath))

		global.APP_LOG.Error("远程下载失败",
			zap.String("url", utils.TruncateString(url, 100)),
			zap.String("remotePath", remotePath),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("远程下载失败: %w", err)
	}

	// 移动文件到最终位置
	mvCmd := fmt.Sprintf("mv %s %s", tmpPath, remotePath)
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
