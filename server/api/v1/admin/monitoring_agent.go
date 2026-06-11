package admin

import (
	"context"
	"strconv"
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
)

// DeployAgentRequest is the request body for deploying the agent.
// Version is optional; when omitted the server's compatible agent version is used.
type DeployAgentRequest struct {
	Version string `json:"version"`
}

// DeployAgent godoc
// @Summary 部署监控Agent
// @Description 将监控Agent部署到指定节点主机（耗时最長十分钟）
// @Tags 管理员/节点
// @Accept json
// @Produce json
// @Param id path uint true "节点ID"
// @Param body body DeployAgentRequest false "部署参数（version可选）"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/monitoring/agent [post]
func DeployAgent(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	// 验证Provider是否存在
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	var req DeployAgentRequest
	_ = c.ShouldBindJSON(&req)
	if req.Version == "" {
		req.Version = constant.CompatibleAgentVersion
	}

	task, err := taskService.CreateAgentMonitoringAdminTask(uint(providerID), middleware.GetOwnerAdminID(c), "agent-deploy", req.Version)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{
		"taskId":  task.ID,
		"task_id": task.ID,
		"status":  task.Status,
	}, "Agent部署任务已提交")
}

// UninstallAgent godoc
// @Summary 卸载监控Agent
// @Description 从指定节点主机卸载监控Agent
// @Tags 管理员/节点
// @Produce json
// @Param id path uint true "节点ID"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/monitoring/agent [delete]
func UninstallAgent(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	// 验证Provider是否存在
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	task, err := taskService.CreateAgentMonitoringAdminTask(uint(providerID), middleware.GetOwnerAdminID(c), "agent-uninstall", "")
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{
		"taskId":  task.ID,
		"task_id": task.ID,
		"status":  task.Status,
	}, "Agent卸载任务已提交")
}

// GetAgentStatus godoc
// @Summary 获取Agent状态
// @Description 检查指定节点的监控Agent运行状态、版本与监控数量
// @Tags 管理员/节点
// @Produce json
// @Param id path uint true "节点ID"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/monitoring/status [get]
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
