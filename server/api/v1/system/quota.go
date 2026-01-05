package system

import (
	"oneclickvirt/service/resources"
	"strconv"

	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// GetUserQuotaInfo 获取用户配额信息
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
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, quotaInfo, "获取配额信息成功")
}
