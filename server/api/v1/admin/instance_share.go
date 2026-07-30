package admin

import (
	"strconv"

	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	shareService "oneclickvirt/service/share"

	"github.com/gin-gonic/gin"
)

type createInstanceShareRequest struct {
	ExpiresInMinutes int `json:"expiresInMinutes"`
}

// CreateAdminInstanceShare 创建管理员实例临时分享链接

// @Summary 创建管理员 实例 共享
// @Description 创建创建管理员 实例 共享
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "创建管理员 实例 共享成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建管理员 实例 共享失败"
// @Router /admin/instances/{id}/share-links [post]
func CreateAdminInstanceShare(c *gin.Context) {
	userID, err := middleware.GetUserIDFromContext(c)
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

	creatorType := shareService.CreatorTypeAdmin
	if middleware.GetOwnerAdminID(c) > 0 {
		creatorType = shareService.CreatorTypeNormalAdmin
	}
	result, err := shareService.NewInstanceShareService().CreateForAdmin(
		userID,
		creatorType,
		middleware.GetOwnerAdminID(c),
		uint(instanceID),
		req.ExpiresInMinutes,
	)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result, "分享链接创建成功")
}
