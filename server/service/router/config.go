package router

import (
	"oneclickvirt/api/v1/config"
	"oneclickvirt/middleware"
	authModel "oneclickvirt/model/auth"

	"github.com/gin-gonic/gin"
)

// InitConfigRouter 配置路由
func InitConfigRouter(Router *gin.RouterGroup) {
	// 统一配置API
	ConfigGroup := Router.Group("/v1/config")
	ConfigGroup.Use(middleware.RequireAuth(authModel.AuthLevelAdmin))
	{
		ConfigGroup.GET("", config.GetUnifiedConfig)
		ConfigGroup.PUT("", config.UpdateUnifiedConfig)
	}
}
