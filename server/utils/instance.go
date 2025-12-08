package utils

import (
	"fmt"
	"math/rand"
	"strings"
)

// GenerateInstanceName 生成实例名称（全局统一函数）
// 生成格式: provider-name-4位随机字符 (如: docker-d73a)
func GenerateInstanceName(providerName string) string {
	randomStr := fmt.Sprintf("%04x", rand.Intn(65536)) // 生成4位16进制随机字符

	// 清理provider名称，移除特殊字符
	cleanName := strings.ReplaceAll(strings.ToLower(providerName), " ", "-")
	cleanName = strings.ReplaceAll(cleanName, "_", "-")

	return fmt.Sprintf("%s-%s", cleanName, randomStr)
}
