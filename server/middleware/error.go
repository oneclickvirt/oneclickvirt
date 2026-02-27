package middleware

import (
	"runtime/debug"

	"oneclickvirt/global"
	"oneclickvirt/model/common"

	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
)

// ErrorHandler 是全局 panic 捕获与统一错误响应中间件。
//
// 功能：
//   - 通过 defer+recover 捕获所有 panic，记录 Error 级别日志（含堆栈、请求路径、request_id）；
//   - 处理业务层通过 c.Error() 附加的错误，按错误类型返回对应 HTTP 状态码；
//   - 须在 RequestIDMiddleware 之后注册，以便在日志中包含 request_id。
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				global.APP_LOG.Error("服务端 panic 已恢复",
					zap.Any("error", err),
					zap.String("stack", string(debug.Stack())),
					zap.String("request_id", GetRequestID(c)), // 全链路追踪ID
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
					zap.String("ip", c.ClientIP()),
				)

				common.ResponseWithError(c, common.NewError(common.CodeInternalError, "系统遇到了意外错误，请稍后重试"))
				c.Abort()
			}
		}()

		c.Next()

		// 处理 handler 通过 c.Error() 附加的业务错误
		if len(c.Errors) > 0 {
			err := c.Errors.Last()

			global.APP_LOG.Error("HTTP 请求处理错误",
				zap.Error(err.Err),
				zap.String("request_id", GetRequestID(c)), // 全链路追踪ID
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method),
				zap.String("ip", c.ClientIP()),
			)

			// 按 Gin 错误类型返回不同响应
			switch err.Type {
			case gin.ErrorTypeBind:
				// 参数绑定失败（客户端错误）
				common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, err.Error()))
			case gin.ErrorTypePublic:
				// 可对外暴露的业务错误
				common.ResponseWithError(c, common.NewError(common.CodeError, err.Error()))
			default:
				// 内部错误，不暴露详情
				common.ResponseWithError(c, common.NewError(common.CodeInternalError, "请稍后重试"))
			}
		}
	}
}
