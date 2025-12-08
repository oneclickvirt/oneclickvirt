package middleware

import (
	"bytes"
	"io"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// LoggerMiddleware 统一日志中间件，避免重复记录
func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// 读取请求体（限制大小避免内存暴增）
		const maxBodySize = 1 << 20 // 1MB
		var body []byte
		if c.Request.Body != nil && c.Request.ContentLength < maxBodySize {
			body, _ = io.ReadAll(io.LimitReader(c.Request.Body, maxBodySize))
			c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
		} // 处理请求
		c.Next()

		// 计算处理时间
		latency := time.Since(start)

		// 获取响应状态
		status := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method
		userAgent := c.Request.UserAgent()

		// 过滤敏感路径，避免过度记录
		if shouldSkipLogging(path) {
			return
		}

		// 构建日志字段
		fields := []zap.Field{
			zap.String("method", method),
			zap.String("path", path),
			zap.Int("status", status),
			zap.String("ip", clientIP),
			zap.Duration("latency", latency),
			zap.String("user_agent", utils.TruncateString(userAgent, 100)),
		}

		// 查询参数（如果存在且不敏感）
		if raw != "" && !containsSensitiveInfo(raw) {
			fields = append(fields, zap.String("query", utils.TruncateString(raw, 200)))
		}

		// 请求体（仅对特定方法和非敏感内容）
		if shouldLogRequestBody(method, path) && len(body) > 0 && len(body) < 1000 {
			bodyStr := string(body)
			if !containsSensitiveInfo(bodyStr) {
				fields = append(fields, zap.String("body", utils.TruncateString(bodyStr, 300)))
			}
		}

		// 错误信息（如果存在）
		if len(c.Errors) > 0 {
			errorStr := strings.TrimRight(c.Errors.ByType(gin.ErrorTypePrivate).String(), "\n")
			fields = append(fields, zap.String("errors", utils.TruncateString(errorStr, 200)))
		}

		// 根据状态码选择日志级别
		switch {
		case status >= 500:
			global.APP_LOG.Error("HTTP请求处理失败", fields...)
		case status >= 400:
			global.APP_LOG.Warn("HTTP请求客户端错误", fields...)
		case status >= 300:
			global.APP_LOG.Debug("HTTP请求重定向", fields...)
		case latency > 5*time.Second:
			global.APP_LOG.Warn("HTTP请求处理时间过长", fields...)
		case path == "/api/health" || path == "/health":
			// 健康检查只用debug级别
			global.APP_LOG.Debug("健康检查", fields...)
		default:
			global.APP_LOG.Info("HTTP请求", fields...)
		}
	}
}

// shouldSkipLogging 判断是否应该跳过日志记录
func shouldSkipLogging(path string) bool {
	skipPaths := []string{
		"/favicon.ico",
		"/robots.txt",
		"/assets/",
		"/static/",
		"/public/",
	}

	for _, skipPath := range skipPaths {
		if strings.HasPrefix(path, skipPath) {
			return true
		}
	}

	return false
}

// shouldLogRequestBody 判断是否应该记录请求体
func shouldLogRequestBody(method, path string) bool {
	// 只对POST、PUT、PATCH记录请求体
	if method != "POST" && method != "PUT" && method != "PATCH" {
		return false
	}

	// 跳过文件上传等大请求体的接口
	skipBodyPaths := []string{
		"/api/upload",
		"/api/file",
		"/api/avatar",
	}

	for _, skipPath := range skipBodyPaths {
		if strings.Contains(path, skipPath) {
			return false
		}
	}

	return true
}

// containsSensitiveInfo 检查内容是否包含敏感信息
func containsSensitiveInfo(content string) bool {
	content = strings.ToLower(content)
	sensitiveKeywords := []string{
		"password",
		"token",
		"secret",
		"key",
		"auth",
		"credential",
		"passwd",
		"pwd",
	}

	for _, keyword := range sensitiveKeywords {
		if strings.Contains(content, keyword) {
			return true
		}
	}

	return false
}
