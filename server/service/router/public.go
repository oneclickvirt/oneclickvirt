package router

import (
	"oneclickvirt/api/v1/public"
	"oneclickvirt/api/v1/system"

	"github.com/gin-gonic/gin"
)

// InitPublicRouter 公开路由（需要数据库连接）
// 注意：version 和 build-info 已移至 NoDBGroup，DB 宕机时仍可访问。
func InitPublicRouter(Router *gin.RouterGroup) {
	PublicRouter := Router.Group("v1/public")
	{
		PublicRouter.GET("announcements", system.GetAnnouncement)
		PublicRouter.GET("stats", public.GetDashboardStats)
		PublicRouter.GET("system-images/available", system.GetAvailableSystemImages)
		PublicRouter.GET("providers/:id/hardware-report", public.GetProviderHardwareReport)
		PublicRouter.GET("instance-shares/:token", public.GetSharedInstanceDetail)
		PublicRouter.POST("instance-shares/:token/action", public.SharedInstanceAction)
		PublicRouter.PUT("instance-shares/:token/reset-password", public.ResetSharedInstancePassword)
		PublicRouter.GET("instance-shares/:token/password/:taskId", public.GetSharedInstanceNewPassword)
		PublicRouter.GET("instance-shares/:token/images/filtered", public.GetSharedInstanceImages)
		PublicRouter.GET("instance-shares/:token/ports", public.GetSharedInstancePorts)
		PublicRouter.GET("instance-shares/:token/monitoring", public.GetSharedInstanceMonitoring)
		PublicRouter.GET("instance-shares/:token/monitoring/resources", public.GetSharedInstanceResourceMonitoring)
		PublicRouter.GET("instance-shares/:token/traffic/detail", public.GetSharedInstanceTrafficDetail)
		PublicRouter.GET("instance-shares/:token/snapshots", public.GetSharedInstanceSnapshots)
		PublicRouter.GET("instance-shares/:token/snapshots/:snapshotId/download", public.DownloadSharedSnapshot)
		PublicRouter.GET("instance-shares/:token/ssh", public.SharedSSHWebSocket)
		PublicRouter.GET("instance-shares/:token/exec", public.SharedExecWebSocket)
		PublicRouter.GET("instance-shares/:token/sftp/list", public.SharedSFTPList)
		PublicRouter.GET("instance-shares/:token/sftp/download", public.SharedSFTPDownload)
		PublicRouter.POST("instance-shares/:token/sftp/upload", public.SharedSFTPUpload)
		PublicRouter.GET("instance-shares/:token/sftp/upload/status", public.SharedSFTPUploadStatus)
		PublicRouter.POST("instance-shares/:token/sftp/upload/abort", public.SharedSFTPUploadAbort)
	}
}
