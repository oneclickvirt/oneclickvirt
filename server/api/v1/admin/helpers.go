package admin

import (
	"errors"
	"net/http"

	"oneclickvirt/middleware"
	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// getUserIDFromContext 从认证上下文中获取用户ID
func getUserIDFromContext(c *gin.Context) (uint, error) {
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		return 0, errors.New("用户未认证")
	}
	return authCtx.UserID, nil
}

// respondUnauthorized 返回未授权错误
func respondUnauthorized(c *gin.Context, msg string) {
	c.JSON(http.StatusUnauthorized, common.Response{
		Code: 401,
		Msg:  msg,
	})
}
