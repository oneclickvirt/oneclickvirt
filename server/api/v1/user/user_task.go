package user

import (
	"oneclickvirt/service/task"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	"oneclickvirt/model/user"
	userService "oneclickvirt/service/user"

	"github.com/gin-gonic/gin"
)

// GetUserTasks 获取用户任务列表
// @Summary 获取用户任务列表
// @Description 获取当前用户的任务列表
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param taskType query string false "任务类型"
// @Param status query string false "任务状态"
// @Param providerId query string false "节点ID"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/tasks [get]
func GetUserTasks(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.UserTasksRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userServiceInstance := userService.NewService()
	tasks, total, err := userServiceInstance.GetUserTasks(userID, req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取任务列表失败"))
		return
	}

	common.ResponseSuccessWithPagination(c, tasks, total, req.Page, req.PageSize)
}

// CancelUserTask 取消用户任务
// @Summary 取消用户任务
// @Description 用户取消自己的等待中任务
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param taskId path int true "任务ID"
// @Success 200 {object} common.Response "操作成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "操作失败"
// @Router /user/tasks/{taskId}/cancel [post]
func CancelUserTask(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	taskIDStr := c.Param("taskId")
	taskID, err := strconv.ParseUint(taskIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的任务ID"))
		return
	}

	taskService := task.GetTaskService()
	if err := taskService.CancelTask(uint(taskID), userID); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "任务已取消")
}

// CreateUserInstance 创建实例
// @Summary 创建实例
// @Description 用户创建新的虚拟机或容器实例（异步处理）
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body user.CreateInstanceRequest true "创建实例请求参数"
// @Success 200 {object} common.Response{data=object} "任务创建成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "创建失败"
// @Router /user/instances [post]
func CreateUserInstance(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.CreateInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Error("CreateUserInstance binding error: " + err.Error())
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}

	userServiceInstance := userService.NewService()
	task, err := userServiceInstance.CreateUserInstance(userID, req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	// 返回任务信息
	responseData := map[string]interface{}{
		"taskId":     task.ID,
		"status":     task.Status,
		"message":    "实例创建任务已提交，正在后台处理",
		"created_at": task.CreatedAt,
	}

	common.ResponseSuccess(c, responseData, "实例创建任务已提交")
}

// GetInstanceTypePermissions 获取实例类型权限配置
// @Summary 获取实例类型权限配置
// @Description 获取当前用户可以创建的实例类型权限配置，基于用户配额和Provider能力
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/instance-type-permissions [get]
func GetInstanceTypePermissions(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	userServiceInstance := userService.NewService()
	permissions, err := userServiceInstance.GetInstanceTypePermissions(userID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取实例类型权限配置失败"))
		return
	}

	common.ResponseSuccess(c, permissions)
}
