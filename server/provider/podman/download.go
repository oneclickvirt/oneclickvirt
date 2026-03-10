package podman

import (
	"crypto/md5"
	"fmt"
	"path/filepath"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// downloadImageToRemote 在远程服务器上下载镜像
func (p *PodmanProvider) downloadImageToRemote(imageURL, imageName, providerCountry, architecture string, useCDN bool) (string, error) {
	downloadDir := imageDir

	if _, err := p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", downloadDir)); err != nil {
		return "", fmt.Errorf("创建远程下载目录失败: %w", err)
	}

	fileName := p.generateRemoteFileName(imageName, imageURL, architecture)
	remotePath := filepath.Join(downloadDir, fileName)

	if p.isRemoteFileValid(remotePath) {
		global.APP_LOG.Debug("远程镜像文件已存在且完整，跳过下载",
			zap.String("imageName", imageName),
			zap.String("remotePath", remotePath))
		return remotePath, nil
	}

	downloadURL := p.getDownloadURL(imageURL, providerCountry, useCDN)

	global.APP_LOG.Debug("开始在远程服务器下载镜像",
		zap.String("imageName", imageName),
		zap.String("downloadURL", downloadURL))

	if err := p.downloadFileToRemote(downloadURL, remotePath); err != nil {
		p.removeRemoteFile(remotePath)
		return "", fmt.Errorf("远程下载镜像失败: %w", err)
	}

	return remotePath, nil
}

// cleanupRemoteImage 清理远程镜像文件
func (p *PodmanProvider) cleanupRemoteImage(imageName, imageURL, architecture string) error {
	fileName := p.generateRemoteFileName(imageName, imageURL, architecture)
	remotePath := filepath.Join(imageDir, fileName)
	return p.removeRemoteFile(remotePath)
}

// generateRemoteFileName 生成远程文件名
func (p *PodmanProvider) generateRemoteFileName(imageName, imageURL, architecture string) string {
	combined := fmt.Sprintf("%s_%s_%s", imageName, imageURL, architecture)
	hasher := md5.New()
	hasher.Write([]byte(combined))
	md5Hash := fmt.Sprintf("%x", hasher.Sum(nil))

	safeName := strings.ReplaceAll(imageName, "/", "_")
	safeName = strings.ReplaceAll(safeName, ":", "_")
	return fmt.Sprintf("%s_%s.tar", safeName, md5Hash[:8])
}

// isRemoteFileValid 检查远程文件是否存在且完整
func (p *PodmanProvider) isRemoteFileValid(remotePath string) bool {
	cmd := fmt.Sprintf("test -f %s -a -s %s", remotePath, remotePath)
	_, err := p.sshClient.Execute(cmd)
	return err == nil
}

// removeRemoteFile 删除远程文件
func (p *PodmanProvider) removeRemoteFile(remotePath string) error {
	_, err := p.sshClient.Execute(fmt.Sprintf("rm -f %s", remotePath))
	return err
}

// downloadFileToRemote 在远程服务器上下载文件
func (p *PodmanProvider) downloadFileToRemote(url, remotePath string) error {
	tmpPath := remotePath + ".tmp"
	curlCmd := fmt.Sprintf(
		"curl -4 -L -C - --connect-timeout 30 --retry 5 --retry-delay 10 --retry-max-time 0 -o %s '%s'",
		tmpPath, url,
	)

	output, err := p.sshClient.Execute(curlCmd)
	if err != nil {
		p.sshClient.Execute(fmt.Sprintf("rm -f %s", tmpPath))
		global.APP_LOG.Error("远程下载失败",
			zap.String("url", utils.TruncateString(url, 100)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("远程下载失败: %w", err)
	}

	if _, err := p.sshClient.Execute(fmt.Sprintf("mv %s %s", tmpPath, remotePath)); err != nil {
		return fmt.Errorf("移动文件失败: %w", err)
	}

	return nil
}

// ensureSSHScriptsAvailable 确保SSH脚本文件在远程服务器上可用
func (p *PodmanProvider) ensureSSHScriptsAvailable(providerCountry string) error {
	scriptsDir := "/usr/local/bin"
	scripts := []string{"ssh_bash.sh", "ssh_sh.sh"}

	allExist := true
	for _, script := range scripts {
		if !p.isRemoteFileValid(filepath.Join(scriptsDir, script)) {
			allExist = false
			break
		}
	}

	if allExist {
		return nil
	}

	for _, script := range scripts {
		scriptPath := filepath.Join(scriptsDir, script)
		if p.isRemoteFileValid(scriptPath) {
			continue
		}

		baseURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/main/scripts/", scriptRepo) + script
		downloadURL := p.getSSHScriptDownloadURL(baseURL, providerCountry)

		global.APP_LOG.Debug("开始下载SSH脚本",
			zap.String("script", script),
			zap.String("downloadURL", downloadURL))

		if err := p.downloadFileToRemote(downloadURL, scriptPath); err != nil {
			return fmt.Errorf("下载SSH脚本 %s 失败: %w", script, err)
		}

		chmodCmd := fmt.Sprintf("chmod +x %s", scriptPath)
		if _, err := p.sshClient.Execute(chmodCmd); err != nil {
			return fmt.Errorf("设置SSH脚本 %s 执行权限失败: %w", script, err)
		}

		dos2unixCmd := fmt.Sprintf("command -v dos2unix >/dev/null 2>&1 && dos2unix %s || true", scriptPath)
		p.sshClient.Execute(dos2unixCmd)
	}

	return nil
}

// getSSHScriptDownloadURL 获取SSH脚本下载URL，支持CDN
func (p *PodmanProvider) getSSHScriptDownloadURL(originalURL, providerCountry string) string {
	if providerCountry == "CN" || providerCountry == "cn" {
		cdnEndpoints := utils.GetCDNEndpoints()
		for _, endpoint := range cdnEndpoints {
			cdnURL := endpoint + originalURL
			testCmd := fmt.Sprintf("curl -s -I --max-time 5 '%s' | head -n 1 | grep -q '200'", cdnURL)
			if _, err := p.sshClient.Execute(testCmd); err == nil {
				return cdnURL
			}
		}
	}
	return originalURL
}
