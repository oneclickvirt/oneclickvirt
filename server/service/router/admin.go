package router

import (
	"oneclickvirt/api/v1/admin"
	"oneclickvirt/api/v1/config"
	"oneclickvirt/api/v1/system"
	"oneclickvirt/api/v1/traffic"
	"oneclickvirt/middleware"

	"github.com/gin-gonic/gin"
)

// InitAdminRouter 管理员路由
func InitAdminRouter(Router *gin.RouterGroup) {
	// 普通管理员和超管都可以访问的路由（level >= 2）
	NormalAdminGroup := Router.Group("/v1/admin")
	NormalAdminGroup.Use(middleware.RequireNormalAdmin())
	{
		// 仪表盘
		NormalAdminGroup.GET("/dashboard", admin.GetAdminDashboard)

		// 实例管理
		NormalAdminGroup.GET("/instances", admin.GetInstanceList)
		NormalAdminGroup.GET("/instances/:id", admin.GetInstanceDetail)
		NormalAdminGroup.POST("/instances", admin.CreateInstance)
		NormalAdminGroup.PUT("/instances/:id", admin.UpdateInstance)
		NormalAdminGroup.DELETE("/instances/:id", admin.DeleteInstance)
		NormalAdminGroup.POST("/instances/:id/action", admin.AdminInstanceAction)
		NormalAdminGroup.PUT("/instances/:id/reset-password", admin.ResetInstancePassword)
		NormalAdminGroup.GET("/instances/:id/password/:taskId", admin.GetInstanceNewPassword)
		NormalAdminGroup.GET("/instances/:id/ssh", admin.AdminSSHWebSocket)

		// 兑换码管理
		NormalAdminGroup.GET("/redemption-codes", admin.GetRedemptionCodeList)
		NormalAdminGroup.POST("/redemption-codes/batch-create", admin.BatchCreateRedemptionCodes)
		NormalAdminGroup.POST("/redemption-codes/export", admin.ExportRedemptionCodes)
		NormalAdminGroup.POST("/redemption-codes/batch-delete", admin.BatchDeleteRedemptionCodes)

		// Provider管理
		NormalAdminGroup.GET("/providers", admin.GetProviderList)
		NormalAdminGroup.POST("/providers", admin.CreateProvider)
		NormalAdminGroup.GET("/providers/:id", admin.GetProviderDetail)
		NormalAdminGroup.PUT("/providers/:id", admin.UpdateProvider)
		NormalAdminGroup.DELETE("/providers/:id", admin.DeleteProvider)
		NormalAdminGroup.POST("/providers/freeze", admin.FreezeProvider)
		NormalAdminGroup.POST("/providers/unfreeze", admin.UnfreezeProvider)
		NormalAdminGroup.POST("/providers/test-ssh-connection", admin.TestSSHConnection)
		NormalAdminGroup.GET("/providers/check-name", admin.CheckProviderName)
		NormalAdminGroup.GET("/providers/check-endpoint", admin.CheckProviderEndpoint)

		// Provider实例发现与导入
		NormalAdminGroup.POST("/providers/:id/discover", admin.DiscoverProviderInstances)
		NormalAdminGroup.POST("/providers/:id/import", admin.ImportProviderInstances)
		NormalAdminGroup.GET("/providers/:id/orphaned", admin.GetOrphanedInstances)
		NormalAdminGroup.POST("/providers/:id/sync-check", admin.CheckInstanceSync)

		// 证书管理
		NormalAdminGroup.POST("/providers/:id/generate-cert", admin.GenerateProviderCert)
		NormalAdminGroup.POST("/providers/:id/auto-configure-stream", admin.AutoConfigureProviderStream)
		NormalAdminGroup.POST("/providers/:id/health-check", admin.CheckProviderHealth)
		NormalAdminGroup.GET("/providers/:id/status", admin.GetProviderStatus)
		NormalAdminGroup.GET("/providers/:id/detect-gpus", admin.DetectGPUs)
		NormalAdminGroup.GET("/providers/:id/stopped-containers", admin.GetStoppedContainers)
		NormalAdminGroup.GET("/providers/:id/agent-secret", admin.GenerateAgentSecret)
		NormalAdminGroup.POST("/providers/:id/agent-secret", admin.GenerateAgentSecret)
		NormalAdminGroup.POST("/providers/:id/exec", admin.ExecOnProvider)
		NormalAdminGroup.GET("/providers/:id/terminal", admin.AdminProviderTerminal)

		// 配置导出
		NormalAdminGroup.POST("/providers/export-configs", admin.ExportProviderConfigs)

		// 配置任务管理
		NormalAdminGroup.POST("/providers/auto-configure", config.AutoConfigureProvider)
		NormalAdminGroup.GET("/configuration-tasks", config.GetConfigurationTasks)
		NormalAdminGroup.GET("/configuration-tasks/:id", config.GetConfigurationTaskDetail)
		NormalAdminGroup.POST("/configuration-tasks/:id/cancel", config.CancelConfigurationTask)

		// 用户任务管理
		NormalAdminGroup.GET("/tasks", admin.GetAdminTasks)
		NormalAdminGroup.GET("/tasks/:taskId", admin.GetTaskDetail)
		NormalAdminGroup.POST("/tasks/force-stop", admin.ForceStopTask)
		NormalAdminGroup.GET("/tasks/stats", admin.GetTaskStats)
		NormalAdminGroup.GET("/tasks/overall-stats", admin.GetTaskOverallStats)
		NormalAdminGroup.POST("/tasks/:taskId/cancel", admin.CancelUserTaskByAdmin)

		// 端口映射管理
		NormalAdminGroup.GET("/port-mappings", admin.GetPortMappingList)
		NormalAdminGroup.POST("/port-mappings", admin.CreatePortMapping)
		NormalAdminGroup.DELETE("/port-mappings/:id", admin.DeletePortMapping)
		NormalAdminGroup.POST("/port-mappings/batch-delete", admin.BatchDeletePortMapping)
		NormalAdminGroup.POST("/port-mappings/sync", admin.SyncPortMappings)
		NormalAdminGroup.POST("/ports/check", admin.CheckPortAvailability)
		NormalAdminGroup.PUT("/providers/:id/port-config", admin.UpdateProviderPortConfig)
		NormalAdminGroup.GET("/providers/:id/port-usage", admin.GetProviderPortUsage)
		NormalAdminGroup.GET("/instances/:id/port-mappings", admin.GetInstancePortMappings)

		// IPv4地址池管理
		NormalAdminGroup.GET("/providers/:id/ipv4-pool", admin.GetProviderIPv4Pool)
		NormalAdminGroup.POST("/providers/:id/ipv4-pool", admin.SetProviderIPv4Pool)
		NormalAdminGroup.DELETE("/providers/:id/ipv4-pool", admin.ClearProviderIPv4Pool)
		NormalAdminGroup.DELETE("/providers/:id/ipv4-pool/:entry_id", admin.DeleteProviderIPv4PoolEntry)

		// 流量管理API
		adminTrafficAPI := &traffic.AdminTrafficAPI{}
		NormalAdminGroup.GET("/traffic/overview", adminTrafficAPI.GetSystemTrafficOverview)
		NormalAdminGroup.GET("/traffic/provider/:providerId", adminTrafficAPI.GetProviderTrafficStats)
		NormalAdminGroup.GET("/traffic/user/:userId", adminTrafficAPI.GetUserTrafficStats)
		NormalAdminGroup.GET("/traffic/users/rank", adminTrafficAPI.GetAllUsersTrafficRank)
		NormalAdminGroup.POST("/traffic/manage", adminTrafficAPI.ManageTrafficLimits)
		NormalAdminGroup.POST("/traffic/batch-manage", adminTrafficAPI.BatchManageTrafficLimits)
		NormalAdminGroup.POST("/traffic/batch-sync", adminTrafficAPI.BatchSyncUserTraffic)
		NormalAdminGroup.DELETE("/traffic/user/:userId/clear", adminTrafficAPI.ClearUserTrafficRecords)

		// 流量历史API
		NormalAdminGroup.GET("/providers/:id/traffic/history", traffic.GetProviderTrafficHistory)

		// 流量监控管理
		NormalAdminGroup.POST("/providers/traffic-monitor", admin.TrafficMonitorOperation)
		NormalAdminGroup.GET("/providers/traffic-monitor/tasks", admin.GetTrafficMonitorTaskList)
		NormalAdminGroup.GET("/providers/traffic-monitor/tasks/:id", admin.GetTrafficMonitorTaskDetail)
		NormalAdminGroup.GET("/providers/traffic-monitor/latest", admin.GetLatestTrafficMonitorTask)

		// Agent监控管理
		NormalAdminGroup.GET("/providers/:id/monitoring/config", admin.GetMonitoringConfig)
		NormalAdminGroup.PUT("/providers/:id/monitoring/config", admin.UpdateMonitoringConfig)
		NormalAdminGroup.POST("/providers/:id/monitoring/agent", admin.DeployAgent)
		NormalAdminGroup.DELETE("/providers/:id/monitoring/agent", admin.UninstallAgent)
		NormalAdminGroup.GET("/providers/:id/monitoring/status", admin.GetAgentStatus)
		NormalAdminGroup.GET("/providers/:id/monitoring/monitors", admin.GetProviderMonitors)
		NormalAdminGroup.POST("/providers/:id/monitoring/sync", admin.SyncProviderMonitors)
		NormalAdminGroup.DELETE("/providers/:id/monitoring/clear", admin.ClearProviderMonitors)
		NormalAdminGroup.GET("/providers/:id/monitoring/agent-monitors", admin.ListAgentMonitors)
		NormalAdminGroup.GET("/providers/:id/monitoring/resources", admin.GetProviderResourceSummary)
		NormalAdminGroup.GET("/instances/:id/monitoring/resources", admin.GetInstanceResources)

		// 硬件报告
		NormalAdminGroup.POST("/providers/:id/hardware-report", admin.SaveHardwareReport)
		NormalAdminGroup.GET("/providers/:id/hardware-report", admin.GetHardwareTestReport)
		NormalAdminGroup.DELETE("/providers/:id/hardware-report", admin.DeleteHardwareReport)

		// 冻结管理
		NormalAdminGroup.POST("/providers/set-expiry", admin.SetProviderExpiry)
		NormalAdminGroup.POST("/providers/freeze-manual", admin.FreezeProviderManual)
		NormalAdminGroup.POST("/providers/unfreeze-manual", admin.UnfreezeProviderManual)
		NormalAdminGroup.POST("/instances/set-expiry", admin.SetInstanceExpiry)
		NormalAdminGroup.POST("/instances/freeze", admin.FreezeInstance)
		NormalAdminGroup.POST("/instances/unfreeze", admin.UnfreezeInstance)

		// 防火墙/滥用屏蔽管理
		NormalAdminGroup.GET("/block-rules", admin.GetBlockRules)
		NormalAdminGroup.GET("/block-rules/:id", admin.GetBlockRule)
		NormalAdminGroup.POST("/block-rules", admin.CreateBlockRule)
		NormalAdminGroup.PUT("/block-rules/:id", admin.UpdateBlockRule)
		NormalAdminGroup.DELETE("/block-rules/:id", admin.DeleteBlockRule)
		NormalAdminGroup.POST("/block-rules/apply", admin.ApplyBlockRules)
		NormalAdminGroup.POST("/block-rules/remove", admin.RemoveBlockRuleApplications)
		NormalAdminGroup.GET("/block-rules/applications", admin.GetBlockRuleApplications)
		NormalAdminGroup.GET("/providers/:id/block-status", admin.GetProviderBlockStatus)
		NormalAdminGroup.GET("/block-rules/agent-providers", admin.GetAgentEnabledProviders)

		// 域名管理
		NormalAdminGroup.GET("/domains", admin.AdminGetDomains)
		NormalAdminGroup.DELETE("/domains/:id", admin.AdminDeleteDomain)
		NormalAdminGroup.GET("/providers/:id/domain-config", admin.GetDomainConfig)
		NormalAdminGroup.PUT("/providers/:id/domain-config", admin.UpdateDomainConfig)

		// KYC管理
		NormalAdminGroup.GET("/kyc", admin.AdminGetKYCList)
		NormalAdminGroup.PUT("/kyc/:id/review", admin.AdminReviewKYC)

		// 签到配置管理
		NormalAdminGroup.GET("/providers/:id/checkin-config", admin.AdminGetCheckinConfig)
		NormalAdminGroup.PUT("/providers/:id/checkin-config", admin.AdminUpdateCheckinConfig)

		// 管理员分组管理（普通管理员管理自己的分组信息）
		NormalAdminGroup.GET("/group-info", admin.GetAdminGroupInfo)
		NormalAdminGroup.PUT("/group-info", admin.UpdateAdminGroupInfo)
	}

	// 超级管理员专用路由（仅admin用户类型，排除normal_admin）
	SuperAdminGroup := Router.Group("/v1/admin")
	SuperAdminGroup.Use(middleware.RequireSuperAdmin())
	{
		// 系统配置（超管专用）
		SuperAdminGroup.GET("/config", config.GetUnifiedConfig)
		SuperAdminGroup.PUT("/config", config.UpdateUnifiedConfig)

		// 系统镜像管理（超管专用）
		SuperAdminGroup.GET("/system-images", system.GetSystemImageList)
		SuperAdminGroup.POST("/system-images", system.CreateSystemImage)
		SuperAdminGroup.PUT("/system-images/:id", system.UpdateSystemImage)
		SuperAdminGroup.DELETE("/system-images/:id", system.DeleteSystemImage)
		SuperAdminGroup.POST("/system-images/batch-delete", system.BatchDeleteSystemImages)
		SuperAdminGroup.PUT("/system-images/batch-status", system.BatchUpdateSystemImageStatus)

		// 用户管理（超管专用）
		SuperAdminGroup.GET("/users", admin.GetUserList)
		SuperAdminGroup.POST("/users", admin.CreateUser)
		SuperAdminGroup.PUT("/users/:id", admin.UpdateUser)
		SuperAdminGroup.DELETE("/users/:id", admin.DeleteUser)
		SuperAdminGroup.PUT("/users/:id/status", admin.UpdateUserStatus)
		SuperAdminGroup.PUT("/users/:id/level", admin.UpdateUserLevel)
		SuperAdminGroup.PUT("/users/:id/reset-password", admin.ResetUserPassword)
		SuperAdminGroup.PUT("/users/batch-level", admin.AdminBatchUpdateUserLevel)
		SuperAdminGroup.PUT("/users/batch-status", admin.AdminBatchUpdateUserStatus)
		SuperAdminGroup.POST("/users/batch-delete", admin.AdminBatchDeleteUsers)

		// 实例类型权限配置
		SuperAdminGroup.GET("/instance-type-permissions", admin.GetAdminInstanceTypePermissions)
		SuperAdminGroup.PUT("/instance-type-permissions", admin.UpdateAdminInstanceTypePermissions)

		// 邀请码管理（超管专用）
		SuperAdminGroup.GET("/invite-codes", admin.GetInviteCodeList)
		SuperAdminGroup.POST("/invite-codes", admin.CreateInviteCode)
		SuperAdminGroup.POST("/invite-codes/generate", admin.GenerateInviteCode)
		SuperAdminGroup.GET("/invite-codes/export", admin.ExportInviteCodes)
		SuperAdminGroup.POST("/invite-codes/batch-delete", admin.BatchDeleteInviteCodes)
		SuperAdminGroup.DELETE("/invite-codes/:id", admin.DeleteInviteCode)

		// 公告管理（超管专用）
		SuperAdminGroup.GET("/announcements", admin.GetAnnouncements)
		SuperAdminGroup.POST("/announcements", admin.CreateAnnouncement)
		SuperAdminGroup.PUT("/announcements/:id", admin.UpdateAnnouncementItem)
		SuperAdminGroup.DELETE("/announcements/:id", admin.DeleteAnnouncement)
		SuperAdminGroup.PUT("/announcements/batch-status", admin.BatchUpdateAnnouncementStatus)
		SuperAdminGroup.POST("/announcements/batch-delete", admin.BatchDeleteAnnouncements)

		// 系统监控
		SuperAdminGroup.GET("/monitoring/system", admin.GetAdminDashboard)
		SuperAdminGroup.GET("/monitoring/audit-logs", system.GetOperationLogs)

		// 性能监控
		SuperAdminGroup.GET("/performance/metrics", system.GetPerformanceMetrics)
		SuperAdminGroup.GET("/performance/history", system.GetPerformanceHistory)

		// 日志查看
		SuperAdminGroup.GET("/logs/dates", system.GetLogDates)
		SuperAdminGroup.GET("/logs/content", system.GetLogContent)

		// 配额管理
		SuperAdminGroup.GET("/quota/users/:userId", system.GetUserQuotaInfo)

		// 流量同步管理
		SuperAdminGroup.POST("/traffic/sync/instance/:instance_id", admin.SyncInstanceTraffic)
		SuperAdminGroup.POST("/traffic/sync/user/:user_id", admin.SyncUserTraffic)
		SuperAdminGroup.POST("/traffic/sync/provider/:provider_id", admin.SyncProviderTraffic)
		SuperAdminGroup.POST("/traffic/sync/all", admin.SyncAllTraffic)

		// 用户冻结
		SuperAdminGroup.POST("/users/set-expiry", admin.SetUserExpiry)

		// 管理员特殊操作
		SuperAdminGroup.POST("/users/:id/login-as", admin.AdminLoginAsUser)
		SuperAdminGroup.POST("/instances/transfer", admin.AdminTransferInstance)
	}
}
