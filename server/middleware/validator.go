package middleware

import (
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"
)

// sqlInjectionPatterns 预编译的 SQL 注入模式（如果每次请求都重新编译会导致大量 CPU 帀务）
var sqlInjectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(union\s+select)`),
	regexp.MustCompile(`(?i)(drop\s+table)`),
	regexp.MustCompile(`(?i)(delete\s+from)`),
	regexp.MustCompile(`(?i)(insert\s+into)`),
	regexp.MustCompile(`(?i)(update\s+set)`),
	regexp.MustCompile(`(?i)(exec\s*\()`),
	regexp.MustCompile(`(?i)(script\s*>)`),
	regexp.MustCompile(`(?i)(\'\s*or\s*\'\s*=\s*\')`),
	regexp.MustCompile(`(?i)(\'\s*or\s*1\s*=\s*1)`),
	regexp.MustCompile(`(?i)(--\s)`),
	regexp.MustCompile(`(?i)(/\*.*\*/)`),
}

// xssPatterns 预编译的 XSS 模式
var xssPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(<script[^>]*>)`),
	regexp.MustCompile(`(?i)(</script>)`),
	regexp.MustCompile(`(?i)(javascript:)`),
	regexp.MustCompile(`(?i)(on\w+\s*=)`),
	regexp.MustCompile(`(?i)(<iframe[^>]*>)`),
	regexp.MustCompile(`(?i)(<object[^>]*>)`),
	regexp.MustCompile(`(?i)(<embed[^>]*>)`),
	regexp.MustCompile(`(?i)(<link[^>]*>)`),
	regexp.MustCompile(`(?i)(<meta[^>]*>)`),
}

// InputValidator 输入验证中间件（检查 URL 查询字符串和路径）
func InputValidator() gin.HandlerFunc {
	return func(c *gin.Context) {
		target := c.Request.URL.RawQuery + c.Request.URL.Path

		if containsSQLInjection(target) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    400,
				"message": "检测到潜在的SQL注入攻击",
			})
			c.Abort()
			return
		}

		if containsXSS(target) {
			c.JSON(http.StatusBadRequest, gin.H{
				"code":    400,
				"message": "检测到潜在的XSS攻击",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// containsSQLInjection 检查是否包含SQL注入模式
func containsSQLInjection(input string) bool {
	for _, re := range sqlInjectionPatterns {
		if re.MatchString(input) {
			return true
		}
	}
	return false
}

// containsXSS 检查是否包含XSS攻击模式
func containsXSS(input string) bool {
	for _, re := range xssPatterns {
		if re.MatchString(input) {
			return true
		}
	}
	return false
}
