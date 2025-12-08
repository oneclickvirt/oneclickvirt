package router

import (
	oauth2Api "oneclickvirt/api/v1/oauth2"
	"oneclickvirt/middleware"
	authModel "oneclickvirt/model/auth"

	"github.com/gin-gonic/gin"
)

// InitOAuth2Router OAuth2路由
func InitOAuth2Router(Router *gin.RouterGroup) {
	OAuth2Router := Router.Group("v1/oauth2")
	{
		// 管理员路由（需要管理员权限）
		OAuth2Router.Use(middleware.RequireAuth(authModel.AuthLevelAdmin)).
			GET("providers", oauth2Api.GetProviders).                            // 获取所有提供商
			GET("providers/:id", oauth2Api.GetProvider).                         // 获取单个提供商
			POST("providers", oauth2Api.CreateProvider).                         // 创建提供商
			PUT("providers/:id", oauth2Api.UpdateProvider).                      // 更新提供商
			DELETE("providers/:id", oauth2Api.DeleteProvider).                   // 删除提供商
			POST("providers/:id/reset-count", oauth2Api.ResetRegistrationCount). // 重置注册计数
			GET("presets", oauth2Api.GetPresets).                                // 获取预设配置列表
			GET("presets/:name", oauth2Api.GetPreset)                            // 获取指定预设配置
	}

	// OAuth2认证路由（不需要鉴权）
	AuthRouter := Router.Group("v1/auth/oauth2")
	{
		AuthRouter.GET("login", oauth2Api.OAuth2Login)       // OAuth2登录
		AuthRouter.GET("callback", oauth2Api.OAuth2Callback) // OAuth2回调
	}

	// 公开路由（获取启用的提供商列表）
	PublicRouter := Router.Group("v1/public/oauth2")
	{
		PublicRouter.GET("providers", oauth2Api.GetEnabledProviders) // 获取启用的提供商列表
	}
}
