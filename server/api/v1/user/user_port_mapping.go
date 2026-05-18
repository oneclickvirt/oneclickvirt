package user

import (
	"oneclickvirt/middleware"
	"oneclickvirt/service/resources"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/admin/instance"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func getUserIDFromContext(c *gin.Context) (uint, error) {
	return middleware.GetUserIDFromContext(c)
}

// GetInstancePorts 获取实例的端口映射
// @Summary 获取实例端口映射
// @Description 获取指定实例的端口映射信息
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "实例ID"
// @Success 200 {object} common.Response "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 403 {object} common.Response "无权限访问"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/instances/{id}/ports [get]
func GetInstancePorts(c *gin.Context) {
	instanceIDStr := c.Param("id")
	instanceID, err := strconv.ParseUint(instanceIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "实例ID格式错误"))
		return
	}

	userID, err := getUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	// 验证实例是否属于当前用户
	adminInstanceService := instance.Service{}
	instance, err := adminInstanceService.GetInstanceByID(uint(instanceID))
	if err != nil {
		global.APP_LOG.Error("获取实例失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "实例不存在"))
		return
	}

	if instance.UserID != userID {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "无权限访问此实例"))
		return
	}

	// 获取端口映射
	portMappingService := resources.PortMappingService{}
	ports, err := portMappingService.GetPortMappingsByInstanceID(uint(instanceID))
	if err != nil {
		global.APP_LOG.Error("获取端口映射失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// 获取Provider信息以判断是否为agent+no_port_mapping模式
	var providerInfo providerModel.Provider
	agentNoPortMapping := false
	if err := global.APP_DB.Select("connection_type, network_type").
		Where("id = ?", instance.ProviderID).First(&providerInfo).Error; err == nil {
		if providerInfo.ConnectionType == "agent" && providerInfo.NetworkType == "no_port_mapping" {
			agentNoPortMapping = true
		}
	}

	// 直接使用实例的PublicIP字段（agent+no_port_mapping模式下不显示）
	publicIP := instance.PublicIP
	if agentNoPortMapping {
		publicIP = ""
	}

	// 转换为前端期望的格式
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

	// 实例和Provider信息
	response := gin.H{
		"list":     formattedPorts,
		"total":    len(formattedPorts),
		"publicIP": publicIP,
		"instance": map[string]interface{}{
			"id":       instance.ID,
			"name":     instance.Name,
			"username": instance.Username,
		},
	}

	common.ResponseSuccess(c, response)
}

// GetUserPortMappings 获取用户的所有端口映射
// @Summary 获取用户端口映射列表
// @Description 获取当前登录用户的所有端口映射
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码"
// @Param limit query int false "每页数量"
// @Param keyword query string false "搜索关键字"
// @Success 200 {object} common.Response "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/port-mappings [get]
func GetUserPortMappings(c *gin.Context) {
	userID, err := getUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req struct {
		common.PageInfo
		Limit int `form:"limit"`
	}

	if err := c.ShouldBindQuery(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "参数错误"))
		return
	}

	if req.PageSize <= 0 && req.Limit > 0 {
		req.PageSize = req.Limit
	}
	req.Page, req.PageSize = common.NormalizePagination(req.Page, req.PageSize, 20)

	portMappingService := resources.PortMappingService{}
	ports, total, err := portMappingService.GetUserPortMappings(userID, req.Page, req.PageSize, req.Keyword)
	if err != nil {
		global.APP_LOG.Error("获取用户端口映射失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, gin.H{
		"list":     ports,
		"total":    total,
		"page":     req.Page,
		"pageSize": req.PageSize,
	})
}
