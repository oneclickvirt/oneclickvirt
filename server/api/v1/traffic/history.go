package traffic

import (
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	monitoringModel "oneclickvirt/model/monitoring"
	"oneclickvirt/service/traffic"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetInstanceTrafficHistory 获取实例流量历史数据
// @Tags 流量管理
// @Summary 获取实例流量历史
// @Description 获取指定实例的历史流量数据，支持5分钟到24小时的灵活时间范围
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param instance_id path int true "实例ID"
// @Param period query string false "时间范围: 5m, 10m, 15m, 30m, 45m, 1h, 6h, 12h, 24h" default(1h)
// @Param interval query int false "数据点间隔（分钟），0表示自动选择，可选: 5, 15, 30, 60" default(0)
// @Param includeArchived query bool false "是否包含已归档数据（重置前的历史记录）" default(false)
// @Success 200 {object} common.Response{data=[]monitoring.InstanceTrafficHistory}
// @Failure 400 {object} common.Response
// @Failure 401 {object} common.Response
// @Failure 500 {object} common.Response
// @Router /v1/user/instances/{instance_id}/traffic/history [get]
func (api *UserTrafficAPI) GetInstanceTrafficHistory(c *gin.Context) {
	// 获取实例ID
	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的实例ID"))
		return
	}

	// 获取查询参数
	period := c.DefaultQuery("period", "1h")
	intervalStr := c.DefaultQuery("interval", "0")
	interval, err := strconv.Atoi(intervalStr)
	if err != nil || interval < 0 {
		interval = 0 // 默认自动选择
	}

	// 获取includeArchived参数
	includeArchived := c.DefaultQuery("includeArchived", "false") == "true"

	// 验证period参数
	validPeriods := map[string]bool{
		"5m": true, "10m": true, "15m": true, "30m": true, "45m": true,
		"1h": true, "6h": true, "12h": true, "24h": true,
	}
	if !validPeriods[period] {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "period参数必须是5m, 10m, 15m, 30m, 45m, 1h, 6h, 12h, 24h之一"))
		return
	}

	// 验证interval参数
	if interval != 0 && interval != 5 && interval != 15 && interval != 30 && interval != 60 {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "interval参数必须是0, 5, 15, 30, 60之一"))
		return
	}

	// 验证用户是否有权限访问该实例
	userID, exists := c.Get("user_id")
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未登录"))
		return
	}

	// 获取用户类型
	userType, _ := c.Get("user_type")
	isAdmin := userType == "admin"

	// 验证实例是否存在以及用户是否有权限访问
	var instanceUserID uint
	err = global.APP_DB.Table("instances").
		Select("user_id").
		Where("id = ?", instanceID).
		Scan(&instanceUserID).Error
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "实例不存在或无权限"))
		return
	}

	// 管理员可以访问所有实例，普通用户只能访问自己的实例
	if !isAdmin && instanceUserID != userID.(uint) {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "无权限访问该实例"))
		return
	}

	// 获取历史数据
	historyService := traffic.NewHistoryService()
	histories, err := historyService.GetInstanceTrafficHistory(uint(instanceID), period, interval, includeArchived)
	if err != nil {
		global.APP_LOG.Error("获取实例流量历史失败",
			zap.Uint("instanceID", uint(instanceID)),
			zap.Bool("includeArchived", includeArchived),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取流量历史失败"))
		return
	}

	// 如果没有数据，返回空数组而不是nil
	if histories == nil {
		histories = []monitoringModel.InstanceTrafficHistory{}
	}

	common.ResponseSuccess(c, histories, "获取流量历史成功")
}

// GetProviderTrafficHistory 获取Provider流量历史数据
// @Tags 流量管理-管理员
// @Summary 获取Provider流量历史
// @Description 获取指定Provider的历史流量数据，支持5分钟到24小时的灵活时间范围
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param provider_id path int true "Provider ID"
// @Param period query string false "时间范围: 5m, 10m, 15m, 30m, 45m, 1h, 6h, 12h, 24h" default(1h)
// @Param interval query int false "数据点间隔（分钟），0表示自动选择" default(0)
// @Success 200 {object} common.Response{data=[]monitoring.ProviderTrafficHistory}
// @Failure 400 {object} common.Response
// @Failure 401 {object} common.Response
// @Failure 500 {object} common.Response
// @Router /v1/admin/providers/{provider_id}/traffic/history [get]
func GetProviderTrafficHistory(c *gin.Context) {
	// 获取Provider ID
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	// 获取查询参数
	period := c.DefaultQuery("period", "1h")
	intervalStr := c.DefaultQuery("interval", "0")
	interval, err := strconv.Atoi(intervalStr)
	if err != nil || interval < 0 {
		interval = 0
	}

	// 验证period参数
	validPeriods := map[string]bool{
		"5m": true, "10m": true, "15m": true, "30m": true, "45m": true,
		"1h": true, "6h": true, "12h": true, "24h": true,
	}
	if !validPeriods[period] {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "period参数必须是5m, 10m, 15m, 30m, 45m, 1h, 6h, 12h, 24h之一"))
		return
	}

	// 验证interval参数
	if interval != 0 && interval != 5 && interval != 15 && interval != 30 && interval != 60 {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "interval参数必须是0, 5, 15, 30, 60之一"))
		return
	}

	// 获取历史数据
	historyService := traffic.NewHistoryService()
	histories, err := historyService.GetProviderTrafficHistory(uint(providerID), period, interval)
	if err != nil {
		global.APP_LOG.Error("获取Provider流量历史失败",
			zap.Uint("providerID", uint(providerID)),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取流量历史失败"))
		return
	}

	// 如果没有数据，返回空数组而不是nil
	if histories == nil {
		histories = []monitoringModel.ProviderTrafficHistory{}
	}

	common.ResponseSuccess(c, histories, "获取流量历史成功")
}

// GetUserTrafficHistory 获取用户流量历史数据
// @Tags 流量管理
// @Summary 获取用户流量历史
// @Description 获取当前用户的历史流量数据，支持5分钟到24小时的灵活时间范围
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param period query string false "时间范围: 5m, 10m, 15m, 30m, 45m, 1h, 6h, 12h, 24h" default(1h)
// @Param interval query int false "数据点间隔（分钟），0表示自动选择" default(0)
// @Success 200 {object} common.Response{data=[]monitoring.UserTrafficHistory}
// @Failure 400 {object} common.Response
// @Failure 401 {object} common.Response
// @Failure 500 {object} common.Response
// @Router /v1/user/traffic/history [get]
func (api *UserTrafficAPI) GetUserTrafficHistory(c *gin.Context) {
	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未登录"))
		return
	}

	// 获取查询参数
	period := c.DefaultQuery("period", "1h")
	intervalStr := c.DefaultQuery("interval", "0")
	interval, err := strconv.Atoi(intervalStr)
	if err != nil || interval < 0 {
		interval = 0
	}

	// 验证period参数
	validPeriods := map[string]bool{
		"5m": true, "10m": true, "15m": true, "30m": true, "45m": true,
		"1h": true, "6h": true, "12h": true, "24h": true,
	}
	if !validPeriods[period] {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "period参数必须是5m, 10m, 15m, 30m, 45m, 1h, 6h, 12h, 24h之一"))
		return
	}

	// 验证interval参数
	if interval != 0 && interval != 5 && interval != 15 && interval != 30 && interval != 60 {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "interval参数必须是0, 5, 15, 30, 60之一"))
		return
	}

	// 获取历史数据
	historyService := traffic.NewHistoryService()
	histories, err := historyService.GetUserTrafficHistory(userID.(uint), period, interval)
	if err != nil {
		global.APP_LOG.Error("获取用户流量历史失败",
			zap.Uint("userID", userID.(uint)),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取流量历史失败"))
		return
	}

	// 如果没有数据，返回空数组而不是nil
	if histories == nil {
		histories = []monitoringModel.UserTrafficHistory{}
	}

	common.ResponseSuccess(c, histories, "获取流量历史成功")
}
