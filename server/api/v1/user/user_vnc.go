package user

import (
	"strconv"

	adminAPI "oneclickvirt/api/v1/admin"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	trafficService "oneclickvirt/service/traffic"

	"github.com/gin-gonic/gin"
)

// UserInstanceVNCInfo returns whether legacy WebVNC is available for a user's VM.

// @Summary 用户 实例 VNC 信息
// @Description 获取用户 实例 VNC 信息
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "用户 实例 VNC 信息成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "用户 实例 VNC 信息失败"
// @Router /user/instances/{id}/vnc [get]
func UserInstanceVNCInfo(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}
	if err := trafficService.NewThreeTierLimitService().EnsureUserInstanceOperationAllowed(userID, uint(instanceID), "vnc"); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
		return
	}
	info, err := adminAPI.BuildInstanceVNCInfoForUser(uint(instanceID), userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, info)
}

// UserInstanceVNCWebSocket proxies a VNC TCP stream to WebSocket for noVNC.

// @Summary 用户 实例 VNC Web Socket
// @Description 获取用户 实例 VNC Web Socket
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "用户 实例 VNC Web Socket成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "用户 实例 VNC Web Socket失败"
// @Router /user/instances/{id}/vnc/ws [get]
func UserInstanceVNCWebSocket(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}
	if err := trafficService.NewThreeTierLimitService().EnsureUserInstanceOperationAllowed(userID, uint(instanceID), "vnc"); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
		return
	}
	adminAPI.ProxyInstanceVNCForUser(c, uint(instanceID), userID)
}

// UserInstanceConsoleInfo exposes all console protocols supported by the
// instance without changing the legacy /vnc contract.
func UserInstanceConsoleInfo(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}
	if err := trafficService.NewThreeTierLimitService().EnsureUserInstanceOperationAllowed(userID, uint(instanceID), "vnc"); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
		return
	}
	info, err := adminAPI.BuildInstanceConsoleInfoForUser(uint(instanceID), userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	// SPICE loads a set of same-origin resources in an iframe. Set a short-lived,
	// path-limited cookie after this regular authenticated request so neither the
	// iframe URL nor node-served JavaScript receives the login JWT.
	middleware.IssueConsoleSessionCookie(c, uint(instanceID))
	common.ResponseSuccess(c, info)
}

// UserInstanceConsoleRepair starts the idempotent Incus/LXD SPICE adapter.
func UserInstanceConsoleRepair(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}
	if err := trafficService.NewThreeTierLimitService().EnsureUserInstanceOperationAllowed(userID, uint(instanceID), "vnc"); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
		return
	}
	info, err := adminAPI.RepairInstanceConsoleForUser(uint(instanceID), userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, info)
}

// UserInstanceConsoleWebSocket proxies a selected VNC/SPICE TCP stream.
func UserInstanceConsoleWebSocket(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}
	if err := trafficService.NewThreeTierLimitService().EnsureUserInstanceOperationAllowed(userID, uint(instanceID), "vnc"); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
		return
	}
	adminAPI.ProxyInstanceConsoleForUser(c, uint(instanceID), userID)
}

// UserInstanceConsoleSpiceWebSocket proxies the SPICE websockify stream.
func UserInstanceConsoleSpiceWebSocket(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}
	if err := trafficService.NewThreeTierLimitService().EnsureUserInstanceOperationAllowed(userID, uint(instanceID), "vnc"); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
		return
	}
	adminAPI.ProxyInstanceConsoleSpiceForUser(c, uint(instanceID), userID)
}

// UserInstanceConsoleSpiceAsset serves the node's spice-html5 resources.
func UserInstanceConsoleSpiceAsset(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}
	if err := trafficService.NewThreeTierLimitService().EnsureUserInstanceOperationAllowed(userID, uint(instanceID), "vnc"); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
		return
	}
	adminAPI.ServeInstanceConsoleSpiceAssetForUser(c, uint(instanceID), userID)
}
