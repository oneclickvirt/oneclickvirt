package docker

import (
	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// getDownloadURL 确定下载URL
func (d *DockerProvider) getDownloadURL(originalURL, providerCountry string, useCDN bool) string {
	// 如果不使用CDN，直接返回原始URL
	if !useCDN {
		global.APP_LOG.Info("镜像配置不使用CDN，使用原始URL",
			zap.String("originalURL", utils.TruncateString(originalURL, 100)))
		return originalURL
	}

	// 默认随机尝试CDN，不再限制地区
	if cdnURL := utils.GetCDNURL(d.sshClient, originalURL, "Docker"); cdnURL != "" {
		return cdnURL
	}
	return originalURL
}
