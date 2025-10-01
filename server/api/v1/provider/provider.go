package provider

import (
	"net/http"
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
		global.APP_LOG.Error("参数绑定失败", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "参数错误: " + err.Error(),
		})
		return
	}

	if err := providerApiService.ConnectProvider(c.Request.Context(), req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "Provider连接成功",
	})
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
	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": providers,
	})
}

// GetProviderStatus 获取Provider状态
// @Summary 获取Provider状态
// @Description 获取指定类型虚拟化提供者的连接状态和支持的实例类型
// @Tags 虚拟化管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param type path string true "Provider类型" Enums(docker,lxd,incus,proxmox)
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 404 {object} common.Response "Provider不存在"
// @Router /provider/{type}/status [get]
func (p *ProviderApi) GetProviderStatus(c *gin.Context) {
	providerType := c.Param("type")

	data, err := providerApiService.GetProviderStatus(providerType)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": data,
	})
}

// GetProviderCapabilities 获取Provider能力
// @Summary 获取Provider能力
// @Description 获取指定类型虚拟化提供者支持的功能和实例类型
// @Tags 虚拟化管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param type path string true "Provider类型" Enums(docker,lxd,incus,proxmox)
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 404 {object} common.Response "Provider不存在"
// @Router /provider/{type}/capabilities [get]
func (p *ProviderApi) GetProviderCapabilities(c *gin.Context) {
	providerType := c.Param("type")

	data, err := providerApiService.GetProviderCapabilities(providerType)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"code": 404,
			"msg":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": data,
	})
}

// ListInstances 获取实例列表
// @Summary 获取实例列表
// @Description 获取指定Provider下的所有实例
// @Tags 虚拟化管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param type path string true "Provider类型" Enums(docker,lxd,incus,proxmox)
// @Success 200 {object} common.Response{data=[]object} "获取成功"
// @Failure 404 {object} common.Response "Provider不存在"
// @Failure 500 {object} common.Response "获取失败"
// @Router /provider/{type}/instances [get]
func (p *ProviderApi) ListInstances(c *gin.Context) {
	providerType := c.Param("type")

	instances, err := providerApiService.ListInstances(c.Request.Context(), providerType)
	if err != nil {
		if err.Error() == "Provider不存在" {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 404,
				"msg":  err.Error(),
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 500,
				"msg":  err.Error(),
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": instances,
	})
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
// @Router /provider/{type}/instances [post]
func (p *ProviderApi) CreateInstance(c *gin.Context) {
	providerType := c.Param("type")

	var req provider.CreateInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Error("参数绑定失败", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "参数错误: " + err.Error(),
		})
		return
	}

	if err := providerApiService.CreateInstance(c.Request.Context(), providerType, req); err != nil {
		if err.Error() == "Provider不存在" {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 404,
				"msg":  err.Error(),
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 500,
				"msg":  err.Error(),
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "实例创建成功",
	})
}

// GetInstance 获取实例详情
func (p *ProviderApi) GetInstance(c *gin.Context) {
	providerType := c.Param("type")
	instanceID := c.Param("id")

	instance, err := providerApiService.GetInstance(c.Request.Context(), providerType, instanceID)
	if err != nil {
		if err.Error() == "Provider不存在" {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 404,
				"msg":  err.Error(),
			})
		} else if err.Error() == "实例不存在" {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 404,
				"msg":  err.Error(),
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 500,
				"msg":  err.Error(),
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": instance,
	})
}

// StartInstance 启动实例
func (p *ProviderApi) StartInstance(c *gin.Context) {
	providerType := c.Param("type")
	instanceID := c.Param("id")

	if err := providerApiService.StartInstance(c.Request.Context(), providerType, instanceID); err != nil {
		if err.Error() == "Provider不存在" {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 404,
				"msg":  err.Error(),
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 500,
				"msg":  err.Error(),
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "实例启动成功",
	})
}

// StopInstance 停止实例
func (p *ProviderApi) StopInstance(c *gin.Context) {
	providerType := c.Param("type")
	instanceID := c.Param("id")

	if err := providerApiService.StopInstance(c.Request.Context(), providerType, instanceID); err != nil {
		if err.Error() == "Provider不存在" {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 404,
				"msg":  err.Error(),
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 500,
				"msg":  err.Error(),
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "实例停止成功",
	})
}

// DeleteInstance 删除实例
func (p *ProviderApi) DeleteInstance(c *gin.Context) {
	providerType := c.Param("type")
	instanceID := c.Param("id")

	if err := providerApiService.DeleteInstance(c.Request.Context(), providerType, instanceID); err != nil {
		if err.Error() == "Provider不存在" {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 404,
				"msg":  err.Error(),
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 500,
				"msg":  err.Error(),
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "实例删除成功",
	})
}

// ListImages 获取镜像列表
func (p *ProviderApi) ListImages(c *gin.Context) {
	providerType := c.Param("type")

	images, err := providerApiService.ListImages(c.Request.Context(), providerType)
	if err != nil {
		if err.Error() == "Provider不存在" {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 404,
				"msg":  err.Error(),
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 500,
				"msg":  err.Error(),
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": images,
	})
}

// PullImage 拉取镜像
func (p *ProviderApi) PullImage(c *gin.Context) {
	providerType := c.Param("type")

	var req struct {
		Image string `json:"image" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Error("参数绑定失败", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 400,
			"msg":  "参数错误: " + err.Error(),
		})
		return
	}

	if err := providerApiService.PullImage(c.Request.Context(), providerType, req.Image); err != nil {
		if err.Error() == "Provider不存在" {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 404,
				"msg":  err.Error(),
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 500,
				"msg":  err.Error(),
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "镜像拉取成功",
	})
}

// DeleteImage 删除镜像
func (p *ProviderApi) DeleteImage(c *gin.Context) {
	providerType := c.Param("type")
	imageID := c.Param("id")

	if err := providerApiService.DeleteImage(c.Request.Context(), providerType, imageID); err != nil {
		if err.Error() == "Provider不存在" {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 404,
				"msg":  err.Error(),
			})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{
				"code": 500,
				"msg":  err.Error(),
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"msg":  "镜像删除成功",
	})
}
