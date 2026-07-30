package admin

import (
	"fmt"
	"net/http"
	"strconv"

	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	snapshotSvc "oneclickvirt/service/snapshot"
	"oneclickvirt/service/taskgate"

	"github.com/gin-gonic/gin"
)

// @Summary 获取快照 概览
// @Description 获取获取快照 概览
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取快照 概览成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取快照 概览失败"
// @Router /admin/snapshots/overview [get]
func GetSnapshotOverview(c *gin.Context) {
	service := &snapshotSvc.Service{}
	data, err := service.OverviewForAdmin(middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, data)
}

// @Summary 获取快照 列表
// @Description 获取获取快照 列表
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Success 200 {object} common.Response{data=object} "获取快照 列表成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取快照 列表失败"
// @Router /admin/snapshots [get]
func GetSnapshotList(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	providerID := parseUintQuery(c, "providerId")
	instanceID := parseUintQuery(c, "instanceId")
	filter := snapshotSvc.ListFilter{
		Page:         page,
		PageSize:     pageSize,
		ProviderID:   providerID,
		InstanceID:   instanceID,
		ProviderType: c.Query("providerType"),
		Status:       c.Query("status"),
	}
	service := &snapshotSvc.Service{}
	list, total, err := service.ListAdminSnapshots(filter, middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccessWithPagination(c, list, total, filter.Page, filter.PageSize)
}

// @Summary 获取实例 快照s
// @Description 获取获取实例 快照s
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取实例 快照s成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取实例 快照s失败"
// @Router /admin/instances/{id}/snapshots [get]
func GetInstanceSnapshots(c *gin.Context) {
	instanceID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	service := &snapshotSvc.Service{}
	list, total, err := service.ListAdminInstanceSnapshots(instanceID, page, pageSize, middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccessWithPagination(c, list, total, page, pageSize)
}

// @Summary 获取快照 任务
// @Description 获取获取快照 任务
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取快照 任务成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取快照 任务失败"
// @Router /admin/snapshot-tasks/{id} [get]
func GetSnapshotTask(c *gin.Context) {
	taskID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	service := &snapshotSvc.Service{}
	task, err := service.GetSnapshotTask(taskID, middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, task)
}

// @Summary 创建实例 快照
// @Description 创建创建实例 快照
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "创建实例 快照成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建实例 快照失败"
// @Router /admin/instances/{id}/snapshots [post]
func CreateInstanceSnapshot(c *gin.Context) {
	instanceID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	var req snapshotSvc.SnapshotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	service := &snapshotSvc.Service{}
	result, err := service.StartCreateSnapshotTaskForAdmin(instanceID, req, currentAdminID(c), middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result, "快照创建任务已提交")
}

// @Summary 批量Create 实例 快照s
// @Description 创建批量Create 实例 快照s
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "批量Create 实例 快照s成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "批量Create 实例 快照s失败"
// @Router /admin/snapshot-batches [post]
func BatchCreateInstanceSnapshots(c *gin.Context) {
	var req snapshotSvc.BatchSnapshotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	service := &snapshotSvc.Service{}
	result, err := service.StartBatchCreateSnapshotTasks(req, currentAdminID(c), middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result, "快照创建任务已提交")
}

// @Summary 删除快照
// @Description 删除删除快照
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "删除快照成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "删除快照失败"
// @Router /admin/snapshots/{id} [delete]
func DeleteSnapshot(c *gin.Context) {
	snapshotID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	service := &snapshotSvc.Service{}
	result, err := service.StartDeleteSnapshotTaskForAdmin(snapshotID, currentAdminID(c), middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result, "快照删除任务已提交")
}

// @Summary 恢复快照
// @Description 创建恢复快照
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "恢复快照成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "恢复快照失败"
// @Router /admin/snapshots/{id}/restore [post]
func RestoreSnapshot(c *gin.Context) {
	snapshotID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	service := &snapshotSvc.Service{}
	result, err := service.StartRestoreSnapshotTaskForAdmin(snapshotID, currentAdminID(c), middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result, "快照恢复任务已提交")
}

// @Summary 下载快照
// @Description 获取下载快照
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "下载快照成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "下载快照失败"
// @Router /admin/snapshots/download/{id} [get]
func DownloadSnapshot(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	snapshotID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	service := &snapshotSvc.Service{}
	payload, filename, err := service.BuildSnapshotDownloadManifest(snapshotID, 0, middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
}

// @Summary 获取快照 计划s
// @Description 获取获取快照 计划s
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取快照 计划s成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取快照 计划s失败"
// @Router /admin/snapshot-schedules [get]
func GetSnapshotSchedules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	service := &snapshotSvc.Service{}
	list, total, err := service.ListSchedulesForAdmin(page, pageSize, middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccessWithPagination(c, list, total, page, pageSize)
}

// @Summary 创建快照 计划
// @Description 创建创建快照 计划
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "创建快照 计划成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建快照 计划失败"
// @Router /admin/snapshot-schedules [post]
func CreateSnapshotSchedule(c *gin.Context) {
	var req snapshotSvc.ScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	service := &snapshotSvc.Service{}
	schedule, err := service.CreateScheduleForAdmin(req, currentAdminID(c), middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, schedule)
}

// @Summary 更新快照 计划
// @Description 更新更新快照 计划
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "更新快照 计划成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新快照 计划失败"
// @Router /admin/snapshot-schedules/{id} [put]
func UpdateSnapshotSchedule(c *gin.Context) {
	id, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	var req snapshotSvc.ScheduleUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	service := &snapshotSvc.Service{}
	schedule, err := service.UpdateScheduleForAdmin(id, req, middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, schedule)
}

// @Summary 删除快照 计划
// @Description 删除删除快照 计划
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "删除快照 计划成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "删除快照 计划失败"
// @Router /admin/snapshot-schedules/{id} [delete]
func DeleteSnapshotSchedule(c *gin.Context) {
	id, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	service := &snapshotSvc.Service{}
	if err := service.DeleteScheduleForAdmin(id, middleware.GetOwnerAdminID(c)); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil)
}

func currentAdminID(c *gin.Context) uint {
	if v, exists := c.Get("user_id"); exists {
		if id, ok := v.(uint); ok {
			return id
		}
	}
	return 0
}

func parsePathUint(c *gin.Context, key string) (uint, bool) {
	value, err := strconv.ParseUint(c.Param(key), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的ID"))
		return 0, false
	}
	return uint(value), true
}

func parseUintQuery(c *gin.Context, key string) uint {
	value := c.Query(key)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0
	}
	return uint(parsed)
}
