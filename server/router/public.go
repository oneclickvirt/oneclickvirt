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
		PublicRouter.GET("init/check", public.CheckInit)
		PublicRouter.POST("init", public.InitSystem)
		PublicRouter.POST("test-db-connection", public.TestDatabaseConnection)
		PublicRouter.GET("register-config", public.GetRegisterConfig)
		PublicRouter.GET("system-config", public.GetPublicSystemConfig)
		PublicRouter.GET("recommended-db-type", public.GetRecommendedDatabaseType)
		PublicRouter.GET("system-images/available", system.GetAvailableSystemImages)
	}

	StaticRouter := Router.Group("v1/static")
	{
		StaticRouter.GET(":type/*path", system.ServeStaticFile)
	}
}
