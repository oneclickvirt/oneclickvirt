package router

import (
	"oneclickvirt/api/v1/provider"
	"oneclickvirt/api/v1/system"
	"oneclickvirt/middleware"
	authModel "oneclickvirt/model/auth"

	"github.com/gin-gonic/gin"
)

// InitResourceRouter 基于资源的细粒度权限路由
func InitResourceRouter(Router *gin.RouterGroup) {
	// 基于资源的细粒度权限路由（虚拟化资源）
	ResourceGroup := Router.Group("/v1/resources")
	ResourceGroup.Use(middleware.RequireAuth(authModel.AuthLevelUser))
	{
		// 虚拟化资源管理，使用基于资源的权限验证
		VirtualizationGroup := ResourceGroup.Group("/virtualization")
		VirtualizationGroup.Use(middleware.RequireResourcePermission("virtualization"))
		{
			VirtualizationGroup.GET("/providers", system.GetProviders)
		}
	}
}

// InitProviderRouter Provider API路由
func InitProviderRouter(Router *gin.RouterGroup) {
	ProviderGroup := Router.Group("/v1/providers")
	ProviderGroup.Use(middleware.RequireAuth(authModel.AuthLevelUser))
	{
		providerApi := &provider.ProviderApi{}
		ProviderGroup.GET("/", providerApi.GetProviders)
		ProviderGroup.POST("/connect", providerApi.ConnectProvider)

		// 动态Provider路由 - 使用Provider ID
		DynamicProviderGroup := ProviderGroup.Group("/:id")
		{
			DynamicProviderGroup.GET("/status", providerApi.GetProviderStatus)
			DynamicProviderGroup.GET("/capabilities", providerApi.GetProviderCapabilities)
			DynamicProviderGroup.GET("/instances", providerApi.ListInstances)
			DynamicProviderGroup.POST("/instances", providerApi.CreateInstance)
			DynamicProviderGroup.GET("/instances/:name", providerApi.GetInstance)
			DynamicProviderGroup.POST("/instances/:name/start", providerApi.StartInstance)
			DynamicProviderGroup.POST("/instances/:name/stop", providerApi.StopInstance)
			DynamicProviderGroup.DELETE("/instances/:name", providerApi.DeleteInstance)
			DynamicProviderGroup.GET("/images", providerApi.ListImages)
			DynamicProviderGroup.POST("/images/pull", providerApi.PullImage)
			DynamicProviderGroup.DELETE("/images/:image", providerApi.DeleteImage)
		}
	}
}
