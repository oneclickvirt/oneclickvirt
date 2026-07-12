package admin

import (
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	"oneclickvirt/service/task"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// RepairPortMappings rebuilds node-side forwarding rules from controller DB records.
// @Summary 按数据库记录重建端口转发
// @Description 预览或创建后台任务，将控制端数据库中的端口映射重新应用到节点。执行时必须提供确认词 REBUILD。
// @Tags Admin-Port-Mapping
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param data body admin.RepairPortMappingsRequest true "修复参数"
// @Success 200 {object} common.Response
// @Failure 400 {object} common.Response
// @Failure 401 {object} common.Response
// @Failure 409 {object} common.Response
// @Failure 500 {object} common.Response
// @Failure 503 {object} common.Response
// @Router /admin/port-mappings/repair [post]
func RepairPortMappings(c *gin.Context) {
	var req adminModel.RepairPortMappingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	if len(req.ProviderIDs) > 0 {
		if err := ensureProviderOwners(c, req.ProviderIDs); err != nil {
			common.ResponseWithError(c, err)
			return
		}
	}
	if len(req.PortIDs) > 0 {
		if err := ensurePortMappingOwners(c, req.PortIDs); err != nil {
			common.ResponseWithError(c, err)
			return
		}
	}

	userID, err := getUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权"))
		return
	}
	taskService := task.GetTaskService()
	if taskService == nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnavailable, "任务服务正在初始化，请稍后重试"))
		return
	}

	if req.DryRun {
		preview, previewErr := taskService.PreviewRepairPortMappings(c.Request.Context(), &req, middleware.GetOwnerAdminID(c))
		if previewErr != nil {
			global.APP_LOG.Error("生成端口映射修复预览失败", zap.Uint("userId", userID), zap.Error(previewErr))
			common.ResponseWithError(c, common.ClassifyError(previewErr))
			return
		}
		common.ResponseSuccess(c, preview, "端口映射修复预览已生成")
		return
	}

	if req.Confirmation != adminModel.PortMappingRepairConfirmation {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "二次确认失败，请输入 REBUILD"))
		return
	}
	response, createErr := taskService.CreateRepairPortMappingsTasks(c.Request.Context(), userID, &req, middleware.GetOwnerAdminID(c))
	if createErr != nil {
		global.APP_LOG.Error("创建端口映射修复任务失败", zap.Uint("userId", userID), zap.Error(createErr))
		common.ResponseWithError(c, common.ClassifyError(createErr))
		return
	}

	message := fmt.Sprintf("已创建 %d 个端口映射修复任务", response.TaskCount)
	if response.FailedCount > 0 {
		message = fmt.Sprintf("已创建 %d 个修复任务，%d 个Provider任务创建失败", response.TaskCount, response.FailedCount)
	}
	global.APP_LOG.Warn("管理员确认按数据库记录重建端口转发",
		zap.Uint("userId", userID),
		zap.Int("taskCount", response.TaskCount),
		zap.Int("failedCount", response.FailedCount),
		zap.Uints("portIds", req.PortIDs))
	common.ResponseSuccess(c, response, message)
}
