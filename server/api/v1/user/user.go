package user

import (
	"errors"
	"oneclickvirt/service/pmacct"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/task"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	"oneclickvirt/model/resource"
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

// GetAvailableProviders 获取可用节点列表
// @Summary 获取可用节点列表
// @Description 获取当前用户可以申领的节点列表，根据资源使用情况筛选
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=[]user.AvailableProviderResponse} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/provider/available [get]
func GetAvailableProviders(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	userServiceInstance := userService.NewService()
	providers, err := userServiceInstance.GetAvailableProviders(userID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取可用节点失败"))
		return
	}

	common.ResponseSuccess(c, providers)
}

// GetSystemImages 获取系统镜像列表
// @Summary 获取系统镜像列表
// @Description 获取当前用户可以使用的系统镜像列表，支持按Provider和实例类型过滤
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param providerType query string false "Provider类型"
// @Param providerId query uint false "Provider ID"
// @Param instanceType query string false "实例类型" Enums(container,vm)
// @Param architecture query string false "架构"
// @Success 200 {object} common.Response{data=[]user.SystemImageResponse} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/images [get]
func GetUserSystemImages(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.SystemImagesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userServiceInstance := userService.NewService()
	images, err := userServiceInstance.GetSystemImages(userID, req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取系统镜像失败"))
		return
	}

	common.ResponseSuccess(c, images)
}

// GetFilteredSystemImages 获取过滤后的系统镜像列表
// @Summary 获取过滤后的系统镜像列表
// @Description 根据Provider ID和实例类型获取匹配的系统镜像列表
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param provider_id query uint true "Provider ID"
// @Param instance_type query string true "实例类型" Enums(container,vm)
// @Param architecture query string false "架构类型" Enums(amd64,arm64)
// @Success 200 {object} common.Response{data=[]user.SystemImageResponse} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/images/filtered [get]
func GetFilteredSystemImages(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	providerID := c.Query("provider_id")
	instanceType := c.Query("instance_type")

	if providerID == "" || instanceType == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "provider_id和instance_type参数必填"))
		return
	}

	// 转换providerID为uint
	id, err := strconv.ParseUint(providerID, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "provider_id参数格式错误"))
		return
	}

	userServiceInstance := userService.NewService()
	images, err := userServiceInstance.GetFilteredSystemImages(userID, uint(id), instanceType)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, images)
}

// GetProviderCapabilities 获取Provider能力信息
// @Summary 获取Provider能力信息
// @Description 获取指定Provider支持的实例类型和架构信息
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path uint true "Provider ID"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/provider/{id}/capabilities [get]
func GetProviderCapabilities(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	providerID := c.Param("id")
	if providerID == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "providerId参数必填"))
		return
	}

	// 转换providerID为uint
	id, err := strconv.ParseUint(providerID, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "providerId参数格式错误"))
		return
	}

	userServiceInstance := userService.NewService()
	capabilities, err := userServiceInstance.GetProviderCapabilities(userID, uint(id))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, capabilities)
}

// GetUserTasks 获取用户任务列表
// @Summary 获取用户任务列表
// @Description 获取当前用户的任务列表
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param taskType query string false "任务类型"
// @Param status query string false "任务状态"
// @Param providerId query string false "节点ID"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/tasks [get]
func GetUserTasks(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.UserTasksRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userServiceInstance := userService.NewService()
	tasks, total, err := userServiceInstance.GetUserTasks(userID, req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取任务列表失败"))
		return
	}

	common.ResponseSuccessWithPagination(c, tasks, total, req.Page, req.PageSize)
}

// CancelUserTask 取消用户任务
// @Summary 取消用户任务
// @Description 用户取消自己的等待中任务
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param taskId path int true "任务ID"
// @Success 200 {object} common.Response "操作成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "操作失败"
// @Router /user/tasks/{taskId}/cancel [post]
func CancelUserTask(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	taskIDStr := c.Param("taskId")
	taskID, err := strconv.ParseUint(taskIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的任务ID"))
		return
	}

	taskService := task.GetTaskService()
	if err := taskService.CancelTask(uint(taskID), userID); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "任务已取消")
}

// CreateUserInstance 创建实例
// @Summary 创建实例
// @Description 用户创建新的虚拟机或容器实例（异步处理）
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body user.CreateInstanceRequest true "创建实例请求参数"
// @Success 200 {object} common.Response{data=object} "任务创建成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "创建失败"
// @Router /user/instances [post]
func CreateUserInstance(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.CreateInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Error("CreateUserInstance binding error: " + err.Error())
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}

	userServiceInstance := userService.NewService()
	task, err := userServiceInstance.CreateUserInstance(userID, req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	// 返回任务信息
	responseData := map[string]interface{}{
		"taskId":     task.ID,
		"status":     task.Status,
		"message":    "实例创建任务已提交，正在后台处理",
		"created_at": task.CreatedAt,
	}

	common.ResponseSuccess(c, responseData, "实例创建任务已提交")
}

// GetInstanceTypePermissions 获取实例类型权限配置
// @Summary 获取实例类型权限配置
// @Description 获取当前用户可以创建的实例类型权限配置，基于用户配额和Provider能力
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/instance-type-permissions [get]
func GetInstanceTypePermissions(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	userServiceInstance := userService.NewService()
	permissions, err := userServiceInstance.GetInstanceTypePermissions(userID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取实例类型权限配置失败"))
		return
	}

	common.ResponseSuccess(c, permissions)
}

// GetUserInstanceDetail 获取用户实例详情
// @Summary 获取用户实例详情
// @Description 获取用户实例的详细信息
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "实例ID"
// @Success 200 {object} common.Response{data=user.UserInstanceDetailResponse} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 403 {object} common.Response "实例不存在或无权限"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/instances/{id} [get]
func GetUserInstanceDetail(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	userServiceInstance := userService.NewService()
	detail, err := userServiceInstance.GetInstanceDetail(userID, uint(instanceID))
	if err != nil {
		if err.Error() == "实例不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "实例不存在或无权限"))
			return
		}
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取实例详情失败"))
		return
	}

	common.ResponseSuccess(c, detail)
}

// GetInstanceConfig 获取实例配置选项
// @Summary 获取实例配置选项
// @Description 获取可用的镜像、规格等实例创建配置选项
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=user.InstanceConfigResponse} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/instance-config [get]
func GetInstanceConfig(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	// 获取可选的 provider_id 参数
	var providerID uint
	if providerIDStr := c.Query("provider_id"); providerIDStr != "" {
		if id, err := strconv.ParseUint(providerIDStr, 10, 32); err == nil {
			providerID = uint(id)
		}
	}

	userServiceInstance := userService.NewService()
	config, err := userServiceInstance.GetInstanceConfig(userID, providerID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取实例配置失败"))
		return
	}

	common.ResponseSuccess(c, config)
}

// GetActiveReservations 获取用户的活跃资源预留
// @Summary 获取用户的活跃资源预留
// @Description 获取当前用户的所有活跃资源预留记录
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=[]resource.ResourceReservation} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/active-reservations [get]
func GetActiveReservations(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	reservationService := resources.GetResourceReservationService()
	reservations, err := reservationService.GetActiveReservations()
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取预留资源失败"))
		return
	}

	// 过滤用户自己的预留记录
	var userReservations []resource.ResourceReservation
	for _, reservation := range reservations {
		if reservation.UserID == userID {
			userReservations = append(userReservations, reservation)
		}
	}

	common.ResponseSuccess(c, userReservations)
}

// GetInstanceMonitoring 获取实例监控数据
// @Summary 获取实例监控数据
// @Description 获取用户实例的监控数据，包括流量统计信息
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "实例ID"
// @Success 200 {object} common.Response{data=user.InstanceMonitoringResponse} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 403 {object} common.Response "实例不存在或无权限"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/instances/{id}/monitoring [get]
func GetInstanceMonitoring(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	userServiceInstance := userService.NewService()
	monitoring, err := userServiceInstance.GetInstanceMonitoring(userID, uint(instanceID))
	if err != nil {
		if err.Error() == "实例不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "实例不存在或无权限"))
			return
		}
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取监控数据失败"))
		return
	}

	common.ResponseSuccess(c, monitoring)
}

// ResetInstancePassword 用户重置实例密码
// @Summary 用户重置实例密码
// @Description 用户重置自己实例的登录密码，创建异步任务执行密码重置操作
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "实例ID"
// @Param request body user.ResetInstancePasswordRequest true "重置实例密码请求参数（可为空对象）"
// @Success 200 {object} common.Response{data=user.ResetInstancePasswordResponse} "任务创建成功，返回任务ID"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 403 {object} common.Response "实例不存在或无权限"
// @Failure 500 {object} common.Response "创建任务失败"
// @Router /user/instances/{id}/reset-password [put]
func ResetInstancePassword(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	var req user.ResetInstancePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 由于不需要参数，忽略绑定错误
	}

	global.APP_LOG.Info("用户创建重置实例密码任务",
		zap.Uint("userID", userID),
		zap.Uint64("instanceID", instanceID))

	userInstanceService := userService.NewService()
	taskID, err := userInstanceService.ResetInstancePassword(userID, uint(instanceID))
	if err != nil {
		global.APP_LOG.Error("用户创建重置实例密码任务失败",
			zap.Uint("userID", userID),
			zap.Uint64("instanceID", instanceID),
			zap.Error(err))
		if err.Error() == "实例不存在或无权限" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	response := user.ResetInstancePasswordResponse{
		TaskID: taskID,
	}

	global.APP_LOG.Info("用户创建重置实例密码任务成功",
		zap.Uint("userID", userID),
		zap.Uint64("instanceID", instanceID),
		zap.Uint("taskID", taskID))

	common.ResponseSuccess(c, response, "密码重置任务创建成功")
}

// GetInstanceNewPassword 获取实例重置后的新密码
// @Summary 获取实例重置后的新密码
// @Description 通过任务ID获取实例重置后的新密码
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "实例ID"
// @Param taskId path int true "任务ID"
// @Success 200 {object} common.Response{data=user.GetInstancePasswordResponse} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 403 {object} common.Response "实例不存在或无权限"
// @Failure 404 {object} common.Response "任务不存在或未完成"
// @Router /user/instances/{id}/password/{taskId} [get]
func GetInstanceNewPassword(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	taskIDStr := c.Param("taskId")
	taskID, err := strconv.ParseUint(taskIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的任务ID"))
		return
	}

	userInstanceService := userService.NewService()
	newPassword, resetTime, err := userInstanceService.GetInstanceNewPassword(userID, uint(instanceID), uint(taskID))
	if err != nil {
		global.APP_LOG.Error("用户获取实例新密码失败",
			zap.Uint("userID", userID),
			zap.Uint64("instanceID", instanceID),
			zap.Uint64("taskID", taskID),
			zap.Error(err))

		if err.Error() == "实例不存在或无权限" || err.Error() == "任务不存在或无权限" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
		if err.Error() == "任务尚未完成" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
			return
		}
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	response := user.GetInstancePasswordResponse{
		NewPassword: newPassword,
		ResetTime:   resetTime,
	}

	global.APP_LOG.Info("用户获取实例新密码成功",
		zap.Uint("userID", userID),
		zap.Uint64("instanceID", instanceID),
		zap.Uint64("taskID", taskID))

	common.ResponseSuccess(c, response, "获取新密码成功")
}

// GetInstancePmacctSummary 获取实例pmacct流量汇总
// @Summary 获取实例pmacct流量汇总
// @Description 获取用户实例的pmacct流量汇总信息，包括今日、本月和总流量统计
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param instance_id path int true "实例ID"
// @Success 200 {object} common.Response{data=monitoring.PmacctSummary} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 403 {object} common.Response "无权限访问该实例"
// @Failure 404 {object} common.Response "实例不存在"
// @Failure 500 {object} common.Response "获取失败"
// @Router /user/instances/{instance_id}/pmacct/summary [get]
func GetInstancePmacctSummary(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "实例ID格式错误"))
		return
	}

	// 验证用户是否有权限访问该实例
	userServiceInstance := userService.NewService()
	_, err = userServiceInstance.GetInstanceDetail(userID, uint(instanceID))
	if err != nil {
		if err.Error() == "实例不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "实例不存在或无权限"))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "验证实例权限失败"))
		}
		return
	}

	// pmacct不需要interfaceName，因为它只监控一个公网IP
	pmacctService := pmacct.NewService()
	summary, err := pmacctService.GetPmacctSummary(uint(instanceID))
	if err != nil {
		global.APP_LOG.Error("获取实例pmacct汇总失败",
			zap.Uint("userID", userID),
			zap.Uint64("instanceID", instanceID),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	global.APP_LOG.Info("用户获取实例pmacct汇总成功",
		zap.Uint("userID", userID),
		zap.Uint64("instanceID", instanceID))

	common.ResponseSuccess(c, summary, "获取pmacct汇总成功")
}

// QueryInstancePmacctData 查询实例pmacct流量数据
// @Summary 查询实例pmacct流量数据
// @Description 查询实例的pmacct流量数据
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param instance_id path int true "实例ID"
// @Success 200 {object} common.Response{data=monitoring.PmacctSummary} "查询成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 403 {object} common.Response "无权限访问该实例"
// @Failure 404 {object} common.Response "实例不存在"
// @Failure 500 {object} common.Response "查询失败"
// @Router /user/instances/{instance_id}/pmacct/query [get]
func QueryInstancePmacctData(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "实例ID格式错误"))
		return
	}

	// 验证用户是否有权限访问该实例
	userServiceInstance := userService.NewService()
	_, err = userServiceInstance.GetInstanceDetail(userID, uint(instanceID))
	if err != nil {
		if err.Error() == "实例不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "实例不存在或无权限"))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "验证实例权限失败"))
		}
		return
	}

	pmacctService := pmacct.NewService()
	summary, err := pmacctService.GetPmacctSummary(uint(instanceID))
	if err != nil {
		global.APP_LOG.Error("查询pmacct数据失败",
			zap.Uint("userID", userID),
			zap.Uint64("instanceID", instanceID),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	global.APP_LOG.Info("用户查询pmacct数据成功",
		zap.Uint("userID", userID),
		zap.Uint64("instanceID", instanceID))

	common.ResponseSuccess(c, summary, "查询pmacct数据成功")
}
