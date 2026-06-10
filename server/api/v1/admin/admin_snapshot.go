package admin

import (
	"strconv"

	"oneclickvirt/model/common"
	snapshotSvc "oneclickvirt/service/snapshot"

	"github.com/gin-gonic/gin"
)

func GetSnapshotOverview(c *gin.Context) {
	service := &snapshotSvc.Service{}
	data, err := service.Overview()
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
	list, total, err := service.ListSnapshots(filter)
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
	filter := snapshotSvc.ListFilter{Page: page, PageSize: pageSize, InstanceID: instanceID}
	service := &snapshotSvc.Service{}
	list, total, err := service.ListSnapshots(filter)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccessWithPagination(c, list, total, filter.Page, filter.PageSize)
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
	creator := uint(0)
	if v, exists := c.Get("user_id"); exists {
		if id, ok := v.(uint); ok {
			creator = id
		}
	}
	service := &snapshotSvc.Service{}
	result, err := service.StartCreateSnapshotTask(instanceID, req, creator, "manual")
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
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
	creator := uint(0)
	if v, exists := c.Get("user_id"); exists {
		if id, ok := v.(uint); ok {
			creator = id
		}
	}
	result, err := service.StartDeleteSnapshotTask(snapshotID, creator)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
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
	creator := uint(0)
	if v, exists := c.Get("user_id"); exists {
		if id, ok := v.(uint); ok {
			creator = id
		}
	}
	result, err := service.StartRestoreSnapshotTask(snapshotID, creator)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}
	common.ResponseSuccess(c, result, "快照恢复任务已提交")
}

func GetSnapshotSchedules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	service := &snapshotSvc.Service{}
	list, total, err := service.ListSchedules(page, pageSize)
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
	creator := uint(0)
	if v, exists := c.Get("user_id"); exists {
		if id, ok := v.(uint); ok {
			creator = id
		}
	}
	service := &snapshotSvc.Service{}
	schedule, err := service.CreateSchedule(req, creator)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
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
	schedule, err := service.UpdateSchedule(id, req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
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
	if err := service.DeleteSchedule(id); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}
	common.ResponseSuccess(c, nil)
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
