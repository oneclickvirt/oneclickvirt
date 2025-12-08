package lxd

import (
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// convertMemoryFormat converts memory from MB to MiB for LXD compatibility
//
// LXD limits.memory parameter supports the following units (as per official documentation):
// - MiB, GiB, TiB (binary units, base 1024) - RECOMMENDED
// - MB, GB, TB (decimal units, base 1000)
// - % (percentage of host memory)
// - Raw bytes (no suffix)
//
// Reference: https://documentation.ubuntu.com/lxd/en/latest/reference/instance_options/#instance-options-limits
// Note: LXD internally prefers binary units (MiB) for better precision and consistency
func convertMemoryFormat(memoryStr string) string {
	if memoryStr == "" {
		return ""
	}

	// 如果已经是MiB或GiB格式，直接返回
	if strings.HasSuffix(memoryStr, "MiB") || strings.HasSuffix(memoryStr, "GiB") {
		return memoryStr
	}

	// 处理各种可能的格式：512m, 512M, 512MB
	var numStr string
	if strings.HasSuffix(memoryStr, "MB") {
		numStr = strings.TrimSuffix(memoryStr, "MB")
	} else if strings.HasSuffix(memoryStr, "m") || strings.HasSuffix(memoryStr, "M") {
		numStr = memoryStr[:len(memoryStr)-1]
	} else {
		// 纯数字，假设是MB
		numStr = memoryStr
	}

	if num, err := strconv.Atoi(numStr); err == nil {
		return fmt.Sprintf("%dMiB", num)
	}

	// 如果转换失败，返回原值
	return memoryStr
}

// convertDiskFormat 转换磁盘格式为LXD支持的格式
func convertDiskFormat(disk string) string {
	if disk == "" {
		return ""
	}

	// 检查是否已经是正确的格式（以 iB 或 B 结尾）
	if strings.HasSuffix(disk, "iB") || strings.HasSuffix(disk, "B") {
		return disk
	}

	// 处理MB格式
	if strings.HasSuffix(disk, "M") || strings.HasSuffix(disk, "MB") || strings.HasSuffix(disk, "m") {
		numStr := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(disk, "MB"), "M"), "m")
		if num, err := strconv.Atoi(numStr); err == nil {
			return fmt.Sprintf("%dMiB", num)
		}
	}

	// 处理GB格式
	if strings.HasSuffix(disk, "G") || strings.HasSuffix(disk, "GB") || strings.HasSuffix(disk, "g") {
		numStr := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(disk, "GB"), "G"), "g")
		if num, err := strconv.Atoi(numStr); err == nil {
			return fmt.Sprintf("%dGiB", num)
		}
	}

	// 如果没有单位，假设是MB
	if num, err := strconv.Atoi(disk); err == nil {
		return fmt.Sprintf("%dMiB", num)
	}

	// 默认返回原值
	return disk
}

// min 辅助函数，返回两个整数中的较小值
func (l *LXDProvider) min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// getDownloadURL 确定下载URL
func (l *LXDProvider) getDownloadURL(originalURL, providerCountry string, useCDN bool) string {
	// 如果不使用CDN，直接返回原始URL
	if !useCDN {
		global.APP_LOG.Info("镜像配置不使用CDN，使用原始URL",
			zap.String("originalURL", utils.TruncateString(originalURL, 100)))
		return originalURL
	}

	// 默认随机尝试CDN，不再限制地区
	if cdnURL := utils.GetCDNURL(l.sshClient, originalURL, "LXD"); cdnURL != "" {
		return cdnURL
	}
	return originalURL
}
