package admin

import (
	"context"
	"strconv"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	"oneclickvirt/model/common"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	providerService "oneclickvirt/service/provider"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// DeployAgentRequest is the request body for deploying the agent.
// Version is optional; when omitted the server's compatible agent version is used.
type DeployAgentRequest struct {
	Version string `json:"version"`
}

// DeployAgent deploys the monitoring agent to a provider host.
// The provider is identified by the :id URL path parameter.
func DeployAgent(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	var req DeployAgentRequest
	// Body is optional — version is the only field and it has a default.
	_ = c.ShouldBindJSON(&req)

	if req.Version == "" {
		req.Version = constant.CompatibleAgentVersion
	}

	// Get or create monitoring config (token is auto-generated on creation)
	config, err := agentService.GetMonitoringConfig(global.APP_DB, uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// Ensure token exists (legacy configs created before auto-generation was added)
	if config.AgentToken == "" {
		config.AgentToken = agentService.GenerateAgentToken()
		if err := global.APP_DB.Save(config).Error; err != nil {
			common.ResponseWithError(c, common.ClassifyError(err))
			return
		}
	}

	// Get provider instance from the registry
	providerInstance, err := providerService.GetProviderInstanceByID(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider未连接: "+err.Error()))
		return
	}

	// Check kernel version for nft support (the deploy script will install nft if needed)
	if config.TrafficCollectMethod == "" || config.TrafficCollectMethod == "nft" {
		nftCtx, nftCancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer nftCancel()

		kernelOK, err := agentService.DetectKernelVersionForNFT(nftCtx, providerInstance)
		if err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("kernel version check failed, proceeding with deploy",
					zap.Error(err))
			}
			// Don't block deploy on detection failure - the deploy script will handle it
		} else if !kernelOK {
			// Kernel too old for nftables, auto-switch to iptables mode
			config.TrafficCollectMethod = "ipt"
			if err := global.APP_DB.Save(config).Error; err != nil {
				common.ResponseWithError(c, common.ClassifyError(err))
				return
			}
			if global.APP_LOG != nil {
				global.APP_LOG.Info("kernel too old for nftables, switched to iptables mode",
					zap.Uint("providerID", uint(providerID)))
			}
		}
	}

	// Get provider model from database for proxy config
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, uint(providerID)).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// Agent 模式节点无需重复部署监控 agent（已通过 ocv 安装）
	if dbProvider.ConnectionType == "agent" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Agent 模式节点已部署 Agent，无需重复部署监控。"))
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
		TrafficCollectMethod:    config.TrafficCollectMethod,
		// Reverse proxy config from provider database model
		EnableReverseProxy: dbProvider.EnableDomainBinding,
		ProxyHTTPPort:      dbProvider.ProxyHTTPPort,
		ProxyHTTPSPort:     dbProvider.ProxyHTTPSPort,
		ProxyEnableHTTP:    dbProvider.ProxyEnableHTTP,
		ProxyEnableHTTPS:   dbProvider.ProxyEnableHTTPS,
		ProxyTLSCertPath:   dbProvider.ProxyTLSCertPath,
		ProxyTLSKeyPath:    dbProvider.ProxyTLSKeyPath,
	}
	logs, err := agentService.DeployAgentWithConfig(deployCtx, providerInstance, agentCfg, req.Version)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// Update config
	config.AgentInstalled = true
	config.AgentVersion = req.Version
	config.MonitoringMode = "agent"
	if err := global.APP_DB.Save(config).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, gin.H{"config": config, "output": logs}, "Agent部署成功")
}

// UninstallAgent removes the agent from a provider host.
func UninstallAgent(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	// Agent 模式节点不允许从主控端卸载 agent
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, uint(providerID)).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	if dbProvider.ConnectionType == "agent" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Agent 模式节点不能从主控端卸载。请在节点上执行 ocv uninstall 进行卸载。"))
		return
	}

	providerInstance, err := providerService.GetProviderInstanceByID(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider未连接: "+err.Error()))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()

	if err := agentService.UninstallAgent(ctx, providerInstance); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// Update config
	config, _ := agentService.GetMonitoringConfig(global.APP_DB, uint(providerID))
	if config != nil {
		config.AgentInstalled = false
		config.MonitoringMode = "pmacct"
		_ = global.APP_DB.Save(config).Error // best-effort update after successful uninstall
	}

	common.ResponseSuccess(c, nil, "Agent已卸载")
}

// GetAgentStatus checks the agent status on a provider host.
func GetAgentStatus(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, uint(providerID)).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	if dbProvider.ConnectionType == "agent" {
		config, _ := agentService.GetMonitoringConfig(global.APP_DB, uint(providerID))
		runtimeHealth := agentService.GetHub().GetRuntimeHealth(uint(providerID))
		isRunning := runtimeHealth.Connected

		var monitorCount int64
		global.APP_DB.Model(&monitoringModel.AgentMonitor{}).Where("provider_id = ?", providerID).Count(&monitorCount)

		common.ResponseSuccess(c, gin.H{
			"is_running":        isRunning,
			"version":           dbProvider.Version,
			"config":            config,
			"monitor_count":     monitorCount,
			"status":            runtimeHealth.Status,
			"control_last_seen": runtimeHealth.ControlLastSeen,
		})
		return
	}

	providerInstance, err := providerService.GetProviderInstanceByID(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider未连接: "+err.Error()))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	isRunning, version := agentService.CheckAgentStatus(ctx, providerInstance)

	config, _ := agentService.GetMonitoringConfig(global.APP_DB, uint(providerID))

	// Count monitors
	var monitorCount int64
	global.APP_DB.Model(&monitoringModel.AgentMonitor{}).Where("provider_id = ?", providerID).Count(&monitorCount)

	common.ResponseSuccess(c, gin.H{
		"is_running":    isRunning,
		"version":       version,
		"config":        config,
		"monitor_count": monitorCount,
	})
}
