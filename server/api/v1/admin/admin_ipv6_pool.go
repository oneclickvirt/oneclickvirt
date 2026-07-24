package admin

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"oneclickvirt/model/common"
	"oneclickvirt/service/ipv6pool"

	"github.com/gin-gonic/gin"
)

func parseProviderID(c *gin.Context) (uint, error) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return 0, common.NewError(common.CodeValidationError, "无效的服务商ID")
	}
	return uint(id), nil
}

// GetProviderIPv6Pool 获取服务商IPv6地址池列表
// @Summary 获取IPv6地址池
// @Description 管理员获取指定服务商的IPv6地址池（分页）
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(100)
// @Success 200 {object} common.Response "获取成功"
// @Router /admin/providers/{id}/ipv6-pool [get]
func GetProviderIPv6Pool(c *gin.Context) {
	providerID, err := parseProviderID(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	if err := ensureProviderOwner(c, providerID); err != nil {
		common.ResponseWithError(c, err)
		return
	}
	page, pageSize := 1, 100
	if p, e := strconv.Atoi(c.DefaultQuery("page", "1")); e == nil && p > 0 {
		page = p
	}
	if p, e := strconv.Atoi(c.DefaultQuery("pageSize", "100")); e == nil && p > 0 && p <= 500 {
		pageSize = p
	}
	svc := ipv6pool.NewService()
	entries, total, err := svc.GetIPv6Pool(providerID, page, pageSize)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	stats, err := svc.GetPoolStatsDetail(providerID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{"list": entries, "total": total, "page": page, "pageSize": pageSize, "stats": ipv6PoolStatsResponse(stats)}, "获取成功")
}

// SetProviderIPv6Pool 向服务商IPv6地址池追加地址或网段
// @Summary 设置IPv6地址池
// @Description 向指定服务商的IPv6地址池中追加离散地址或CIDR网段
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param body body object true "IPv6地址或CIDR列表"
// @Success 200 {object} common.Response "设置成功"
// @Router /admin/providers/{id}/ipv6-pool [post]
func SetProviderIPv6Pool(c *gin.Context) {
	providerID, err := parseProviderID(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	if err := ensureProviderOwner(c, providerID); err != nil {
		common.ResponseWithError(c, err)
		return
	}
	var req struct {
		Addresses string `json:"addresses" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+err.Error()))
		return
	}
	added, invalid, err := ipv6pool.NewService().SetIPv6Pool(providerID, req.Addresses)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	common.ResponseSuccess(c, gin.H{"added": added, "addedCount": len(added), "invalidLines": invalid}, "设置成功")
}

// ClearProviderIPv6Pool 清空服务商IPv6地址池中未分配的地址
// @Summary 清空未分配IPv6地址
// @Description 清空指定服务商地址池中所有未分配的IPv6地址和无绑定网段
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Success 200 {object} common.Response "清空成功"
// @Router /admin/providers/{id}/ipv6-pool [delete]
func ClearProviderIPv6Pool(c *gin.Context) {
	providerID, err := parseProviderID(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	if err := ensureProviderOwner(c, providerID); err != nil {
		common.ResponseWithError(c, err)
		return
	}
	deleted, err := ipv6pool.NewService().ClearUnallocated(providerID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{"deleted": deleted}, "清空成功")
}

// DeleteProviderIPv6PoolEntry 删除地址池中的单个未分配IPv6条目
// @Summary 删除单个IPv6地址条目
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param entry_id path int true "地址条目ID"
// @Success 200 {object} common.Response "删除成功"
// @Router /admin/providers/{id}/ipv6-pool/{entry_id} [delete]
func DeleteProviderIPv6PoolEntry(c *gin.Context) {
	providerID, err := parseProviderID(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	if err := ensureProviderOwner(c, providerID); err != nil {
		common.ResponseWithError(c, err)
		return
	}
	entryID, err := strconv.ParseUint(c.Param("entry_id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的条目ID"))
		return
	}
	if err := ipv6pool.NewService().DeleteAddress(providerID, uint(entryID)); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	common.ResponseSuccess(c, nil, "删除成功")
}

// SyncProviderIPv6Pool reads the configured node-side address file once and
// reconciles it with the controller pool. The service performs all remote I/O
// before its short database transaction.
// @Summary 同步节点IPv6地址文件
// @Description 读取指定服务商节点上的IPv6地址文件并与主控地址池对账
// @Tags 服务商管理
// @Security BearerAuth
// @Param id path int true "服务商ID"
// @Param body body object false "可选的节点IPv6文件绝对路径"
// @Success 200 {object} common.Response "同步成功"
// @Router /admin/providers/{id}/ipv6-pool/sync [post]
func SyncProviderIPv6Pool(c *gin.Context) {
	providerID, err := parseProviderID(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	if err := ensureProviderOwner(c, providerID); err != nil {
		common.ResponseWithError(c, err)
		return
	}
	var req struct {
		FilePath *string `json:"filePath"`
	}
	if bindErr := json.NewDecoder(c.Request.Body).Decode(&req); bindErr != nil && !errors.Is(bindErr, io.EOF) {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+bindErr.Error()))
		return
	}
	service := ipv6pool.NewService()
	var result ipv6pool.SyncResult
	if req.FilePath != nil {
		result, err = service.SyncProviderFile(c.Request.Context(), providerID, *req.FilePath)
	} else {
		result, err = service.SyncProviderFile(c.Request.Context(), providerID)
	}
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{
		"path": result.Path, "added": result.Added, "removed": result.Removed,
		"preservedAllocated": result.PreservedAllocated, "invalidLines": result.InvalidLines,
		"parsedCount": result.ParsedCount, "remoteReadCount": result.RemoteReadCount,
		"stats":    ipv6PoolStatsResponse(result.Stats),
		"syncedAt": result.SyncedAt,
	}, "同步成功")
}

func ipv6PoolStatsResponse(stats ipv6pool.PoolStats) gin.H {
	return gin.H{
		"total": stats.Entries, "entries": stats.Entries,
		"materialized": stats.Materialized, "ranges": stats.Ranges,
		"openRanges": stats.OpenRanges, "pendingRetire": stats.PendingRetire, "allocated": stats.Allocated,
		"reusable": stats.Reusable, "available": stats.Available,
		"availableExact": stats.AvailableExact, "availableSaturated": stats.AvailableSaturated,
		"availableSemantic": "reusable_addresses_plus_remaining_range_capacity_upper_bound",
	}
}
