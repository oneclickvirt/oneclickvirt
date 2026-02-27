package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/gin-gonic/gin"
)

// RequestIDKey 是在 gin.Context 中存储请求ID的键名。
const RequestIDKey = "request_id"

// RequestIDHeader 是客户端可传入、服务端回传的 HTTP 头名称。
const RequestIDHeader = "X-Request-ID"

// RequestIDMiddleware 为每个 HTTP 请求注入唯一的请求追踪ID。
//
// 行为：
//   - 优先使用客户端通过 X-Request-ID 请求头传入的ID（需满足长度/格式限制）；
//   - 若客户端未提供或ID无效，则服务端自动生成一个16字节随机十六进制ID；
//   - 最终ID写入 gin.Context（键名 "request_id"）以及响应头 X-Request-ID，
//     便于跨链路追踪和客户端关联日志。
//
// 注意：须在 LoggerMiddleware 之前注册，以确保日志中可携带 request_id。
//
// 使用方式（在 router 中全局注册）：
//
//	router.Use(middleware.RequestIDMiddleware())
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 尝试复用客户端传入的请求ID（最长64字符，仅允许十六进制字符和连字符）
		requestID := c.GetHeader(RequestIDHeader)
		if !isValidRequestID(requestID) {
			// 客户端未提供或格式无效，服务端自行生成
			requestID = generateRequestID()
		}

		// 2. 写入上下文，供日志、链路追踪等组件读取
		c.Set(RequestIDKey, requestID)

		// 3. 回写响应头，便于客户端将响应与请求日志关联
		c.Header(RequestIDHeader, requestID)

		c.Next()
	}
}

// GetRequestID 从 gin.Context 中安全地读取请求追踪ID。
// 若上下文中不存在（例如非HTTP调用场景），返回空字符串。
func GetRequestID(c *gin.Context) string {
	if id, exists := c.Get(RequestIDKey); exists {
		if s, ok := id.(string); ok {
			return s
		}
	}
	return ""
}

// generateRequestID 生成一个16字节（32个十六进制字符）的随机请求ID。
func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 极低概率失败，降级返回固定占位值
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

// isValidRequestID 校验客户端传入的请求ID格式是否合法：
// 非空、长度不超过64字符、仅包含十六进制字符和连字符（UUID/hex格式兼容）。
func isValidRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !strings.ContainsRune("0123456789abcdefABCDEF-", c) {
			return false
		}
	}
	return true
}
