package admin

import (
	"net/http"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	adminSystem "oneclickvirt/service/admin/system"
	"strconv"

	"github.com/gin-gonic/gin"
)

// GetAnnouncements 获取公告列表
// @Summary 获取公告列表
// @Description 获取系统公告列表（分页）
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/announcements [get]
func GetAnnouncements(c *gin.Context) {
	var req admin.AnnouncementListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	// 设置默认分页参数
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 10
	}

	// 状态过滤逻辑：只有当URL中明确有status参数时才进行状态过滤
	statusParam := c.Query("status")
	if statusParam == "" {
		req.Status = -1 // -1表示获取所有状态
	}
	// 如果有status参数，ShouldBindQuery已经正确处理了参数绑定

	systemService := adminSystem.NewService()
	announcements, total, err := systemService.GetAnnouncementList(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "获取公告列表失败",
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "获取成功",
		Data: map[string]interface{}{
			"list":  announcements,
			"total": total,
		},
	})
}

// CreateAnnouncement 创建公告
// @Summary 创建公告
// @Description 管理员创建新公告
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.CreateAnnouncementRequest true "创建公告请求参数"
// @Success 200 {object} common.Response "创建成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建失败"
// @Router /admin/announcements [post]
func CreateAnnouncement(c *gin.Context) {
	var req admin.CreateAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	// 获取当前用户ID
	uid, err := getUserIDFromContext(c)
	if err != nil {
		respondUnauthorized(c, "未授权")
		return
	}

	systemService := adminSystem.NewService()
	err = systemService.CreateAnnouncement(req, uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "创建公告成功",
	})
}

// UpdateAnnouncementItem 更新公告
// @Summary 更新公告
// @Description 管理员更新公告信息
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "公告ID"
// @Param request body admin.UpdateAnnouncementRequest true "更新公告请求参数"
// @Success 200 {object} common.Response "更新成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新失败"
// @Router /admin/announcements/{id} [put]
func UpdateAnnouncementItem(c *gin.Context) {
	announcementIDStr := c.Param("id")
	announcementID, err := strconv.ParseUint(announcementIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的公告ID",
		})
		return
	}

	var req admin.UpdateAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	// 设置公告ID
	req.ID = uint(announcementID)

	systemService := adminSystem.NewService()
	err = systemService.UpdateAnnouncement(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "更新公告成功",
	})
}

// DeleteAnnouncement 删除公告
// @Summary 删除公告
// @Description 管理员删除公告
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "公告ID"
// @Success 200 {object} common.Response "删除成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "删除失败"
// @Router /admin/announcements/{id} [delete]
func DeleteAnnouncement(c *gin.Context) {
	announcementIDStr := c.Param("id")
	announcementID, err := strconv.ParseUint(announcementIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的公告ID",
		})
		return
	}

	systemService := adminSystem.NewService()
	err = systemService.DeleteAnnouncement(uint(announcementID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "删除公告成功",
	})
}

// BatchDeleteAnnouncements 批量删除公告
// @Summary 批量删除公告
// @Description 管理员批量删除公告
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.BatchAnnouncementRequest true "批量操作请求"
// @Success 200 {object} common.Response "删除成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "删除失败"
// @Router /admin/announcements/batch-delete [delete]
func BatchDeleteAnnouncements(c *gin.Context) {
	var req admin.BatchAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "请选择要删除的公告",
		})
		return
	}

	systemService := adminSystem.NewService()
	err := systemService.BatchDeleteAnnouncements(req.IDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "批量删除公告成功",
	})
}

// BatchUpdateAnnouncementStatus 批量更新公告状态
// @Summary 批量更新公告状态
// @Description 管理员批量更新公告状态
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.BatchUpdateStatusRequest true "批量状态更新请求"
// @Success 200 {object} common.Response "更新成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新失败"
// @Router /admin/announcements/batch-status [put]
func BatchUpdateAnnouncementStatus(c *gin.Context) {
	var req admin.BatchUpdateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "请选择要更新的公告",
		})
		return
	}

	systemService := adminSystem.NewService()
	err := systemService.BatchUpdateAnnouncementStatus(req.IDs, req.Status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "批量更新公告状态成功",
	})
}
