package admin

import (
	"net/http"
	"oneclickvirt/service/provider"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	"oneclickvirt/service/admin/instance"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/task"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetAdminDashboard 获取管理员仪表板
// @Summary 获取管理员仪表板
// @Description 获取管理员后台首页的统计数据
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/dashboard [get]
func GetAdminDashboard(c *gin.Context) {
	global.APP_LOG.Info("管理员获取仪表板数据", zap.String("admin_ip", c.ClientIP()))
	dashboardService := &resources.AdminDashboardService{}
	dashboard, err := dashboardService.GetAdminDashboard()
	if err != nil {
		global.APP_LOG.Error("获取管理员仪表板失败", zap.Error(err), zap.String("admin_ip", c.ClientIP()))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取管理员首页数据失败"))
		return
	}
	common.ResponseSuccess(c, dashboard)
}

// GetInstanceList 获取实例列表
// @Summary 获取实例列表
// @Description 管理员获取系统中所有实例的列表，支持分页和过滤
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param name query string false "实例名称"
// @Param status query string false "实例状态"
// @Param providerName query string false "节点名称"
// @Param ownerName query string false "所有者名称"
// @Param instance_type query string false "实例类型"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/instances [get]
func GetInstanceList(c *gin.Context) {
	var req admin.InstanceListRequest

	// 使用请求处理服务处理参数
	requestProcessService := provider.RequestProcessService{}
	if err := requestProcessService.ProcessInstanceListRequest(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	instanceService := instance.NewService(task.GetTaskService())
	instances, total, err := instanceService.GetInstanceList(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "获取实例列表失败",
		})
		return
	}
	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "获取成功",
		Data: map[string]interface{}{
			"list":  instances,
			"total": total,
		},
	})
}

// CreateInstance 创建实例
// @Summary 创建实例
// @Description 管理员创建新的虚拟化实例
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.CreateInstanceRequest true "创建实例请求参数"
// @Success 200 {object} common.Response "创建成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/instances [post]
func CreateInstance(c *gin.Context) {
	var req admin.CreateInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("管理员创建实例参数错误", zap.Error(err), zap.String("admin_ip", c.ClientIP()))
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	global.APP_LOG.Info("管理员开始创建实例",
		zap.String("instance_name", utils.TruncateString(req.Name, 50)),
		zap.String("provider", req.Provider),
		zap.String("admin_ip", c.ClientIP()))

	instanceService := instance.NewService(task.GetTaskService())
	err := instanceService.CreateInstance(req)
	if err != nil {
		global.APP_LOG.Error("管理员创建实例失败",
			zap.Error(err),
			zap.String("instance_name", utils.TruncateString(req.Name, 50)),
			zap.String("provider", req.Provider),
			zap.String("admin_ip", c.ClientIP()))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	global.APP_LOG.Info("管理员创建实例成功",
		zap.String("instance_name", utils.TruncateString(req.Name, 50)),
		zap.String("provider", req.Provider),
		zap.String("admin_ip", c.ClientIP()))

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "创建实例成功",
	})
}

func UpdateInstance(c *gin.Context) {
	var req admin.UpdateInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("管理员更新实例参数错误", zap.Error(err), zap.String("admin_ip", c.ClientIP()))
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	global.APP_LOG.Info("管理员开始更新实例",
		zap.Uint("instance_id", req.ID),
		zap.String("admin_ip", c.ClientIP()))

	instanceService := instance.NewService(task.GetTaskService())
	err := instanceService.UpdateInstance(req)
	if err != nil {
		global.APP_LOG.Error("管理员更新实例失败",
			zap.Error(err),
			zap.Uint("instance_id", req.ID),
			zap.String("admin_ip", c.ClientIP()))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	global.APP_LOG.Info("管理员更新实例成功",
		zap.Uint("instance_id", req.ID),
		zap.String("admin_ip", c.ClientIP()))

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "更新实例成功",
	})
}

func DeleteInstance(c *gin.Context) {
	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		global.APP_LOG.Warn("管理员删除实例ID参数错误",
			zap.Error(err),
			zap.String("instance_id_str", utils.TruncateString(instanceIDStr, 20)),
			zap.String("admin_ip", c.ClientIP()))
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的实例ID",
		})
		return
	}

	global.APP_LOG.Info("管理员开始删除实例",
		zap.Uint64("instance_id", instanceID),
		zap.String("admin_ip", c.ClientIP()))

	instanceService := instance.NewService(task.GetTaskService())
	err = instanceService.DeleteInstance(uint(instanceID))
	if err != nil {
		global.APP_LOG.Error("管理员删除实例失败",
			zap.Error(err),
			zap.Uint64("instance_id", instanceID),
			zap.String("admin_ip", c.ClientIP()))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 400,
			Msg:  err.Error(),
		})
		return
	}

	global.APP_LOG.Info("管理员删除任务创建成功",
		zap.Uint64("instance_id", instanceID),
		zap.String("admin_ip", c.ClientIP()))

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "删除任务已创建，请查看任务列表了解进度",
	})
}

// GetInstanceTypePermissions 获取实例类型权限配置
// @Summary 获取实例类型权限配置
// @Description 管理员获取实例类型最低等级要求配置
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/instance-type-permissions [get]
func GetAdminInstanceTypePermissions(c *gin.Context) {
	dashboardService := &resources.AdminDashboardService{}
	permissions := dashboardService.GetInstanceTypePermissions()

	common.ResponseSuccess(c, permissions)
}

// UpdateInstanceTypePermissions 更新实例类型权限配置
// @Summary 更新实例类型权限配置
// @Description 管理员更新实例类型最低等级要求配置
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.UpdateInstanceTypePermissionsRequest true "权限配置参数"
// @Success 200 {object} common.Response "更新成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新失败"
// @Router /admin/instance-type-permissions [put]
func UpdateAdminInstanceTypePermissions(c *gin.Context) {
	var req admin.UpdateInstanceTypePermissionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	dashboardService := &resources.AdminDashboardService{}
	err := dashboardService.UpdateInstanceTypePermissions(req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "权限配置更新成功")
}

// AdminInstanceAction 管理员执行实例操作
// @Summary 管理员执行实例操作
// @Description 管理员对实例执行启动、停止、重启等操作
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "实例ID"
// @Param request body admin.InstanceActionRequest true "操作请求参数"
// @Success 200 {object} common.Response "操作成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 403 {object} common.Response "权限不足"
// @Failure 404 {object} common.Response "实例不存在"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/instances/{id}/action [post]
func AdminInstanceAction(c *gin.Context) {
	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	var req admin.InstanceActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误"))
		return
	}

	// 验证操作类型
	validActions := map[string]bool{
		"start":   true,
		"stop":    true,
		"restart": true,
		"reset":   true,
		"delete":  true,
	}

	if !validActions[req.Action] {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的操作类型"))
		return
	}

	global.APP_LOG.Info("管理员执行实例操作",
		zap.Uint64("instanceId", instanceID),
		zap.String("action", req.Action),
		zap.String("admin_ip", c.ClientIP()))

	instanceService := instance.NewService(task.GetTaskService())
	err = instanceService.InstanceAction(uint(instanceID), req)
	if err != nil {
		global.APP_LOG.Error("管理员实例操作失败",
			zap.Uint64("instanceId", instanceID),
			zap.String("action", req.Action),
			zap.Error(err))

		if err.Error() == "实例不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, "实例不存在"))
			return
		}

		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "操作失败"))
		return
	}

	global.APP_LOG.Info("管理员实例操作成功",
		zap.Uint64("instanceId", instanceID),
		zap.String("action", req.Action))

	common.ResponseSuccess(c, nil, "操作已提交")
}

// ResetInstancePassword 管理员重置实例密码
// @Summary 管理员重置实例密码
// @Description 管理员重置指定实例的登录密码，创建异步任务执行密码重置操作
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "实例ID"
// @Param request body admin.ResetInstancePasswordRequest true "重置实例密码请求参数（可为空对象）"
// @Success 200 {object} common.Response{data=admin.ResetInstancePasswordResponse} "任务创建成功，返回任务ID"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 404 {object} common.Response "实例不存在"
// @Failure 500 {object} common.Response "创建任务失败"
// @Router /admin/instances/{id}/reset-password [put]
func ResetInstancePassword(c *gin.Context) {
	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	var req admin.ResetInstancePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 由于不需要参数，忽略绑定错误
	}

	global.APP_LOG.Info("管理员创建重置实例密码任务",
		zap.Uint64("instanceID", instanceID),
		zap.String("admin_ip", c.ClientIP()))

	adminInstanceService := instance.Service{}
	taskID, err := adminInstanceService.ResetInstancePassword(uint(instanceID))
	if err != nil {
		global.APP_LOG.Error("管理员创建重置实例密码任务失败",
			zap.Uint64("instanceID", instanceID),
			zap.Error(err))
		if err.Error() == "实例不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
			return
		}
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	response := admin.ResetInstancePasswordResponse{
		TaskID: taskID,
	}

	global.APP_LOG.Info("管理员创建重置实例密码任务成功",
		zap.Uint64("instanceID", instanceID),
		zap.Uint("taskID", taskID))

	common.ResponseSuccess(c, response, "密码重置任务创建成功")
}

// GetInstanceNewPassword 管理员获取实例重置后的新密码
// @Summary 管理员获取实例重置后的新密码
// @Description 通过任务ID获取实例重置后的新密码
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "实例ID"
// @Param taskId path int true "任务ID"
// @Success 200 {object} common.Response{data=admin.GetInstancePasswordResponse} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 404 {object} common.Response "实例或任务不存在"
// @Router /admin/instances/{id}/password/{taskId} [get]
func GetInstanceNewPassword(c *gin.Context) {
	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	taskIDStr := c.Param("taskId")
	taskID, err := strconv.ParseUint(taskIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的任务ID"))
		return
	}

	adminInstanceService := instance.Service{}
	newPassword, resetTime, err := adminInstanceService.GetInstanceNewPassword(uint(instanceID), uint(taskID))
	if err != nil {
		global.APP_LOG.Error("管理员获取实例新密码失败",
			zap.Uint64("instanceID", instanceID),
			zap.Uint64("taskID", taskID),
			zap.Error(err))

		if err.Error() == "实例不存在" || err.Error() == "任务不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
			return
		}
		if err.Error() == "任务尚未完成" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
			return
		}
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	response := admin.GetInstancePasswordResponse{
		NewPassword: newPassword,
		ResetTime:   resetTime,
	}

	global.APP_LOG.Info("管理员获取实例新密码成功",
		zap.Uint64("instanceID", instanceID),
		zap.Uint64("taskID", taskID))

	common.ResponseSuccess(c, response, "获取新密码成功")
}
