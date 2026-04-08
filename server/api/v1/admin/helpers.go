package admin

import (
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// getUserIDFromContext 从认证上下文中获取用户ID（使用全局函数）
func getUserIDFromContext(c *gin.Context) (uint, error) {
	return middleware.GetUserIDFromContext(c)
}

// respondUnauthorized 返回未授权错误
func respondUnauthorized(c *gin.Context, msg string) {
	common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, msg))
}
