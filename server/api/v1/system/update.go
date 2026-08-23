package system

import (
	"net/http"

	"oneclickvirt/model/common"
	updateService "oneclickvirt/service/update"

	"github.com/gin-gonic/gin"
)

// GetUpdateInfo 返回超级管理员可见的版本、部署能力、回退版本和人工命令。
// @Summary 查看更新能力和版本
// @Description 根据部署模式返回在线升级能力；Docker、Compose、源码和未知模式仅展示人工命令。
// @Tags 系统更新
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response
// @Router /admin/system/check-updates [get]
func GetUpdateInfo(c *gin.Context) {
	info, err := updateService.GetService().CheckUpdates(c.Request.Context())
	if err != nil {
		// Release 元数据不可达时仍返回部署能力和人工命令，避免管理员失去恢复路径。
		info.Error = err.Error()
	}
	common.ResponseSuccess(c, info, "获取更新信息成功")
}

// GetRollbackVersions 返回可回退的远程版本和本地备份。
// @Summary 查看回退版本
// @Tags 系统更新
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response
// @Router /admin/system/rollback-versions [get]
func GetRollbackVersions(c *gin.Context) {
	info, err := updateService.GetService().RollbackVersions(c.Request.Context())
	if err != nil {
		info.Error = err.Error()
	}
	common.ResponseSuccess(c, info, "获取回退版本成功")
}

// StartUpdate schedules a validated release update. The actual file switch is
// asynchronous so the request can return before systemd restarts the process.
// @Summary 在线升级
// @Tags 系统更新
// @Accept json
// @Produce json
// @Param request body update.UpdateRequest false "目标版本，留空表示最新版本"
// @Security BearerAuth
// @Success 200 {object} common.Response
// @Failure 400 {object} common.Response
// @Failure 409 {object} common.Response
// @Router /admin/system/update [post]
func StartUpdate(c *gin.Context) {
	var request updateService.UpdateRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&request); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeBadRequest, "请求参数格式错误"))
			return
		}
	}
	state, err := updateService.GetService().StartUpdate(c.Request.Context(), request.Version)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"msg":     "升级任务已提交",
		"message": "升级任务已提交",
		"data":    state,
	})
}

// StartRollback schedules a validated rollback to an older release.
// @Summary 在线回退
// @Tags 系统更新
// @Accept json
// @Produce json
// @Param request body update.UpdateRequest true "目标版本"
// @Security BearerAuth
// @Success 200 {object} common.Response
// @Router /admin/system/rollback [post]
func StartRollback(c *gin.Context) {
	var request updateService.UpdateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeBadRequest, "请提供有效的回退版本"))
		return
	}
	state, err := updateService.GetService().StartRollback(c.Request.Context(), request.Version, request.BackupID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, state, "回退任务已提交")
}

// StartRestart schedules a restart of the managed controller and configured
// reverse-proxy reloads.
// @Summary 重启主控服务
// @Tags 系统更新
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response
// @Router /admin/system/restart [post]
func StartRestart(c *gin.Context) {
	state, err := updateService.GetService().StartRestart(c.Request.Context())
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, state, "重启任务已提交")
}

// GetUpdateStatus returns the process-local update operation state.
// @Summary 查看更新任务状态
// @Tags 系统更新
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response
// @Router /admin/system/update-status [get]
func GetUpdateStatus(c *gin.Context) {
	common.ResponseSuccess(c, updateService.GetService().Operation(), "获取更新任务状态成功")
}
