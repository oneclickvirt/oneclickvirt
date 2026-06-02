package podman

import (
	"fmt"
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

// ensureRegistriesConf 确保 /etc/containers/registries.conf 配置有效
// 确认 "invalid condition: location is unset and prefix is not in the format: *.example.com" 错误
func (p *PodmanProvider) ensureRegistriesConf() {
	if p.sshClient == nil {
		return
	}
	// 检测 registries.conf 是否有效
	testCmd := fmt.Sprintf("%s info >/dev/null 2>&1", cliName)
	_, err := p.sshClient.Execute(testCmd)
	if err == nil {
		return
	}
	// 配置有问题，备份并写入最小有效配置
	global.APP_LOG.Warn("检测到 registries.conf 配置异常，尝试确认")
	backupCmd := "cp /etc/containers/registries.conf /etc/containers/registries.conf.bak 2>/dev/null; true"
	p.sshClient.Execute(backupCmd)
	minimalConf := `unqualified-search-registries = ["docker.io"]`
	writeCmd := fmt.Sprintf("echo '%s' > /etc/containers/registries.conf", minimalConf)
	if _, err := p.sshClient.Execute(writeCmd); err != nil {
		global.APP_LOG.Error("确认 registries.conf 失败", zap.Error(err))
		return
	}
	// 验证确认后的配置
	_, err = p.sshClient.Execute(testCmd)
	if err != nil {
		global.APP_LOG.Error("确认后 registries.conf 仍然无效", zap.Error(err))
	} else {
		global.APP_LOG.Info("registries.conf 确认成功")
	}
}
