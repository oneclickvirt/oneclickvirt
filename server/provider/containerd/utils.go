package containerd

import (
	"regexp"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func containerNameFilter(name string) string {
	return shellSingleQuote("name=^" + regexp.QuoteMeta(name) + "$")
}

// getDownloadURL 确定下载URL
func (c *ContainerdProvider) getDownloadURL(originalURL, providerCountry string, useCDN bool) string {
	if !useCDN {
		global.APP_LOG.Debug("镜像配置不使用CDN，使用原始URL",
			zap.String("originalURL", utils.TruncateString(originalURL, 100)))
		return originalURL
	}

	if cdnURL := utils.GetCDNURL(c.sshClient, originalURL, "Containerd"); cdnURL != "" {
		return cdnURL
	}
	return originalURL
}
