package router

import (
	"oneclickvirt/api/v1/auth"
	"oneclickvirt/api/v1/public"
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
		UserGroup.POST("/user/instances", middleware.RequireKYCFor("create-instance"), user.CreateUserInstance)
		UserGroup.GET("/user/instances/:id", user.GetUserInstanceDetail)
		UserGroup.GET("/user/instances/:id/monitoring", user.GetInstanceMonitoring)
		UserGroup.GET("/user/instances/:id/monitoring/resources", user.GetInstanceResourceMonitoring)
		UserGroup.GET("/user/instances/:id/monitoring/status", user.GetInstanceMonitoringStatus)
		UserGroup.GET("/user/instances/:id/pmacct/summary", user.GetInstancePmacctSummary)
		UserGroup.GET("/user/instances/:id/pmacct/query", user.QueryInstancePmacctData)
		UserGroup.PUT("/user/instances/:id/reset-password", user.ResetInstancePassword)
		UserGroup.GET("/user/instances/:id/password/:taskId", user.GetInstanceNewPassword)
		UserGroup.GET("/user/instances/:id/ports", user.GetInstancePorts)
		UserGroup.GET("/user/instances/:id/ssh", user.SSHWebSocket)   // WebSocket SSH连接
		UserGroup.GET("/user/instances/:id/exec", user.ExecWebSocket) // WebSocket Container Exec
		UserGroup.GET("/user/instances/:id/sftp/list", user.UserSFTPList)
		UserGroup.GET("/user/instances/:id/sftp/download", user.UserSFTPDownload)
		UserGroup.POST("/user/instances/:id/sftp/upload", user.UserSFTPUpload)
		UserGroup.GET("/user/instances/:id/sftp/upload/status", user.UserSFTPUploadStatus)
		UserGroup.POST("/user/instances/:id/sftp/upload/abort", user.UserSFTPUploadAbort)
		UserGroup.POST("/user/instances/action", user.InstanceAction)
		UserGroup.POST("/user/instances/batch-action", user.BatchInstanceAction)

		// 端口映射
		UserGroup.GET("/user/port-mappings", user.GetUserPortMappings)

		// 资源管理
		UserGroup.GET("/user/resources/available", user.GetAvailableResources)
		UserGroup.POST("/user/resources/claim", user.ClaimResource)
		UserGroup.GET("/user/providers/available", user.GetAvailableProviders)
		UserGroup.GET("/user/images", user.GetUserSystemImages)
		UserGroup.GET("/user/images/filtered", user.GetFilteredSystemImages)
		UserGroup.GET("/user/providers/:id/capabilities", user.GetProviderCapabilities)
		UserGroup.GET("/user/providers/:id/gpus", user.GetProviderGPUs) // 获取Provider缓存的GPU/NPU设备列表
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
		UserGroup.GET("/user/traffic/pmacct/:instanceId", trafficAPI.GetPmacctData)
		UserGroup.GET("/user/traffic/history", trafficAPI.GetUserTrafficHistory)
		UserGroup.GET("/user/instances/:id/traffic/history", trafficAPI.GetInstanceTrafficHistory)

		// 仪表盘统计
		UserGroup.GET("/dashboard/stats", public.GetDashboardStats)

		// 兑换码兑换
		UserGroup.POST("/user/redemption-codes/redeem", middleware.RequireKYCFor("redeem-code"), user.RedeemCode)

		// 域名绑定
		UserGroup.GET("/user/domains", user.GetUserDomains)
		UserGroup.POST("/user/domains", middleware.RequireKYCFor("domain-bind"), user.CreateUserDomain)
		UserGroup.PUT("/user/domains/:id", user.UpdateUserDomain)
		UserGroup.DELETE("/user/domains/:id", user.DeleteUserDomain)

		// KYC实名认证
		UserGroup.GET("/user/kyc", user.GetUserKYC)
		UserGroup.POST("/user/kyc", user.SubmitUserKYC)
		UserGroup.POST("/user/kyc/alipay", user.SubmitAlipayKYC)
		UserGroup.GET("/user/kyc/alipay/result", user.QueryAlipayKYCResult)

		// 签到续期
		UserGroup.POST("/user/checkin/code/:instance_id", user.GenerateCheckinCode)
		UserGroup.POST("/user/checkin", user.DoCheckin)
		UserGroup.GET("/user/checkin/records", user.GetCheckinRecords)
		UserGroup.GET("/user/checkin/stats", user.GetCheckinStats)
		UserGroup.POST("/user/checkin/batch", user.BatchCheckin)
		UserGroup.POST("/user/checkin/batch-checkin", user.BatchCheckin)

		// API Token管理
		UserGroup.POST("/user/api-tokens", auth.CreateApiToken)
		UserGroup.GET("/user/api-tokens", auth.GetApiTokenList)
		UserGroup.DELETE("/user/api-tokens/:id", auth.DeleteApiToken)
	}
}
