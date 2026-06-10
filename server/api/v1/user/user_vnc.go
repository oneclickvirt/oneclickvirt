package user

import (
	"strconv"

	adminAPI "oneclickvirt/api/v1/admin"
	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// UserInstanceVNCInfo returns whether WebVNC is available for a user's VM.
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
	info, err := adminAPI.BuildInstanceVNCInfoForUser(uint(instanceID), userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, info)
}

// UserInstanceVNCWebSocket proxies a VNC TCP stream to WebSocket for noVNC.
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
	adminAPI.ProxyInstanceVNCForUser(c, uint(instanceID), userID)
}
