package user

import (
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	userService "oneclickvirt/service/user"

	"github.com/gin-gonic/gin"
)

// GetUserInfo 获取用户信息
// @Summary 获取用户信息
// @Description 获取当前登录用户的详细信息
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 401 {object} common.Response "用户未授权"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/info [get]
func GetUserInfo(c *gin.Context) {
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权"))
		return
	}

	userServiceInstance := userService.NewService()
	userDashboard, err := userServiceInstance.GetUserDashboard(authCtx.UserID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, userDashboard, "获取成功")
}
