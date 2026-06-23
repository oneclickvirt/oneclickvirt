package admin

import (
	"encoding/json"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	adminProvider "oneclickvirt/service/admin/provider"
	"oneclickvirt/service/provider"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"go.uber.org/zap"
)

func GetProviderList(c *gin.Context) {
	var req admin.ProviderListRequest
	req.Page = 1
	req.PageSize = 10

	if err := c.ShouldBindQuery(&req); err != nil {
		global.APP_LOG.Warn("Provider列表查询参数绑定失败，使用默认值", zap.Error(err))
	}

	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > 100 {
		req.PageSize = 10
	}

	providerService := adminProvider.NewService()
	providers, total, err := providerService.GetProviderList(req, middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccessWithPagination(c, providers, total, req.Page, req.PageSize)
}

// CreateProvider 创建提供商
func CreateProvider(c *gin.Context) {
	var req admin.CreateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("CreateProvider参数绑定失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	providerService := adminProvider.NewService()
	providerObj, err := providerService.CreateProvider(req, middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, providerObj, "创建提供商成功")
}

// UpdateProvider 更新提供商
func UpdateProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	var req admin.UpdateProviderRequest
	body, err := c.GetRawData()
	if err != nil {
		global.APP_LOG.Warn("UpdateProvider读取请求体失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	if err := binding.JSON.BindBody(body, &req); err != nil {
		global.APP_LOG.Warn("UpdateProvider参数绑定失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawFields); err == nil {
		req.ProvidedFields = make(map[string]bool, len(rawFields))
		for key := range rawFields {
			req.ProvidedFields[key] = true
		}
	}

	req.ID = uint(id)

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(req.ID, ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "无权操作该Provider"))
			return
		}
	}

	providerService := adminProvider.NewService()
	if err := providerService.UpdateProvider(req); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	if err := provider.GetProviderService().ReloadProvider(req.ID); err != nil {
		global.APP_LOG.Warn("Provider缓存刷新失败，新配置将在下次重启后生效",
			zap.Uint("providerID", req.ID),
			zap.Error(err))
	}

	common.ResponseSuccess(c, nil, "更新提供商成功")
}

// DeleteProvider 删除提供商
func DeleteProvider(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的提供商ID"))
		return
	}

	forceDelete := c.Query("force") == "true"

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(uint(providerID), ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "无权操作该Provider"))
			return
		}
	}

	providerService := adminProvider.NewService()
	err = providerService.DeleteProvider(uint(providerID), forceDelete)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "删除提供商成功")
}

// FreezeProvider 冻结提供商
func FreezeProvider(c *gin.Context) {
	var req admin.FreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	providerService := adminProvider.NewService()
	if err := providerService.FreezeProvider(req); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "提供商已冻结")
}

// UnfreezeProvider 解冻提供商
func UnfreezeProvider(c *gin.Context) {
	var req admin.UnfreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	providerService := adminProvider.NewService()
	if err := providerService.UnfreezeProvider(req); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "提供商已解冻")
}
