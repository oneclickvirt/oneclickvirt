package admin

import (
	"net/http"

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
	c.JSON(http.StatusUnauthorized, common.Response{
		Code: 401,
		Msg:  msg,
	})
}
