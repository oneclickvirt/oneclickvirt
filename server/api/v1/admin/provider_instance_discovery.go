package admin

import (
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	adminProvider "oneclickvirt/service/admin/provider"
	"oneclickvirt/service/scheduler"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// QueueProviderInstanceSync 将非纯净节点实例同步挂入管理员任务池。
// @Summary 提交节点实例同步任务
// @Description 为已开启非纯净节点发现的Provider创建持久化后台同步任务
// @Tags Provider管理
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=adminModel.Task} "同步任务已提交"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 409 {object} common.Response "已有同类任务"
// @Router /admin/providers/{id}/sync-instances [post]
func QueueProviderInstanceSync(c *gin.Context) {
	providerID, err := parseOwnedProviderID(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	userID, err := getUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "无法识别当前管理员"))
		return
	}
	created, err := adminProvider.NewService().CreateConfiguredInstanceSyncTask(providerID, userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	respondQueuedProviderTask(c, created, "实例同步任务已提交，请在管理员任务列表查看进度")
}

// QueueProviderRecoverySync submits an explicit post-reboot recovery task.
// @Summary 提交节点强制恢复同步任务
// @Description 一次有界远端发现；仅为明确期望运行且远端已停止的实例入队启动，并重放节点级映射与出口状态
// @Tags Provider管理
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=adminModel.Task} "强制恢复同步任务已提交"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 404 {object} common.Response "Provider不存在"
// @Failure 409 {object} common.Response "已有恢复同步任务或恢复租约"
// @Router /admin/providers/{id}/force-recovery-sync [post]
func QueueProviderRecoverySync(c *gin.Context) {
	providerID, err := parseOwnedProviderID(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	userID, err := getUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "无法识别当前管理员"))
		return
	}
	created, err := scheduler.CreateProviderRecoverySyncTask(providerID, userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	respondQueuedProviderTask(c, created, "强制恢复同步任务已提交，请在管理员任务列表查看进度")
}

// QueueProviderHealthCheck 将可能触发远端探测和资源同步的健康检查挂入任务池。
// @Summary 提交Provider健康检查任务
// @Description 创建持久化后台任务执行远端健康探测与资源刷新
// @Tags Provider管理
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=adminModel.Task} "健康检查任务已提交"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 404 {object} common.Response "Provider不存在"
// @Failure 409 {object} common.Response "已有同类任务"
// @Router /admin/providers/{id}/health-check-task [post]
func QueueProviderHealthCheck(c *gin.Context) {
	providerID, err := parseOwnedProviderID(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	userID, err := getUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "无法识别当前管理员"))
		return
	}
	created, err := adminProvider.NewService().CreateHealthCheckTask(providerID, userID, true)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	respondQueuedProviderTask(c, created, "健康检查任务已提交，请在管理员任务列表查看进度")
}

func respondQueuedProviderTask(c *gin.Context, created *adminModel.Task, message string) {
	common.ResponseSuccess(c, created, message)
}

func parseOwnedProviderID(c *gin.Context) (uint, error) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || providerID == 0 {
		return 0, common.NewError(common.CodeValidationError, "Provider ID无效")
	}
	if ownerAdminID := middleware.GetOwnerAdminID(c); ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(uint(providerID), ownerAdminID); err != nil {
			return 0, common.NewError(common.CodeForbidden, "无权操作该Provider")
		}
	}
	return uint(providerID), nil
}

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
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider ID无效"))
		return
	}
	if err := ensureProviderOwner(c, uint(providerID)); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	providerService := adminProvider.NewService()
	result, err := providerService.DiscoverProviderInstances(c.Request.Context(), uint(providerID))
	if err != nil {
		global.APP_LOG.Error("发现Provider实例失败",
			zap.Uint64("providerId", providerID),
			zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, result, "发现实例成功")
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
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider ID无效"))
		return
	}
	if err := ensureProviderOwner(c, uint(providerID)); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	var importOptions adminProvider.ImportOptions
	if err := c.ShouldBindJSON(&importOptions); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}

	// 验证至少指定了要导入的实例
	if len(importOptions.InstanceUUIDs) == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "必须指定要导入的实例(instanceUuids)"))
		return
	}

	// 确保ProviderID一致
	importOptions.ProviderID = uint(providerID)

	// 如果没有指定MarkConflicts，默认启用
	if c.Query("skipConflictCheck") != "true" {
		importOptions.MarkConflicts = true
	}

	providerService := adminProvider.NewService()
	result, err := providerService.ImportDiscoveredInstances(c.Request.Context(), importOptions)
	if err != nil {
		global.APP_LOG.Error("导入Provider实例失败",
			zap.Uint64("providerId", providerID),
			zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, result, "导入实例成功")
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
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider ID无效"))
		return
	}
	if err := ensureProviderOwner(c, uint(providerID)); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	providerService := adminProvider.NewService()
	orphanedInstances, err := providerService.GetOrphanedInstances(c.Request.Context(), uint(providerID))
	if err != nil {
		global.APP_LOG.Error("获取未纳管实例失败",
			zap.Uint64("providerId", providerID),
			zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"orphanedInstances": orphanedInstances,
		"total":             len(orphanedInstances),
	}, "获取成功")
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
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider ID无效"))
		return
	}
	if err := ensureProviderOwner(c, uint(providerID)); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	providerService := adminProvider.NewService()
	report, err := providerService.CompareInstancesWithRemote(c.Request.Context(), uint(providerID))
	if err != nil {
		global.APP_LOG.Error("检查实例同步失败",
			zap.Uint64("providerId", providerID),
			zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, report, "检查完成")
}

// CleanupOrphanInstances 强制单向同步：删除远程孤儿实例
// @Summary 清理孤儿实例
// @Description 创建后台任务，强制单向删除远程服务器上存在但数据库中不存在的实例（主控数据库为权威来源，需双重确认）
// @Tags Provider管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response "清理任务已提交"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/providers/{id}/cleanup-orphans [post]
func CleanupOrphanInstances(c *gin.Context) {
	providerID, err := parseOwnedProviderID(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	userID, err := getUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "无法识别当前管理员"))
		return
	}
	created, err := adminProvider.NewService().CreateOrphanCleanupTask(providerID, userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, created, "孤儿实例清理任务已提交，请在管理员任务列表查看进度")
}
