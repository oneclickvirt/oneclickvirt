package admin

import (
	"net/http"
	"strconv"

	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	"oneclickvirt/service/admin/invite"

	"github.com/gin-gonic/gin"
)

// GetInviteCodeList 获取邀请码列表
// @Summary 获取邀请码列表
// @Description 管理员获取系统中的邀请码列表，支持分页和查询
// @Tags 邀请码管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param keyword query string false "搜索关键字"
// @Param status query string false "状态筛选"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/invite-codes [get]
func GetInviteCodeList(c *gin.Context) {
	var req admin.InviteCodeListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	inviteService := invite.NewService()
	codes, total, err := inviteService.GetInviteCodeList(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "获取邀请码列表失败",
		})
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

// CreateInviteCode 创建邀请码
// @Summary 创建邀请码
// @Description 管理员创建新的邀请码
// @Tags 邀请码管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.CreateInviteCodeRequest true "创建邀请码请求参数"
// @Success 200 {object} common.Response "创建成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/invite-codes [post]
func CreateInviteCode(c *gin.Context) {
	var req admin.CreateInviteCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	// 获取当前管理员ID
	createdBy := uint(1) // 这里应该从JWT中获取管理员ID

	inviteService := invite.NewService()
	err := inviteService.CreateInviteCode(req, createdBy)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "创建邀请码成功",
	})
}

// DeleteInviteCode 删除邀请码
// @Summary 删除邀请码
// @Description 管理员删除指定的邀请码
// @Tags 邀请码管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "邀请码ID"
// @Success 200 {object} common.Response "删除成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/invite-codes/{id} [delete]
func DeleteInviteCode(c *gin.Context) {
	codeIDStr := c.Param("id")
	codeID, err := strconv.ParseUint(codeIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的邀请码ID",
		})
		return
	}

	inviteService := invite.NewService()
	err = inviteService.DeleteInviteCode(uint(codeID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "删除邀请码成功",
	})
}

// GenerateInviteCode 批量生成邀请码
// @Summary 批量生成邀请码
// @Description 管理员批量生成邀请码，可指定数量和权限等级
// @Tags 邀请码管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.CreateInviteCodeRequest true "生成邀请码请求参数"
// @Success 200 {object} common.Response{data=[]string} "生成成功，返回邀请码列表"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/invite-codes/generate [post]
func GenerateInviteCode(c *gin.Context) {
	var req admin.CreateInviteCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	// 获取当前管理员ID
	createdBy := uint(1) // 这里应该从JWT中获取管理员ID

	inviteService := invite.NewService()
	codes, err := inviteService.GenerateInviteCodes(req, createdBy)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "生成邀请码成功",
		Data: codes,
	})
}

// BatchDeleteInviteCodes 批量删除邀请码
// @Summary 批量删除邀请码
// @Description 管理员批量删除指定的邀请码
// @Tags 邀请码管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.BatchDeleteInviteCodesRequest true "批量删除邀请码请求参数"
// @Success 200 {object} common.Response "删除成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/invite-codes/batch-delete [post]
func BatchDeleteInviteCodes(c *gin.Context) {
	var req admin.BatchDeleteInviteCodesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	inviteService := invite.NewService()
	err := inviteService.BatchDeleteInviteCodes(req.IDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "批量删除邀请码成功",
	})
}

// ExportInviteCodes 导出邀请码
// @Summary 导出邀请码
// @Description 导出选中的邀请码为文本格式（每行一个）
// @Tags 邀请码管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param ids query []int false "邀请码ID列表，不传则导出全部"
// @Success 200 {object} common.Response{data=[]string} "导出成功，返回邀请码列表"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/invite-codes/export [get]
func ExportInviteCodes(c *gin.Context) {
	var req struct {
		IDs []uint `form:"ids"`
	}

	// 从 query 参数获取 IDs（GET 请求）
	if err := c.ShouldBindQuery(&req); err != nil {
		// 如果没有传IDs，则导出全部
		req.IDs = nil
	}

	inviteService := invite.NewService()
	codes, err := inviteService.ExportInviteCodes(req.IDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "导出邀请码成功",
		Data: codes,
	})
}
