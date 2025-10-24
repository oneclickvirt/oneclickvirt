package docker

import (
	"fmt"
	"strings"

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
	if cdnURL := d.getCDNURL(originalURL); cdnURL != "" {
		return cdnURL
	}
	return originalURL
}

// getCDNURL 获取CDN URL - 测试CDN可用性
func (d *DockerProvider) getCDNURL(originalURL string) string {
	cdnEndpoints := utils.GetCDNEndpoints()

	// 使用已知存在的测试文件来检测CDN可用性
	testURL := "https://raw.githubusercontent.com/spiritLHLS/ecs/main/back/test"

	// 测试每个CDN端点，找到第一个可用的就使用
	for _, endpoint := range cdnEndpoints {
		cdnTestURL := endpoint + testURL
		// 测试CDN可用性 - 检查是否包含 "success" 字符串
		testCmd := fmt.Sprintf("curl -sL -k --max-time 6 '%s' 2>/dev/null | grep -q 'success' && echo 'ok' || echo 'failed'", cdnTestURL)
		result, err := d.sshClient.Execute(testCmd)
		if err == nil && strings.TrimSpace(result) == "ok" {
			cdnURL := endpoint + originalURL
			global.APP_LOG.Info("找到可用CDN，使用CDN下载Docker镜像",
				zap.String("originalURL", utils.TruncateString(originalURL, 100)),
				zap.String("cdnURL", utils.TruncateString(cdnURL, 100)),
				zap.String("cdnEndpoint", endpoint))
			return cdnURL
		}
		// 短暂延迟避免过于频繁的请求
		d.sshClient.Execute("sleep 0.5")
	}

	global.APP_LOG.Info("未找到可用CDN，使用原始URL",
		zap.String("originalURL", utils.TruncateString(originalURL, 100)))
	return ""
}
