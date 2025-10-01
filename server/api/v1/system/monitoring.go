package system

import (
	"net/http"
	"oneclickvirt/service/resources"

	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

type MonitoringApi struct{}

// GetSystemStats 获取系统统计信息
// @Summary 获取系统统计信息
// @Description 获取服务器的CPU、内存、磁盘、网络等系统资源使用情况
// @Tags 系统监控
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=system.SystemStats} "获取成功"
// @Failure 401 {object} common.Response "认证失败"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/monitoring/system [get]
func (m *MonitoringApi) GetSystemStats(c *gin.Context) {
	monitoringService := resources.MonitoringService{}
	stats := monitoringService.GetSystemStats()

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "获取成功",
		Data: stats,
	})
}

// GetHealthCheck 获取系统健康检查
// @Summary 获取系统健康检查
// @Description 检查各个组件的健康状态
// @Tags 系统监控
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "检查成功"
// @Failure 401 {object} common.Response "认证失败"
// @Failure 500 {object} common.Response "检查失败"
// @Router /admin/monitoring/health [get]
func (m *MonitoringApi) GetHealthCheck(c *gin.Context) {
	monitoringService := resources.MonitoringService{}
	health := monitoringService.CheckHealth()

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "检查成功",
		Data: health,
	})
}

// GetMetrics 获取Prometheus格式的指标
// @Summary 获取Prometheus格式的指标
// @Description 返回Prometheus格式的系统监控指标，用于监控系统集成
// @Tags 系统监控
// @Accept json
// @Produce text/plain
// @Security BearerAuth
// @Success 200 {string} string "Prometheus指标数据"
// @Failure 401 {object} common.Response "认证失败"
// @Router /admin/monitoring/metrics [get]
func (m *MonitoringApi) GetMetrics(c *gin.Context) {
	monitoringService := resources.MonitoringService{}
	metrics := monitoringService.GeneratePrometheusMetrics()

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.String(http.StatusOK, metrics)
}

// GetSystemLogs 获取系统日志
// @Summary 获取系统日志
// @Description 获取系统运行日志
// @Tags 监控管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param level query string false "日志级别" Enums(info,warn,error,debug)
// @Param limit query int false "返回条数" default(100)
// @Param offset query int false "偏移量" default(0)
// @Success 200 {object} common.Response{data=[]object} "获取成功"
// @Failure 500 {object} common.Response "获取失败"
// @Router /api/v1/admin/monitoring/logs [get]
func GetSystemLogs(c *gin.Context) {
	level := c.DefaultQuery("level", "info")
	limit := c.DefaultQuery("limit", "100")
	offset := c.DefaultQuery("offset", "0")

	// 使用服务层获取日志
	monitoringService := resources.MonitoringService{}
	logs := monitoringService.GetSystemLogs(level, limit, offset)

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "获取系统日志成功",
		Data: logs,
	})
}

// GetOperationLogs 获取操作审计日志
// @Summary 获取操作审计日志
// @Description 获取用户操作审计日志
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param user_id query int false "用户ID"
// @Param action query string false "操作类型"
// @Param start_time query string false "开始时间"
// @Param end_time query string false "结束时间"
// @Param limit query int false "返回条数" default(100)
// @Param offset query int false "偏移量" default(0)
// @Success 200 {object} common.Response{data=[]object} "获取成功"
// @Failure 500 {object} common.Response "获取失败"
// @Router /api/v1/admin/monitoring/audit-logs [get]
func GetOperationLogs(c *gin.Context) {
	userID := c.Query("user_id")
	action := c.Query("action")
	startTime := c.Query("start_time")
	endTime := c.Query("end_time")
	limit := c.DefaultQuery("limit", "100")
	offset := c.DefaultQuery("offset", "0")

	// 使用服务层获取审计日志
	monitoringService := resources.MonitoringService{}
	auditLogs := monitoringService.GetOperationLogs(userID, action, startTime, endTime, limit, offset)

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "获取操作日志成功",
		Data: auditLogs,
	})
}
