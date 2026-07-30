package admin

import (
	"encoding/json"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	adminProvider "oneclickvirt/service/admin/provider"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"go.uber.org/zap"
)

// @Summary 获取Provider 列表
// @Description 获取获取Provider 列表
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取Provider 列表成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取Provider 列表失败"
// @Router /admin/providers [get]
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

// @Summary 创建Provider
// @Description 创建创建Provider
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "创建Provider成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建Provider失败"
// @Router /admin/providers [post]
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

// @Summary 更新Provider
// @Description 更新更新Provider
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "更新Provider成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新Provider失败"
// @Router /admin/providers/{id} [put]
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

	var responseData interface{}
	message := "更新提供商成功，运行时连接刷新任务已提交"
	requestUserID, userErr := getUserIDFromContext(c)
	if userErr != nil {
		global.APP_LOG.Warn("Provider配置已保存，但无法识别运行时刷新任务管理员",
			zap.Uint("providerID", req.ID), zap.Error(userErr))
		message = "更新提供商成功，但运行时连接刷新任务未能入队"
	} else if reloadTask, taskErr := providerService.CreateRuntimeReloadTask(req.ID, requestUserID); taskErr != nil {
		global.APP_LOG.Warn("Provider配置已保存，但运行时刷新任务入队失败",
			zap.Uint("providerID", req.ID), zap.Error(taskErr))
		message = "更新提供商成功，但运行时连接刷新任务入队失败"
	} else {
		responseData = map[string]interface{}{"runtimeReloadTask": reloadTask}
	}

	common.ResponseSuccess(c, responseData, message)
}

// DeleteProvider 将Provider删除操作提交到管理员后台任务。
// @Summary 提交Provider删除任务
// @Description 创建持久化后台任务清理Provider及其关联资源；force=true时允许强制清理
// @Tags Provider管理
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Param force query bool false "是否强制删除；仅清理数据库，宿主机实例、出口规则和隧道可能残留" default(false)
// @Success 200 {object} common.Response{data=admin.Task} "Provider删除任务已提交"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 401 {object} common.Response "未认证"
// @Failure 403 {object} common.Response "无权操作该Provider"
// @Failure 404 {object} common.Response "Provider不存在"
// @Failure 409 {object} common.Response "已有冲突任务或任务池暂停接收"
// @Router /admin/providers/{id} [delete]
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

	requestUserID, err := getUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "无法识别当前管理员"))
		return
	}
	providerService := adminProvider.NewService()
	deleteTask, err := providerService.CreateDeleteTask(uint(providerID), requestUserID, forceDelete)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, deleteTask, "Provider删除任务已提交，请在管理员任务列表查看进度")
}

// FreezeProvider 冻结提供商

// @Summary 冻结Provider
// @Description 创建冻结Provider
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "冻结Provider成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "冻结Provider失败"
// @Router /admin/providers/freeze [post]
func FreezeProvider(c *gin.Context) {
	var req admin.FreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	if err := ensureProviderOwner(c, req.ID); err != nil {
		common.ResponseWithError(c, err)
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

// @Summary 解冻Provider
// @Description 创建解冻Provider
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "解冻Provider成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "解冻Provider失败"
// @Router /admin/providers/unfreeze [post]
func UnfreezeProvider(c *gin.Context) {
	var req admin.UnfreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	if err := ensureProviderOwner(c, req.ID); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	providerService := adminProvider.NewService()
	if err := providerService.UnfreezeProvider(req); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "提供商已解冻")
}
