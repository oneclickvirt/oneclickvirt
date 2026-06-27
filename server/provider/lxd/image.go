package lxd

import (
	"context"
	"crypto/sha256"
	"fmt"
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
func (l *LXDProvider) handleImageDownloadAndImport(ctx context.Context, config *provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
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

	// 本地镜像优先：如果用户/数据库传入的本来就是本地 alias/fingerprint，先直接使用。
	// 这可以避免有可用本地缓存时仍然去下载或走远程源。
	if config.ImageURL == "" && l.imageExists(originalImageName) {
		config.Image = originalImageName
		global.APP_LOG.Info("LXD"+imageTypeStr+"镜像已在本地存在，优先使用本地镜像",
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
		// 始终使用数据库中的最新架构值（可能已被 detectAndUpdateArchitecture 自动纠正），
		// 避免使用 l.config.Architecture 中的缓存旧值导致 ARM 节点仍生成 amd64 别名
		architecture := l.getCurrentArchitecture()
		config.Image = imageNameWithPrefix + "_" + config.InstanceType + "_" + l.generateImageAlias(config.ImageURL, originalImageName, architecture)[len(originalImageName)+1:]

		// 快速路径：精确别名已存在
		if l.imageExists(config.Image) {
			global.APP_LOG.Debug("LXD"+imageTypeStr+"镜像已存在，跳过导入",
				zap.String("alias", utils.TruncateString(config.Image, 100)),
				zap.String("type", config.InstanceType))
			if progressCallback != nil {
				progressCallback(28, "镜像已缓存，跳过下载")
			}
			return nil
		}

		// 前缀匹配只能作为优化，不能直接返回成功。
		// 只有成功把目标 alias 绑定到已有 fingerprint，并再次验证 alias 可用后，才跳过下载。
		if fp, existingAlias := l.findImageFingerprintByPrefix(imageNameWithPrefix + "_" + config.InstanceType + "_"); fp != "" {
			global.APP_LOG.Info("LXD"+imageTypeStr+"镜像已通过前缀匹配找到，尝试修补目标别名",
				zap.String("existingAlias", utils.TruncateString(existingAlias, 100)),
				zap.String("targetAlias", utils.TruncateString(config.Image, 100)),
				zap.String("fingerprint", utils.TruncateString(fp, 64)))
			if err := l.ensureImageAliasFromFingerprint(config.Image, fp); err == nil {
				if progressCallback != nil {
					progressCallback(28, "镜像别名已自动修补，跳过下载")
				}
				return nil
			} else {
				global.APP_LOG.Warn("LXD镜像前缀匹配别名修补失败，将继续重新下载导入",
					zap.String("targetAlias", utils.TruncateString(config.Image, 100)),
					zap.String("fingerprint", utils.TruncateString(fp, 64)),
					zap.Error(err))
			}
		}
		if fp, existingAlias := l.findImageFingerprintByURLAliasSuffix(config.Image, config.InstanceType); fp != "" {
			global.APP_LOG.Info("LXD"+imageTypeStr+"镜像已通过URL别名后缀匹配找到，尝试修补目标别名",
				zap.String("existingAlias", utils.TruncateString(existingAlias, 100)),
				zap.String("targetAlias", utils.TruncateString(config.Image, 100)),
				zap.String("fingerprint", utils.TruncateString(fp, 64)))
			if err := l.ensureImageAliasFromFingerprint(config.Image, fp); err == nil {
				if progressCallback != nil {
					progressCallback(28, "镜像别名已按URL缓存自动修补，跳过下载")
				}
				return nil
			} else {
				global.APP_LOG.Warn("LXD镜像URL别名后缀匹配修补失败，将继续重新下载导入",
					zap.String("targetAlias", utils.TruncateString(config.Image, 100)),
					zap.String("fingerprint", utils.TruncateString(fp, 64)),
					zap.Error(err))
			}
		}

		if progressCallback != nil {
			progressCallback(17, "开始下载镜像...")
		}
		return l.downloadAndImportImage(ctx, config, originalImageName, imageTypeStr, progressCallback)
	}

	// 无 URL（数据库无匹配的系统镜像）：先尝试 spiritlhl simplestreams 镜像源，
	// 成功后复制到本地 alias，再使用本地镜像创建实例；失败再回退到官方 images:/ubuntu: 远程。
	if fallbackAlias := l.spiritlhlLocalAlias(originalImageName, config.InstanceType); fallbackAlias != "" {
		if err := l.copySpiritlhlImageToLocal(originalImageName, fallbackAlias, config.InstanceType); err == nil {
			config.Image = fallbackAlias
			global.APP_LOG.Info("已从spiritlhl LXD镜像源复制镜像到本地",
				zap.String("original", originalImageName),
				zap.String("alias", utils.TruncateString(config.Image, 100)),
				zap.String("type", config.InstanceType))
			if progressCallback != nil {
				progressCallback(28, "已使用spiritlhl镜像源缓存到本地")
			}
			return nil
		} else {
			global.APP_LOG.Warn("spiritlhl LXD镜像源回退失败，将继续尝试官方远程镜像",
				zap.String("image", originalImageName),
				zap.String("targetAlias", fallbackAlias),
				zap.Error(err))
		}
	}

	// 兜底：尝试解析为 LXD 官方远程镜像引用。
	// Docker 风格名称 "debian:12" → LXD 远程 "images:debian/12/cloud"
	// 如果已经是 LXD 远程格式（含 / 或 :），直接使用。
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

// downloadAndImportImage 下载镜像并导入到 LXD。
// progressCallback 用于报告下载/导入进度；可为 nil。
func (l *LXDProvider) downloadAndImportImage(ctx context.Context, config *provider.InstanceConfig, originalImageName, imageTypeStr string, progressCallback provider.ProgressCallback) error {
	// 使用 singleflight 确保同一别名只有一个协程执行下载+导入；
	// 同时使用远端 flock 防止多个 oneclickvirt 进程/容器同时导入同一 fingerprint。
	aliasKey := config.Image
	imageURL := config.ImageURL
	useCDN := config.UseCDN
	trySpiritlhlFallback := func(stage string, cause error) error {
		if err := l.copySpiritlhlImageToLocal(originalImageName, aliasKey, config.InstanceType); err == nil {
			global.APP_LOG.Info("LXD镜像下载/导入异常后已回退到spiritlhl本地缓存",
				zap.String("stage", stage),
				zap.String("alias", utils.TruncateString(aliasKey, 100)),
				zap.String("type", config.InstanceType))
			return nil
		} else {
			global.APP_LOG.Warn("LXD镜像spiritlhl回退失败",
				zap.String("stage", stage),
				zap.String("alias", utils.TruncateString(aliasKey, 100)),
				zap.Error(err))
		}
		return cause
	}
	_, err, _ := l.imageImportGroup.Do(aliasKey, func() (interface{}, error) {
		// 等待期间镜像可能已由其他协程导入完毕，再次检查
		if l.imageExists(aliasKey) {
			global.APP_LOG.Debug("LXD"+imageTypeStr+"镜像已由并发协程完成导入，跳过",
				zap.String("alias", utils.TruncateString(aliasKey, 100)))
			return nil, nil
		}

		if progressCallback != nil {
			progressCallback(19, "下载镜像到远程服务器...")
		}
		global.APP_LOG.Info("开始在远程服务器下载LXD"+imageTypeStr+"镜像",
			zap.String("imageURL", utils.TruncateString(imageURL, 200)),
			zap.String("type", config.InstanceType),
			zap.Bool("useCDN", useCDN))

		imagePath, err := l.downloadImageToRemote(imageURL, originalImageName, l.config.Country, l.getCurrentArchitecture(), config.InstanceType, useCDN)
		if err != nil {
			return nil, trySpiritlhlFallback("download", fmt.Errorf("下载%s镜像失败: %w", imageTypeStr, err))
		}

		if progressCallback != nil {
			progressCallback(23, "镜像下载完成，开始校验...")
		}
		global.APP_LOG.Info("LXD"+imageTypeStr+"镜像下载成功",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("type", config.InstanceType))

		plan, err := l.buildImageImportPlan(imagePath, aliasKey, config.InstanceType)
		if err != nil {
			_ = l.removeRemoteFile(imagePath)
			return nil, trySpiritlhlFallback("build-import-plan", err)
		}
		defer plan.cleanup()

		// 如果本地已经存在同 fingerprint 镜像，只需要修补目标 alias，不要重复 import。
		if plan.fingerprint != "" {
			if existingFP := l.findImageFingerprint(plan.fingerprint); existingFP != "" {
				global.APP_LOG.Info("LXD镜像fingerprint已存在，自动修补目标别名",
					zap.String("alias", utils.TruncateString(aliasKey, 100)),
					zap.String("fingerprint", utils.TruncateString(existingFP, 64)))
				if err := l.ensureImageAliasFromFingerprint(aliasKey, existingFP); err != nil {
					return nil, trySpiritlhlFallback("alias-repair-existing-fingerprint", fmt.Errorf("LXD%s镜像已存在但别名修补失败: %w", imageTypeStr, err))
				}
				return nil, nil
			}
		}

		if progressCallback != nil {
			progressCallback(25, "导入镜像到LXD...")
		}
		global.APP_LOG.Info("开始导入LXD"+imageTypeStr+"镜像",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("alias", utils.TruncateString(aliasKey, 100)),
			zap.String("fingerprint", utils.TruncateString(plan.fingerprint, 64)),
			zap.String("type", config.InstanceType))

		importOutput, importErr := l.sshClient.Execute(l.wrapImageImportWithLock(plan.importCmd, plan.fingerprint, aliasKey))
		if importErr != nil {
			errText := importOutput
			if errText == "" {
				errText = importErr.Error()
			} else {
				errText += "\n" + importErr.Error()
			}
			lowerErrText := strings.ToLower(errText)

			// 同内容镜像已存在：优先使用本地计算的 fingerprint；输出中带 fingerprint 时也兼容解析。
			if strings.Contains(lowerErrText, "fingerprint already exists") || strings.Contains(lowerErrText, "same fingerprint") {
				fingerprint := plan.fingerprint
				if parsed := extractFingerprintFromOutput(errText); parsed != "" {
					fingerprint = parsed
				}
				if fingerprint != "" {
					global.APP_LOG.Info("LXD镜像指纹重复，自动修补目标别名",
						zap.String("alias", utils.TruncateString(aliasKey, 100)),
						zap.String("fingerprint", utils.TruncateString(fingerprint, 64)))
					if err := l.ensureImageAliasFromFingerprint(aliasKey, fingerprint); err == nil {
						return nil, nil
					} else {
						global.APP_LOG.Warn("LXD镜像指纹重复后的别名修补失败",
							zap.String("alias", utils.TruncateString(aliasKey, 100)),
							zap.String("fingerprint", utils.TruncateString(fingerprint, 64)),
							zap.Error(err))
					}
				}
				if l.imageExists(aliasKey) {
					return nil, nil
				}
				if fp, existingAlias := l.findImageFingerprintByURLAliasSuffix(aliasKey, config.InstanceType); fp != "" {
					global.APP_LOG.Info("LXD镜像指纹重复后按URL别名后缀找到已有镜像，自动修补目标别名",
						zap.String("existingAlias", utils.TruncateString(existingAlias, 100)),
						zap.String("targetAlias", utils.TruncateString(aliasKey, 100)),
						zap.String("fingerprint", utils.TruncateString(fp, 64)))
					if err := l.ensureImageAliasFromFingerprint(aliasKey, fp); err == nil {
						return nil, nil
					} else {
						global.APP_LOG.Warn("LXD镜像URL别名后缀匹配后的别名修补失败",
							zap.String("alias", utils.TruncateString(aliasKey, 100)),
							zap.String("fingerprint", utils.TruncateString(fp, 64)),
							zap.Error(err))
					}
				}
			}

			global.APP_LOG.Error("LXD镜像导入命令失败",
				zap.String("alias", utils.TruncateString(aliasKey, 100)),
				zap.String("imagePath", utils.TruncateString(imagePath, 200)),
				zap.String("lxcOutput", utils.TruncateString(errText, 1000)),
				zap.Error(importErr))
			_ = l.removeRemoteFile(imagePath)
			return nil, trySpiritlhlFallback("import", fmt.Errorf("LXD%s镜像导入失败: %s (lxc output: %s)", imageTypeStr, importErr.Error(), utils.TruncateString(errText, 500)))
		}

		// import 成功后仍必须验证 alias。部分异常环境会 import 成功但 alias 未创建，
		// 如果 fingerprint 已知则自动补 alias；否则明确失败，避免拖到 lxc init 才报找不到镜像。
		if !l.imageExists(aliasKey) {
			if plan.fingerprint != "" {
				if err := l.ensureImageAliasFromFingerprint(aliasKey, plan.fingerprint); err != nil {
					return nil, trySpiritlhlFallback("post-import-alias-verify", fmt.Errorf("LXD%s镜像导入成功但别名不可用，自动修补失败: %w", imageTypeStr, err))
				}
			} else {
				return nil, trySpiritlhlFallback("post-import-alias-empty-fingerprint", fmt.Errorf("LXD%s镜像导入成功但别名不可用: %s", imageTypeStr, aliasKey))
			}
		}

		global.APP_LOG.Info("LXD"+imageTypeStr+"镜像导入成功",
			zap.String("imagePath", utils.TruncateString(imagePath, 200)),
			zap.String("alias", utils.TruncateString(aliasKey, 100)),
			zap.String("fingerprint", utils.TruncateString(plan.fingerprint, 64)),
			zap.String("type", config.InstanceType))

		// 导入成功后删除远程镜像 zip 文件
		if err := l.cleanupRemoteImage(originalImageName, imageURL, l.getCurrentArchitecture(), config.InstanceType); err != nil {
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

// extractFingerprintFromOutput 从 LXD/Incus 错误输出中提取镜像指纹
// 典型输出: "Error: Image with same fingerprint already exists: abc123def456..."
func extractFingerprintFromOutput(output string) string {
	lower := strings.ToLower(output)
	idx := strings.Index(lower, "fingerprint")
	if idx < 0 {
		return ""
	}
	rest := output[idx:]
	// 查找 sha256 指纹（64 位十六进制字符串）
	for _, prefix := range []string{"sha256:", "exists: "} {
		if pi := strings.Index(strings.ToLower(rest), prefix); pi >= 0 {
			fp := strings.TrimSpace(rest[pi+len(prefix):])
			// 提取连续的十六进制字符
			end := 0
			for end < len(fp) && (('0' <= fp[end] && fp[end] <= '9') ||
				('a' <= fp[end] && fp[end] <= 'f') ||
				('A' <= fp[end] && fp[end] <= 'F')) {
				end++
			}
			if end >= 12 { // 至少 12 位才算有效
				return fp[:end]
			}
		}
	}
	return ""
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

// queryAndSetSystemImage 从数据库查询匹配的系统镜像记录并设置到配置中。
// 如果 config.ImageURL 已由上层设置（例如用户选择了特定系统镜像），则跳过查询，
// 避免模糊匹配返回不同的镜像导致 URL/别名不一致。
func (l *LXDProvider) queryAndSetSystemImage(ctx context.Context, config *provider.InstanceConfig) error {
	// 若上层已设置 ImageURL，直接信任该值，避免二次查询覆盖
	if config.ImageURL != "" {
		global.APP_LOG.Debug("ImageURL已由上层设置，跳过queryAndSetSystemImage",
			zap.String("image", config.Image),
			zap.String("imageURL", utils.TruncateString(config.ImageURL, 100)))
		return nil
	}

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
		// 使用数据库最新架构值（可能已被 auto-detect 纠正）
		query = query.Where("architecture = ?", l.getCurrentArchitecture())
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

// getCurrentArchitecture 从数据库读取最新的 Provider 架构值。
// 用于镜像别名生成，确保自动架构检测纠正后立即生效，避免使用 l.config.Architecture 中的缓存旧值。
func (l *LXDProvider) getCurrentArchitecture() string {
	var p providerModel.Provider
	if err := global.APP_DB.Select("architecture").Where("id = ?", l.config.ID).First(&p).Error; err == nil && p.Architecture != "" {
		return p.Architecture
	}
	return l.config.Architecture
}

// lxdImageImportPlan 记录一次镜像导入所需的命令、指纹和临时目录。
type lxdImageImportPlan struct {
	importCmd   string
	fingerprint string
	cleanupDir  string
	cleanupFunc func()
}

func (p *lxdImageImportPlan) cleanup() {
	if p != nil && p.cleanupFunc != nil {
		p.cleanupFunc()
	}
}

// buildImageImportPlan 兼容 oneclickvirt/lxd、oneclickvirt/incus 以及标准 LXD split/single 镜像。
// 它会递归查找 zip 内的 metadata/rootfs/disk 文件，避免因文件名为 incus.tar.xz、嵌套目录等导致导入异常。
func (l *LXDProvider) buildImageImportPlan(imagePath, aliasKey, instanceType string) (*lxdImageImportPlan, error) {
	plan := &lxdImageImportPlan{}
	workPath := imagePath

	if strings.HasSuffix(strings.ToLower(imagePath), ".zip") {
		extractDir := strings.TrimSuffix(imagePath, ".zip")
		plan.cleanupDir = extractDir
		plan.cleanupFunc = func() {
			_, _ = l.sshClient.Execute(fmt.Sprintf("rm -rf %s", shellSingleQuote(extractDir)))
		}
		// 先清理旧解压目录，避免上次失败残留文件参与本次导入。
		_, _ = l.sshClient.Execute(fmt.Sprintf("rm -rf %s && mkdir -p %s", shellSingleQuote(extractDir), shellSingleQuote(extractDir)))
		if _, err := l.sshClient.Execute(fmt.Sprintf("unzip -oq %s -d %s", shellSingleQuote(imagePath), shellSingleQuote(extractDir))); err != nil {
			return nil, fmt.Errorf("解压LXD镜像失败: %w", err)
		}
		workPath = extractDir
	}

	if instanceType == "vm" {
		metadataPath := l.findRemoteImageFile(workPath, "metadata")
		diskPath := l.findRemoteImageFile(workPath, "disk")
		if metadataPath != "" && diskPath != "" {
			plan.fingerprint = l.computeRemoteSplitFingerprint(metadataPath, diskPath)
			plan.importCmd = fmt.Sprintf("lxc image import %s %s --alias %s --vm", shellSingleQuote(metadataPath), shellSingleQuote(diskPath), shellSingleQuote(aliasKey))
			return plan, nil
		}

		vmImagePath := l.findRemoteImageFile(workPath, "vm-single")
		if vmImagePath == "" && !strings.HasSuffix(strings.ToLower(imagePath), ".zip") && l.isRemoteFileValid(imagePath) {
			vmImagePath = imagePath
		}
		if vmImagePath == "" {
			return nil, fmt.Errorf("未找到可导入的LXD虚拟机镜像文件")
		}
		plan.fingerprint = l.computeRemoteFileFingerprint(vmImagePath)
		plan.importCmd = fmt.Sprintf("lxc image import %s --alias %s --vm", shellSingleQuote(vmImagePath), shellSingleQuote(aliasKey))
		return plan, nil
	}

	metadataPath := l.findRemoteImageFile(workPath, "metadata")
	rootfsPath := l.findRemoteImageFile(workPath, "rootfs")
	if metadataPath != "" && rootfsPath != "" {
		plan.fingerprint = l.computeRemoteSplitFingerprint(metadataPath, rootfsPath)
		plan.importCmd = fmt.Sprintf("lxc image import %s %s --alias %s", shellSingleQuote(metadataPath), shellSingleQuote(rootfsPath), shellSingleQuote(aliasKey))
		return plan, nil
	}

	singlePath := l.findRemoteImageFile(workPath, "container-single")
	if singlePath == "" && !strings.HasSuffix(strings.ToLower(imagePath), ".zip") && l.isRemoteFileValid(imagePath) {
		singlePath = imagePath
	}
	if singlePath == "" {
		return nil, fmt.Errorf("未找到可导入的LXD容器镜像文件")
	}
	plan.fingerprint = l.computeRemoteFileFingerprint(singlePath)
	plan.importCmd = fmt.Sprintf("lxc image import %s --alias %s", shellSingleQuote(singlePath), shellSingleQuote(aliasKey))
	return plan, nil
}

// findRemoteImageFile 在远端递归查找镜像组成文件。
func (l *LXDProvider) findRemoteImageFile(basePath, kind string) string {
	var cmd string
	switch kind {
	case "metadata":
		cmd = fmt.Sprintf("find %s -type f \\( -name 'lxd.tar.xz' -o -name 'incus.tar.xz' -o -name 'metadata.tar.xz' -o -name 'metadata.tar.gz' -o -name 'metadata.tar' \\) | sort | head -1", shellSingleQuote(basePath))
		if out, err := l.sshClient.Execute(cmd); err == nil && utils.CleanCommandOutput(out) != "" {
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
	out, err := l.sshClient.Execute(cmd)
	if err != nil {
		return ""
	}
	return utils.CleanCommandOutput(out)
}

func (l *LXDProvider) computeRemoteFileFingerprint(path string) string {
	out, err := l.sshClient.Execute(fmt.Sprintf("sha256sum %s 2>/dev/null | awk '{print $1}'", shellSingleQuote(path)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (l *LXDProvider) computeRemoteSplitFingerprint(metadataPath, dataPath string) string {
	// LXD/Incus split images use the metadata tarball fingerprint as the image fingerprint;
	// the rootfs/disk hash is referenced from metadata, so do not hash metadata+rootfs here.
	_ = dataPath
	return l.computeRemoteFileFingerprint(metadataPath)
}

func (l *LXDProvider) wrapImageImportWithLock(importCmd, fingerprint, alias string) string {
	lockKey := fingerprint
	if lockKey == "" {
		lockKey = alias
	}
	lockFile := "/tmp/oneclickvirt-lxd-image-" + sanitizeLockKey(lockKey) + ".lock"
	return fmt.Sprintf("if command -v flock >/dev/null 2>&1; then flock %s -c %s; else %s; fi", shellSingleQuote(lockFile), shellSingleQuote(importCmd), importCmd)
}

// imageExists 检查镜像 alias 是否精确存在。
func (l *LXDProvider) imageExists(alias string) bool {
	return l.getImageFingerprint(alias) != ""
}

// findImageFingerprint 查找本地是否已有指定 fingerprint 的镜像。
func (l *LXDProvider) findImageFingerprint(fingerprint string) string {
	if strings.TrimSpace(fingerprint) == "" {
		return ""
	}
	cmd := fmt.Sprintf("lxc image info %s 2>/dev/null | awk -F': ' '/^Fingerprint:/{print $2; exit}'", shellSingleQuote(fingerprint))
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

// findImageFingerprintByPrefix 通过别名前缀查找本地镜像，返回 fingerprint 和匹配到的 alias。
func (l *LXDProvider) findImageFingerprintByPrefix(prefix string) (string, string) {
	cmd := fmt.Sprintf("lxc image list --format csv -c f,l 2>/dev/null | awk -F, -v p=%s '{for(i=2;i<=NF;i++){gsub(/^ +| +$/, \"\", $i); if(index($i,p)==1){print $1 \"\\t\" $i; exit}}}'", shellSingleQuote(prefix))
	output, err := l.sshClient.Execute(cmd)
	if err != nil || strings.TrimSpace(output) == "" {
		return "", ""
	}
	parts := strings.SplitN(strings.TrimSpace(output), "\t", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func imageURLAliasSuffix(alias, instanceType string) string {
	alias = strings.TrimSpace(alias)
	instanceType = strings.ToLower(strings.TrimSpace(instanceType))
	if alias == "" || instanceType == "" {
		return ""
	}
	marker := "_" + instanceType + "_"
	idx := strings.LastIndex(strings.ToLower(alias), marker)
	if idx < 0 {
		return ""
	}
	suffix := alias[idx:]
	if !hasShortHexHashSuffix(suffix) {
		return ""
	}
	return suffix
}

func hasShortHexHashSuffix(value string) bool {
	dash := strings.LastIndex(value, "-")
	if dash < 0 || len(value)-dash-1 != 8 {
		return false
	}
	for _, ch := range value[dash+1:] {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

// findImageFingerprintByURLAliasSuffix matches aliases generated from the same
// image URL even when the human-readable image name differs.
func (l *LXDProvider) findImageFingerprintByURLAliasSuffix(alias, instanceType string) (string, string) {
	suffix := imageURLAliasSuffix(alias, instanceType)
	if suffix == "" {
		return "", ""
	}
	cmd := fmt.Sprintf("lxc image list --format csv -c f,l 2>/dev/null | awk -F, -v s=%s '{for(i=2;i<=NF;i++){gsub(/^ +| +$/, \"\", $i); if(length($i)>=length(s) && substr($i,length($i)-length(s)+1)==s){print $1 \"\\t\" $i; exit}}}'", shellSingleQuote(suffix))
	output, err := l.sshClient.Execute(cmd)
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
func (l *LXDProvider) findImageByPrefix(prefix string) string {
	_, alias := l.findImageFingerprintByPrefix(prefix)
	return alias
}

// getImageFingerprint 获取 LXD 镜像的指纹（用于创建别名引用）。
func (l *LXDProvider) getImageFingerprint(alias string) string {
	if strings.TrimSpace(alias) == "" {
		return ""
	}
	cmd := fmt.Sprintf("lxc image info %s 2>/dev/null | awk -F': ' '/^Fingerprint:/{print $2; exit}'", shellSingleQuote(alias))
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

// ensureImageAliasFromFingerprint 确保目标 alias 精确指向已有 fingerprint。
func (l *LXDProvider) ensureImageAliasFromFingerprint(alias, fingerprint string) error {
	alias = strings.TrimSpace(alias)
	fingerprint = strings.TrimSpace(fingerprint)
	if alias == "" || fingerprint == "" {
		return fmt.Errorf("alias或fingerprint为空")
	}
	if existing := l.getImageFingerprint(alias); existing != "" {
		if strings.HasPrefix(existing, fingerprint) || strings.HasPrefix(fingerprint, existing) || existing == fingerprint {
			return nil
		}
	}
	if existing := l.findImageFingerprint(fingerprint); existing != "" {
		fingerprint = existing
	}
	cmd := fmt.Sprintf("lxc image alias delete %s >/dev/null 2>&1 || true; lxc image alias create %s %s", shellSingleQuote(alias), shellSingleQuote(alias), shellSingleQuote(fingerprint))
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return fmt.Errorf("创建LXD镜像别名失败: %w (output: %s)", err, utils.TruncateString(output, 300))
	}
	if !l.imageExists(alias) {
		return fmt.Errorf("创建LXD镜像别名后验证失败: %s", alias)
	}
	return nil
}

func (l *LXDProvider) spiritlhlLocalAlias(imageName, instanceType string) string {
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
	arch := sanitizeLockKey(l.getCurrentArchitecture())
	return fmt.Sprintf("oneclickvirt_%s_%s_%s-spiritlhl", base, instanceType, arch)
}

func (l *LXDProvider) ensureSpiritlhlRemote() error {
	checkCmd := "lxc remote list --format csv 2>/dev/null | awk -F, '$1==\"spiritlhl\"{found=1} END{exit !found}'"
	if _, err := l.sshClient.Execute(checkCmd); err == nil {
		return nil
	}
	cmd := "lxc remote add spiritlhl https://lxdimages.spiritlhl.net --protocol simplestreams --public"
	output, err := l.sshClient.Execute(cmd)
	if err == nil {
		return nil
	}
	// 如果名称存在但配置损坏或 URL 不对，重建一次 remote。
	rebuildCmd := "lxc remote remove spiritlhl >/dev/null 2>&1 || true; lxc remote add spiritlhl https://lxdimages.spiritlhl.net --protocol simplestreams --public"
	output, err = l.sshClient.Execute(rebuildCmd)
	if err != nil {
		return fmt.Errorf("添加spiritlhl LXD远程镜像源失败: %w (output: %s)", err, utils.TruncateString(output, 300))
	}
	return nil
}

func (l *LXDProvider) copySpiritlhlImageToLocal(imageName, targetAlias, instanceType string) error {
	if strings.TrimSpace(targetAlias) == "" {
		return fmt.Errorf("目标镜像alias为空")
	}
	if l.imageExists(targetAlias) {
		return nil
	}
	candidates := buildSpiritlhlImageCandidates(imageName)
	if len(candidates) == 0 {
		return fmt.Errorf("无法从镜像名生成spiritlhl候选路径: %s", imageName)
	}
	if err := l.ensureSpiritlhlRemote(); err != nil {
		return err
	}
	if err := l.copySpiritlhlImageCandidates(targetAlias, instanceType, candidates); err == nil {
		return nil
	}
	// remote 可能存在但指向旧地址或协议不对，重建一次再重试。
	_, _ = l.sshClient.Execute("lxc remote remove spiritlhl >/dev/null 2>&1 || true; lxc remote add spiritlhl https://lxdimages.spiritlhl.net --protocol simplestreams --public")
	return l.copySpiritlhlImageCandidates(targetAlias, instanceType, candidates)
}

func (l *LXDProvider) copySpiritlhlImageCandidates(targetAlias, instanceType string, candidates []string) error {
	var lastErr error
	for _, candidate := range candidates {
		source := "spiritlhl:" + candidate
		vmFlag := ""
		if instanceType == "vm" {
			vmFlag = " --vm"
		}
		cmd := fmt.Sprintf("lxc image alias delete %s >/dev/null 2>&1 || true; lxc image copy %s local: --alias %s%s --auto-update=false", shellSingleQuote(targetAlias), shellSingleQuote(source), shellSingleQuote(targetAlias), vmFlag)
		output, err := l.sshClient.ExecuteWithTimeout(cmd, 1*time.Hour)
		if err == nil && l.imageExists(targetAlias) {
			global.APP_LOG.Info("已复制spiritlhl LXD镜像到本地", zap.String("source", source), zap.String("alias", utils.TruncateString(targetAlias, 100)), zap.String("type", instanceType))
			return nil
		}
		if l.imageExists(targetAlias) {
			return nil
		}
		errText := output
		if err != nil {
			errText += "\n" + err.Error()
		}
		if strings.Contains(strings.ToLower(errText), "fingerprint") {
			if fp := extractFingerprintFromOutput(errText); fp != "" {
				if aliasErr := l.ensureImageAliasFromFingerprint(targetAlias, fp); aliasErr == nil {
					return nil
				}
			}
			if fp, _ := l.findImageFingerprintByURLAliasSuffix(targetAlias, instanceType); fp != "" {
				if aliasErr := l.ensureImageAliasFromFingerprint(targetAlias, fp); aliasErr == nil {
					return nil
				}
			}
		}
		lastErr = fmt.Errorf("复制候选镜像 %s 失败: %v (output: %s)", source, err, utils.TruncateString(output, 1200))
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
	// archlinux 在部分 simplestreams 源里也可能有 archlinux/cloud 这种别名。
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
