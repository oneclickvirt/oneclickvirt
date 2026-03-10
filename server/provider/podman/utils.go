package podman

import (
	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// getDownloadURL 确定下载URL
func (p *PodmanProvider) getDownloadURL(originalURL, providerCountry string, useCDN bool) string {
	if !useCDN {
		global.APP_LOG.Debug("镜像配置不使用CDN，使用原始URL",
			zap.String("originalURL", utils.TruncateString(originalURL, 100)))
		return originalURL
	}

	if cdnURL := utils.GetCDNURL(p.sshClient, originalURL, "Podman"); cdnURL != "" {
		return cdnURL
	}
	return originalURL
}
