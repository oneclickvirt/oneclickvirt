package admin

import (
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	agentService "oneclickvirt/service/agent"

	"github.com/gin-gonic/gin"
)

type InstanceEgressReconcileRequest struct {
	Apply *bool `json:"apply"`
}

type InstanceEgressDependencyRequest struct {
	PackageSet string `json:"package_set"`
}

func parseInstanceEgressID(c *gin.Context) (uint, bool) {
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || instanceID == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return 0, false
	}
	if err := ensureInstanceOwner(c, uint(instanceID)); err != nil {
		common.ResponseWithError(c, err)
		return 0, false
	}
	return uint(instanceID), true
}

// GetInstanceEgress returns Agent capabilities, sanitized profiles, the
// instance binding and its existing traffic-monitor counters.
// @Summary 获取实例独立出口状态
// @Description 返回节点能力、脱敏配置、绑定、fail-closed执行状态和流量计数
// @Tags 管理员/实例
// @Security BearerAuth
// @Produce json
// @Param id path uint true "实例ID"
// @Success 200 {object} common.Response "获取成功"
// @Failure 400 {object} common.Response "参数或配置错误"
// @Failure 403 {object} common.Response "无权访问实例"
// @Router /admin/instances/{id}/egress [get]
func GetInstanceEgress(c *gin.Context) {
	instanceID, ok := parseInstanceEgressID(c)
	if !ok {
		return
	}
	service := agentService.NewInstanceEgressService(global.APP_DB)
	status, err := service.GetStatus(c.Request.Context(), instanceID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, status)
}

// BindInstanceEgress writes one profile and binding to the node Agent, stores
// controller desired state, then reconciles. A blocked reconcile is returned
// as success with fail-closed state so the UI can report it without implying
// that traffic silently fell back to the host route.
// @Summary 设置实例独立出口
// @Description 保存控制端期望状态，下发节点Agent并执行fail-closed协调
// @Tags 管理员/实例
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path uint true "实例ID"
// @Param body body agentService.InstanceEgressBindRequest true "出口配置与绑定"
// @Success 200 {object} common.Response "配置已保存"
// @Failure 400 {object} common.Response "请求、网络模式或Agent能力不支持"
// @Failure 403 {object} common.Response "无权访问实例"
// @Router /admin/instances/{id}/egress [put]
func BindInstanceEgress(c *gin.Context) {
	instanceID, ok := parseInstanceEgressID(c)
	if !ok {
		return
	}
	var req agentService.InstanceEgressBindRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+err.Error()))
		return
	}
	service := agentService.NewInstanceEgressService(global.APP_DB)
	result, err := service.Bind(c.Request.Context(), instanceID, req)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	message := "独立出口配置已保存"
	if result.Reconcile != nil && result.Reconcile.Applied {
		message = "独立出口配置已保存并生效"
	} else if result.Reconcile != nil && result.Reconcile.FailClosed {
		message = "独立出口配置已保存，当前处于fail-closed状态"
	} else if result.ReconcileError != "" {
		message = "独立出口配置已保存，但节点协调失败"
	}
	common.ResponseSuccess(c, result, message)
}

// UnbindInstanceEgress godoc
// @Summary 解除实例独立出口
// @Description 持久化待清理标记，删除Agent绑定并重新协调
// @Tags 管理员/实例
// @Security BearerAuth
// @Produce json
// @Param id path uint true "实例ID"
// @Param apply query bool false "是否立即应用" default(true)
// @Success 200 {object} common.Response "解绑结果"
// @Failure 400 {object} common.Response "参数或节点配置错误"
// @Failure 403 {object} common.Response "无权访问实例"
// @Router /admin/instances/{id}/egress [delete]
func UnbindInstanceEgress(c *gin.Context) {
	instanceID, ok := parseInstanceEgressID(c)
	if !ok {
		return
	}
	apply := true
	if raw := c.Query("apply"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, "apply参数无效"))
			return
		}
		apply = value
	}
	service := agentService.NewInstanceEgressService(global.APP_DB)
	result, err := service.Unbind(c.Request.Context(), instanceID, apply)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	message := "已解除实例独立出口绑定"
	if result.ReconcileError != "" {
		message = "已解除绑定，但节点清理协调失败"
	}
	common.ResponseSuccess(c, result, message)
}

// ReconcileInstanceEgress godoc
// @Summary 协调实例独立出口
// @Description 从控制端期望状态恢复Agent配置并返回路由计划结果
// @Tags 管理员/实例
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path uint true "实例ID"
// @Param body body InstanceEgressReconcileRequest true "协调参数"
// @Success 200 {object} common.Response "协调结果"
// @Failure 400 {object} common.Response "参数或期望状态错误"
// @Failure 403 {object} common.Response "无权访问实例"
// @Router /admin/instances/{id}/egress/reconcile [post]
func ReconcileInstanceEgress(c *gin.Context) {
	instanceID, ok := parseInstanceEgressID(c)
	if !ok {
		return
	}
	apply := true
	var req InstanceEgressReconcileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+err.Error()))
		return
	}
	if req.Apply != nil {
		apply = *req.Apply
	}
	service := agentService.NewInstanceEgressService(global.APP_DB)
	result, err := service.Reconcile(c.Request.Context(), instanceID, apply)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	message := "独立出口协调完成"
	if !result.Reconcile.Applied && result.Reconcile.FailClosed {
		message = "独立出口未生效，实例流量保持fail-closed"
	}
	common.ResponseSuccess(c, result, message)
}

// EnsureInstanceEgressDependencies godoc
// @Summary 安装实例出口依赖
// @Description 在已启用自动安装保护的节点Agent上安装native或WireGuard依赖集
// @Tags 管理员/实例
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path uint true "实例ID"
// @Param body body InstanceEgressDependencyRequest true "依赖集（native或wireguard）"
// @Success 200 {object} common.Response "依赖安装与能力检查结果"
// @Failure 400 {object} common.Response "依赖集或Agent安装保护无效"
// @Failure 403 {object} common.Response "无权访问实例"
// @Router /admin/instances/{id}/egress/dependencies [post]
func EnsureInstanceEgressDependencies(c *gin.Context) {
	instanceID, ok := parseInstanceEgressID(c)
	if !ok {
		return
	}
	var req InstanceEgressDependencyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+err.Error()))
		return
	}
	service := agentService.NewInstanceEgressService(global.APP_DB)
	result, err := service.EnsureDependencies(c.Request.Context(), instanceID, req.PackageSet)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	message := result.Result.Message
	if message == "" {
		message = "节点依赖检查完成"
	}
	common.ResponseSuccess(c, result, message)
}
