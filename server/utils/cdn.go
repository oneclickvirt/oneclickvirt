package utils

import (
	"fmt"
	"strings"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// SSHExecutor 定义SSH执行接口，用于CDN测试
type SSHExecutor interface {
	Execute(cmd string) (string, error)
}

// GetCDNURL 获取CDN URL - 测试CDN可用性
// 参数:
//   - sshClient: SSH客户端，用于执行远程命令
//   - originalURL: 原始URL
//   - providerType: provider类型（如"LXD"、"Incus"、"Docker"、"Proxmox"），用于日志记录
//
// 返回:
//   - string: 可用的CDN URL，如果没有可用CDN则返回空字符串
func GetCDNURL(sshClient SSHExecutor, originalURL, providerType string) string {
	cdnEndpoints := GetCDNEndpoints()

	// 使用已知存在的测试文件来检测CDN可用性
	testURL := "https://raw.githubusercontent.com/spiritLHLS/ecs/main/back/test"

	// 测试每个CDN端点，找到第一个可用的就使用
	for _, endpoint := range cdnEndpoints {
		cdnTestURL := endpoint + testURL
		// 测试CDN可用性 - 检查是否包含 "success" 字符串
		testCmd := fmt.Sprintf("curl -sL -k --max-time 6 '%s' 2>/dev/null | grep -q 'success' && echo 'ok' || echo 'failed'", cdnTestURL)
		result, err := sshClient.Execute(testCmd)
		if err == nil && strings.TrimSpace(result) == "ok" {
			cdnURL := endpoint + originalURL
			global.APP_LOG.Info(fmt.Sprintf("找到可用CDN，使用CDN下载%s镜像", providerType),
				zap.String("originalURL", TruncateString(originalURL, 100)),
				zap.String("cdnURL", TruncateString(cdnURL, 100)),
				zap.String("cdnEndpoint", endpoint))
			return cdnURL
		}
		// 短暂延迟避免过于频繁的请求
		sshClient.Execute("sleep 0.5")
	}

	global.APP_LOG.Info("未找到可用CDN，使用原始URL",
		zap.String("originalURL", TruncateString(originalURL, 100)))
	return ""
}
