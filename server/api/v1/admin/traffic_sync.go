package admin

import (
	"strconv"

	"oneclickvirt/model/common"
	"oneclickvirt/service/traffic"

	"github.com/gin-gonic/gin"
)

// SyncInstanceTraffic 手动同步单个实例的流量数据
// @Tags 管理员-流量同步
// @Summary 手动同步单个实例的流量数据
// @Description 立即触发指定实例的流量数据同步
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param instance_id path int true "实例ID"
// @Success 200 {object} common.Response "同步成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "未认证"
// @Failure 403 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "内部错误"
// @Router /admin/traffic/sync/instance/{instance_id} [post]
func SyncInstanceTraffic(c *gin.Context) {
	// 检查管理员权限
	if !requireAdminOnly(c) {
		return
	}

	// 获取实例ID
	instanceIDStr := c.Param("instance_id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的实例ID"))
		return
	}

	// 触发同步
	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerInstanceTrafficSync(uint(instanceID), "管理员手动触发")

	common.ResponseSuccess(c, nil, "实例流量同步已触发")
}

// SyncUserTraffic 手动同步用户所有实例的流量数据
// @Tags 管理员-流量同步
// @Summary 手动同步用户所有实例的流量数据
// @Description 立即触发指定用户所有实例的流量数据同步
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param user_id path int true "用户ID"
// @Success 200 {object} common.Response "同步成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "未认证"
// @Failure 403 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "内部错误"
// @Router /admin/traffic/sync/user/{user_id} [post]
func SyncUserTraffic(c *gin.Context) {
	// 检查管理员权限
	if !requireAdminOnly(c) {
		return
	}

	// 获取用户ID
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的用户ID"))
		return
	}

	// 触发同步
	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerUserTrafficSync(uint(userID), "管理员手动触发")

	common.ResponseSuccess(c, nil, "用户流量同步已触发")
}

// SyncProviderTraffic 手动同步Provider所有实例的流量数据
// @Tags 管理员-流量同步
// @Summary 手动同步Provider所有实例的流量数据
// @Description 立即触发指定Provider所有实例的流量数据同步
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param provider_id path int true "Provider ID"
// @Success 200 {object} common.Response "同步成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "未认证"
// @Failure 403 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "内部错误"
// @Router /admin/traffic/sync/provider/{provider_id} [post]
func SyncProviderTraffic(c *gin.Context) {
	// 检查管理员权限
	if !requireAdminOnly(c) {
		return
	}

	// 获取Provider ID
	providerIDStr := c.Param("provider_id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	// 触发同步
	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerProviderTrafficSync(uint(providerID), "管理员手动触发")

	common.ResponseSuccess(c, nil, "Provider流量同步已触发")
}

// SyncAllTraffic 手动同步全系统流量数据
// @Tags 管理员-流量同步
// @Summary 手动同步全系统流量数据
// @Description 立即触发全系统所有实例的流量数据同步
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response "同步成功"
// @Failure 401 {object} common.Response "未认证"
// @Failure 403 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "内部错误"
// @Router /admin/traffic/sync/all [post]
func SyncAllTraffic(c *gin.Context) {
	// 检查管理员权限
	if !requireAdminOnly(c) {
		return
	}

	// 触发全系统流量同步
	go func() {
		threeTierService := traffic.NewThreeTierLimitService()
		if err := threeTierService.CheckAllTrafficLimits(c.Request.Context()); err != nil {
			// 错误会在服务内部记录日志
		}
	}()

	common.ResponseSuccess(c, nil, "全系统流量同步已触发")
}
