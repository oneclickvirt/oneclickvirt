package provider

import (
	"context"
	"time"

	"oneclickvirt/model/common"
	"oneclickvirt/service/provider"

	"oneclickvirt/global"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ProviderApi struct{}

// 使用全局服务实例或者直接在方法中创建
var providerApiService = &provider.ProviderApiService{}

// ConnectProvider 连接Provider
// @Summary 连接虚拟化Provider
// @Description 连接到虚拟化提供者（如Docker、LXD、Proxmox等）
// @Tags 虚拟化管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body provider.ConnectProviderRequest true "连接参数"
// @Success 200 {object} common.Response "连接成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "连接失败"
// @Router /provider/connect [post]
func (p *ProviderApi) ConnectProvider(c *gin.Context) {
	var req provider.ConnectProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("参数绑定失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	if err := providerApiService.ConnectProvider(ctx, req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "Provider连接成功")
}

// GetProviders 获取所有Provider
// @Summary 获取所有Provider
// @Description 获取系统中配置的所有虚拟化提供者
// @Tags 虚拟化管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=[]object} "获取成功"
// @Router /provider/ [get]
func (p *ProviderApi) GetProviders(c *gin.Context) {
	providers := providerApiService.GetAllProviders()
	common.ResponseSuccess(c, providers, "获取成功")
}

// GetProviderStatus 获取Provider状态
// @Summary 获取Provider状态
// @Description 获取指定Provider的连接状态和支持的实例类型
// @Tags 虚拟化管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 404 {object} common.Response "Provider不存在"
// @Router /provider/{id}/status [get]
func (p *ProviderApi) GetProviderStatus(c *gin.Context) {
	providerID := c.Param("id")

	data, err := providerApiService.GetProviderStatusByID(providerID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		return
	}

	common.ResponseSuccess(c, data, "获取成功")
}

// GetProviderCapabilities 获取Provider能力
// @Summary 获取Provider能力
// @Description 获取指定Provider支持的功能和实例类型
// @Tags 虚拟化管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 404 {object} common.Response "Provider不存在"
// @Router /provider/{id}/capabilities [get]
func (p *ProviderApi) GetProviderCapabilities(c *gin.Context) {
	providerID := c.Param("id")

	data, err := providerApiService.GetProviderCapabilitiesByID(providerID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		return
	}

	common.ResponseSuccess(c, data, "获取成功")
}

// ListInstances 获取实例列表
// @Summary 获取实例列表
// @Description 获取指定Provider下的所有实例
// @Tags 虚拟化管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=[]object} "获取成功"
// @Failure 404 {object} common.Response "Provider不存在"
// @Failure 500 {object} common.Response "获取失败"
// @Router /provider/{id}/instances [get]
func (p *ProviderApi) ListInstances(c *gin.Context) {
	providerID := c.Param("id")

	instances, err := providerApiService.ListInstancesByProviderID(c.Request.Context(), providerID)
	if err != nil {
		if err.Error() == "Provider不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		} else {
			common.ResponseWithError(c, common.ClassifyError(err))
		}
		return
	}

	common.ResponseSuccess(c, instances, "获取成功")
}

// CreateInstance 创建实例
// @Summary 创建实例
// @Description 在指定Provider上创建新的虚拟机或容器实例
// @Tags 虚拟化管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param type path string true "Provider类型" Enums(docker,lxd,incus,proxmox)
// @Param request body provider.CreateInstanceRequest true "创建实例请求参数"
// @Success 200 {object} common.Response{data=object} "创建成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 404 {object} common.Response "Provider不存在"
// @Failure 500 {object} common.Response "创建失败"
// @Router /provider/{id}/instances [post]
func (p *ProviderApi) CreateInstance(c *gin.Context) {
	providerID := c.Param("id")

	var req provider.CreateInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("参数绑定失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}

	if err := providerApiService.CreateInstanceByProviderIDFromString(c.Request.Context(), providerID, req); err != nil {
		if err.Error() == "Provider不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		} else {
			common.ResponseWithError(c, common.ClassifyError(err))
		}
		return
	}

	common.ResponseSuccess(c, nil, "实例创建成功")
}

// GetInstance 获取实例详情
func (p *ProviderApi) GetInstance(c *gin.Context) {
	providerID := c.Param("id")
	instanceName := c.Param("name")

	instance, err := providerApiService.GetInstanceByProviderID(c.Request.Context(), providerID, instanceName)
	if err != nil {
		if err.Error() == "Provider不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		} else if err.Error() == "实例不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		} else {
			common.ResponseWithError(c, common.ClassifyError(err))
		}
		return
	}

	common.ResponseSuccess(c, instance, "获取成功")
}

// StartInstance 启动实例
func (p *ProviderApi) StartInstance(c *gin.Context) {
	providerID := c.Param("id")
	instanceName := c.Param("name")

	if err := providerApiService.StartInstanceByProviderIDFromString(c.Request.Context(), providerID, instanceName); err != nil {
		if err.Error() == "Provider不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		} else {
			common.ResponseWithError(c, common.ClassifyError(err))
		}
		return
	}

	common.ResponseSuccess(c, nil, "实例启动成功")
}

// StopInstance 停止实例
func (p *ProviderApi) StopInstance(c *gin.Context) {
	providerID := c.Param("id")
	instanceName := c.Param("name")

	if err := providerApiService.StopInstanceByProviderIDFromString(c.Request.Context(), providerID, instanceName); err != nil {
		if err.Error() == "Provider不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		} else {
			common.ResponseWithError(c, common.ClassifyError(err))
		}
		return
	}

	common.ResponseSuccess(c, nil, "实例停止成功")
}

// DeleteInstance 删除实例
func (p *ProviderApi) DeleteInstance(c *gin.Context) {
	providerID := c.Param("id")
	instanceName := c.Param("name")

	if err := providerApiService.DeleteInstanceByProviderIDFromString(c.Request.Context(), providerID, instanceName); err != nil {
		if err.Error() == "Provider不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		} else {
			common.ResponseWithError(c, common.ClassifyError(err))
		}
		return
	}

	common.ResponseSuccess(c, nil, "实例删除成功")
}

// ListImages 获取镜像列表
func (p *ProviderApi) ListImages(c *gin.Context) {
	providerID := c.Param("id")

	images, err := providerApiService.ListImagesByProviderID(c.Request.Context(), providerID)
	if err != nil {
		if err.Error() == "Provider不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		} else {
			common.ResponseWithError(c, common.ClassifyError(err))
		}
		return
	}

	common.ResponseSuccess(c, images, "获取成功")
}

// PullImage 拉取镜像
func (p *ProviderApi) PullImage(c *gin.Context) {
	providerID := c.Param("id")

	var req struct {
		Image string `json:"image" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("参数绑定失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}

	if err := providerApiService.PullImageByProviderID(c.Request.Context(), providerID, req.Image); err != nil {
		if err.Error() == "Provider不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		} else {
			common.ResponseWithError(c, common.ClassifyError(err))
		}
		return
	}

	common.ResponseSuccess(c, nil, "镜像拉取成功")
}

// DeleteImage 删除镜像
func (p *ProviderApi) DeleteImage(c *gin.Context) {
	providerID := c.Param("id")
	imageName := c.Param("image")

	if err := providerApiService.DeleteImageByProviderID(c.Request.Context(), providerID, imageName); err != nil {
		if err.Error() == "Provider不存在" {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
		} else {
			common.ResponseWithError(c, common.ClassifyError(err))
		}
		return
	}

	common.ResponseSuccess(c, nil, "镜像删除成功")
}
