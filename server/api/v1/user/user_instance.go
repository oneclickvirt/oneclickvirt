package user

import (
	"oneclickvirt/service/resources"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	"oneclickvirt/model/resource"
	"oneclickvirt/model/user"
	userService "oneclickvirt/service/user"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

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

	global.APP_LOG.Debug("用户创建重置实例密码任务",
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

	global.APP_LOG.Debug("用户获取实例新密码成功",
		zap.Uint("userID", userID),
		zap.Uint64("instanceID", instanceID),
		zap.Uint64("taskID", taskID))

	common.ResponseSuccess(c, response, "获取新密码成功")
}
