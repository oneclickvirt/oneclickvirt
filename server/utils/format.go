package utils

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// 最大日志长度限制
	MaxLogLength = 1000
	// 数组/对象最大元素数量
	MaxArrayElements = 10
	// 字符串最大长度
	MaxStringLength = 2000
)

// TruncateString 截断字符串，如果超长则显示省略号
func TruncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// TruncateJSON 截断JSON数据，减少日志长度
func TruncateJSON(data interface{}) string {
	truncated := truncateValue(data, 0)
	result, err := json.Marshal(truncated)
	if err != nil {
		return fmt.Sprintf("marshal_error: %v", err)
	}

	resultStr := string(result)
	if len(resultStr) > MaxLogLength {
		return TruncateString(resultStr, MaxLogLength)
	}
	return resultStr
}

// truncateValue 递归截断复杂数据结构
func truncateValue(value interface{}, depth int) interface{} {
	// 防止递归过深
	if depth > 5 {
		return "..."
	}

	switch v := value.(type) {
	case string:
		return TruncateString(v, MaxStringLength)
	case []interface{}:
		if len(v) > MaxArrayElements {
			truncated := make([]interface{}, MaxArrayElements+1)
			for i := 0; i < MaxArrayElements; i++ {
				truncated[i] = truncateValue(v[i], depth+1)
			}
			truncated[MaxArrayElements] = fmt.Sprintf("... and %d more items", len(v)-MaxArrayElements)
			return truncated
		}
		result := make([]interface{}, len(v))
		for i, item := range v {
			result[i] = truncateValue(item, depth+1)
		}
		return result
	case map[string]interface{}:
		if len(v) > MaxArrayElements {
			truncated := make(map[string]interface{})
			count := 0
			for k, val := range v {
				if count >= MaxArrayElements {
					truncated["..."] = fmt.Sprintf("and %d more fields", len(v)-MaxArrayElements)
					break
				}
				truncated[k] = truncateValue(val, depth+1)
				count++
			}
			return truncated
		}
		result := make(map[string]interface{})
		for k, val := range v {
			result[k] = truncateValue(val, depth+1)
		}
		return result
	default:
		return v
	}
}

// SanitizeUserInput 清理用户输入，防止日志注入
func SanitizeUserInput(input string) string {
	// 移除潜在的日志注入字符
	input = strings.ReplaceAll(input, "\n", "\\n")
	input = strings.ReplaceAll(input, "\r", "\\r")
	input = strings.ReplaceAll(input, "\t", "\\t")

	return TruncateString(input, MaxStringLength)
}

// FormatError 格式化错误信息，避免过长的堆栈信息
func FormatError(err error) string {
	if err == nil {
		return ""
	}

	errStr := err.Error()
	return TruncateString(errStr, MaxStringLength)
}
