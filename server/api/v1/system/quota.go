package system

import (
	"oneclickvirt/service/resources"
	"strconv"

	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// GetUserQuotaInfo 获取用户配额信息

// @Summary 获取用户 配额 信息
// @Description 获取获取用户 配额 信息
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取用户 配额 信息成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取用户 配额 信息失败"
// @Router /admin/quota/users/{userId} [get]
func GetUserQuotaInfo(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的用户ID"))
		return
	}

	quotaService := resources.NewQuotaService()
	quotaInfo, err := quotaService.GetUserQuotaInfo(uint(userID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, quotaInfo, "获取配额信息成功")
}
