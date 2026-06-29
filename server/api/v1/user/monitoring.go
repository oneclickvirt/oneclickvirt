package user

import (
	"strconv"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"

	"github.com/gin-gonic/gin"
)

// GetInstanceResourceMonitoring returns resource monitoring data for a user's instance.
func GetInstanceResourceMonitoring(c *gin.Context) {
	userID, err := middleware.GetUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权"))
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	// Verify the instance belongs to this user
	var instance providerModel.Instance
	if err := global.APP_DB.Where("id = ? AND user_id = ?", instanceID, userID).First(&instance).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "实例不存在"))
		return
	}
	if !constant.IsDetailAvailableStatus(instance.Status) {
		common.ResponseWithError(c, common.NewError(common.CodeConflict, "实例正在操作进行中，请在任务详情中查看进度"))
		return
	}

	hoursStr := c.DefaultQuery("hours", "24")
	hours, _ := strconv.Atoi(hoursStr)
	if hours <= 0 || hours > 24 {
		hours = 24
	}

	ctx := c.Request.Context()
	resSvc := agentService.NewResourceSyncService(ctx, global.APP_DB)
	metrics, err := resSvc.GetInstanceResources(uint(instanceID), hours)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// Determine disk monitoring availability from provider config
	diskMonitoringEnabled := true
	var provider providerModel.Provider
	if err := global.APP_DB.Select("container_limit_disk, vm_limit_disk").
		Where("id = ?", instance.ProviderID).First(&provider).Error; err == nil {
		if instance.InstanceType == "vm" {
			diskMonitoringEnabled = provider.VMLimitDisk
		} else {
			diskMonitoringEnabled = provider.ContainerLimitDisk
		}
	}

	common.ResponseSuccess(c, gin.H{
		"metrics":                 metrics,
		"disk_monitoring_enabled": diskMonitoringEnabled,
	})
}

// GetInstanceMonitoringStatus returns monitoring status for a user's instance.
func GetInstanceMonitoringStatus(c *gin.Context) {
	userID, err := middleware.GetUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权"))
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	// Verify ownership
	var instance providerModel.Instance
	if err := global.APP_DB.Where("id = ? AND user_id = ?", instanceID, userID).First(&instance).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "实例不存在"))
		return
	}
	if !constant.IsDetailAvailableStatus(instance.Status) {
		common.ResponseWithError(c, common.NewError(common.CodeConflict, "实例正在操作进行中，请在任务详情中查看进度"))
		return
	}

	// Get agent monitor info
	var monitor monitoringModel.AgentMonitor
	hasMonitor := global.APP_DB.Where("instance_id = ?", instanceID).First(&monitor).Error == nil

	// Get monitoring config for provider
	config, _ := agentService.GetMonitoringConfig(global.APP_DB, instance.ProviderID)

	// Get latest resource metric
	var latestMetric *monitoringModel.ResourceMetric
	var metric monitoringModel.ResourceMetric
	if err := global.APP_DB.Where("instance_id = ?", instanceID).
		Order("timestamp DESC").First(&metric).Error; err == nil {
		latestMetric = &metric
	}

	common.ResponseSuccess(c, gin.H{
		"has_monitor":     hasMonitor,
		"monitoring_mode": config.MonitoringMode,
		"latest_resource": latestMetric,
		"monitor_info": func() interface{} {
			if hasMonitor {
				return gin.H{
					"interfaces":    monitor.Interfaces,
					"is_enabled":    monitor.IsEnabled,
					"last_sync_at":  monitor.LastSyncAt,
					"provider_kind": monitor.ProviderKind,
				}
			}
			return nil
		}(),
	})
}
