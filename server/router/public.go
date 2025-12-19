package router

import (
	"oneclickvirt/api/v1/public"
	"oneclickvirt/api/v1/system"

	"github.com/gin-gonic/gin"
)

// InitPublicRouter 公开路由
func InitPublicRouter(Router *gin.RouterGroup) {
	PublicRouter := Router.Group("v1/public")
	{
		PublicRouter.GET("announcements", system.GetAnnouncement)
		PublicRouter.GET("stats", public.GetDashboardStats)
		// init/check, init, test-db-connection, recommended-db-type 已在 setup.go 中单独注册（不需要数据库健康检查）
		PublicRouter.GET("register-config", public.GetRegisterConfig)
		PublicRouter.GET("system-config", public.GetPublicSystemConfig)
		PublicRouter.GET("system-images/available", system.GetAvailableSystemImages)
	}

	StaticRouter := Router.Group("v1/static")
	{
		StaticRouter.GET(":type/*path", system.ServeStaticFile)
	}
}
