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

// @Summary 创建用户 实例 共享
// @Description 创建创建用户 实例 共享
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "创建用户 实例 共享成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建用户 实例 共享失败"
// @Router /user/instances/{id}/share-links [post]
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
