package admin

import (
	"oneclickvirt/middleware"
	"oneclickvirt/service/task"
	"strconv"

	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// GetTaskPoolStatus 获取任务池全局状态
// @Summary 获取任务池全局状态
// @Description 获取任务池是否接受新任务以及当前队列排空状态
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=adminModel.TaskPoolStatusResponse} "获取成功"
// @Failure 401 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/tasks/pool-status [get]
func GetTaskPoolStatus(c *gin.Context) {
	taskService := task.GetTaskService()
	status, err := taskService.GetTaskPoolStatus()
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, status)
}

// UpdateTaskPoolStatus 更新任务池全局开关
// @Summary 更新任务池全局开关
// @Description 超级管理员开启/关闭任务池新任务入口，关闭后不会再新增 pending 任务，但已有任务继续执行
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body adminModel.TaskPoolControlRequest true "任务池开关请求"
// @Success 200 {object} common.Response{data=adminModel.TaskPoolStatusResponse} "更新成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "更新失败"
// @Router /admin/tasks/pool-status [put]
func UpdateTaskPoolStatus(c *gin.Context) {
	var req adminModel.TaskPoolControlRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	taskService := task.GetTaskService()
	status, err := taskService.SetTaskPoolEnabled(req.Enabled, req.Message)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, status)
}

// GetAdminTasks 获取管理员任务列表
// @Summary 获取管理员任务列表
// @Description 获取所有用户的任务列表，支持分页和筛选
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "页大小" default(10)
// @Param providerId query int false "Provider ID"
// @Param username query string false "用户名"
// @Param taskType query string false "任务类型"
// @Param status query string false "任务状态"
// @Param instanceType query string false "实例类型"
// @Success 200 {object} common.Response{data=adminModel.AdminTaskListResponse} "获取成功"
// @Failure 401 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/tasks [get]
func GetAdminTasks(c *gin.Context) {
	var req adminModel.AdminTaskListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	// 设置默认值
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 10
	}

	taskService := task.GetTaskService()
	tasks, total, err := taskService.GetAdminTasks(req, middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	response := adminModel.AdminTaskListResponse{
		List:     tasks,
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
	}

	common.ResponseSuccess(c, response)
}

// ForceStopTask 强制停止任务
// @Summary 强制停止任务
// @Description 管理员强制停止运行中的任务
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body adminModel.ForceStopTaskRequest true "强制停止任务请求"
// @Success 200 {object} common.Response "操作成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "操作失败"
// @Router /admin/tasks/force-stop [post]
func ForceStopTask(c *gin.Context) {
	var req adminModel.ForceStopTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	taskService := task.GetTaskService()
	if err := taskService.ForceStopTask(req.TaskID, req.Reason); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "任务已强制停止")
}

// GetTaskStats 获取任务统计
// @Summary 获取任务统计
// @Description 获取系统任务统计信息
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=adminModel.TaskStatsResponse} "获取成功"
// @Failure 401 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/tasks/stats [get]
func GetTaskStats(c *gin.Context) {
	taskService := task.GetTaskService()
	stats, err := taskService.GetTaskStats()
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, stats)
}

// CancelUserTask 管理员取消用户任务
// @Summary 管理员取消用户任务
// @Description 管理员取消指定用户的任务
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param taskId path int true "任务ID"
// @Success 200 {object} common.Response "操作成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "操作失败"
// @Router /admin/tasks/{taskId}/cancel [post]
func CancelUserTaskByAdmin(c *gin.Context) {
	taskIDStr := c.Param("taskId")
	taskID, err := strconv.ParseUint(taskIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的任务ID"))
		return
	}

	taskService := task.GetTaskService()
	if err := taskService.CancelTaskByAdmin(uint(taskID), "管理员取消"); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "任务已取消")
}

// GetTaskOverallStats 获取任务总体统计信息
// @Summary 获取任务总体统计信息
// @Description 获取所有任务的总体统计信息，包括各种状态的任务数量
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=adminModel.TaskStatsResponse} "获取成功"
// @Failure 401 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/tasks/overall-stats [get]
func GetTaskOverallStats(c *gin.Context) {
	taskService := task.GetTaskService()
	stats, err := taskService.GetTaskOverallStats()
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, stats)
}

// GetTaskDetail 获取任务详情
// @Summary 获取任务详情
// @Description 管理员获取指定任务的详细信息
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param taskId path int true "任务ID"
// @Success 200 {object} common.Response{data=adminModel.AdminTaskDetailResponse} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "权限不足"
// @Failure 404 {object} common.Response "任务不存在"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/tasks/{taskId} [get]
func GetTaskDetail(c *gin.Context) {
	taskIDStr := c.Param("taskId")
	taskID, err := strconv.ParseUint(taskIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的任务ID"))
		return
	}

	taskService := task.GetTaskService()
	detail, err := taskService.GetTaskDetail(uint(taskID))
	if err != nil {
		if err.Error() == "任务不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, "任务不存在"))
			return
		}
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, detail)
}
