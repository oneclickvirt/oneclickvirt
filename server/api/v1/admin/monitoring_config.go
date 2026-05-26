package admin

import (
	"context"
	"strconv"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	providerService "oneclickvirt/service/provider"

	"github.com/gin-gonic/gin"
)

// GetMonitoringConfig godoc
// @Summary 获取监控配置
// @Description 获取指定节点的监控配置
// @Tags 管理员/节点
// @Produce json
// @Param id path uint true "节点ID"
// @Success 200 {object} common.Response
// @Router /api/v1/admin/providers/{id}/monitoring/config [get]
func GetMonitoringConfig(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	config, err := agentService.GetMonitoringConfig(global.APP_DB, uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, config)
}

// UpdateMonitoringConfigRequest is the request body for updating monitoring config.
type UpdateMonitoringConfigRequest struct {
	MonitoringMode          string `json:"monitoring_mode"`
	AgentPort               int    `json:"agent_port"`
	CollectInterval         int    `json:"collect_interval"`
	ResourceCollectInterval int    `json:"resource_collect_interval"`
	ExtraExcludeCIDRsV4     string `json:"extra_exclude_cidrs_v4"`
	ExtraExcludeCIDRsV6     string `json:"extra_exclude_cidrs_v6"`
	TrafficCollectMethod    string `json:"traffic_collect_method"` // "nft" or "ipt"
}

// UpdateMonitoringConfig godoc
// @Summary 更新监控配置
// @Description 更新指定节点的监控配置，如果Agent已安装则同步配置到远程Agent
// @Tags 管理员/节点
// @Accept json
// @Produce json
// @Param id path uint true "节点ID"
// @Param body body UpdateMonitoringConfigRequest true "监控配置"
// @Success 200 {object} common.Response
// @Router /api/v1/admin/providers/{id}/monitoring/config [put]
func UpdateMonitoringConfig(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	var req UpdateMonitoringConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+err.Error()))
		return
	}

	// Validate monitoring mode
	validModes := map[string]bool{"agent": true, "passive": true, "": true}
	if !validModes[req.MonitoringMode] {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的监控模式"))
		return
	}

	// Reject non-positive intervals and negative port
	if req.CollectInterval < 0 || req.ResourceCollectInterval < 0 || req.AgentPort < 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "配置值不能为负数"))
		return
	}
	if req.CollectInterval == 0 && req.ResourceCollectInterval == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "采集间隔不能为零"))
		return
	}

	config, err := agentService.GetMonitoringConfig(global.APP_DB, uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
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
	if req.TrafficCollectMethod == "nft" || req.TrafficCollectMethod == "ipt" {
		config.TrafficCollectMethod = req.TrafficCollectMethod
	}

	if err := global.APP_DB.Save(config).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// If agent is installed, sync the config to the remote agent
	syncMsg := ""
	if config.AgentInstalled && config.MonitoringMode == "agent" {
		// Get provider model from database for proxy config
		var dbProvider providerModel.Provider
		if err := global.APP_DB.First(&dbProvider, uint(providerID)).Error; err == nil {
			providerInstance, provErr := providerService.GetProviderInstanceByID(uint(providerID))
			if provErr == nil {
				agentCfg := &agentService.AgentConfig{
					Token:                   config.AgentToken,
					TrafficCollectInterval:  config.CollectInterval,
					ResourceCollectInterval: config.ResourceCollectInterval,
					TrafficCollectMethod:    config.TrafficCollectMethod,
					ExtraExcludeCIDRsV4:     config.ExtraExcludeCIDRsV4,
					ExtraExcludeCIDRsV6:     config.ExtraExcludeCIDRsV6,
					// Reverse proxy config from provider database model
					EnableReverseProxy: dbProvider.EnableDomainBinding,
					ProxyHTTPPort:      dbProvider.ProxyHTTPPort,
					ProxyHTTPSPort:     dbProvider.ProxyHTTPSPort,
					ProxyEnableHTTP:    dbProvider.ProxyEnableHTTP,
					ProxyEnableHTTPS:   dbProvider.ProxyEnableHTTPS,
					ProxyTLSCertPath:   dbProvider.ProxyTLSCertPath,
					ProxyTLSKeyPath:    dbProvider.ProxyTLSKeyPath,
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
		} else {
			syncMsg = "配置已保存，但获取Provider信息失败"
		}
	}

	msg := "success"
	if syncMsg != "" {
		msg = syncMsg
	}
	common.ResponseSuccess(c, config, msg)
}
