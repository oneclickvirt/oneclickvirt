package system

import (
	"oneclickvirt/service/provider"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	adminInstance "oneclickvirt/service/admin/instance"
	adminProvider "oneclickvirt/service/admin/provider"
	adminSystem "oneclickvirt/service/admin/system"
	adminUser "oneclickvirt/service/admin/user"
	"oneclickvirt/service/task"
	userService "oneclickvirt/service/user"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func GetAnnouncement(c *gin.Context) {
	// 获取查询参数
	announcementType := c.Query("type") // homepage, topbar 或者为空获取所有
	systemService := adminSystem.NewService()
	announcements, err := systemService.GetActiveAnnouncements(announcementType)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取公告列表失败"))
		return
	}
	common.ResponseSuccess(c, announcements, "获取成功")
}

func GetUsers(c *gin.Context) {
	var req admin.UserListRequest

	// 使用请求处理服务处理参数
	requestProcessService := provider.RequestProcessService{}
	if err := requestProcessService.ProcessUserListRequest(c, &req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userService := adminUser.NewService()
	users, total, err := userService.GetUserList(req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取用户列表失败"))
		return
	}
	common.ResponseSuccessWithPagination(c, users, total, req.Page, req.PageSize)
}

func GetProviders(c *gin.Context) {
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未认证"))
		return
	}

	userSvc := userService.NewService()
	providers, err := userSvc.GetAvailableProviders(authCtx.UserID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取可用节点失败"))
		return
	}
	common.ResponseSuccess(c, providers)
}

func UpdateProviderStatus(c *gin.Context) {
	// 从URL路径参数获取ID
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}
	var req admin.UpdateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("UpdateProvider参数绑定失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}
	// 设置ID从URL参数
	req.ID = uint(id)
	providerService := adminProvider.NewService()
	if err := providerService.UpdateProvider(req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}
	common.ResponseSuccess(c, nil, "更新提供商成功")
}

func GetAllInstances(c *gin.Context) {
	var req admin.InstanceListRequest

	// 使用请求处理服务处理参数
	requestProcessService := provider.RequestProcessService{}
	if err := requestProcessService.ProcessInstanceListRequest(c, &req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	instanceService := adminInstance.NewService(task.GetTaskService())
	instances, total, err := instanceService.GetInstanceList(req, 0)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取实例列表失败"))
		return
	}
	common.ResponseSuccess(c, map[string]interface{}{
		"list":  instances,
		"total": total,
	})
}

func AdminInstanceAction(c *gin.Context) {
	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	var req struct {
		Action string `json:"action" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	// 调用管理员实例操作服务
	adminReq := admin.InstanceActionRequest{
		Action: req.Action,
	}

	instanceService := adminInstance.NewService(task.GetTaskService())
	if err := instanceService.InstanceAction(uint(instanceID), adminReq); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "操作任务已创建，请查看任务列表了解进度")
}

// GetProviderMonitoring 获取节点监控数据
// @Summary 获取节点监控数据
// @Description 获取节点的监控和性能数据
// @Tags 系统监控
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Router /admin/monitoring/provider [get]
func GetProviderMonitoring(c *gin.Context) {
	common.ResponseSuccess(c, map[string]interface{}{
		"provider": []interface{}{},
	})
}
