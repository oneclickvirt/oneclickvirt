package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
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

// GetMonitoringConfig returns the monitoring configuration for a provider.
func GetMonitoringConfig(c *gin.Context) {
	providerIDStr := c.Param("id")
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
	TrafficCollectMethod    string `json:"traffic_collect_method"` // "nft" or "ipt"
}

// UpdateMonitoringConfig updates the monitoring configuration for a provider.
// If the agent is installed, it also syncs the config to the remote agent and restarts it.
func UpdateMonitoringConfig(c *gin.Context) {
	providerIDStr := c.Param("id")
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
	if req.TrafficCollectMethod == "nft" || req.TrafficCollectMethod == "ipt" {
		config.TrafficCollectMethod = req.TrafficCollectMethod
	}

	if err := global.APP_DB.Save(config).Error; err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "更新监控配置失败: " + err.Error()})
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
	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: msg, Data: config})
}

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
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "无效的Provider ID"})
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
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "获取监控配置失败: " + err.Error()})
		return
	}

	// Ensure token exists (legacy configs created before auto-generation was added)
	if config.AgentToken == "" {
		config.AgentToken = agentService.GenerateAgentToken()
		global.APP_DB.Save(config)
	}

	// Get provider instance from the registry
	providerInstance, err := providerService.GetProviderInstanceByID(uint(providerID))
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "Provider未连接: " + err.Error()})
		return
	}

	// Check kernel/nft support first
	nftCtx, nftCancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer nftCancel()

	supportsNFT, err := agentService.DetectKernelSupportsNFT(nftCtx, providerInstance)
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
			Data: gin.H{"config": config, "output": "内核不支持nft，已自动切换为pmacct模式\n"},
		})
		return
	}

	// Get provider model from database for proxy config
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, uint(providerID)).Error; err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 50000,
			Msg:  "获取Provider配置失败: " + err.Error(),
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
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 50000,
			Msg:  "部署Agent失败: " + err.Error(),
			Data: gin.H{"output": logs},
		})
		return
	}

	// Update config
	config.AgentInstalled = true
	config.AgentVersion = req.Version
	config.MonitoringMode = "agent"
	global.APP_DB.Save(config)

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  "Agent部署成功",
		Data: gin.H{"config": config, "output": logs},
	})
}

// UninstallAgent removes the agent from a provider host.
func UninstallAgent(c *gin.Context) {
	providerIDStr := c.Param("id")
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
	providerIDStr := c.Param("id")
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

// GetProviderMonitors returns all agent monitors for a provider with pagination.
func GetProviderMonitors(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "无效的Provider ID"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var total int64
	global.APP_DB.Model(&monitoringModel.AgentMonitor{}).Where("provider_id = ?", providerID).Count(&total)

	var monitors []monitoringModel.AgentMonitor
	if err := global.APP_DB.Where("provider_id = ?", providerID).
		Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&monitors).Error; err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "查询监控列表失败: " + err.Error()})
		return
	}

	// Batch check which instances are deleted
	instanceIDs := make([]uint, 0, len(monitors))
	for _, m := range monitors {
		instanceIDs = append(instanceIDs, m.InstanceID)
	}

	activeInstanceIDs := make(map[uint]bool)
	if len(instanceIDs) > 0 {
		var activeInstances []struct{ ID uint }
		global.APP_DB.Model(&providerModel.Instance{}).
			Select("id").
			Where("id IN ?", instanceIDs).
			Scan(&activeInstances)
		for _, inst := range activeInstances {
			activeInstanceIDs[inst.ID] = true
		}
	}

	// Build enriched response
	type MonitorWithStatus struct {
		monitoringModel.AgentMonitor
		InstanceDeleted bool `json:"instance_deleted"`
	}

	result := make([]MonitorWithStatus, 0, len(monitors))
	for _, m := range monitors {
		result = append(result, MonitorWithStatus{
			AgentMonitor:    m,
			InstanceDeleted: !activeInstanceIDs[m.InstanceID],
		})
	}

	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: "success", Data: map[string]interface{}{
		"list":     result,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	}})
}

// SyncProviderMonitors ensures all active instances have monitors and cleans up stale ones.
func SyncProviderMonitors(c *gin.Context) {
	providerIDStr := c.Param("id")
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

	if config.MonitoringMode != "agent" || !config.AgentInstalled {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "Agent未安装或未启用Agent监控模式"})
		return
	}

	providerInstance, err := providerService.GetProviderInstanceByID(uint(providerID))
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "Provider未连接: " + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	monitorSvc := agentService.NewMonitorService(ctx, global.APP_DB)

	// Ensure all running instances have monitors and update existing ones' interfaces
	if err := monitorSvc.EnsureMonitorsForProvider(providerInstance, uint(providerID), config); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "同步监控失败: " + err.Error()})
		return
	}

	// Return updated list
	var monitors []monitoringModel.AgentMonitor
	if err := global.APP_DB.Where("provider_id = ?", providerID).Find(&monitors).Error; err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "查询监控列表失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: "同步完成", Data: monitors})
}

// ListAgentMonitors returns the list of monitors directly from the remote agent.
func ListAgentMonitors(c *gin.Context) {
	providerIDStr := c.Param("id")
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

	if !config.AgentInstalled {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "Agent未安装"})
		return
	}

	// Get provider to construct client
	var p providerModel.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "Provider不存在"})
		return
	}

	host := p.Endpoint
	if host == "" {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "Provider无Endpoint"})
		return
	}
	// Strip SSH port suffix from endpoint (e.g. "192.168.1.1:22" -> "192.168.1.1")
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}

	port := config.AgentPort
	if port == 0 {
		port = 23782
	}

	client := agentService.GetClient(uint(providerID), host, port, config.AgentToken)
	result, err := client.ListMonitors()
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "查询Agent监控列表失败: " + err.Error()})
		return
	}

	// Pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	monitors := result.Monitors
	total := len(monitors)

	// Enrich with in/out traffic by fetching per-monitor info
	ids := make([]int64, 0, len(monitors))
	for _, m := range monitors {
		ids = append(ids, m.ID)
	}

	infoMap := make(map[int64]*agentService.InfoResponse)
	if len(ids) > 0 {
		infoMap, _ = client.BatchGetInfo(ids)
	}

	// Map instance names to check if they still exist in DB
	instanceNames := make([]string, 0, len(monitors))
	for _, m := range monitors {
		if m.InstanceName != nil {
			instanceNames = append(instanceNames, *m.InstanceName)
		}
	}
	activeInstanceNames := make(map[string]bool)
	if len(instanceNames) > 0 {
		var activeInstances []struct{ Name string }
		global.APP_DB.Model(&providerModel.Instance{}).
			Select("name").
			Where("name IN ? AND provider_id = ?", instanceNames, providerID).
			Scan(&activeInstances)
		for _, inst := range activeInstances {
			activeInstanceNames[inst.Name] = true
		}
	}

	type EnrichedMonitorItem struct {
		ID              int64    `json:"id"`
		Interface       []string `json:"interface"`
		ProviderKind    *string  `json:"provider_kind"`
		InstanceName    *string  `json:"instance_name"`
		TotalBytes      uint64   `json:"total_bytes"`
		TotalBytesIn    uint64   `json:"total_bytes_in"`
		TotalBytesOut   uint64   `json:"total_bytes_out"`
		UpdatedAt       int64    `json:"updated_at"`
		InstanceDeleted bool     `json:"instance_deleted"`
	}

	enriched := make([]EnrichedMonitorItem, 0, len(monitors))
	for _, m := range monitors {
		item := EnrichedMonitorItem{
			ID:           m.ID,
			Interface:    m.Interface,
			ProviderKind: m.ProviderKind,
			InstanceName: m.InstanceName,
			TotalBytes:   m.TotalBytes,
			UpdatedAt:    m.UpdatedAt,
		}
		if info, ok := infoMap[m.ID]; ok {
			item.TotalBytesIn = info.UsedTrafficIn
			item.TotalBytesOut = info.UsedTrafficOut
		}
		if m.InstanceName != nil {
			item.InstanceDeleted = !activeInstanceNames[*m.InstanceName]
		}
		enriched = append(enriched, item)
	}

	// Apply pagination
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(enriched) {
		start = len(enriched)
	}
	if end > len(enriched) {
		end = len(enriched)
	}
	pagedList := enriched[start:end]

	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: "success", Data: map[string]interface{}{
		"monitors": pagedList,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	}})
}

// GetInstanceResources returns resource monitoring data for an instance.
func GetInstanceResources(c *gin.Context) {
	instanceIDStr := c.Param("id")
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
	providerIDStr := c.Param("id")
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

// ClearProviderMonitors clears all agent monitors for a provider.
// This removes all records from both the agent-side and the local DB.
func ClearProviderMonitors(c *gin.Context) {
	providerIDStr := c.Param("id")
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

	// Get all monitors for this provider
	var monitors []monitoringModel.AgentMonitor
	if err := global.APP_DB.Where("provider_id = ?", providerID).Find(&monitors).Error; err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "查询监控列表失败: " + err.Error()})
		return
	}

	// Try to clean up agent-side monitors
	if config.AgentInstalled {
		var p providerModel.Provider
		if err := global.APP_DB.First(&p, providerID).Error; err == nil && p.Endpoint != "" {
			host := p.Endpoint
			if idx := strings.LastIndex(host, ":"); idx > 0 {
				host = host[:idx]
			}
			port := config.AgentPort
			if port == 0 {
				port = 23782
			}
			client := agentService.GetClient(uint(providerID), host, port, config.AgentToken)
			// Call cleanup with empty max_update_time to remove all monitors
			if _, cleanupErr := client.Cleanup(""); cleanupErr != nil {
				global.APP_LOG.Warn("agent cleanup failed, proceeding with local cleanup",
					zap.Uint64("provider_id", providerID),
					zap.Error(cleanupErr))
			}
		}
	}

	// Hard-delete all agent monitors from local DB
	deletedCount := len(monitors)
	if err := global.APP_DB.Unscoped().Where("provider_id = ?", providerID).Delete(&monitoringModel.AgentMonitor{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: "清空监控记录失败: " + err.Error()})
		return
	}

	// Also clear resource metrics for this provider
	global.APP_DB.Where("provider_id = ?", providerID).Delete(&monitoringModel.ResourceMetric{})

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  "清空完成",
		Data: map[string]interface{}{
			"deleted_count": deletedCount,
		},
	})
}
