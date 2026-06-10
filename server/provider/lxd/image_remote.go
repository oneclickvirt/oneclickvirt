package lxd

import (
	"context"
	"crypto/md5"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

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
		"curl -4 -fL -C - --connect-timeout 30 --max-time 360 --retry 5 --retry-delay 10 --retry-max-time 0 -o %s %s",
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

// shouldCleanupCachedImageOnCreateFailure 判断创建失败是否真的由镜像缓存损坏/不兼容引起。
// 存储池不存在、网络配置失败、权限失败等不应清理镜像，否则会把健康镜像 alias 删除。
func (l *LXDProvider) shouldCleanupCachedImageOnCreateFailure(output string, err error) bool {
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
