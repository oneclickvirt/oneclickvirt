package public

import (
	"net"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	authModel "oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	adminInstance "oneclickvirt/service/admin/instance"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/service/resources"
	shareService "oneclickvirt/service/share"
	snapshotSvc "oneclickvirt/service/snapshot"
	"oneclickvirt/service/task"
	"oneclickvirt/service/taskgate"
	trafficService "oneclickvirt/service/traffic"
	userService "oneclickvirt/service/user"

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

func getPublicControllerAccessHost(c *gin.Context) string {
	host := c.GetHeader("X-Forwarded-Host")
	if host == "" {
		host = c.Request.Host
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if comma := strings.Index(host, ","); comma > 0 {
		host = strings.TrimSpace(host[:comma])
	}
	if strings.HasPrefix(host, "[") {
		if parsedHost, _, err := net.SplitHostPort(host); err == nil {
			return strings.Trim(parsedHost, "[]")
		}
		return strings.Trim(host, "[]")
	}
	if strings.Count(host, ":") == 1 {
		if parsedHost, _, err := net.SplitHostPort(host); err == nil {
			return parsedHost
		}
		if colonIdx := strings.LastIndex(host, ":"); colonIdx > 0 {
			return host[:colonIdx]
		}
	}
	return host
}

func loadSharedInstance(c *gin.Context) (*providerModel.InstanceShareLink, *providerModel.Instance, bool) {
	return loadSharedInstanceWithUsage(c, true)
}

// loadSharedInstanceReadOnly keeps token validation current for static
// share-scoped browser resources without counting each asset as a separate
// interactive share use.
func loadSharedInstanceReadOnly(c *gin.Context) (*providerModel.InstanceShareLink, *providerModel.Instance, bool) {
	return loadSharedInstanceWithUsage(c, false)
}

func loadSharedInstanceWithUsage(c *gin.Context, recordUse bool) (*providerModel.InstanceShareLink, *providerModel.Instance, bool) {
	service := shareService.NewInstanceShareService()
	var (
		link     *providerModel.InstanceShareLink
		instance *providerModel.Instance
		err      error
	)
	if recordUse {
		link, instance, err = service.Validate(c.Param("token"))
	} else {
		link, instance, err = service.ValidateReadOnly(c.Param("token"))
	}
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

func ensureSharedInstanceUsable(instance *providerModel.Instance, action string) *common.AppError {
	if constant.IsBusyStatus(instance.Status) {
		return common.NewError(common.CodeConflict, "实例正在操作进行中，请等待当前任务完成")
	}
	if !constant.IsDetailAvailableStatus(instance.Status) {
		return common.NewError(common.CodeConflict, "实例当前状态不允许执行该操作")
	}
	if action == "delete" {
		return nil
	}
	if instance.IsFrozen {
		return common.NewError(common.CodeForbidden, "实例已被冻结或到期，仅允许删除操作")
	}
	if instance.ExpiresAt != nil && instance.ExpiresAt.Before(time.Now()) {
		return common.NewError(common.CodeForbidden, "实例已被冻结或到期，仅允许删除操作")
	}
	return nil
}

// @Summary 获取共享d 实例 详情
// @Description 获取获取共享d 实例 详情
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "获取共享d 实例 详情成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取共享d 实例 详情失败"
// @Router /public/instance-shares/{token} [get]
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

// @Summary 共享d 实例 操作
// @Description 创建共享d 实例 操作
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "共享d 实例 操作成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "共享d 实例 操作失败"
// @Router /public/instance-shares/{token}/action [post]
func SharedInstanceAction(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	var req userModel.InstanceActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	if err := ensureSharedInstanceUsable(instance, req.Action); err != nil {
		common.ResponseWithError(c, err)
		return
	}
	trafficGuard := trafficService.NewThreeTierLimitService()
	if req.Action == "start" {
		if err := trafficGuard.RefreshAndEnsureUserInstanceOperationAllowed(instance.UserID, instance.ID, req.Action); err != nil {
			common.ResponseWithError(c, common.ClassifyError(err))
			return
		}
	} else if err := trafficGuard.EnsureUserInstanceOperationAllowed(instance.UserID, instance.ID, req.Action); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
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

// @Summary Reset 共享d 实例 密码
// @Description 更新Reset 共享d 实例 密码
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "Reset 共享d 实例 密码成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "Reset 共享d 实例 密码失败"
// @Router /public/instance-shares/{token}/reset-password [put]
func ResetSharedInstancePassword(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	if err := ensureSharedInstanceUsable(instance, "reset-password"); err != nil {
		common.ResponseWithError(c, err)
		return
	}
	if err := trafficService.NewThreeTierLimitService().EnsureUserInstanceOperationAllowed(instance.UserID, instance.ID, "reset-password"); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	taskID, err := adminInstance.NewService(task.GetTaskService()).ResetInstancePassword(instance.ID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, userModel.ResetInstancePasswordResponse{TaskID: taskID}, "密码重置任务创建成功")
}

// @Summary 获取共享d 实例 New 密码
// @Description 获取获取共享d 实例 New 密码
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "获取共享d 实例 New 密码成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取共享d 实例 New 密码失败"
// @Router /public/instance-shares/{token}/password/{taskId} [get]
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

// @Summary 获取共享d 实例 镜像s
// @Description 获取获取共享d 实例 镜像s
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "获取共享d 实例 镜像s成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取共享d 实例 镜像s失败"
// @Router /public/instance-shares/{token}/images/filtered [get]
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

// @Summary 获取共享d 实例 Ports
// @Description 获取获取共享d 实例 Ports
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "获取共享d 实例 Ports成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取共享d 实例 Ports失败"
// @Router /public/instance-shares/{token}/ports [get]
func GetSharedInstancePorts(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}

	portMappingService := resources.PortMappingService{}
	ports, err := portMappingService.GetPortMappingsByInstanceID(instance.ID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	var providerInfo providerModel.Provider
	agentNoPortMapping := false
	if err := global.APP_DB.Select("connection_type, network_type").Where("id = ?", instance.ProviderID).First(&providerInfo).Error; err == nil {
		agentNoPortMapping = providerInfo.ConnectionType == "agent" && providerInfo.NetworkType == "no_port_mapping"
	}
	requestHost := getPublicControllerAccessHost(c)
	hasControllerMapping := false
	for _, port := range ports {
		if port.MappingType == "controller" {
			hasControllerMapping = true
			break
		}
	}
	publicIP := instance.PublicIP
	if (agentNoPortMapping || hasControllerMapping) && requestHost != "" {
		publicIP = requestHost
	}
	if (agentNoPortMapping || hasControllerMapping) && requestHost == "" {
		publicIP = ""
	}

	formattedPorts := make([]userModel.PortMappingResponse, len(ports))
	for i, port := range ports {
		formattedPorts[i] = userModel.PortMappingResponse{
			ID:          port.ID,
			HostPort:    port.HostPort,
			GuestPort:   port.GuestPort,
			Protocol:    port.Protocol,
			Status:      port.Status,
			Description: port.Description,
			IsSSH:       port.IsSSH,
			PortType:    port.PortType,
			MappingType: port.MappingType,
			CreatedAt:   port.CreatedAt,
		}
	}

	common.ResponseSuccess(c, gin.H{
		"list":     formattedPorts,
		"total":    len(formattedPorts),
		"publicIP": publicIP,
		"instance": map[string]interface{}{
			"id":       instance.ID,
			"name":     instance.Name,
			"username": instance.Username,
		},
	})
}

// @Summary 获取共享d 实例 监控ing
// @Description 获取获取共享d 实例 监控ing
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "获取共享d 实例 监控ing成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取共享d 实例 监控ing失败"
// @Router /public/instance-shares/{token}/monitoring [get]
func GetSharedInstanceMonitoring(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	monitoring, err := userService.NewService().GetInstanceMonitoring(instance.UserID, instance.ID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, monitoring)
}

// @Summary 获取共享d 实例 资源 监控ing
// @Description 获取获取共享d 实例 资源 监控ing
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "获取共享d 实例 资源 监控ing成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取共享d 实例 资源 监控ing失败"
// @Router /public/instance-shares/{token}/monitoring/resources [get]
func GetSharedInstanceResourceMonitoring(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	if !constant.IsDetailAvailableStatus(instance.Status) {
		common.ResponseWithError(c, common.NewError(common.CodeConflict, "实例正在操作进行中，请等待当前任务完成"))
		return
	}
	hours, _ := strconv.Atoi(c.DefaultQuery("hours", "24"))
	if hours <= 0 || hours > 24 {
		hours = 24
	}
	resSvc := agentService.NewResourceSyncService(c.Request.Context(), global.APP_DB)
	metrics, err := resSvc.GetInstanceResources(instance.ID, hours)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	diskMonitoringEnabled := true
	var provider providerModel.Provider
	if err := global.APP_DB.Select("container_limit_disk, vm_limit_disk").Where("id = ?", instance.ProviderID).First(&provider).Error; err == nil {
		if instance.InstanceType == "vm" {
			diskMonitoringEnabled = provider.VMLimitDisk
		} else {
			diskMonitoringEnabled = provider.ContainerLimitDisk
		}
	}
	common.ResponseSuccess(c, gin.H{
		"metrics":                 metrics,
		"disk_monitoring_enabled": diskMonitoringEnabled,
	})
}

// @Summary 获取共享d 实例 流量 详情
// @Description 获取获取共享d 实例 流量 详情
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "获取共享d 实例 流量 详情成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取共享d 实例 流量 详情失败"
// @Router /public/instance-shares/{token}/traffic/detail [get]
func GetSharedInstanceTrafficDetail(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	var provider providerModel.Provider
	if err := global.APP_DB.Select("traffic_quota_visible").Where("id = ?", instance.ProviderID).First(&provider).Error; err == nil && !provider.TrafficQuotaVisible {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "该实例流量额度不可见"))
		return
	}
	detail, err := trafficService.NewUserTrafficService().GetInstanceTrafficDetail(instance.UserID, instance.ID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, detail, "获取流量详情成功")
}

// @Summary 获取共享d 实例 快照s
// @Description 获取获取共享d 实例 快照s
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "获取共享d 实例 快照s成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取共享d 实例 快照s失败"
// @Router /public/instance-shares/{token}/snapshots [get]
func GetSharedInstanceSnapshots(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	svc := snapshotSvc.Service{}
	snapshots, total, err := svc.ListSnapshots(snapshotSvc.ListFilter{
		Page:       page,
		PageSize:   pageSize,
		InstanceID: instance.ID,
		UserID:     instance.UserID,
	})
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{
		"list":     snapshots,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// @Summary 下载共享d 快照
// @Description 获取下载共享d 快照
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "下载共享d 快照成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "下载共享d 快照失败"
// @Router /public/instance-shares/{token}/snapshots/{snapshotId}/download [get]
func DownloadSharedSnapshot(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
		return
	}
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	snapshotID, err := strconv.ParseUint(c.Param("snapshotId"), 10, 32)
	if err != nil || snapshotID == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的快照ID"))
		return
	}
	svc := snapshotSvc.Service{}
	payload, filename, err := svc.BuildSharedSnapshotDownloadManifest(uint(snapshotID), instance.UserID, instance.ID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Data(200, "application/json; charset=utf-8", payload)
}

// @Summary 共享d SSH Web Socket
// @Description 获取共享d SSH Web Socket
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "共享d SSH Web Socket成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "共享d SSH Web Socket失败"
// @Router /public/instance-shares/{token}/ssh [get]
func SharedSSHWebSocket(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.SSHWebSocket(c)
}

// @Summary 共享d 执行 Web Socket
// @Description 获取共享d 执行 Web Socket
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "共享d 执行 Web Socket成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "共享d 执行 Web Socket失败"
// @Router /public/instance-shares/{token}/exec [get]
func SharedExecWebSocket(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.ExecWebSocket(c)
}

// @Summary 共享d SFTP 列表
// @Description 获取共享d SFTP 列表
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "共享d SFTP 列表成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "共享d SFTP 列表失败"
// @Router /public/instance-shares/{token}/sftp/list [get]
func SharedSFTPList(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.UserSFTPList(c)
}

// @Summary 共享d SFTP Download
// @Description 获取共享d SFTP Download
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "共享d SFTP Download成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "共享d SFTP Download失败"
// @Router /public/instance-shares/{token}/sftp/download [get]
func SharedSFTPDownload(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.UserSFTPDownload(c)
}

// @Summary 共享d SFTP Upload
// @Description 创建共享d SFTP Upload
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "共享d SFTP Upload成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "共享d SFTP Upload失败"
// @Router /public/instance-shares/{token}/sftp/upload [post]
func SharedSFTPUpload(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.UserSFTPUpload(c)
}

// @Summary 共享d SFTP Upload 状态
// @Description 获取共享d SFTP Upload 状态
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "共享d SFTP Upload 状态成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "共享d SFTP Upload 状态失败"
// @Router /public/instance-shares/{token}/sftp/upload/status [get]
func SharedSFTPUploadStatus(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.UserSFTPUploadStatus(c)
}

// @Summary 共享d SFTP Upload Abort
// @Description 创建共享d SFTP Upload Abort
// @Tags 公开接口
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "共享d SFTP Upload Abort成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "共享d SFTP Upload Abort失败"
// @Router /public/instance-shares/{token}/sftp/upload/abort [post]
func SharedSFTPUploadAbort(c *gin.Context) {
	if _, _, ok := loadSharedInstance(c); !ok {
		return
	}
	userAPI.UserSFTPUploadAbort(c)
}
