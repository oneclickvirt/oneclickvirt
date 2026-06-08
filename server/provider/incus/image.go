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

	// 本地镜像优先：如果用户/数据库传入的本来就是本地 alias/fingerprint，先直接使用。
	if config.ImageURL == "" && i.imageExists(originalImageName) {
		config.Image = originalImageName
		global.APP_LOG.Info("Incus"+imageTypeStr+"镜像已在本地存在，优先使用本地镜像",
			zap.String("image", utils.TruncateString(config.Image, 100)),
			zap.String("type", config.InstanceType))
		if progressCallback != nil {
			progressCallback(28, "使用本地已存在镜像")
		}
		return nil
	}

	// 有 URL：下载 → 导入 → 用本地别名
	if config.ImageURL != "" {
		imageNameWithPrefix := "oneclickvirt_" + originalImageName
		// 始终使用数据库中的最新架构值（可能已被 detectAndUpdateArchitecture 自动纠正）
		architecture := i.getCurrentArchitecture()
		config.Image = imageNameWithPrefix + "_" + config.InstanceType + "_" + i.generateImageAlias(config.ImageURL, originalImageName, architecture)[len(originalImageName)+1:]

		// 快速路径：精确别名已存在
		if i.imageExists(config.Image) {
			global.APP_LOG.Debug("Incus"+imageTypeStr+"镜像已存在，跳过导入",
				zap.String("alias", utils.TruncateString(config.Image, 100)),
				zap.String("type", config.InstanceType))
			if progressCallback != nil {
				progressCallback(28, "镜像已缓存，跳过下载")
			}
			return nil
		}

		// 前缀匹配只能作为优化，不能直接返回成功。
		// 只有成功把目标 alias 绑定到已有 fingerprint，并再次验证 alias 可用后，才跳过下载。
		if fp, existingAlias := i.findImageFingerprintByPrefix(imageNameWithPrefix + "_" + config.InstanceType + "_"); fp != "" {
			global.APP_LOG.Info("Incus"+imageTypeStr+"镜像已通过前缀匹配找到，尝试修补目标别名",
				zap.String("existingAlias", utils.TruncateString(existingAlias, 100)),
				zap.String("targetAlias", utils.TruncateString(config.Image, 100)),
				zap.String("fingerprint", utils.TruncateString(fp, 64)))
			if err := i.ensureImageAliasFromFingerprint(config.Image, fp); err == nil {
				if progressCallback != nil {
					progressCallback(28, "镜像别名已自动修补，跳过下载")
				}
				return nil
			} else {
				global.APP_LOG.Warn("Incus镜像前缀匹配别名修补失败，将继续重新下载导入",
					zap.String("targetAlias", utils.TruncateString(config.Image, 100)),
					zap.String("fingerprint", utils.TruncateString(fp, 64)),
					zap.Error(err))
			}
		}

		if progressCallback != nil {
			progressCallback(17, "开始下载镜像...")
		}
		return i.downloadAndImportImage(ctx, config, originalImageName, imageTypeStr, progressCallback)
	}

	// 无 URL（数据库无匹配的系统镜像）：先尝试 spiritlhl simplestreams 镜像源，
	// 成功后复制到本地 alias，再使用本地镜像创建实例；失败再回退到官方 images:/ubuntu: 远程。
	if fallbackAlias := i.spiritlhlLocalAlias(originalImageName, config.InstanceType); fallbackAlias != "" {
		if err := i.copySpiritlhlImageToLocal(originalImageName, fallbackAlias, config.InstanceType); err == nil {
			config.Image = fallbackAlias
			global.APP_LOG.Info("已从spiritlhl Incus镜像源复制镜像到本地",
				zap.String("original", originalImageName),
				zap.String("alias", utils.TruncateString(config.Image, 100)),
				zap.String("type", config.InstanceType))
			if progressCallback != nil {
				progressCallback(28, "已使用spiritlhl镜像源缓存到本地")
			}
			return nil
		} else {
			global.APP_LOG.Warn("spiritlhl Incus镜像源回退失败，将继续尝试官方远程镜像",
				zap.String("image", originalImageName),
				zap.String("targetAlias", fallbackAlias),
				zap.Error(err))
		}
	}

	// 兜底：尝试解析为 Incus 官方远程镜像引用。
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
	// 使用 singleflight 确保同一别名只有一个协程执行下载+导入；
	// 同时使用远端 flock 防止多个 oneclickvirt 进程/容器同时导入同一 fingerprint。
	aliasKey := config.Image
	imageURL := config.ImageURL
	useCDN := config.UseCDN
	trySpiritlhlFallback := func(stage string, cause error) error {
		if err := i.copySpiritlhlImageToLocal(originalImageName, aliasKey, config.InstanceType); err == nil {
			global.APP_LOG.Info("Incus镜像下载/导入异常后已回退到spiritlhl本地缓存",
				zap.String("stage", stage),
				zap.String("alias", utils.TruncateString(aliasKey, 100)),
				zap.String("type", config.InstanceType))
			return nil
		} else {
			global.APP_LOG.Warn("Incus镜像spiritlhl回退失败",
				zap.String("stage", stage),
				zap.String("alias", utils.TruncateString(aliasKey, 100)),
				zap.Error(err))
		}
		return cause
	}
	_, err, _ := i.imageImportGroup.Do(aliasKey, func() (interface{}, error) {
		if i.imageExists(aliasKey) {
			global.APP_LOG.Debug("Incus"+imageTypeStr+"镜像已由并发协程完成导入，跳过",
				zap.String("alias", utils.TruncateString(aliasKey, 100)))
			return nil, nil
		}

		if progressCallback != nil {
			progressCallback(19, "下载镜像到远程服务器...")
		}
		global.APP_LOG.Info("开始在远程服务器下载Incus"+imageTypeStr+"镜像",
			zap.String("imageURL", utils.TruncateString(imageURL, 200)),
			zap.String("type", config.InstanceType),
			zap.Bool("useCDN", useCDN))

		imagePath, err := i.downloadImageToRemote(imageURL, originalImageName, i.getCurrentArchitecture(), config.InstanceType, useCDN)
		if err != nil {
			return nil, trySpiritlhlFallback("download", fmt.Errorf("下载%s镜像失败: %w", imageTypeStr, err))
		}

		if progressCallback != nil {
			progressCallback(23, "镜像下载完成，开始校验...")
		}
		global.APP_LOG.Info("Incus"+imageTypeStr+"镜像下载成功",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("type", config.InstanceType))

		plan, err := i.buildImageImportPlan(imagePath, aliasKey, config.InstanceType)
		if err != nil {
			_ = i.removeRemoteFile(imagePath)
			return nil, trySpiritlhlFallback("build-import-plan", err)
		}
		defer plan.cleanup()

		if plan.fingerprint != "" {
			if existingFP := i.findImageFingerprint(plan.fingerprint); existingFP != "" {
				global.APP_LOG.Info("Incus镜像fingerprint已存在，自动修补目标别名",
					zap.String("alias", utils.TruncateString(aliasKey, 100)),
					zap.String("fingerprint", utils.TruncateString(existingFP, 64)))
				if err := i.ensureImageAliasFromFingerprint(aliasKey, existingFP); err != nil {
					return nil, trySpiritlhlFallback("alias-repair-existing-fingerprint", fmt.Errorf("Incus%s镜像已存在但别名修补失败: %w", imageTypeStr, err))
				}
				return nil, nil
			}
		}

		if progressCallback != nil {
			progressCallback(25, "导入镜像到Incus...")
		}
		global.APP_LOG.Info("开始导入Incus"+imageTypeStr+"镜像",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("alias", utils.TruncateString(aliasKey, 100)),
			zap.String("fingerprint", utils.TruncateString(plan.fingerprint, 64)),
			zap.String("type", config.InstanceType))

		importOutput, importErr := i.sshClient.Execute(i.wrapImageImportWithLock(plan.importCmd, plan.fingerprint, aliasKey))
		if importErr != nil {
			errText := importOutput
			if errText == "" {
				errText = importErr.Error()
			} else {
				errText += "\n" + importErr.Error()
			}
			lowerErrText := strings.ToLower(errText)

			if strings.Contains(lowerErrText, "fingerprint already exists") || strings.Contains(lowerErrText, "same fingerprint") {
				fingerprint := plan.fingerprint
				if parsed := extractFingerprintFromOutput(errText); parsed != "" {
					fingerprint = parsed
				}
				if fingerprint != "" {
					global.APP_LOG.Info("Incus镜像指纹重复，自动修补目标别名",
						zap.String("alias", utils.TruncateString(aliasKey, 100)),
						zap.String("fingerprint", utils.TruncateString(fingerprint, 64)))
					if err := i.ensureImageAliasFromFingerprint(aliasKey, fingerprint); err == nil {
						return nil, nil
					} else {
						global.APP_LOG.Warn("Incus镜像指纹重复后的别名修补失败",
							zap.String("alias", utils.TruncateString(aliasKey, 100)),
							zap.String("fingerprint", utils.TruncateString(fingerprint, 64)),
							zap.Error(err))
					}
				}
				if i.imageExists(aliasKey) {
					return nil, nil
				}
			}

			global.APP_LOG.Error("Incus镜像导入命令失败",
				zap.String("alias", utils.TruncateString(aliasKey, 100)),
				zap.String("imagePath", utils.TruncateString(imagePath, 200)),
				zap.String("incusOutput", utils.TruncateString(errText, 1000)),
				zap.Error(importErr))
			_ = i.removeRemoteFile(imagePath)
			return nil, trySpiritlhlFallback("import", fmt.Errorf("Incus%s镜像导入失败: %s (incus output: %s)", imageTypeStr, importErr.Error(), utils.TruncateString(errText, 500)))
		}

		if !i.imageExists(aliasKey) {
			if plan.fingerprint != "" {
				if err := i.ensureImageAliasFromFingerprint(aliasKey, plan.fingerprint); err != nil {
					return nil, trySpiritlhlFallback("post-import-alias-verify", fmt.Errorf("Incus%s镜像导入成功但别名不可用，自动修补失败: %w", imageTypeStr, err))
				}
			} else {
				return nil, trySpiritlhlFallback("post-import-alias-empty-fingerprint", fmt.Errorf("Incus%s镜像导入成功但别名不可用: %s", imageTypeStr, aliasKey))
			}
		}

		global.APP_LOG.Info("Incus"+imageTypeStr+"镜像导入成功",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("alias", utils.TruncateString(aliasKey, 100)),
			zap.String("fingerprint", utils.TruncateString(plan.fingerprint, 64)),
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

// extractFingerprintFromOutput 从 Incus/LXD 错误输出中提取镜像指纹
// 典型输出: "Error: Image with same fingerprint already exists: abc123def456..."
func extractFingerprintFromOutput(output string) string {
	lower := strings.ToLower(output)
	idx := strings.Index(lower, "fingerprint")
	if idx < 0 {
		return ""
	}
	rest := output[idx:]
	for _, prefix := range []string{"sha256:", "exists: "} {
		if pi := strings.Index(strings.ToLower(rest), prefix); pi >= 0 {
			fp := strings.TrimSpace(rest[pi+len(prefix):])
			end := 0
			for end < len(fp) && (('0' <= fp[end] && fp[end] <= '9') ||
				('a' <= fp[end] && fp[end] <= 'f') ||
				('A' <= fp[end] && fp[end] <= 'F')) {
				end++
			}
			if end >= 12 {
				return fp[:end]
			}
		}
	}
	return ""
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

// incusImageImportPlan 记录一次镜像导入所需的命令、指纹和临时目录。
type incusImageImportPlan struct {
	importCmd   string
	fingerprint string
	cleanupDir  string
	cleanupFunc func()
}

func (p *incusImageImportPlan) cleanup() {
	if p != nil && p.cleanupFunc != nil {
		p.cleanupFunc()
	}
}

// buildImageImportPlan 兼容 oneclickvirt/incus、oneclickvirt/lxd 以及标准 Incus split/single 镜像。
// 它会递归查找 zip 内的 metadata/rootfs/disk 文件，避免因文件名为 lxd.tar.xz、嵌套目录等导致导入异常。
func (i *IncusProvider) buildImageImportPlan(imagePath, aliasKey, instanceType string) (*incusImageImportPlan, error) {
	plan := &incusImageImportPlan{}
	workPath := imagePath

	if strings.HasSuffix(strings.ToLower(imagePath), ".zip") {
		extractDir := strings.TrimSuffix(imagePath, ".zip")
		plan.cleanupDir = extractDir
		plan.cleanupFunc = func() {
			_, _ = i.sshClient.Execute(fmt.Sprintf("rm -rf %s", shellSingleQuote(extractDir)))
		}
		_, _ = i.sshClient.Execute(fmt.Sprintf("rm -rf %s && mkdir -p %s", shellSingleQuote(extractDir), shellSingleQuote(extractDir)))
		if _, err := i.sshClient.Execute(fmt.Sprintf("unzip -oq %s -d %s", shellSingleQuote(imagePath), shellSingleQuote(extractDir))); err != nil {
			return nil, fmt.Errorf("解压Incus镜像失败: %w", err)
		}
		workPath = extractDir
	}

	if instanceType == "vm" {
		metadataPath := i.findRemoteImageFile(workPath, "metadata")
		diskPath := i.findRemoteImageFile(workPath, "disk")
		if metadataPath != "" && diskPath != "" {
			plan.fingerprint = i.computeRemoteSplitFingerprint(metadataPath, diskPath)
			plan.importCmd = fmt.Sprintf("incus image import %s %s --alias %s", shellSingleQuote(metadataPath), shellSingleQuote(diskPath), shellSingleQuote(aliasKey))
			return plan, nil
		}

		vmImagePath := i.findRemoteImageFile(workPath, "vm-single")
		if vmImagePath == "" && !strings.HasSuffix(strings.ToLower(imagePath), ".zip") && i.isRemoteFileValid(imagePath) {
			vmImagePath = imagePath
		}
		if vmImagePath == "" {
			return nil, fmt.Errorf("未找到可导入的Incus虚拟机镜像文件")
		}
		plan.fingerprint = i.computeRemoteFileFingerprint(vmImagePath)
		plan.importCmd = fmt.Sprintf("incus image import %s --alias %s --vm", shellSingleQuote(vmImagePath), shellSingleQuote(aliasKey))
		return plan, nil
	}

	metadataPath := i.findRemoteImageFile(workPath, "metadata")
	rootfsPath := i.findRemoteImageFile(workPath, "rootfs")
	if metadataPath != "" && rootfsPath != "" {
		plan.fingerprint = i.computeRemoteSplitFingerprint(metadataPath, rootfsPath)
		plan.importCmd = fmt.Sprintf("incus image import %s %s --alias %s", shellSingleQuote(metadataPath), shellSingleQuote(rootfsPath), shellSingleQuote(aliasKey))
		return plan, nil
	}

	singlePath := i.findRemoteImageFile(workPath, "container-single")
	if singlePath == "" && !strings.HasSuffix(strings.ToLower(imagePath), ".zip") && i.isRemoteFileValid(imagePath) {
		singlePath = imagePath
	}
	if singlePath == "" {
		return nil, fmt.Errorf("未找到可导入的Incus容器镜像文件")
	}
	plan.fingerprint = i.computeRemoteFileFingerprint(singlePath)
	plan.importCmd = fmt.Sprintf("incus image import %s --alias %s", shellSingleQuote(singlePath), shellSingleQuote(aliasKey))
	return plan, nil
}

func (i *IncusProvider) findRemoteImageFile(basePath, kind string) string {
	var cmd string
	switch kind {
	case "metadata":
		cmd = fmt.Sprintf("find %s -type f \\( -name 'incus.tar.xz' -o -name 'lxd.tar.xz' -o -name 'metadata.tar.xz' -o -name 'metadata.tar.gz' -o -name 'metadata.tar' \\) | sort | head -1", shellSingleQuote(basePath))
		if out, err := i.sshClient.Execute(cmd); err == nil && utils.CleanCommandOutput(out) != "" {
			return utils.CleanCommandOutput(out)
		}
		cmd = fmt.Sprintf("find %s -type f \\( -name '*.tar.xz' -o -name '*.tar.gz' -o -name '*.tar' \\) ! -name 'rootfs*' ! -name '*rootfs*' | sort | head -1", shellSingleQuote(basePath))
	case "rootfs":
		cmd = fmt.Sprintf("find %s -type f \\( -name 'rootfs.squashfs' -o -name 'rootfs.tar.xz' -o -name 'rootfs.tar.gz' -o -name 'rootfs.tar.bz2' -o -name 'rootfs.tar' -o -name 'rootfs.ext4' \\) | sort | head -1", shellSingleQuote(basePath))
	case "disk":
		cmd = fmt.Sprintf("find %s -type f \\( -name 'disk.qcow2' -o -name '*.qcow2' -o -name '*.img' -o -name '*.vmdk' \\) | sort | head -1", shellSingleQuote(basePath))
	case "vm-single":
		cmd = fmt.Sprintf("find %s -type f \\( -name '*.qcow2' -o -name '*.img' -o -name '*.vmdk' -o -name '*.tar.xz' -o -name '*.tar.gz' -o -name '*.tar' \\) | sort | head -1", shellSingleQuote(basePath))
	case "container-single":
		cmd = fmt.Sprintf("find %s -type f \\( -name '*.tar.xz' -o -name '*.tar.gz' -o -name '*.tar.bz2' -o -name '*.tar' \\) | sort | head -1", shellSingleQuote(basePath))
	default:
		return ""
	}
	out, err := i.sshClient.Execute(cmd)
	if err != nil {
		return ""
	}
	return utils.CleanCommandOutput(out)
}

func (i *IncusProvider) computeRemoteFileFingerprint(path string) string {
	out, err := i.sshClient.Execute(fmt.Sprintf("sha256sum %s 2>/dev/null | awk '{print $1}'", shellSingleQuote(path)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (i *IncusProvider) computeRemoteSplitFingerprint(metadataPath, dataPath string) string {
	// LXD/Incus split images use the metadata tarball fingerprint as the image fingerprint;
	// the rootfs/disk hash is referenced from metadata, so do not hash metadata+rootfs here.
	_ = dataPath
	return i.computeRemoteFileFingerprint(metadataPath)
}

func (i *IncusProvider) wrapImageImportWithLock(importCmd, fingerprint, alias string) string {
	lockKey := fingerprint
	if lockKey == "" {
		lockKey = alias
	}
	lockFile := "/tmp/oneclickvirt-incus-image-" + sanitizeLockKey(lockKey) + ".lock"
	return fmt.Sprintf("if command -v flock >/dev/null 2>&1; then flock %s -c %s; else %s; fi", shellSingleQuote(lockFile), shellSingleQuote(importCmd), importCmd)
}

// imageExists 检查镜像 alias 是否精确存在。
func (i *IncusProvider) imageExists(alias string) bool {
	return i.getImageFingerprint(alias) != ""
}

// getCurrentArchitecture 从数据库读取最新的 Provider 架构值。
func (i *IncusProvider) getCurrentArchitecture() string {
	var p providerModel.Provider
	if err := global.APP_DB.Select("architecture").Where("id = ?", i.config.ID).First(&p).Error; err == nil && p.Architecture != "" {
		return p.Architecture
	}
	return i.config.Architecture
}

// findImageFingerprint 查找本地是否已有指定 fingerprint 的镜像。
func (i *IncusProvider) findImageFingerprint(fingerprint string) string {
	if strings.TrimSpace(fingerprint) == "" {
		return ""
	}
	cmd := fmt.Sprintf("incus image info %s 2>/dev/null | awk -F': ' '/^Fingerprint:/{print $2; exit}'", shellSingleQuote(fingerprint))
	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

// findImageFingerprintByPrefix 通过别名前缀查找本地镜像，返回 fingerprint 和匹配到的 alias。
func (i *IncusProvider) findImageFingerprintByPrefix(prefix string) (string, string) {
	cmd := fmt.Sprintf("incus image list --format csv -c f,l 2>/dev/null | awk -F, -v p=%s '{for(i=2;i<=NF;i++){gsub(/^ +| +$/, \"\", $i); if(index($i,p)==1){print $1 \"\\t\" $i; exit}}}'", shellSingleQuote(prefix))
	output, err := i.sshClient.Execute(cmd)
	if err != nil || strings.TrimSpace(output) == "" {
		return "", ""
	}
	parts := strings.SplitN(strings.TrimSpace(output), "\t", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// findImageByPrefix 兼容旧调用：返回第一个匹配的别名。
func (i *IncusProvider) findImageByPrefix(prefix string) string {
	_, alias := i.findImageFingerprintByPrefix(prefix)
	return alias
}

// getImageFingerprint 获取 Incus 镜像的指纹。
func (i *IncusProvider) getImageFingerprint(alias string) string {
	if strings.TrimSpace(alias) == "" {
		return ""
	}
	cmd := fmt.Sprintf("incus image info %s 2>/dev/null | awk -F': ' '/^Fingerprint:/{print $2; exit}'", shellSingleQuote(alias))
	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

// ensureImageAliasFromFingerprint 确保目标 alias 精确指向已有 fingerprint。
func (i *IncusProvider) ensureImageAliasFromFingerprint(alias, fingerprint string) error {
	alias = strings.TrimSpace(alias)
	fingerprint = strings.TrimSpace(fingerprint)
	if alias == "" || fingerprint == "" {
		return fmt.Errorf("alias或fingerprint为空")
	}
	if existing := i.getImageFingerprint(alias); existing != "" {
		if strings.HasPrefix(existing, fingerprint) || strings.HasPrefix(fingerprint, existing) || existing == fingerprint {
			return nil
		}
	}
	if existing := i.findImageFingerprint(fingerprint); existing != "" {
		fingerprint = existing
	}
	cmd := fmt.Sprintf("incus image alias delete %s >/dev/null 2>&1 || true; incus image alias create %s %s", shellSingleQuote(alias), shellSingleQuote(alias), shellSingleQuote(fingerprint))
	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		return fmt.Errorf("创建Incus镜像别名失败: %w (output: %s)", err, utils.TruncateString(output, 300))
	}
	if !i.imageExists(alias) {
		return fmt.Errorf("创建Incus镜像别名后验证失败: %s", alias)
	}
	return nil
}

func (i *IncusProvider) spiritlhlLocalAlias(imageName, instanceType string) string {
	base := strings.TrimSpace(imageName)
	if base == "" {
		return ""
	}
	base = strings.TrimPrefix(base, "local:")
	base = strings.TrimPrefix(base, "images:")
	base = strings.TrimPrefix(base, "spiritlhl:")
	base = strings.TrimPrefix(base, "oneclickvirt_")
	if idx := strings.Index(base, "_container_"); idx > 0 {
		base = base[:idx]
	}
	if idx := strings.Index(base, "_vm_"); idx > 0 {
		base = base[:idx]
	}
	base = strings.ReplaceAll(base, "/", "-")
	base = strings.ReplaceAll(base, ":", "-")
	base = sanitizeLockKey(base)
	if base == "" || base == "unknown" {
		return ""
	}
	arch := sanitizeLockKey(i.getCurrentArchitecture())
	return fmt.Sprintf("oneclickvirt_%s_%s_%s-spiritlhl", base, instanceType, arch)
}

func (i *IncusProvider) ensureSpiritlhlRemote() error {
	checkCmd := "incus remote list --format csv 2>/dev/null | awk -F, '$1==\"spiritlhl\"{found=1} END{exit !found}'"
	if _, err := i.sshClient.Execute(checkCmd); err == nil {
		return nil
	}
	cmd := "incus remote add spiritlhl https://incusimages.spiritlhl.net --protocol simplestreams --public"
	output, err := i.sshClient.Execute(cmd)
	if err == nil {
		return nil
	}
	// 如果名称存在但配置损坏或 URL 不对，重建一次 remote。
	rebuildCmd := "incus remote remove spiritlhl >/dev/null 2>&1 || true; incus remote add spiritlhl https://incusimages.spiritlhl.net --protocol simplestreams --public"
	output, err = i.sshClient.Execute(rebuildCmd)
	if err != nil {
		return fmt.Errorf("添加spiritlhl Incus远程镜像源失败: %w (output: %s)", err, utils.TruncateString(output, 300))
	}
	return nil
}

func (i *IncusProvider) copySpiritlhlImageToLocal(imageName, targetAlias, instanceType string) error {
	if strings.TrimSpace(targetAlias) == "" {
		return fmt.Errorf("目标镜像alias为空")
	}
	if i.imageExists(targetAlias) {
		return nil
	}
	candidates := buildSpiritlhlImageCandidates(imageName)
	if len(candidates) == 0 {
		return fmt.Errorf("无法从镜像名生成spiritlhl候选路径: %s", imageName)
	}
	if err := i.ensureSpiritlhlRemote(); err != nil {
		return err
	}
	if err := i.copySpiritlhlImageCandidates(targetAlias, instanceType, candidates); err == nil {
		return nil
	}
	// remote 可能存在但指向旧地址或协议不对，重建一次再重试。
	_, _ = i.sshClient.Execute("incus remote remove spiritlhl >/dev/null 2>&1 || true; incus remote add spiritlhl https://incusimages.spiritlhl.net --protocol simplestreams --public")
	return i.copySpiritlhlImageCandidates(targetAlias, instanceType, candidates)
}

func (i *IncusProvider) copySpiritlhlImageCandidates(targetAlias, instanceType string, candidates []string) error {
	var lastErr error
	for _, candidate := range candidates {
		source := "spiritlhl:" + candidate
		vmFlag := ""
		if instanceType == "vm" {
			vmFlag = " --vm"
		}
		cmd := fmt.Sprintf("incus image alias delete %s >/dev/null 2>&1 || true; incus image copy %s local: --alias %s%s --auto-update=false", shellSingleQuote(targetAlias), shellSingleQuote(source), shellSingleQuote(targetAlias), vmFlag)
		output, err := i.sshClient.ExecuteWithTimeout(cmd, 1*time.Hour)
		if err == nil && i.imageExists(targetAlias) {
			global.APP_LOG.Info("已复制spiritlhl Incus镜像到本地", zap.String("source", source), zap.String("alias", utils.TruncateString(targetAlias, 100)), zap.String("type", instanceType))
			return nil
		}
		if i.imageExists(targetAlias) {
			return nil
		}
		errText := output
		if err != nil {
			errText += "\n" + err.Error()
		}
		if strings.Contains(strings.ToLower(errText), "fingerprint") {
			if fp := extractFingerprintFromOutput(errText); fp != "" {
				if aliasErr := i.ensureImageAliasFromFingerprint(targetAlias, fp); aliasErr == nil {
					return nil
				}
			}
		}
		lastErr = fmt.Errorf("复制候选镜像 %s 失败: %v (output: %s)", source, err, utils.TruncateString(output, 300))
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的spiritlhl候选镜像")
	}
	return lastErr
}

func buildSpiritlhlImageCandidates(imageName string) []string {
	osName, version, variant := parseSpiritlhlImageName(imageName)
	if osName == "" {
		return nil
	}
	variants := uniqueNonEmpty([]string{variant, "cloud", "default"})
	var out []string
	if version != "" {
		for _, v := range variants {
			out = append(out, fmt.Sprintf("%s/%s/%s", osName, version, v))
		}
	}
	if osName == "archlinux" || osName == "arch" {
		out = append(out, "archlinux/cloud")
	}
	return uniqueNonEmpty(out)
}

func parseSpiritlhlImageName(imageName string) (string, string, string) {
	s := strings.ToLower(strings.TrimSpace(imageName))
	for _, p := range []string{"local:", "images:", "spiritlhl:", "oneclickvirt_", "spiritlhl_"} {
		s = strings.TrimPrefix(s, p)
	}
	if idx := strings.Index(s, "_container_"); idx > 0 {
		s = s[:idx]
	}
	if idx := strings.Index(s, "_vm_"); idx > 0 {
		s = s[:idx]
	}
	tokens := strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.')
	})
	if len(tokens) == 0 {
		return "", "", ""
	}
	knownOS := map[string]bool{
		"almalinux": true, "alpine": true, "archlinux": true, "arch": true, "centos": true,
		"debian": true, "fedora": true, "gentoo": true, "kali": true, "openeuler": true,
		"opensuse": true, "oracle": true, "oraclelinux": true, "rockylinux": true, "ubuntu": true, "openwrt": true,
	}
	variants := map[string]bool{"cloud": true, "default": true, "openrc": true, "systemd": true}
	archTokens := map[string]bool{"amd64": true, "x86": true, "x86_64": true, "arm64": true, "aarch64": true, "container": true, "vm": true, "kvm": true}
	osName := ""
	osIdx := -1
	for idx, t := range tokens {
		if knownOS[t] {
			osName = t
			osIdx = idx
			break
		}
		for known := range knownOS {
			if strings.HasPrefix(t, known) && len(t) > len(known) {
				osName = known
				osIdx = idx
				tokens = append(tokens[:idx+1], append([]string{strings.TrimPrefix(t, known)}, tokens[idx+1:]...)...)
				break
			}
		}
		if osName != "" {
			break
		}
	}
	if osName == "arch" {
		osName = "archlinux"
	}
	if osName == "oraclelinux" {
		osName = "oracle"
	}
	if osName == "" {
		return "", "", ""
	}
	variant := "cloud"
	for _, t := range tokens {
		if variants[t] {
			variant = t
			break
		}
	}
	version := ""
	for _, t := range tokens[osIdx+1:] {
		if t == "" || archTokens[t] || variants[t] || strings.HasPrefix(t, "sha256") {
			continue
		}
		version = t
		break
	}
	if version == "" {
		switch osName {
		case "archlinux", "gentoo":
			version = "current"
		case "kali":
			version = "latest"
		}
	}
	return osName, version, variant
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func sanitizeLockKey(s string) string {
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 96 {
		out = out[:96]
	}
	return out
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
		"curl -4 -fL -C - --connect-timeout 30 --max-time 360 --retry 5 --retry-delay 10 --retry-max-time 0 -o %s %s",
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

// shouldCleanupCachedImageOnCreateFailure 判断创建失败是否真的由镜像缓存损坏/不兼容引起。
// 存储池不存在、网络配置失败、权限失败等不应清理镜像，否则会把健康镜像 alias 删除。
func (i *IncusProvider) shouldCleanupCachedImageOnCreateFailure(output string, err error) bool {
	msg := strings.ToLower(output)
	if err != nil {
		msg += "\n" + strings.ToLower(err.Error())
	}
	if msg == "" {
		return false
	}
	imageIndicators := []string{
		"failed to find image",
		"image not found",
		"no such image",
		"requested image",
		"not a valid image",
		"invalid image",
		"image architecture",
		"architecture mismatch",
		"unsupported image",
	}
	for _, indicator := range imageIndicators {
		if strings.Contains(msg, indicator) {
			return true
		}
	}
	return false
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
