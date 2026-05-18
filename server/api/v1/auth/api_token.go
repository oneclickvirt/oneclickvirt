package auth

import (
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	auth2 "oneclickvirt/service/auth"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ── 用户API Token管理 ────────────────────────────────────────────────

// CreateApiToken 用户创建自己的API Token
// @Summary 创建API Token
// @Description 用户创建自己的API访问令牌
// @Tags API Token管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body auth.ApiTokenCreateRequest true "创建Token请求"
// @Success 200 {object} common.Response{data=auth.ApiTokenCreateResponse} "创建成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建失败"
// @Router /user/api-tokens [post]
func CreateApiToken(c *gin.Context) {
	var req auth.ApiTokenCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}

	if req.Name == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Token名称不能为空"))
		return
	}

	authCtx, ok := middleware.GetAuthContext(c)
	if !ok {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未认证"))
		return
	}

	service := auth2.ApiTokenService{}
	result, err := service.CreateToken(authCtx.UserID, authCtx.Username, authCtx.UserType, req)
	if err != nil {
		global.APP_LOG.Error("创建API Token失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, result, "API Token创建成功")
}

// GetApiTokenList 用户获取自己的API Token列表
// @Summary 获取API Token列表
// @Description 用户获取自己的API Token列表
// @Tags API Token管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param keyword query string false "搜索关键字"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 500 {object} common.Response "获取失败"
// @Router /user/api-tokens [get]
func GetApiTokenList(c *gin.Context) {
	authCtx, ok := middleware.GetAuthContext(c)
	if !ok {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未认证"))
		return
	}

	var req auth.ApiTokenListRequest
	req.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	req.PageSize, _ = strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	req.Keyword = c.Query("keyword")

	service := auth2.ApiTokenService{}
	tokens, total, err := service.GetTokenList(authCtx.UserID, req)
	if err != nil {
		global.APP_LOG.Error("获取API Token列表失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"items": tokens,
		"total": total,
	}, "获取成功")
}

// DeleteApiToken 用户删除自己的API Token
// @Summary 删除API Token
// @Description 用户删除（禁用）自己的API Token
// @Tags API Token管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Token ID"
// @Success 200 {object} common.Response "删除成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "删除失败"
// @Router /user/api-tokens/:id [delete]
func DeleteApiToken(c *gin.Context) {
	authCtx, ok := middleware.GetAuthContext(c)
	if !ok {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未认证"))
		return
	}

	tokenID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Token ID"))
		return
	}

	service := auth2.ApiTokenService{}
	if err := service.DeleteToken(authCtx.UserID, uint(tokenID)); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "API Token已删除")
}

// ── 管理员API Token管理 ──────────────────────────────────────────────

// AdminGetApiTokenList 管理员获取所有API Token列表
// @Summary 管理员获取API Token列表
// @Description 管理员获取所有用户的API Token列表
// @Tags API Token管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param keyword query string false "搜索关键字"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/api-tokens [get]
func AdminGetApiTokenList(c *gin.Context) {
	var req auth.ApiTokenListRequest
	req.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	req.PageSize, _ = strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	req.Keyword = c.Query("keyword")

	service := auth2.ApiTokenService{}
	tokens, total, err := service.GetAdminTokenList(req)
	if err != nil {
		global.APP_LOG.Error("管理员获取API Token列表失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"items": tokens,
		"total": total,
	}, "获取成功")
}

// AdminDeleteApiToken 管理员删除任意API Token
// @Summary 管理员删除API Token
// @Description 管理员删除（禁用）任意API Token
// @Tags API Token管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Token ID"
// @Success 200 {object} common.Response "删除成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "删除失败"
// @Router /admin/api-tokens/:id [delete]
func AdminDeleteApiToken(c *gin.Context) {
	tokenID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Token ID"))
		return
	}

	service := auth2.ApiTokenService{}
	if err := service.AdminDeleteToken(uint(tokenID)); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "API Token已删除")
}
