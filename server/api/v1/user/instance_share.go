package user

import (
	"strconv"

	"oneclickvirt/model/common"
	shareService "oneclickvirt/service/share"

	"github.com/gin-gonic/gin"
)

type createInstanceShareRequest struct {
	ExpiresInMinutes int `json:"expiresInMinutes"`
}

// CreateUserInstanceShare 创建用户实例临时分享链接
func CreateUserInstanceShare(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || instanceID == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	var req createInstanceShareRequest
	_ = c.ShouldBindJSON(&req)

	result, err := shareService.NewInstanceShareService().CreateForUser(userID, uint(instanceID), req.ExpiresInMinutes)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result, "分享链接创建成功")
}
