package admin

import (
	"context"
	"net/http"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	adminProvider "oneclickvirt/service/admin/provider"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// DiscoverProviderInstances 发现Provider上的实例
// @Summary 发现Provider实例
// @Description 扫描Provider上所有已存在的实例，返回实例列表
// @Tags Provider管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=adminProvider.DiscoveryResult} "发现成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/providers/{id}/discover [post]
func DiscoverProviderInstances(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "Provider ID无效",
		})
		return
	}

	providerService := adminProvider.NewService()
	result, err := providerService.DiscoverProviderInstances(context.Background(), uint(providerID))
	if err != nil {
		global.APP_LOG.Error("发现Provider实例失败",
			zap.Uint64("providerId", providerID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "发现实例失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "发现实例成功",
		Data: result,
	})
}

// ImportProviderInstances 导入发现的实例
// @Summary 导入Provider实例
// @Description 将发现的实例导入到系统中进行管理
// @Tags Provider管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Param request body adminProvider.ImportOptions true "导入选项"
// @Success 200 {object} common.Response{data=adminProvider.ImportResult} "导入成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/providers/{id}/import [post]
func ImportProviderInstances(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "Provider ID无效",
		})
		return
	}

	var importOptions adminProvider.ImportOptions
	if err := c.ShouldBindJSON(&importOptions); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	// 确保ProviderID一致
	importOptions.ProviderID = uint(providerID)

	// 如果没有指定MarkConflicts，默认启用
	if c.Query("skipConflictCheck") != "true" {
		importOptions.MarkConflicts = true
	}

	providerService := adminProvider.NewService()
	result, err := providerService.ImportDiscoveredInstances(context.Background(), importOptions)
	if err != nil {
		global.APP_LOG.Error("导入Provider实例失败",
			zap.Uint64("providerId", providerID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "导入实例失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "导入实例成功",
		Data: result,
	})
}

// GetOrphanedInstances 获取未纳管的实例列表
// @Summary 获取未纳管实例
// @Description 获取Provider上已存在但未被系统纳管的实例列表
// @Tags Provider管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=[]provider.DiscoveredInstance} "获取成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/providers/{id}/orphaned [get]
func GetOrphanedInstances(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "Provider ID无效",
		})
		return
	}

	providerService := adminProvider.NewService()
	orphanedInstances, err := providerService.GetOrphanedInstances(context.Background(), uint(providerID))
	if err != nil {
		global.APP_LOG.Error("获取未纳管实例失败",
			zap.Uint64("providerId", providerID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "获取未纳管实例失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "获取成功",
		Data: map[string]interface{}{
			"orphanedInstances": orphanedInstances,
			"total":             len(orphanedInstances),
		},
	})
}

// CheckInstanceSync 检查实例同步状态
// @Summary 检查实例同步
// @Description 比较数据库实例与Provider远程实例，检测新增、删除和变化的实例
// @Tags Provider管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=adminProvider.InstanceSyncReport} "检查成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/providers/{id}/sync-check [post]
func CheckInstanceSync(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "Provider ID无效",
		})
		return
	}

	providerService := adminProvider.NewService()
	report, err := providerService.CompareInstancesWithRemote(context.Background(), uint(providerID))
	if err != nil {
		global.APP_LOG.Error("检查实例同步失败",
			zap.Uint64("providerId", providerID),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "检查实例同步失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "检查完成",
		Data: report,
	})
}
