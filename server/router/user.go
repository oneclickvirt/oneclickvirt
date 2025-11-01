package router

import (
	"oneclickvirt/api/v1/admin"
	"oneclickvirt/api/v1/public"
	"oneclickvirt/api/v1/system"
	"oneclickvirt/api/v1/traffic"
	"oneclickvirt/api/v1/user"
	"oneclickvirt/middleware"
	authModel "oneclickvirt/model/auth"

	"github.com/gin-gonic/gin"
)

// InitUserRouter 用户路由
func InitUserRouter(Router *gin.RouterGroup) {
	UserGroup := Router.Group("/v1")
	UserGroup.Use(middleware.RequireAuth(authModel.AuthLevelUser))
	{
		// 用户管理
		UserGroup.GET("/user/profile", user.GetUserInfo)
		UserGroup.PUT("/user/profile", user.UpdateProfile)
		UserGroup.PUT("/user/reset-password", user.UserResetPassword)
		UserGroup.GET("/user/info", user.GetUserInfo)
		UserGroup.GET("/user/dashboard", user.GetUserDashboard)
		UserGroup.GET("/user/limits", user.GetUserLimits)

		// 实例管理
		UserGroup.GET("/user/instances", user.GetUserInstances)
		UserGroup.POST("/user/instances", user.CreateUserInstance)
		UserGroup.GET("/user/instances/:id", user.GetUserInstanceDetail)
		UserGroup.GET("/user/instances/:id/monitoring", user.GetInstanceMonitoring)
		UserGroup.GET("/user/instances/:id/vnstat/summary", user.GetInstanceVnStatSummary)
		UserGroup.GET("/user/instances/:id/vnstat/query", user.QueryInstanceVnStatData)
		UserGroup.GET("/user/instances/:id/vnstat/interfaces", user.GetInstanceVnStatInterfaces)
		UserGroup.GET("/user/instances/:id/vnstat/dashboard", user.GetInstanceVnStatDashboard)
		UserGroup.PUT("/user/instances/:id/reset-password", user.ResetInstancePassword)
		UserGroup.GET("/user/instances/:id/password/:taskId", user.GetInstanceNewPassword)
		UserGroup.GET("/user/instances/:id/ports", user.GetInstancePorts)
		UserGroup.GET("/user/instances/:id/ssh", user.SSHWebSocket) // WebSocket SSH连接
		UserGroup.POST("/user/instances/action", user.InstanceAction)

		// 端口映射
		UserGroup.GET("/user/port-mappings", user.GetUserPortMappings)

		// 资源管理
		UserGroup.GET("/user/resources/available", user.GetAvailableResources)
		UserGroup.POST("/user/resources/claim", user.ClaimResource)
		UserGroup.GET("/user/providers/available", user.GetAvailableProviders)
		UserGroup.GET("/user/images", user.GetUserSystemImages)
		UserGroup.GET("/user/images/filtered", user.GetFilteredSystemImages)
		UserGroup.GET("/user/providers/:id/capabilities", user.GetProviderCapabilities)
		UserGroup.GET("/user/instance-type-permissions", user.GetInstanceTypePermissions)
		UserGroup.GET("/user/instance-config", user.GetInstanceConfig)

		// 任务管理
		UserGroup.GET("/user/tasks", user.GetUserTasks)
		UserGroup.POST("/user/tasks/:taskId/cancel", user.CancelUserTask)

		// 流量统计API
		trafficAPI := &traffic.UserTrafficAPI{}
		UserGroup.GET("/user/traffic/overview", trafficAPI.GetTrafficOverview)
		UserGroup.GET("/user/traffic/instance/:instanceId", trafficAPI.GetInstanceTrafficDetail)
		UserGroup.GET("/user/traffic/instances", trafficAPI.GetInstancesTrafficSummary)
		UserGroup.GET("/user/traffic/limit-status", trafficAPI.GetTrafficLimitStatus)
		UserGroup.GET("/user/traffic/vnstat/:instanceId", trafficAPI.GetVnStatData)

		// 文件上传
		uploadGroup := UserGroup.Group("/upload")
		uploadGroup.Use(middleware.AvatarUploadLimit()) // 上传大小限制
		{
			uploadGroup.POST("/avatar", system.UploadAvatar)
		}

		// 仪表盘统计
		UserGroup.GET("/dashboard/stats", public.GetDashboardStats)

		// 资源管理（普通用户只能管理自己的资源）
		UserGroup.GET("/instances", user.GetUserInstances)
		UserGroup.POST("/instances", user.CreateUserInstance)
		UserGroup.PUT("/instances/:id", admin.UpdateInstance)
		UserGroup.DELETE("/instances/:id", admin.DeleteInstance)
	}
}
