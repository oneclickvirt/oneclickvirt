package public

import (
	"strconv"
	"time"

	adminInstance "oneclickvirt/service/admin/instance"
	shareService "oneclickvirt/service/share"
	"oneclickvirt/service/task"
	userService "oneclickvirt/service/user"

	adminModel "oneclickvirt/model/admin"
	authModel "oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"

	trafficAPI "oneclickvirt/api/v1/traffic"
	userAPI "oneclickvirt/api/v1/user"

	"github.com/gin-gonic/gin"
)

func setRouteParam(c *gin.Context, key, value string) {
	for i := range c.Params {
		if c.Params[i].Key == key {
			c.Params[i].Value = value
			return
		}
	}
	c.Params = append(c.Params, gin.Param{Key: key, Value: value})
}

func loadSharedInstance(c *gin.Context) (*providerModel.InstanceShareLink, *providerModel.Instance, bool) {
	link, instance, err := shareService.NewInstanceShareService().Validate(c.Param("token"))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return nil, nil, false
	}
	c.Set("user_id", instance.UserID)
	c.Set("auth_context", &authModel.AuthContext{
		UserID:       instance.UserID,
		UserType:     "user",
		Level:        1,
		BaseUserType: "user",
		AllUserTypes: []string{"user"},
		IsEffective:  true,
	})
	setRouteParam(c, "id", strconv.FormatUint(uint64(instance.ID), 10))
	setRouteParam(c, "instanceId", strconv.FormatUint(uint64(instance.ID), 10))
	return link, instance, true
}

func ensureSharedInstanceUsable(instance *providerModel.Instance, action string) bool {
	if action == "delete" {
		return true
	}
	if instance.IsFrozen {
		return false
	}
	return instance.ExpiresAt == nil || !instance.ExpiresAt.Before(time.Now())
}

func GetSharedInstanceDetail(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	detail, err := userService.NewService().GetInstanceDetail(instance.UserID, instance.ID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, detail)
}

func SharedInstanceAction(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	var req userModel.InstanceActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	if !ensureSharedInstanceUsable(instance, req.Action) {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "实例已被冻结或到期，仅允许删除操作"))
		return
	}
	if err := adminInstance.NewService(task.GetTaskService()).InstanceAction(
		instance.ID,
		adminModel.InstanceActionRequest{Action: req.Action, Image: req.Image},
		0,
	); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil, "操作已提交")
}

func ResetSharedInstancePassword(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	if !ensureSharedInstanceUsable(instance, "reset-password") {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "实例已被冻结或到期，无法重置密码"))
		return
	}
	taskID, err := adminInstance.NewService(task.GetTaskService()).ResetInstancePassword(instance.ID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, userModel.ResetInstancePasswordResponse{TaskID: taskID}, "密码重置任务创建成功")
}

func GetSharedInstanceNewPassword(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	taskID, err := strconv.ParseUint(c.Param("taskId"), 10, 32)
	if err != nil || taskID == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的任务ID"))
		return
	}
	password, resetTime, err := adminInstance.NewService(task.GetTaskService()).GetInstanceNewPassword(instance.ID, uint(taskID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, userModel.GetInstancePasswordResponse{NewPassword: password, ResetTime: resetTime}, "获取新密码成功")
}

func GetSharedInstanceImages(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	images, err := userService.NewService().GetFilteredSystemImages(instance.UserID, instance.ProviderID, instance.InstanceType)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, images)
}

func GetSharedInstancePorts(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.GetInstancePorts(c)
}

func GetSharedInstanceMonitoring(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.GetInstanceMonitoring(c)
}

func GetSharedInstanceResourceMonitoring(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.GetInstanceResourceMonitoring(c)
}

func GetSharedInstanceTrafficDetail(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	api := &trafficAPI.UserTrafficAPI{}
	api.GetInstanceTrafficDetail(c)
}

func SharedSSHWebSocket(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.SSHWebSocket(c)
}

func SharedExecWebSocket(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.ExecWebSocket(c)
}

func SharedSFTPList(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.UserSFTPList(c)
}

func SharedSFTPDownload(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.UserSFTPDownload(c)
}

func SharedSFTPUpload(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.UserSFTPUpload(c)
}

func SharedSFTPUploadStatus(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.UserSFTPUploadStatus(c)
}

func SharedSFTPUploadAbort(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.UserSFTPUploadAbort(c)
}
