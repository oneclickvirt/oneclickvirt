package user

import (
	"errors"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	"oneclickvirt/model/user"
	userService "oneclickvirt/service/user"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func getUserID(c *gin.Context) (uint, error) {
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		return 0, errors.New("用户未登录")
	}
	return authCtx.UserID, nil
}

// GetUserDashboard 获取用户仪表板
// @Summary 获取用户仪表板
// @Description 获取当前登录用户的仪表板数据
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/dashboard [get]
func GetUserDashboard(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	userServiceInstance := userService.NewService()
	dashboard, err := userServiceInstance.GetUserDashboard(userID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取用户首页数据失败"))
		return
	}

	common.ResponseSuccess(c, dashboard)
}

// GetAvailableResources 获取可申领资源
// @Summary 获取可申领资源
// @Description 获取当前用户可以申领的资源列表
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param resourceType query string false "资源类型"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/resources/available [get]
func GetAvailableResources(c *gin.Context) {
	var req user.AvailableResourcesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userServiceInstance := userService.NewService()
	resources, total, err := userServiceInstance.GetAvailableResources(req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccessWithPagination(c, resources, total, req.Page, req.PageSize)
}

// ClaimResource 申领资源
// @Summary 申领资源
// @Description 用户申领可用的资源实例
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body user.ClaimResourceRequest true "申领资源请求参数"
// @Success 200 {object} common.Response{data=object} "申领成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "申领失败"
// @Router /user/resources/claim [post]
func ClaimResource(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.ClaimResourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("申领资源参数错误",
			zap.Uint("userID", userID),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	global.APP_LOG.Info("用户申领资源",
		zap.Uint("userID", userID),
		zap.Uint("providerID", req.ProviderID),
		zap.String("instanceType", req.InstanceType),
		zap.String("name", utils.TruncateString(req.Name, 32)))

	userServiceInstance := userService.NewService()
	instance, err := userServiceInstance.ClaimResource(userID, req)
	if err != nil {
		global.APP_LOG.Error("用户申领资源失败",
			zap.Uint("userID", userID),
			zap.Uint("providerID", req.ProviderID),
			zap.String("instanceType", req.InstanceType),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	global.APP_LOG.Info("用户申领资源成功",
		zap.Uint("userID", userID),
		zap.String("instanceName", utils.TruncateString(req.Name, 32)))
	common.ResponseSuccess(c, instance, "申领成功")
}

// GetUserInstances 获取用户实例列表
// @Summary 获取用户实例列表
// @Description 获取当前用户的所有实例
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param status query string false "实例状态"
// @Param type query string false "实例类型"
// @Param providerName query string false "节点名称"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "获取失败"
// @Router /user/instances [get]
func GetUserInstances(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.UserInstanceListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userServiceInstance := userService.NewService()
	instances, total, err := userServiceInstance.GetUserInstances(userID, req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取实例列表失败"))
		return
	}

	common.ResponseSuccessWithPagination(c, instances, total, req.Page, req.PageSize)
}

// InstanceAction 实例操作
// @Summary 实例操作
// @Description 对用户实例执行操作（启动、停止、重启等）
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body user.InstanceActionRequest true "实例操作请求参数"
// @Success 200 {object} common.Response "操作成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "操作失败"
// @Router /user/instances/action [post]
func InstanceAction(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.InstanceActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("实例操作参数错误",
			zap.Uint("userID", userID),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	global.APP_LOG.Info("用户执行实例操作",
		zap.Uint("userID", userID),
		zap.Uint("instanceID", req.InstanceID),
		zap.String("action", req.Action))

	userServiceInstance := userService.NewService()
	err = userServiceInstance.InstanceAction(userID, req)
	if err != nil {
		global.APP_LOG.Error("用户实例操作失败",
			zap.Uint("userID", userID),
			zap.Uint("instanceID", req.InstanceID),
			zap.String("action", req.Action),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	global.APP_LOG.Info("用户实例操作成功",
		zap.Uint("userID", userID),
		zap.Uint("instanceID", req.InstanceID),
		zap.String("action", req.Action))
	common.ResponseSuccess(c, nil, "操作成功")
}

// UpdateProfile 更新个人信息
// @Summary 更新个人信息
// @Description 更新当前用户的个人资料信息
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body user.UpdateProfileRequest true "更新个人信息请求参数"
// @Success 200 {object} common.Response "更新成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "更新失败"
// @Router /user/profile [put]
func UpdateProfile(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userServiceInstance := userService.NewService()
	err = userServiceInstance.UpdateProfile(userID, req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "更新个人信息失败"))
		return
	}

	common.ResponseSuccess(c, nil, "更新成功")
}

// ChangePassword 修改密码
// @Summary 修改密码
// @Description 修改当前用户的登录密码
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body user.ChangePasswordRequest true "修改密码请求参数"
// @Success 200 {object} common.Response "修改成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "修改失败"
// @Router /user/password [put]
func ChangePassword(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userServiceInstance := userService.NewService()
	err = userServiceInstance.ChangePassword(userID, req.OldPassword, req.NewPassword)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidCredentials, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "密码修改成功")
}

// UserResetPassword 用户重置自己的密码
// @Summary 用户重置自己的密码
// @Description 用户重置自己的登录密码，系统自动生成符合安全策略的新密码，并通过绑定的通信渠道发送
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body user.ResetPasswordRequest true "重置密码请求参数（可为空对象）"
// @Success 200 {object} common.Response "重置成功，新密码已发送到绑定的通信渠道"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "重置失败"
// @Router /user/reset-password [put]
func UserResetPassword(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 由于不需要参数，忽略绑定错误
	}

	userServiceInstance := userService.NewService()
	newPassword, err := userServiceInstance.ResetPasswordAndNotify(userID)
	if err != nil {
		// 检查是否是发送失败但密码重置成功的情况
		if newPassword != "" {
			// 密码重置成功，但发送失败，仍然返回新密码
			response := map[string]interface{}{
				"newPassword": newPassword,
			}
			common.ResponseSuccess(c, response, err.Error())
			return
		}
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	response := map[string]interface{}{
		"newPassword": newPassword,
	}
	common.ResponseSuccess(c, response, "密码重置成功，新密码已发送到您绑定的通信渠道")
}

// GetUserLimits 获取用户配额限制
// @Summary 获取用户配额限制
// @Description 获取当前登录用户的配额限制信息
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=user.UserLimitsResponse} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/limits [get]
func GetUserLimits(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	userServiceInstance := userService.NewService()
	limits, err := userServiceInstance.GetUserLimits(userID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取用户配额限制失败"))
		return
	}

	common.ResponseSuccess(c, limits)
}
