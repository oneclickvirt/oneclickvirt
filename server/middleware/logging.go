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

// LoggerMiddleware 是统一的 HTTP 访问日志中间件。
//
// 功能：
//   - 自动按 HTTP 状态码分级记录日志（Debug/Info/Warn/Error）；
//   - 对慢请求（>5s响应）额外以 Warn 级别标记；
//   - 每条日志携带 request_id 字段，与 RequestIDMiddleware 配合实现全链路追踪；
//   - 自动过滤静态资源路径、含敏感关键字的参数/请求体、超大请求体；
//   - 健康检查端点仅以 Debug 级别记录，减少日志噪音。
//
// 须在 RequestIDMiddleware 之后注册，以确保 request_id 已写入上下文。
func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// 读取请求体（限制 1MB，防止内存暴增；读完后重置 Body 供后续 handler 使用）
		const maxBodySize = 1 << 20 // 1 MB
		var body []byte
		if c.Request.Body != nil && c.Request.ContentLength < maxBodySize {
			body, _ = io.ReadAll(io.LimitReader(c.Request.Body, maxBodySize))
			c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
		}

		// 调用后续 handler/middleware
		c.Next()

		// 过滤静态资源等高频噪音路径，无需记录访问日志
		if shouldSkipLogging(path) {
			return
		}

		latency := time.Since(start)
		status := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method
		userAgent := c.Request.UserAgent()

		// 基础字段：每条 HTTP 日志都携带 request_id，用于全链路追踪
		fields := []zap.Field{
			zap.String("request_id", GetRequestID(c)), // 全链路追踪ID
			zap.String("method", method),
			zap.String("path", path),
			zap.Int("status", status),
			zap.String("ip", clientIP),
			zap.Duration("latency", latency),
			zap.String("user_agent", utils.TruncateString(userAgent, 100)),
		}

		// 查询参数（存在且不含敏感关键字时追加）
		if raw != "" && !containsSensitiveInfo(raw) {
			fields = append(fields, zap.String("query", utils.TruncateString(raw, 200)))
		}

		// 请求体（仅 POST/PUT/PATCH、非文件上传路径、内容非敏感且小于 1KB 时记录）
		if shouldLogRequestBody(method, path) && len(body) > 0 && len(body) < 1000 {
			if bodyStr := string(body); !containsSensitiveInfo(bodyStr) {
				fields = append(fields, zap.String("body", utils.TruncateString(bodyStr, 300)))
			}
		}

		// Gin 框架内部注册的错误（通过 c.Error() 附加）
		if len(c.Errors) > 0 {
			errorStr := strings.TrimRight(c.Errors.ByType(gin.ErrorTypePrivate).String(), "\n")
			fields = append(fields, zap.String("errors", utils.TruncateString(errorStr, 200)))
		}

		// 日志级别策略：
		//   500+ → Error  : 服务端异常，需立即关注
		//   400–499 → Warn : 客户端错误，可能存在攻击或接口误用
		//   300–399 → Debug: 重定向，通常无需关注
		//   慢请求  → Warn : 延迟超过 5s，性能告警
		//   健康检查 → Debug: 高频无意义流量，不污染 Info 日志
		//   其他    → Info
		switch {
		case status >= 500:
			global.APP_LOG.Error("HTTP请求处理失败", fields...)
		case status >= 400:
			global.APP_LOG.Warn("HTTP请求客户端错误", fields...)
		case status >= 300:
			global.APP_LOG.Debug("HTTP请求重定向", fields...)
		case latency > 5*time.Second:
			global.APP_LOG.Warn("HTTP请求响应超时", fields...)
		case path == "/api/health" || path == "/health":
			global.APP_LOG.Debug("健康检查", fields...)
		default:
			global.APP_LOG.Info("HTTP请求", fields...)
		}
	}
}

// shouldSkipLogging 判断路径是否属于高频静态资源，跳过访问日志以降低噪音。
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

// shouldLogRequestBody 判断是否需要记录请求体。
// 仅对变更类方法（POST/PUT/PATCH）且非文件上传路径生效。
func shouldLogRequestBody(method, path string) bool {
	if method != "POST" && method != "PUT" && method != "PATCH" {
		return false
	}

	// 文件上传类接口的请求体过大，跳过记录
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

// containsSensitiveInfo 检查字符串是否包含敏感关键字（密码、Token 等），
// 用于在记录日志前过滤可能泄露凭证的内容。
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
