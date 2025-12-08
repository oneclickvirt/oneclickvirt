package utils

import (
	"regexp"
	"strconv"
	"strings"
)

// IsValidLXDInstanceName 检查LXD/Incus实例名称是否有效
// LXD/Incus实例名称规则：
// - 长度不超过63个字符
// - 只能包含字母、数字、连字符和下划线
// - 不能以连字符开头或结尾
// - 不能包含连续的连字符
func IsValidLXDInstanceName(name string) bool {
	if name == "" {
		return false
	}

	if len(name) > 63 {
		return false
	}

	// 使用正则表达式验证格式
	pattern := `^[a-zA-Z0-9]([a-zA-Z0-9\-_]*[a-zA-Z0-9])?$`
	matched, err := regexp.MatchString(pattern, name)
	if err != nil || !matched {
		return false
	}

	// 检查是否包含连续的连字符
	if strings.Contains(name, "--") {
		return false
	}

	return true
}

// IsNumeric 检查字符串是否为纯数字
func IsNumeric(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// IsFloat 检查字符串是否为浮点数
func IsFloat(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}
