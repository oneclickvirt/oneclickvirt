package admin

import (
	"net/http"
	"strconv"

	"oneclickvirt/model/common"
	adminProvider "oneclickvirt/service/admin/provider"

	"github.com/gin-gonic/gin"
)

// GetProviderIPv4Pool 获取服务商IPv4地址池列表
// @Summary 获取IPv4地址池
// @Description 管理员获取指定服务商的IPv4地址池（分页）
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(100)
// @Success 200 {object} common.Response "获取成功"
// @Router /admin/providers/{id}/ipv4-pool [get]
func GetProviderIPv4Pool(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: common.CodeInvalidParam, Msg: "无效的服务商ID"})
		return
	}

	page := 1
	pageSize := 100
	if p, err := strconv.Atoi(c.DefaultQuery("page", "1")); err == nil && p > 0 {
		page = p
	}
	if ps, err := strconv.Atoi(c.DefaultQuery("pageSize", "100")); err == nil && ps > 0 && ps <= 500 {
		pageSize = ps
	}

	svc := adminProvider.NewIPv4PoolService()
	entries, total, err := svc.GetIPv4Pool(uint(providerID), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: common.CodeInternalError, Msg: "获取地址池失败"})
		return
	}

	totalInt, allocated, available := svc.GetPoolStats(uint(providerID))

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "获取成功",
		Data: gin.H{
			"list":     entries,
			"total":    total,
			"page":     page,
			"pageSize": pageSize,
			"stats": gin.H{
				"total":     totalInt,
				"allocated": allocated,
				"available": available,
			},
		},
	})
}

// SetProviderIPv4Pool 向服务商IPv4地址池中追加地址
// @Summary 设置IPv4地址池
// @Description 向指定服务商的IPv4地址池中追加IP（每行一个IP或CIDR）
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param body body object true "地址列表（每行一个IP/CIDR，可含注释行）"
// @Success 200 {object} common.Response "设置成功"
// @Router /admin/providers/{id}/ipv4-pool [post]
func SetProviderIPv4Pool(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: common.CodeInvalidParam, Msg: "无效的服务商ID"})
		return
	}

	var req struct {
		Addresses string `json:"addresses" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: common.CodeInvalidParam, Msg: "请求参数错误: " + err.Error()})
		return
	}

	svc := adminProvider.NewIPv4PoolService()
	added, invalidLines, err := svc.SetIPv4Pool(uint(providerID), req.Addresses)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: common.CodeInvalidParam, Msg: err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "设置成功",
		Data: gin.H{
			"added":        added,
			"addedCount":   len(added),
			"invalidLines": invalidLines,
		},
	})
}

// ClearProviderIPv4Pool 清空服务商IPv4地址池中未分配的地址
// @Summary 清空未分配IPv4地址
// @Description 清空指定服务商地址池中所有未分配的IP地址
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Success 200 {object} common.Response "清空成功"
// @Router /admin/providers/{id}/ipv4-pool [delete]
func ClearProviderIPv4Pool(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: common.CodeInvalidParam, Msg: "无效的服务商ID"})
		return
	}

	svc := adminProvider.NewIPv4PoolService()
	count, err := svc.ClearUnallocated(uint(providerID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: common.CodeInternalError, Msg: "清空失败"})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: common.CodeSuccess,
		Msg:  "清空成功",
		Data: gin.H{"deleted": count},
	})
}

// DeleteProviderIPv4PoolEntry 删除地址池中的单个未分配地址
// @Summary 删除单个IPv4地址
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param entry_id path int true "地址条目ID"
// @Success 200 {object} common.Response "删除成功"
// @Router /admin/providers/{id}/ipv4-pool/{entry_id} [delete]
func DeleteProviderIPv4PoolEntry(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: common.CodeInvalidParam, Msg: "无效的服务商ID"})
		return
	}
	entryID, err := strconv.ParseUint(c.Param("entry_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: common.CodeInvalidParam, Msg: "无效的条目ID"})
		return
	}

	svc := adminProvider.NewIPv4PoolService()
	if err := svc.DeleteAddress(uint(providerID), uint(entryID)); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: common.CodeInvalidParam, Msg: err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: common.CodeSuccess, Msg: "删除成功"})
}
