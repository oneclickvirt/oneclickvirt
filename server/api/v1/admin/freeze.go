package admin

import (
	"net/http"

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
		c.JSON(http.StatusBadRequest, common.Response{
			Code: common.CodeInvalidParam,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	if err := freezeService.SetUserExpiry(req.UserID, req.ExpiresAt); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: common.CodeInternalError,
			Msg:  "设置用户过期时间失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "设置成功",
	})
}

// SetProviderExpiry 设置Provider过期时间
func SetProviderExpiry(c *gin.Context) {
	var req adminModel.SetProviderExpiryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: common.CodeInvalidParam,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	if err := freezeService.SetProviderExpiry(req.ProviderID, req.ExpiresAt); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: common.CodeInternalError,
			Msg:  "设置Provider过期时间失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "设置成功",
	})
}

// SetInstanceExpiry 设置实例过期时间
func SetInstanceExpiry(c *gin.Context) {
	var req adminModel.SetInstanceExpiryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: common.CodeInvalidParam,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	if err := freezeService.SetInstanceExpiry(req.InstanceID, req.ExpiresAt); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: common.CodeInternalError,
			Msg:  "设置实例过期时间失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "设置成功",
	})
}

// FreezeUser 手动冻结用户
func FreezeUser(c *gin.Context) {
	var req adminModel.FreezeUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: common.CodeInvalidParam,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	if err := freezeService.FreezeUser(req.UserID, req.Reason); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: common.CodeInternalError,
			Msg:  "冻结用户失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "冻结成功",
	})
}

// FreezeProviderManual 手动冻结Provider
func FreezeProviderManual(c *gin.Context) {
	var req adminModel.FreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: common.CodeInvalidParam,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	if err := freezeService.FreezeProvider(req.ID, req.Reason); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: common.CodeInternalError,
			Msg:  "冻结Provider失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "冻结成功",
	})
}

// FreezeInstance 手动冻结实例
func FreezeInstance(c *gin.Context) {
	var req adminModel.FreezeInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: common.CodeInvalidParam,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	if err := freezeService.FreezeInstance(req.InstanceID, req.Reason); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: common.CodeInternalError,
			Msg:  "冻结实例失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "冻结成功",
	})
}

// UnfreezeUser 解冻用户
func UnfreezeUser(c *gin.Context) {
	var req adminModel.UnfreezeUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: common.CodeInvalidParam,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	if err := freezeService.UnfreezeUser(req.UserID); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: common.CodeInternalError,
			Msg:  "解冻用户失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "解冻成功",
	})
}

// UnfreezeProviderManual 解冻Provider
func UnfreezeProviderManual(c *gin.Context) {
	var req adminModel.UnfreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: common.CodeInvalidParam,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	if err := freezeService.UnfreezeProvider(req.ID); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: common.CodeInternalError,
			Msg:  "解冻Provider失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "解冻成功",
	})
}

// UnfreezeInstance 解冻实例
func UnfreezeInstance(c *gin.Context) {
	var req adminModel.UnfreezeInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: common.CodeInvalidParam,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	if err := freezeService.UnfreezeInstance(req.InstanceID); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: common.CodeInternalError,
			Msg:  "解冻实例失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "解冻成功",
	})
}
