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

func GetSnapshotOverview(c *gin.Context) {
	service := &snapshotSvc.Service{}
	data, err := service.OverviewForAdmin(middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, data)
}

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

func GetSnapshotTask(c *gin.Context) {
	taskID, ok := parsePathUint(c, "id")
	if !ok {
		return
	}
	service := &snapshotSvc.Service{}
	task, err := service.GetSnapshotTask(taskID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, task)
}

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
