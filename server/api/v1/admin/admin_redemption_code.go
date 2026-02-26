package admin

import (
	"net/http"

	"oneclickvirt/middleware"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	redemptionService "oneclickvirt/service/admin/redemption"
	"oneclickvirt/service/task"

	"github.com/gin-gonic/gin"
)

// GetRedemptionCodeList 获取兑换码列表
// @Summary 获取兑换码列表
// @Description 管理员获取兑换码列表，支持分页和筛选
// @Tags 兑换码管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param code query string false "兑换码关键字"
// @Param status query string false "状态筛选"
// @Param providerId query int false "节点ID筛选"
// @Success 200 {object} common.Response "获取成功"
// @Router /admin/redemption-codes [get]
func GetRedemptionCodeList(c *gin.Context) {
	var req adminModel.RedemptionCodeListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 400, Msg: "参数错误"})
		return
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 10
	}

	svc := redemptionService.NewService(task.GetTaskService())
	codes, total, err := svc.GetList(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 500, Msg: "获取兑换码列表失败"})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "获取成功",
		Data: map[string]interface{}{
			"list":  codes,
			"total": total,
		},
	})
}

// BatchCreateRedemptionCodes 批量创建兑换码
// @Summary 批量创建兑换码
// @Description 管理员批量创建兑换码，触发后台实例创建任务
// @Tags 兑换码管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body adminModel.BatchCreateRedemptionCodesRequest true "批量创建请求"
// @Success 200 {object} common.Response "创建成功"
// @Router /admin/redemption-codes/batch-create [post]
func BatchCreateRedemptionCodes(c *gin.Context) {
	var req adminModel.BatchCreateRedemptionCodesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 400, Msg: "参数错误: " + err.Error()})
		return
	}

	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, common.Response{Code: 401, Msg: "未登录"})
		return
	}
	adminID := authCtx.UserID

	svc := redemptionService.NewService(task.GetTaskService())
	if err := svc.BatchCreate(req, adminID); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 500, Msg: err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 200, Msg: "批量创建成功，实例创建任务已提交"})
}

// BatchDeleteRedemptionCodes 批量删除兑换码
// @Summary 批量删除兑换码
// @Description 管理员批量删除兑换码（硬删除）及关联实例
// @Tags 兑换码管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body adminModel.BatchDeleteRedemptionCodesRequest true "批量删除请求"
// @Success 200 {object} common.Response "删除成功"
// @Router /admin/redemption-codes/batch-delete [post]
func BatchDeleteRedemptionCodes(c *gin.Context) {
	var req adminModel.BatchDeleteRedemptionCodesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 400, Msg: "参数错误: " + err.Error()})
		return
	}

	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, common.Response{Code: 401, Msg: "未登录"})
		return
	}
	adminID := authCtx.UserID

	svc := redemptionService.NewService(task.GetTaskService())
	if err := svc.BatchDelete(req.IDs, adminID); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 500, Msg: err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 200, Msg: "批量删除成功"})
}

// ExportRedemptionCodes 导出兑换码
// @Summary 导出兑换码
// @Description 管理员导出指定兑换码的字符串列表
// @Tags 兑换码管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body adminModel.ExportRedemptionCodesRequest true "导出请求"
// @Success 200 {object} common.Response "导出成功"
// @Router /admin/redemption-codes/export [post]
func ExportRedemptionCodes(c *gin.Context) {
	var req adminModel.ExportRedemptionCodesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 400, Msg: "参数错误"})
		return
	}

	svc := redemptionService.NewService(task.GetTaskService())
	codes, err := svc.ExportByIDs(req.IDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 500, Msg: "导出失败"})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "导出成功",
		Data: map[string]interface{}{
			"codes": codes,
			"count": len(codes),
		},
	})
}
