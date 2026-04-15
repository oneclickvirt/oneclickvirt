package admin

import (
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	"oneclickvirt/service/admin"

	"github.com/gin-gonic/gin"
)

var freezeService = admin.NewFreezeManagementService()

// SetUserExpiry 设置用户过期时间
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
func SetProviderExpiry(c *gin.Context) {
	var req adminModel.SetProviderExpiryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}

	if err := freezeService.SetProviderExpiry(req.ProviderID, req.ExpiresAt); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "设置成功")
}

// SetInstanceExpiry 设置实例过期时间
func SetInstanceExpiry(c *gin.Context) {
	var req adminModel.SetInstanceExpiryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}

	if err := freezeService.SetInstanceExpiry(req.InstanceID, req.ExpiresAt); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "设置成功")
}

// FreezeProviderManual 手动冻结Provider
func FreezeProviderManual(c *gin.Context) {
	var req adminModel.FreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}

	if err := freezeService.FreezeProvider(req.ID, req.Reason); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "冻结成功")
}

// FreezeInstance 手动冻结实例
func FreezeInstance(c *gin.Context) {
	var req adminModel.FreezeInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}

	if err := freezeService.FreezeInstance(req.InstanceID, req.Reason); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "冻结成功")
}

// UnfreezeProviderManual 解冻Provider
func UnfreezeProviderManual(c *gin.Context) {
	var req adminModel.UnfreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}

	if err := freezeService.UnfreezeProvider(req.ID); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "解冻成功")
}

// UnfreezeInstance 解冻实例
func UnfreezeInstance(c *gin.Context) {
	var req adminModel.UnfreezeInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误: "+err.Error()))
		return
	}

	if err := freezeService.UnfreezeInstance(req.InstanceID); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "解冻成功")
}
