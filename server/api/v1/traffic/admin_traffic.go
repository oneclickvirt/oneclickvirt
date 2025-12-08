package traffic

import (
	"fmt"
	"net/http"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	"oneclickvirt/service/traffic"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AdminTrafficAPI 管理员流量API
type AdminTrafficAPI struct{}

// GetSystemTrafficOverview 获取系统流量概览
// @Summary 获取系统流量概览
// @Description 获取整个系统的流量使用情况概览
// @Tags 管理员流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} common.Response
// @Router /api/v1/admin/traffic/overview [get]
func (api *AdminTrafficAPI) GetSystemTrafficOverview(c *gin.Context) {
	trafficLimitService := traffic.NewLimitService()

	// 获取系统全局流量统计
	systemStats, err := trafficLimitService.GetSystemTrafficStats()
	if err != nil {
		global.APP_LOG.Error("获取系统流量统计失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 50000,
			Msg:  "获取系统流量统计失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  "获取系统流量概览成功",
		Data: systemStats,
	})
}

// GetProviderTrafficStats 获取Provider流量统计
// @Summary 获取Provider流量统计
// @Description 获取指定Provider的流量使用情况
// @Tags 管理员流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param providerId path int true "Provider ID"
// @Success 200 {object} common.Response
// @Router /api/v1/admin/traffic/provider/{providerId} [get]
func (api *AdminTrafficAPI) GetProviderTrafficStats(c *gin.Context) {
	providerIDStr := c.Param("providerId")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 40000,
			Msg:  "Provider ID格式错误",
		})
		return
	}

	trafficLimitService := traffic.NewLimitService()

	// 获取Provider流量使用情况
	providerUsage, err := trafficLimitService.GetProviderTrafficUsageWithPmacct(uint(providerID))
	if err != nil {
		global.APP_LOG.Error("获取Provider流量统计失败",
			zap.Uint("providerID", uint(providerID)),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 50000,
			Msg:  "获取Provider流量统计失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  "获取Provider流量统计成功",
		Data: providerUsage,
	})
}

// GetUserTrafficStats 获取用户流量统计
// @Summary 获取用户流量统计
// @Description 获取指定用户的流量使用情况
// @Tags 管理员流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param userId path int true "用户ID"
// @Success 200 {object} common.Response
// @Router /api/v1/admin/traffic/user/{userId} [get]
func (api *AdminTrafficAPI) GetUserTrafficStats(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 40000,
			Msg:  "用户ID格式错误",
		})
		return
	}

	trafficLimitService := traffic.NewLimitService()

	// 获取用户流量使用情况
	userUsage, err := trafficLimitService.GetUserTrafficUsageWithPmacct(uint(userID))
	if err != nil {
		global.APP_LOG.Error("获取用户流量统计失败",
			zap.Uint("userID", uint(userID)),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 50000,
			Msg:  "获取用户流量统计失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  "获取用户流量统计成功",
		Data: userUsage,
	})
}

// GetAllUsersTrafficRank 获取所有用户流量排行
// @Summary 获取用户流量排行榜
// @Description 获取系统中所有用户的流量使用排行榜，支持分页和搜索
// @Tags 管理员流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param page query int false "页码，默认1"
// @Param pageSize query int false "每页数量，默认10"
// @Param username query string false "按用户名搜索"
// @Param nickname query string false "按昵称搜索"
// @Success 200 {object} common.Response
// @Router /api/v1/admin/traffic/users/rank [get]
func (api *AdminTrafficAPI) GetAllUsersTrafficRank(c *gin.Context) {
	// 获取分页参数
	pageStr := c.DefaultQuery("page", "1")
	pageSizeStr := c.DefaultQuery("pageSize", "10")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page <= 0 {
		page = 1
	}
	pageSize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pageSize <= 0 {
		pageSize = 10
	}

	// 获取搜索参数
	username := c.Query("username")
	nickname := c.Query("nickname")

	trafficLimitService := traffic.NewLimitService()

	// 获取用户流量排行榜
	userRankings, total, err := trafficLimitService.GetUsersTrafficRanking(page, pageSize, username, nickname)
	if err != nil {
		global.APP_LOG.Error("获取用户流量排行榜失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 50000,
			Msg:  "获取用户流量排行榜失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  "获取用户流量排行榜成功",
		Data: map[string]interface{}{
			"rankings": userRankings,
			"total":    total,
			"page":     page,
			"pageSize": pageSize,
		},
	})
}

// ManageTrafficLimits 管理流量限制
// @Summary 管理流量限制
// @Description 手动设置或解除用户/Provider的流量限制
// @Tags 管理员流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param request body ManageTrafficLimitRequest true "流量限制管理请求"
// @Success 200 {object} common.Response
// @Router /api/v1/admin/traffic/manage [post]
func (api *AdminTrafficAPI) ManageTrafficLimits(c *gin.Context) {
	var req ManageTrafficLimitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 40000,
			Msg:  "请求参数错误: " + err.Error(),
		})
		return
	}

	trafficLimitService := traffic.NewLimitService()

	var err error
	var result string

	switch req.Type {
	case "user":
		if req.Action == "limit" {
			err = trafficLimitService.SetUserTrafficLimit(req.TargetID, req.Reason)
			result = "设置用户流量限制"
		} else if req.Action == "unlimit" {
			err = trafficLimitService.RemoveUserTrafficLimit(req.TargetID)
			result = "解除用户流量限制"
		} else {
			c.JSON(http.StatusBadRequest, common.Response{
				Code: 40000,
				Msg:  "不支持的操作类型",
			})
			return
		}
	case "provider":
		if req.Action == "limit" {
			err = trafficLimitService.SetProviderTrafficLimit(req.TargetID, req.Reason)
			result = "设置Provider流量限制"
		} else if req.Action == "unlimit" {
			err = trafficLimitService.RemoveProviderTrafficLimit(req.TargetID)
			result = "解除Provider流量限制"
		} else {
			c.JSON(http.StatusBadRequest, common.Response{
				Code: 40000,
				Msg:  "不支持的操作类型",
			})
			return
		}
	default:
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 40000,
			Msg:  "不支持的目标类型",
		})
		return
	}

	if err != nil {
		global.APP_LOG.Error("管理流量限制失败",
			zap.String("type", req.Type),
			zap.String("action", req.Action),
			zap.Uint("targetID", req.TargetID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 50000,
			Msg:  result + "失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  result + "成功",
		Data: map[string]interface{}{
			"type":      req.Type,
			"action":    req.Action,
			"target_id": req.TargetID,
			"reason":    req.Reason,
		},
	})
}

// ManageTrafficLimitRequest 流量限制管理请求
type ManageTrafficLimitRequest struct {
	Type     string `json:"type" binding:"required"`      // "user" 或 "provider"
	Action   string `json:"action" binding:"required"`    // "limit" 或 "unlimit"
	TargetID uint   `json:"target_id" binding:"required"` // 目标用户ID或Provider ID
	Reason   string `json:"reason"`                       // 限制原因（仅在action为limit时需要）
}

// BatchManageTrafficLimits 批量管理流量限制
// @Summary 批量管理流量限制
// @Description 批量设置或解除用户的流量限制
// @Tags 管理员流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param request body BatchManageTrafficLimitRequest true "批量流量限制管理请求"
// @Success 200 {object} common.Response
// @Router /api/v1/admin/traffic/batch-manage [post]
func (api *AdminTrafficAPI) BatchManageTrafficLimits(c *gin.Context) {
	var req BatchManageTrafficLimitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 40000,
			Msg:  "请求参数错误: " + err.Error(),
		})
		return
	}

	if len(req.UserIDs) == 0 {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 40000,
			Msg:  "用户ID列表不能为空",
		})
		return
	}

	trafficLimitService := traffic.NewLimitService()

	successCount := 0
	failCount := 0
	var errors []string

	for _, userID := range req.UserIDs {
		var err error
		if req.Action == "limit" {
			err = trafficLimitService.SetUserTrafficLimit(userID, req.Reason)
		} else if req.Action == "unlimit" {
			err = trafficLimitService.RemoveUserTrafficLimit(userID)
		} else {
			errors = append(errors, fmt.Sprintf("用户ID %d: 不支持的操作类型", userID))
			failCount++
			continue
		}

		if err != nil {
			errors = append(errors, fmt.Sprintf("用户ID %d: %s", userID, err.Error()))
			failCount++
		} else {
			successCount++
		}
	}

	result := "批量" + map[string]string{"limit": "限制", "unlimit": "解除限制"}[req.Action] + "流量"

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  fmt.Sprintf("%s完成，成功: %d, 失败: %d", result, successCount, failCount),
		Data: map[string]interface{}{
			"success_count": successCount,
			"fail_count":    failCount,
			"errors":        errors,
		},
	})
}

// BatchSyncUserTraffic 批量同步用户流量
// @Summary 批量同步用户流量
// @Description 批量触发用户流量数据同步
// @Tags 管理员流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param request body BatchSyncTrafficRequest true "批量同步流量请求"
// @Success 200 {object} common.Response
// @Router /api/v1/admin/traffic/batch-sync [post]
func (api *AdminTrafficAPI) BatchSyncUserTraffic(c *gin.Context) {
	var req BatchSyncTrafficRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 40000,
			Msg:  "请求参数错误: " + err.Error(),
		})
		return
	}

	if len(req.UserIDs) == 0 {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 40000,
			Msg:  "用户ID列表不能为空",
		})
		return
	}

	// 触发异步同步任务
	go func() {
		for _, userID := range req.UserIDs {
			// 这里可以调用实际的同步逻辑
			// 目前只是记录日志
			global.APP_LOG.Info("触发用户流量同步",
				zap.Uint("userID", userID))
		}
	}()

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  fmt.Sprintf("已触发 %d 个用户的流量同步任务", len(req.UserIDs)),
		Data: map[string]interface{}{
			"user_ids": req.UserIDs,
		},
	})
}

// BatchManageTrafficLimitRequest 批量流量限制管理请求
type BatchManageTrafficLimitRequest struct {
	Action  string `json:"action" binding:"required"` // "limit" 或 "unlimit"
	UserIDs []uint `json:"user_ids" binding:"required"`
	Reason  string `json:"reason"` // 限制原因（仅在action为limit时需要）
}

// BatchSyncTrafficRequest 批量同步流量请求
type BatchSyncTrafficRequest struct {
	UserIDs []uint `json:"user_ids" binding:"required"`
}

// ClearUserTrafficRecords 清空用户流量记录
// @Summary 清空用户流量记录
// @Description 删除指定用户的所有历史流量记录，用于重新计数
// @Tags 管理员流量
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param userId path int true "用户ID"
// @Success 200 {object} common.Response
// @Router /api/v1/admin/traffic/user/{userId}/clear [delete]
func (api *AdminTrafficAPI) ClearUserTrafficRecords(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 40000,
			Msg:  "用户ID格式错误",
		})
		return
	}

	clearService := traffic.NewClearService()

	deletedCount, err := clearService.ClearUserTrafficRecords(uint(userID))
	if err != nil {
		global.APP_LOG.Error("清空用户流量记录失败",
			zap.Uint("userID", uint(userID)),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 50000,
			Msg:  "清空用户流量记录失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 0,
		Msg:  "清空用户流量记录成功",
		Data: map[string]interface{}{
			"user_id":       userID,
			"deleted_count": deletedCount,
		},
	})
}
