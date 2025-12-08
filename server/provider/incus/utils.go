package incus

import (
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// convertMemoryFormat 转换内存格式为Incus支持的格式
func convertMemoryFormat(memory string) string {
	if memory == "" {
		return ""
	}

	// 检查是否已经是正确的格式（以 iB 结尾）
	if strings.HasSuffix(memory, "iB") {
		return memory
	}

	// 处理MB格式
	if strings.HasSuffix(memory, "M") || strings.HasSuffix(memory, "MB") || strings.HasSuffix(memory, "m") {
		numStr := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(memory, "MB"), "M"), "m")
		if num, err := strconv.Atoi(numStr); err == nil {
			return fmt.Sprintf("%dMiB", num)
		}
	}

	// 处理GB格式
	if strings.HasSuffix(memory, "G") || strings.HasSuffix(memory, "GB") || strings.HasSuffix(memory, "g") {
		numStr := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(memory, "GB"), "G"), "g")
		if num, err := strconv.Atoi(numStr); err == nil {
			return fmt.Sprintf("%dGiB", num)
		}
	}

	// 如果没有单位，假设是MB
	if num, err := strconv.Atoi(memory); err == nil {
		return fmt.Sprintf("%dMiB", num)
	}

	// 默认返回原值
	return memory
}

// convertDiskFormat 转换磁盘格式为Incus支持的格式
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

// m 辅助函数，返回两个整数中的较小值
func m(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// getDownloadURL 确定下载URL
func (i *IncusProvider) getDownloadURL(originalURL string, useCDN bool) string {
	// 如果不使用CDN，直接返回原始URL
	if !useCDN {
		global.APP_LOG.Info("镜像配置不使用CDN，使用原始URL",
			zap.String("originalURL", utils.TruncateString(originalURL, 100)))
		return originalURL
	}

	// 默认随机尝试CDN，不再限制地区
	if cdnURL := utils.GetCDNURL(i.sshClient, originalURL, "Incus"); cdnURL != "" {
		return cdnURL
	}
	return originalURL
}
