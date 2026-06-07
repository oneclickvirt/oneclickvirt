package traffic

import (
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	"oneclickvirt/service/pmacct"
	"oneclickvirt/service/traffic"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// UserTrafficAPI 用户流量API
type UserTrafficAPI struct{}

type instanceTrafficAccess struct {
	UserID              uint
	TrafficQuotaVisible bool
}

func ensureUserCanViewInstanceTraffic(userID, instanceID uint) error {
	var access instanceTrafficAccess
	tx := global.APP_DB.Table("instances").
		Select("instances.user_id, COALESCE(providers.traffic_quota_visible, 1) AS traffic_quota_visible").
		Joins("LEFT JOIN providers ON providers.id = instances.provider_id").
		Where("instances.id = ?", instanceID).
		Limit(1).
		Scan(&access)
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 || access.UserID != userID {
		return common.NewError(common.CodeForbidden, "无权限访问该实例")
	}
	if !access.TrafficQuotaVisible {
		return common.NewError(common.CodeForbidden, "该实例流量额度不可见")
	}
	return nil
}

// GetTrafficOverview 获取用户流量概览
// @Summary 获取用户流量概览
// @Description 基于pmacct获取用户流量使用情况概览
// @Tags 用户流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} common.Response
// @Router /user/traffic/overview [get]
func (api *UserTrafficAPI) GetTrafficOverview(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权访问"))
		return
	}

	userTrafficService := traffic.NewUserTrafficService()
	overview, err := userTrafficService.GetUserTrafficOverview(userID)
	if err != nil {
		global.APP_LOG.Error("获取用户流量概览失败",
			zap.Uint("userID", userID),
			zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, overview, "获取流量概览成功")
}

// GetInstanceTrafficDetail 获取实例流量详情
// @Summary 获取实例流量详情
// @Description 获取指定实例的详细流量统计信息
// @Tags 用户流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param instanceId path int true "实例ID"
// @Success 200 {object} common.Response
// @Router /user/traffic/instance/{instanceId} [get]
func (api *UserTrafficAPI) GetInstanceTrafficDetail(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权访问"))
		return
	}

	instanceIDStr := c.Param("instanceId")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "实例ID格式错误"))
		return
	}
	if err := ensureUserCanViewInstanceTraffic(userID, uint(instanceID)); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	userTrafficService := traffic.NewUserTrafficService()
	detail, err := userTrafficService.GetInstanceTrafficDetail(userID, uint(instanceID))
	if err != nil {
		global.APP_LOG.Error("获取实例流量详情失败",
			zap.Uint("userID", userID),
			zap.Uint("instanceID", uint(instanceID)),
			zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, detail, "获取实例流量详情成功")
}

// GetInstancesTrafficSummary 获取用户所有实例流量汇总
// @Summary 获取用户所有实例流量汇总
// @Description 获取用户所有实例的流量使用汇总信息
// @Tags 用户流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} common.Response
// @Router /user/traffic/instances [get]
func (api *UserTrafficAPI) GetInstancesTrafficSummary(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权访问"))
		return
	}

	userTrafficService := traffic.NewUserTrafficService()
	summary, err := userTrafficService.GetUserInstancesTrafficSummary(userID)
	if err != nil {
		global.APP_LOG.Error("获取用户实例流量汇总失败",
			zap.Uint("userID", userID),
			zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, summary, "获取实例流量汇总成功")
}

// GetTrafficLimitStatus 获取流量限制状态
// @Summary 获取流量限制状态
// @Description 获取用户的流量限制状态和受限实例信息
// @Tags 用户流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} common.Response
// @Router /user/traffic/limit-status [get]
func (api *UserTrafficAPI) GetTrafficLimitStatus(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权访问"))
		return
	}

	userTrafficService := traffic.NewUserTrafficService()
	status, err := userTrafficService.GetTrafficLimitStatus(userID)
	if err != nil {
		global.APP_LOG.Error("获取流量限制状态失败",
			zap.Uint("userID", userID),
			zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, status, "获取流量限制状态成功")
}

// GetPmacctData 获取原始pmacct数据
// @Summary 获取原始pmacct数据
// @Description 获取指定实例的原始pmacct统计数据
// @Tags 用户流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param instanceId path int true "实例ID"
// @Param interface query string false "网络接口名称"
// @Success 200 {object} common.Response
// @Router /user/traffic/pmacct/{instanceId} [get]
func (api *UserTrafficAPI) GetPmacctData(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权访问"))
		return
	}

	instanceIDStr := c.Param("instanceId")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "实例ID格式错误"))
		return
	}
	if err := ensureUserCanViewInstanceTraffic(userID, uint(instanceID)); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// 获取pmacct数据（pmacct不需要interfaceName，因为它只监控一个公网IP）
	pmacctService := pmacct.NewService()
	pmacctSummary, err := pmacctService.GetPmacctSummary(uint(instanceID))
	if err != nil {
		global.APP_LOG.Error("获取pmacct数据失败",
			zap.Uint("userID", userID),
			zap.Uint("instanceID", uint(instanceID)),
			zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, pmacctSummary, "获取pmacct数据成功")
}

// getUserIDFromContext 从上下文中获取用户ID（使用全局函数）
func getUserIDFromContext(c *gin.Context) uint {
	userID, err := middleware.GetUserIDFromContext(c)
	if err != nil {
		return 0
	}
	return userID
}
