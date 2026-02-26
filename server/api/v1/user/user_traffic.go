package user

import (
	"oneclickvirt/service/pmacct"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	userService "oneclickvirt/service/user"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetInstancePmacctSummary 获取实例pmacct流量汇总
// @Summary 获取实例pmacct流量汇总
// @Description 获取用户实例的pmacct流量汇总信息，包括今日、本月和总流量统计
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param instance_id path int true "实例ID"
// @Success 200 {object} common.Response{data=monitoring.PmacctSummary} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 403 {object} common.Response "无权限访问该实例"
// @Failure 404 {object} common.Response "实例不存在"
// @Failure 500 {object} common.Response "获取失败"
// @Router /user/instances/{instance_id}/pmacct/summary [get]
func GetInstancePmacctSummary(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "实例ID格式错误"))
		return
	}

	// 验证用户是否有权限访问该实例
	userServiceInstance := userService.NewService()
	_, err = userServiceInstance.GetInstanceDetail(userID, uint(instanceID))
	if err != nil {
		if err.Error() == "实例不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "实例不存在或无权限"))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "验证实例权限失败"))
		}
		return
	}

	// pmacct不需要interfaceName，因为它只监控一个公网IP
	pmacctService := pmacct.NewService()
	summary, err := pmacctService.GetPmacctSummary(uint(instanceID))
	if err != nil {
		global.APP_LOG.Error("获取实例pmacct汇总失败",
			zap.Uint("userID", userID),
			zap.Uint64("instanceID", instanceID),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	global.APP_LOG.Info("用户获取实例pmacct汇总成功",
		zap.Uint("userID", userID),
		zap.Uint64("instanceID", instanceID))

	common.ResponseSuccess(c, summary, "获取pmacct汇总成功")
}

// QueryInstancePmacctData 查询实例pmacct流量数据
// @Summary 查询实例pmacct流量数据
// @Description 查询实例的pmacct流量数据
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param instance_id path int true "实例ID"
// @Success 200 {object} common.Response{data=monitoring.PmacctSummary} "查询成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 403 {object} common.Response "无权限访问该实例"
// @Failure 404 {object} common.Response "实例不存在"
// @Failure 500 {object} common.Response "查询失败"
// @Router /user/instances/{instance_id}/pmacct/query [get]
func QueryInstancePmacctData(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "实例ID格式错误"))
		return
	}

	// 验证用户是否有权限访问该实例
	userServiceInstance := userService.NewService()
	_, err = userServiceInstance.GetInstanceDetail(userID, uint(instanceID))
	if err != nil {
		if err.Error() == "实例不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "实例不存在或无权限"))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "验证实例权限失败"))
		}
		return
	}

	pmacctService := pmacct.NewService()
	summary, err := pmacctService.GetPmacctSummary(uint(instanceID))
	if err != nil {
		global.APP_LOG.Error("查询pmacct数据失败",
			zap.Uint("userID", userID),
			zap.Uint64("instanceID", instanceID),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	global.APP_LOG.Info("用户查询pmacct数据成功",
		zap.Uint("userID", userID),
		zap.Uint64("instanceID", instanceID))

	common.ResponseSuccess(c, summary, "查询pmacct数据成功")
}

// RedeemCode 用户兑换码兑换实例
// @Summary 兑换码兑换
// @Description 通过兑换码获取对应的云实例
// @Tags 用户实例管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body object true "兑换码请求 {\"code\": \"XXXXXXXXXXXXXXXX\"}"
// @Success 200 {object} common.Response "兑换成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "未登录"
// @Router /user/redemption-codes/redeem [post]
func RedeemCode(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "请输入兑换码"))
		return
	}

	userServiceInstance := userService.NewService()
	if err := userServiceInstance.RedeemCode(userID, req.Code); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "兑换成功，实例已绑定到您的账户")
}
