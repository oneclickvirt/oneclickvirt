package user

import (
	"net/http"
	"strconv"

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
		c.JSON(http.StatusUnauthorized, common.Response{Code: 40001, Msg: "未授权"})
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "无效的实例ID"})
		return
	}

	// Verify the instance belongs to this user
	var instance providerModel.Instance
	if err := global.APP_DB.Where("id = ? AND user_id = ?", instanceID, userID).First(&instance).Error; err != nil {
		c.JSON(http.StatusNotFound, common.Response{Code: 40400, Msg: "实例不存在"})
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
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "获取资源数据失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: "success", Data: metrics})
}

// GetInstanceMonitoringStatus returns monitoring status for a user's instance.
func GetInstanceMonitoringStatus(c *gin.Context) {
	userID, err := middleware.GetUserIDFromContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, common.Response{Code: 40001, Msg: "未授权"})
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "无效的实例ID"})
		return
	}

	// Verify ownership
	var instance providerModel.Instance
	if err := global.APP_DB.Where("id = ? AND user_id = ?", instanceID, userID).First(&instance).Error; err != nil {
		c.JSON(http.StatusNotFound, common.Response{Code: 40400, Msg: "实例不存在"})
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

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  "success",
		Data: gin.H{
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
		},
	})
}
