package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	monitoringModel "oneclickvirt/model/monitoring"
	agentService "oneclickvirt/service/agent"
	providerService "oneclickvirt/service/provider"

	"github.com/gin-gonic/gin"
)

// GetMonitoringConfig returns the monitoring configuration for a provider.
func GetMonitoringConfig(c *gin.Context) {
	providerIDStr := c.Param("providerId")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "无效的Provider ID"})
		return
	}

	config, err := agentService.GetMonitoringConfig(global.APP_DB, uint(providerID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "获取监控配置失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: "success", Data: config})
}

// UpdateMonitoringConfigRequest is the request body for updating monitoring config.
type UpdateMonitoringConfigRequest struct {
	MonitoringMode          string `json:"monitoring_mode"`
	AgentPort               int    `json:"agent_port"`
	CollectInterval         int    `json:"collect_interval"`
	ResourceCollectInterval int    `json:"resource_collect_interval"`
	ExtraExcludeCIDRsV4     string `json:"extra_exclude_cidrs_v4"`
	ExtraExcludeCIDRsV6     string `json:"extra_exclude_cidrs_v6"`
}

// UpdateMonitoringConfig updates the monitoring configuration for a provider.
// If the agent is installed, it also syncs the config to the remote agent and restarts it.
func UpdateMonitoringConfig(c *gin.Context) {
	providerIDStr := c.Param("providerId")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "无效的Provider ID"})
		return
	}

	var req UpdateMonitoringConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "请求参数错误: " + err.Error()})
		return
	}

	config, err := agentService.GetMonitoringConfig(global.APP_DB, uint(providerID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "获取监控配置失败: " + err.Error()})
		return
	}

	// Update fields
	if req.MonitoringMode != "" {
		config.MonitoringMode = req.MonitoringMode
	}
	if req.AgentPort > 0 {
		config.AgentPort = req.AgentPort
	}
	if req.CollectInterval > 0 {
		config.CollectInterval = req.CollectInterval
	}
	if req.ResourceCollectInterval > 0 {
		config.ResourceCollectInterval = req.ResourceCollectInterval
	}
	config.ExtraExcludeCIDRsV4 = req.ExtraExcludeCIDRsV4
	config.ExtraExcludeCIDRsV6 = req.ExtraExcludeCIDRsV6

	if err := global.APP_DB.Save(config).Error; err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "更新监控配置失败: " + err.Error()})
		return
	}

	// If agent is installed, sync the config to the remote agent
	syncMsg := ""
	if config.AgentInstalled && config.MonitoringMode == "agent" {
		providerInstance, err := providerService.GetProviderInstanceByID(uint(providerID))
		if err == nil {
			agentCfg := &agentService.AgentConfig{
				Token:                   config.AgentToken,
				TrafficCollectInterval:  config.CollectInterval,
				ResourceCollectInterval: config.ResourceCollectInterval,
				ExtraExcludeCIDRsV4:     config.ExtraExcludeCIDRsV4,
				ExtraExcludeCIDRsV6:     config.ExtraExcludeCIDRsV6,
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
			defer cancel()
			if syncErr := agentService.SyncAgentConfig(ctx, providerInstance, agentCfg); syncErr != nil {
				syncMsg = "配置已保存，但同步到Agent失败: " + syncErr.Error()
			} else {
				syncMsg = "配置已保存并同步到Agent"
			}
		} else {
			syncMsg = "配置已保存，但Provider未连接无法同步到Agent"
		}
	}

	msg := "success"
	if syncMsg != "" {
		msg = syncMsg
	}
	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: msg, Data: config})
}

// DeployAgentRequest is the request body for deploying the agent.
type DeployAgentRequest struct {
	ProviderID uint   `json:"provider_id" binding:"required"`
	Version    string `json:"version" binding:"required"`
}

// DeployAgent deploys the monitoring agent to a provider host.
func DeployAgent(c *gin.Context) {
	var req DeployAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "请求参数错误: " + err.Error()})
		return
	}

	// Get or create monitoring config
	config, err := agentService.GetMonitoringConfig(global.APP_DB, req.ProviderID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "获取监控配置失败: " + err.Error()})
		return
	}

	// Generate token if not set
	if config.AgentToken == "" {
		config.AgentToken = agentService.GenerateAgentToken()
		global.APP_DB.Save(config)
	}

	// Get provider instance from the registry
	providerInstance, err := providerService.GetProviderInstanceByID(req.ProviderID)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "Provider未连接: " + err.Error()})
		return
	}

	// Check kernel/nft support first
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	supportsNFT, err := agentService.DetectKernelSupportsNFT(ctx, providerInstance)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "检测内核NFT支持失败: " + err.Error()})
		return
	}

	if !supportsNFT {
		// Fallback to pmacct
		config.MonitoringMode = "pmacct"
		global.APP_DB.Save(config)
		c.JSON(http.StatusOK, common.Response{
			Code: 0,
			Msg:  "内核不支持nft，已自动切换为pmacct模式",
			Data: config,
		})
		return
	}

	// Deploy agent with full config
	deployCtx, deployCancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer deployCancel()

	agentCfg := &agentService.AgentConfig{
		Token:                   config.AgentToken,
		TrafficCollectInterval:  config.CollectInterval,
		ResourceCollectInterval: config.ResourceCollectInterval,
		ExtraExcludeCIDRsV4:     config.ExtraExcludeCIDRsV4,
		ExtraExcludeCIDRsV6:     config.ExtraExcludeCIDRsV6,
	}
	if err := agentService.DeployAgentWithConfig(deployCtx, providerInstance, agentCfg, req.Version); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "部署Agent失败: " + err.Error()})
		return
	}

	// Update config
	config.AgentInstalled = true
	config.AgentVersion = req.Version
	config.MonitoringMode = "agent"
	global.APP_DB.Save(config)

	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: "Agent部署成功", Data: config})
}

// UninstallAgent removes the agent from a provider host.
func UninstallAgent(c *gin.Context) {
	providerIDStr := c.Param("providerId")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "无效的Provider ID"})
		return
	}

	providerInstance, err := providerService.GetProviderInstanceByID(uint(providerID))
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "Provider未连接: " + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()

	if err := agentService.UninstallAgent(ctx, providerInstance); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "卸载Agent失败: " + err.Error()})
		return
	}

	// Update config
	config, _ := agentService.GetMonitoringConfig(global.APP_DB, uint(providerID))
	if config != nil {
		config.AgentInstalled = false
		config.MonitoringMode = "pmacct"
		global.APP_DB.Save(config)
	}

	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: "Agent已卸载"})
}

// GetAgentStatus checks the agent status on a provider host.
func GetAgentStatus(c *gin.Context) {
	providerIDStr := c.Param("providerId")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "无效的Provider ID"})
		return
	}

	providerInstance, err := providerService.GetProviderInstanceByID(uint(providerID))
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "Provider未连接: " + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	isRunning, version := agentService.CheckAgentStatus(ctx, providerInstance)

	config, _ := agentService.GetMonitoringConfig(global.APP_DB, uint(providerID))

	// Count monitors
	var monitorCount int64
	global.APP_DB.Model(&monitoringModel.AgentMonitor{}).Where("provider_id = ?", providerID).Count(&monitorCount)

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  "success",
		Data: gin.H{
			"is_running":    isRunning,
			"version":       version,
			"config":        config,
			"monitor_count": monitorCount,
		},
	})
}

// GetProviderMonitors returns all agent monitors for a provider.
func GetProviderMonitors(c *gin.Context) {
	providerIDStr := c.Param("providerId")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "无效的Provider ID"})
		return
	}

	var monitors []monitoringModel.AgentMonitor
	if err := global.APP_DB.Where("provider_id = ?", providerID).Find(&monitors).Error; err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "查询监控列表失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: "success", Data: monitors})
}

// GetInstanceResources returns resource monitoring data for an instance.
func GetInstanceResources(c *gin.Context) {
	instanceIDStr := c.Param("instanceId")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "无效的实例ID"})
		return
	}

	hoursStr := c.DefaultQuery("hours", "24")
	hours, _ := strconv.Atoi(hoursStr)
	if hours <= 0 {
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

// GetProviderResourceSummary returns latest resource usage for all instances of a provider.
func GetProviderResourceSummary(c *gin.Context) {
	providerIDStr := c.Param("providerId")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "无效的Provider ID"})
		return
	}

	ctx := c.Request.Context()
	resSvc := agentService.NewResourceSyncService(ctx, global.APP_DB)
	metrics, err := resSvc.GetProviderResourceSummary(uint(providerID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "获取资源概览失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: "success", Data: metrics})
}
