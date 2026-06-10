package public

import (
	"net"
	"strconv"
	"strings"
	"time"

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
	"oneclickvirt/service/task"
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

func GetSharedInstanceResourceMonitoring(c *gin.Context) {
	_, instance, ok := loadSharedInstance(c)
	if !ok {
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
