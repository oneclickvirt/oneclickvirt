package oauth2

import (
	"oneclickvirt/global"
	"oneclickvirt/model/common"
	oauth2Service "oneclickvirt/service/oauth2"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetProviders 获取所有OAuth2提供商
// @Summary 获取OAuth2提供商列表
// @Description 获取所有OAuth2提供商（包括禁用的）
// @Tags OAuth2
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=[]oauth2.OAuth2Provider}
// @Router /oauth2/providers [get]
func GetProviders(c *gin.Context) {
	providerService := oauth2Service.ProviderService{}
	providers, err := providerService.GetAllProviders()
	if err != nil {
		global.APP_LOG.Error("获取OAuth2提供商列表失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取列表失败"))
		return
	}

	// 隐藏敏感信息
	for i := range providers {
		if providers[i].ClientSecret != "" {
			providers[i].ClientSecret = "********"
		}
	}

	common.ResponseSuccess(c, providers)
}

// GetEnabledProviders 获取启用的OAuth2提供商
// @Summary 获取启用的OAuth2提供商
// @Description 获取所有启用的OAuth2提供商（公开接口）
// @Tags OAuth2
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=[]map[string]interface{}}
// @Router /public/oauth2/providers [get]
func GetEnabledProviders(c *gin.Context) {
	svc := oauth2Service.NewService()
	providers, err := svc.GetAllEnabledProviders()
	if err != nil {
		global.APP_LOG.Error("获取启用的OAuth2提供商失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取列表失败"))
		return
	}

	// 只返回必要的字段
	result := make([]map[string]interface{}, len(providers))
	for i, p := range providers {
		result[i] = map[string]interface{}{
			"id":          p.ID,
			"name":        p.Name,
			"displayName": p.DisplayName,
		}
	}

	common.ResponseSuccess(c, result)
}

// GetProvider 获取指定OAuth2提供商
// @Summary 获取OAuth2提供商详情
// @Description 获取指定OAuth2提供商的详细信息
// @Tags OAuth2
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "提供商ID"
// @Success 200 {object} common.Response{data=oauth2.OAuth2Provider}
// @Router /oauth2/providers/{id} [get]
func GetProvider(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	providerService := oauth2Service.ProviderService{}
	provider, err := providerService.GetProvider(uint(id))
	if err != nil {
		if appErr, ok := err.(*common.AppError); ok {
			common.ResponseWithError(c, appErr)
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取提供商失败"))
		}
		return
	}

	// 隐藏敏感信息
	provider.ClientSecret = "********"

	common.ResponseSuccess(c, provider)
}

// CreateProvider 创建OAuth2提供商
// @Summary 创建OAuth2提供商
// @Description 创建新的OAuth2提供商配置
// @Tags OAuth2
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body oauth2Service.CreateProviderRequest true "提供商配置"
// @Success 200 {object} common.Response{data=oauth2.OAuth2Provider}
// @Router /oauth2/providers [post]
func CreateProvider(c *gin.Context) {
	var req oauth2Service.CreateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	// 设置默认值
	if req.UserIDField == "" {
		req.UserIDField = "id"
	}
	if req.UsernameField == "" {
		req.UsernameField = "username"
	}
	if req.EmailField == "" {
		req.EmailField = "email"
	}
	if req.AvatarField == "" {
		req.AvatarField = "avatar"
	}
	if req.DefaultLevel == 0 {
		req.DefaultLevel = 1
	}

	providerService := oauth2Service.ProviderService{}
	provider, err := providerService.CreateProvider(&req)
	if err != nil {
		if appErr, ok := err.(*common.AppError); ok {
			common.ResponseWithError(c, appErr)
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "创建提供商失败"))
		}
		return
	}

	global.APP_LOG.Info("创建OAuth2提供商", zap.String("name", req.Name))
	common.ResponseSuccess(c, provider, "创建成功")
}

// UpdateProvider 更新OAuth2提供商
// @Summary 更新OAuth2提供商
// @Description 更新OAuth2提供商配置
// @Tags OAuth2
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "提供商ID"
// @Param request body oauth2Service.UpdateProviderRequest true "更新内容"
// @Success 200 {object} common.Response
// @Router /oauth2/providers/{id} [put]
func UpdateProvider(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var req oauth2Service.UpdateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	providerService := oauth2Service.ProviderService{}
	if err := providerService.UpdateProvider(uint(id), &req); err != nil {
		if appErr, ok := err.(*common.AppError); ok {
			common.ResponseWithError(c, appErr)
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "更新提供商失败"))
		}
		return
	}

	global.APP_LOG.Info("更新OAuth2提供商", zap.Uint64("id", id))
	common.ResponseSuccess(c, nil, "更新成功")
}

// DeleteProvider 删除OAuth2提供商
// @Summary 删除OAuth2提供商
// @Description 删除OAuth2提供商（如有用户使用则无法删除）
// @Tags OAuth2
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "提供商ID"
// @Success 200 {object} common.Response
// @Router /oauth2/providers/{id} [delete]
func DeleteProvider(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	providerService := oauth2Service.ProviderService{}
	if err := providerService.DeleteProvider(uint(id)); err != nil {
		if appErr, ok := err.(*common.AppError); ok {
			common.ResponseWithError(c, appErr)
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "删除提供商失败"))
		}
		return
	}

	global.APP_LOG.Info("删除OAuth2提供商", zap.Uint64("id", id))
	common.ResponseSuccess(c, nil, "删除成功")
}

// ResetRegistrationCount 重置注册计数
// @Summary 重置注册计数
// @Description 重置OAuth2提供商的注册计数
// @Tags OAuth2
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "提供商ID"
// @Success 200 {object} common.Response
// @Router /oauth2/providers/{id}/reset-count [post]
func ResetRegistrationCount(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	providerService := oauth2Service.ProviderService{}
	if err := providerService.ResetRegistrationCount(uint(id)); err != nil {
		if appErr, ok := err.(*common.AppError); ok {
			common.ResponseWithError(c, appErr)
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "重置计数失败"))
		}
		return
	}

	global.APP_LOG.Info("重置OAuth2注册计数", zap.Uint64("id", id))
	common.ResponseSuccess(c, nil, "重置成功")
}

// GetPresets 获取OAuth2预设配置列表
// @Summary 获取OAuth2预设配置
// @Description 获取所有可用的OAuth2预设配置（linuxdo, idcflare, github, generic）
// @Tags OAuth2
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=map[string]oauth2Service.PresetConfig}
// @Router /oauth2/presets [get]
func GetPresets(c *gin.Context) {
	presets := oauth2Service.GetPresetConfigs()
	common.ResponseSuccess(c, presets)
}

// GetPreset 获取指定的OAuth2预设配置
// @Summary 获取指定预设配置
// @Description 获取指定名称的OAuth2预设配置详情
// @Tags OAuth2
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "预设名称" Enums(linuxdo, idcflare, github, generic)
// @Success 200 {object} common.Response{data=oauth2Service.PresetConfig}
// @Router /oauth2/presets/{name} [get]
func GetPreset(c *gin.Context) {
	name := c.Param("name")
	preset, exists := oauth2Service.GetPresetConfig(name)
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "预设配置不存在"))
		return
	}
	common.ResponseSuccess(c, preset)
}
