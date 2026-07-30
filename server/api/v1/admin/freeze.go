package admin

import (
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	"oneclickvirt/service/admin"

	"github.com/gin-gonic/gin"
)

var freezeService = admin.NewFreezeManagementService()

// SetUserExpiry 设置用户过期时间

// @Summary 设置用户 Expiry
// @Description 创建设置用户 Expiry
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "设置用户 Expiry成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "设置用户 Expiry失败"
// @Router /admin/users/set-expiry [post]
func SetUserExpiry(c *gin.Context) {
	var req adminModel.SetUserExpiryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}
	if err := freezeService.SetUserExpiry(req.UserID, req.ExpiresAt); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "设置成功")
}

// SetProviderExpiry 设置Provider过期时间

// @Summary 设置Provider Expiry
// @Description 创建设置Provider Expiry
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "设置Provider Expiry成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "设置Provider Expiry失败"
// @Router /admin/providers/set-expiry [post]
func SetProviderExpiry(c *gin.Context) {
	var req adminModel.SetProviderExpiryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}
	if err := ensureProviderOwner(c, req.ProviderID); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	if err := freezeService.SetProviderExpiry(req.ProviderID, req.ExpiresAt); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "设置成功")
}

// SetInstanceExpiry 设置实例过期时间

// @Summary 设置实例 Expiry
// @Description 创建设置实例 Expiry
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "设置实例 Expiry成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "设置实例 Expiry失败"
// @Router /admin/instances/set-expiry [post]
func SetInstanceExpiry(c *gin.Context) {
	var req adminModel.SetInstanceExpiryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}
	if err := ensureInstanceOwner(c, req.InstanceID); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	if err := freezeService.SetInstanceExpiry(req.InstanceID, req.ExpiresAt); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "设置成功")
}

// FreezeProviderManual 手动冻结Provider

// @Summary 冻结Provider Manual
// @Description 创建冻结Provider Manual
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "冻结Provider Manual成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "冻结Provider Manual失败"
// @Router /admin/providers/freeze-manual [post]
func FreezeProviderManual(c *gin.Context) {
	var req adminModel.FreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}
	if err := ensureProviderOwner(c, req.ID); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	if err := freezeService.FreezeProvider(req.ID, req.Reason); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "冻结成功")
}

// FreezeInstance 手动冻结实例

// @Summary 冻结实例
// @Description 创建冻结实例
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "冻结实例成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "冻结实例失败"
// @Router /admin/instances/freeze [post]
func FreezeInstance(c *gin.Context) {
	var req adminModel.FreezeInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}
	if err := ensureInstanceOwner(c, req.InstanceID); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	if err := freezeService.FreezeInstance(req.InstanceID, req.Reason); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "冻结成功")
}

// UnfreezeProviderManual 解冻Provider

// @Summary 解冻Provider Manual
// @Description 创建解冻Provider Manual
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "解冻Provider Manual成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "解冻Provider Manual失败"
// @Router /admin/providers/unfreeze-manual [post]
func UnfreezeProviderManual(c *gin.Context) {
	var req adminModel.UnfreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}
	if err := ensureProviderOwner(c, req.ID); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	if err := freezeService.UnfreezeProvider(req.ID); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "解冻成功")
}

// UnfreezeInstance 解冻实例

// @Summary 解冻实例
// @Description 创建解冻实例
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "解冻实例成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "解冻实例失败"
// @Router /admin/instances/unfreeze [post]
func UnfreezeInstance(c *gin.Context) {
	var req adminModel.UnfreezeInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}
	if err := ensureInstanceOwner(c, req.InstanceID); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	if err := freezeService.UnfreezeInstance(req.InstanceID); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "解冻成功")
}
