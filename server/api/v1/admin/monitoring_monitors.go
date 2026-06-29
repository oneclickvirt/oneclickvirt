package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	providerService "oneclickvirt/service/provider"
	taskService "oneclickvirt/service/task"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
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
// @Description 创建后台任务确保所有运行中实例均有监控器，并清理失效的监控记录
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

	var running monitoringModel.MonitorSyncTask
	if err := global.APP_DB.Where("provider_id = ? AND status IN ?", providerID, []string{"pending", "running"}).
		Order("id DESC").First(&running).Error; err == nil {
		common.ResponseSuccess(c, buildMonitorSyncTaskResponse(&running), "已有同步任务正在执行")
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	if err := taskService.GetTaskService().EnsureTaskPoolAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	now := time.Now()
	task := monitoringModel.MonitorSyncTask{
		ProviderID: uint(providerID),
		TaskID:     fmt.Sprintf("monitor-sync-%d-%d", providerID, now.UnixNano()),
		Status:     "pending",
	}
	if err := global.APP_DB.Create(&task).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	adminTask, err := taskService.CreateMonitorSyncAdminTask(uint(providerID), task.ID, middleware.GetOwnerAdminID(c))
	if err != nil {
		_ = global.APP_DB.Model(&task).Updates(map[string]interface{}{
			"status":        "failed",
			"error_message": err.Error(),
			"finished_at":   time.Now(),
		}).Error
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	_ = global.APP_DB.Model(&task).Update("admin_task_id", adminTask.ID).Error

	common.ResponseSuccess(c, buildMonitorSyncTaskResponse(&task), "同步任务已提交")
}

func GetProviderMonitorSyncTask(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}
	taskID := strings.TrimSpace(c.Param("taskId"))
	if taskID == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的任务ID"))
		return
	}

	var task monitoringModel.MonitorSyncTask
	if err := global.APP_DB.Where("provider_id = ? AND task_id = ?", providerID, taskID).First(&task).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, buildMonitorSyncTaskResponse(&task))
}

func GetLatestProviderMonitorSyncTask(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	var task monitoringModel.MonitorSyncTask
	if err := global.APP_DB.Where("provider_id = ?", providerID).Order("id DESC").First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ResponseSuccess(c, nil)
			return
		}
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, buildMonitorSyncTaskResponse(&task))
}

func buildMonitorSyncTaskResponse(task *monitoringModel.MonitorSyncTask) map[string]interface{} {
	errs := []string{}
	if task.ErrorsJSON != "" {
		_ = json.Unmarshal([]byte(task.ErrorsJSON), &errs)
	}
	return map[string]interface{}{
		"task_id":       task.TaskID,
		"admin_task_id": task.AdminTaskID,
		"provider_id":   task.ProviderID,
		"status":        task.Status,
		"total":         task.Total,
		"created":       task.Created,
		"updated":       task.Updated,
		"unchanged":     task.Unchanged,
		"failed":        task.Failed,
		"cleaned":       task.Cleaned,
		"error_message": task.ErrorMessage,
		"errors":        errs,
		"started_at":    task.StartedAt,
		"finished_at":   task.FinishedAt,
		"summary": map[string]interface{}{
			"total":     task.Total,
			"created":   task.Created,
			"updated":   task.Updated,
			"unchanged": task.Unchanged,
			"failed":    task.Failed,
			"cleaned":   task.Cleaned,
			"errors":    errs,
		},
	}
}

func runProviderMonitorSyncTask(taskID string, providerID uint, config monitoringModel.MonitoringConfig) {
	now := time.Now()
	_ = global.APP_DB.Model(&monitoringModel.MonitorSyncTask{}).Where("task_id = ?", taskID).
		Updates(map[string]interface{}{"status": "running", "started_at": &now}).Error

	finish := func(status string, summary *agentService.MonitorSyncSummary, taskErr error) {
		if summary == nil {
			summary = &agentService.MonitorSyncSummary{}
		}
		finished := time.Now()
		errorMessage := ""
		if taskErr != nil {
			errorMessage = taskErr.Error()
			if len(summary.Errors) == 0 || summary.Errors[len(summary.Errors)-1] != errorMessage {
				summary.Errors = append(summary.Errors, errorMessage)
			}
		}
		errorsJSON, _ := json.Marshal(summary.Errors)
		updates := map[string]interface{}{
			"status":        status,
			"total":         summary.Total,
			"created":       summary.Created,
			"updated":       summary.Updated,
			"unchanged":     summary.Unchanged,
			"failed":        summary.Failed,
			"cleaned":       summary.Cleaned,
			"error_message": errorMessage,
			"errors_json":   string(errorsJSON),
			"finished_at":   &finished,
		}
		if err := global.APP_DB.Model(&monitoringModel.MonitorSyncTask{}).Where("task_id = ?", taskID).Updates(updates).Error; err != nil && global.APP_LOG != nil {
			global.APP_LOG.Warn("update monitor sync task failed", zap.String("task_id", taskID), zap.Error(err))
		}
	}

	providerInstance, err := providerService.GetProviderInstanceByID(providerID)
	if err != nil {
		finish("failed", &agentService.MonitorSyncSummary{Failed: 1}, fmt.Errorf("Provider未连接: %w", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	monitorSvc := agentService.NewMonitorService(ctx, global.APP_DB.Session(&gorm.Session{}))
	summary, err := monitorSvc.EnsureMonitorsForProvider(providerInstance, providerID, &config)
	if err != nil {
		finish("failed", summary, err)
		return
	}

	cleaned, cleanupErr := monitorSvc.CleanupStaleMonitors(providerID, &config)
	if cleanupErr != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("cleanup stale monitors failed during provider sync", zap.Uint("provider_id", providerID), zap.Error(cleanupErr))
		}
		summary.Errors = append(summary.Errors, "cleanup: "+cleanupErr.Error())
		summary.Failed++
	}
	summary.Cleaned = cleaned
	finish("completed", summary, nil)
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
			ID:            m.ID,
			Interface:     m.Interface,
			ProviderKind:  m.ProviderKind,
			InstanceName:  m.InstanceName,
			TotalBytes:    m.TotalBytes,
			TotalBytesIn:  m.TotalBytesIn,
			TotalBytesOut: m.TotalBytesOut,
			UpdatedAt:     m.UpdatedAt,
		}
		if info, ok := infoMap[m.ID]; ok {
			item.TotalBytes = info.UsedTraffic
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
	var instance providerModel.Instance
	if err := global.APP_DB.Select("id", "status").First(&instance, instanceID).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "实例不存在"))
		return
	}
	if !constant.IsDetailAvailableStatus(instance.Status) {
		common.ResponseWithError(c, common.NewError(common.CodeConflict, "实例正在操作进行中，请在任务详情中查看进度"))
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
