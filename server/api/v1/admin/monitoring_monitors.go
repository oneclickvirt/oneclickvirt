package admin

import (
	"context"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	providerService "oneclickvirt/service/provider"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetProviderMonitors godoc
// @Summary 获取节点监控列表（DB）
// @Description 获取指定节点下所有Agent监控记录，支持分页
// @Tags 管理员/节点
// @Produce json
// @Param id path uint true "节点ID"
// @Param page query int false "页码"
// @Param pageSize query int false "每页条数"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/monitoring/monitors [get]
func GetProviderMonitors(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
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
		common.ResponseWithError(c, common.ClassifyError(err))
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

	common.ResponseSuccess(c, map[string]interface{}{
		"list":     result,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// SyncProviderMonitors godoc
// @Summary 同步节点监控器
// @Description 确保所有运行中实例均有监控器，并清理失效的监控记录（最长5分钟）
// @Tags 管理员/节点
// @Produce json
// @Param id path uint true "节点ID"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/monitoring/sync [post]
func SyncProviderMonitors(c *gin.Context) {
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

	if config.MonitoringMode != "agent" || !config.AgentInstalled {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Agent未安装或未启用Agent监控模式"))
		return
	}

	providerInstance, err := providerService.GetProviderInstanceByID(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider未连接: "+err.Error()))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	monitorSvc := agentService.NewMonitorService(ctx, global.APP_DB)

	// Ensure all running instances have monitors and update existing ones' interfaces
	if err := monitorSvc.EnsureMonitorsForProvider(providerInstance, uint(providerID), config); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// Return updated list
	var monitors []monitoringModel.AgentMonitor
	if err := global.APP_DB.Where("provider_id = ?", providerID).Find(&monitors).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, monitors, "同步完成")
}

// ListAgentMonitors godoc
// @Summary 获取Agent监控列表（实时）
// @Description 直接从 Agent读取监控器列表；Agent模式节点回读数据库缓存
// @Tags 管理员/节点
// @Produce json
// @Param id path uint true "节点ID"
// @Param page query int false "页码"
// @Param pageSize query int false "每页条数"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/monitoring/agent-monitors [get]
func ListAgentMonitors(c *gin.Context) {
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

	if !config.AgentInstalled {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Agent未安装"))
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

	// Get provider to check connection type
	var p struct {
		Endpoint       string
		AgentRemoteIP  string
		ConnectionType string
	}
	if err := global.APP_DB.Raw(
		"SELECT endpoint, agent_remote_ip, connection_type FROM providers WHERE id = ?", providerID,
	).Scan(&p).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	// For agent-mode providers, the agent's HTTP API is behind NAT/WS tunnel.
	// Return MySQL-synced data instead of making direct HTTP calls.
	if p.ConnectionType == "agent" {
		listAgentMonitorsFromDB(c, uint(providerID), page, pageSize)
		return
	}

	host := agentService.ResolveAgentHost(p.Endpoint, p.AgentRemoteIP)
	if host == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider无可达地址"))
		return
	}

	port := config.AgentPort
	if port == 0 {
		port = 23782
	}

	client := agentService.GetClientWithMode(uint(providerID), host, port, config.AgentToken, p.ConnectionType == "agent")
	result, err := client.ListMonitors()
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
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

	common.ResponseSuccess(c, map[string]interface{}{
		"monitors": pagedList,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// listAgentMonitorsFromDB returns agent monitors from MySQL for agent-mode providers.
func listAgentMonitorsFromDB(c *gin.Context, providerID uint, page, pageSize int) {
	var total int64
	global.APP_DB.Model(&monitoringModel.AgentMonitor{}).Where("provider_id = ?", providerID).Count(&total)

	var monitors []monitoringModel.AgentMonitor
	if err := global.APP_DB.Where("provider_id = ?", providerID).
		Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&monitors).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// Batch check which instances are deleted
	instanceIDs := make([]uint, 0, len(monitors))
	instanceNames := make([]string, 0, len(monitors))
	for _, m := range monitors {
		instanceIDs = append(instanceIDs, m.InstanceID)
		if m.InstanceName != "" {
			instanceNames = append(instanceNames, m.InstanceName)
		}
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
		ifaces := strings.Split(m.Interfaces, ",")
		if len(ifaces) == 1 && ifaces[0] == "" {
			ifaces = nil
		}
		pk := m.ProviderKind
		in := m.InstanceName
		item := EnrichedMonitorItem{
			ID:              m.AgentMonitorID,
			Interface:       ifaces,
			ProviderKind:    &pk,
			InstanceName:    &in,
			TotalBytes:      m.LastTrafficBytesIn + m.LastTrafficBytesOut,
			TotalBytesIn:    m.LastTrafficBytesIn,
			TotalBytesOut:   m.LastTrafficBytesOut,
			UpdatedAt:       m.LastSyncAt.Unix(),
			InstanceDeleted: !activeInstanceIDs[m.InstanceID],
		}
		enriched = append(enriched, item)
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"monitors": enriched,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// GetInstanceResources godoc
// @Summary 获取实例资源监控数据
// @Description 获取指定实例的CPU/内存资源监控指标
// @Tags 管理员/实例
// @Produce json
// @Param id path uint true "实例ID"
// @Param hours query int false "查询时间范围小时数（默认24）"
// @Success 200 {object} common.Response
// @Router /admin/instances/{id}/monitoring/resources [get]
func GetInstanceResources(c *gin.Context) {
	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
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
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, metrics)
}

// GetProviderResourceSummary godoc
// @Summary 获取节点资源汇总
// @Description 获取节点下所有实例的最新资源使用汇总
// @Tags 管理员/节点
// @Produce json
// @Param id path uint true "节点ID"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/monitoring/resources [get]
func GetProviderResourceSummary(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	ctx := c.Request.Context()
	resSvc := agentService.NewResourceSyncService(ctx, global.APP_DB)
	metrics, err := resSvc.GetProviderResourceSummary(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, metrics)
}

// ClearProviderMonitors godoc
// @Summary 清空节点监控器
// @Description 清除指定节点下所有Agent监控记录及资源指标
// @Tags 管理员/节点
// @Produce json
// @Param id path uint true "节点ID"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/monitoring/clear [delete]
func ClearProviderMonitors(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	// 检查Provider是否存在
	var p providerModel.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	config, err := agentService.GetMonitoringConfig(global.APP_DB, uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// Get all monitors for this provider
	var monitors []monitoringModel.AgentMonitor
	if err := global.APP_DB.Where("provider_id = ?", providerID).Find(&monitors).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// Try to clean up agent-side monitors
	if config.AgentInstalled {
		var p struct {
			Endpoint       string
			AgentRemoteIP  string
			ConnectionType string
		}
		if err := global.APP_DB.Raw(
			"SELECT endpoint, agent_remote_ip, connection_type FROM providers WHERE id = ?", providerID,
		).Scan(&p).Error; err == nil {
			host := agentService.ResolveAgentHost(p.Endpoint, p.AgentRemoteIP)
			if host == "" && p.ConnectionType == "agent" {
				host = "127.0.0.1" // loopback fallback; calls are routed through WS fallback
			}
			if host != "" {
				port := config.AgentPort
				if port == 0 {
					port = 23782
				}
				client := agentService.GetClientWithMode(uint(providerID), host, port, config.AgentToken, p.ConnectionType == "agent")
				// Call cleanup with empty max_update_time to remove all monitors
				if _, cleanupErr := client.Cleanup("0s"); cleanupErr != nil {
					global.APP_LOG.Warn("agent cleanup failed, proceeding with local cleanup",
						zap.Uint64("provider_id", providerID),
						zap.Error(cleanupErr))
				}
			}
		}
	}

	// Hard-delete all agent monitors from local DB
	deletedCount := len(monitors)
	if err := global.APP_DB.Unscoped().Where("provider_id = ?", providerID).Delete(&monitoringModel.AgentMonitor{}).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// Also clear resource metrics for this provider
	global.APP_DB.Where("provider_id = ?", providerID).Delete(&monitoringModel.ResourceMetric{})

	common.ResponseSuccess(c, map[string]interface{}{
		"deleted_count": deletedCount,
	}, "清空完成")
}
