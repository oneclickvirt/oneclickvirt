package user

import (
	"encoding/json"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/model/user"
	userService "oneclickvirt/service/user"

	"github.com/gin-gonic/gin"
)

// GetAvailableProviders 获取可用节点列表
// @Summary 获取可用节点列表
// @Description 获取当前用户可以申领的节点列表，根据资源使用情况筛选
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=[]user.AvailableProviderResponse} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/provider/available [get]
func GetAvailableProviders(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	userServiceInstance := userService.NewService()
	providers, err := userServiceInstance.GetAvailableProviders(userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, providers)
}

// GetSystemImages 获取系统镜像列表
// @Summary 获取系统镜像列表
// @Description 获取当前用户可以使用的系统镜像列表，支持按Provider和实例类型过滤
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param providerType query string false "Provider类型"
// @Param providerId query uint false "Provider ID"
// @Param instanceType query string false "实例类型" Enums(container,vm)
// @Param architecture query string false "架构"
// @Success 200 {object} common.Response{data=[]user.SystemImageResponse} "获取成功"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/images [get]
func GetUserSystemImages(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req user.SystemImagesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userServiceInstance := userService.NewService()
	images, err := userServiceInstance.GetSystemImages(userID, req)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, images)
}

// GetFilteredSystemImages 获取过滤后的系统镜像列表
// @Summary 获取过滤后的系统镜像列表
// @Description 根据Provider ID和实例类型获取匹配的系统镜像列表
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param provider_id query uint true "Provider ID"
// @Param instance_type query string true "实例类型" Enums(container,vm)
// @Param architecture query string false "架构类型" Enums(amd64,arm64)
// @Success 200 {object} common.Response{data=[]user.SystemImageResponse} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/images/filtered [get]
func GetFilteredSystemImages(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	providerID := c.Query("provider_id")
	instanceType := c.Query("instance_type")

	if providerID == "" || instanceType == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "provider_id和instance_type参数必填"))
		return
	}

	// 转换providerID为uint
	id, err := strconv.ParseUint(providerID, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "provider_id参数格式错误"))
		return
	}

	userServiceInstance := userService.NewService()
	images, err := userServiceInstance.GetFilteredSystemImages(userID, uint(id), instanceType)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, images)
}

// GetProviderCapabilities 获取Provider能力信息
// @Summary 获取Provider能力信息
// @Description 获取指定Provider支持的实例类型和架构信息
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path uint true "Provider ID"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /user/provider/{id}/capabilities [get]
func GetProviderCapabilities(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	providerID := c.Param("id")
	if providerID == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "providerId参数必填"))
		return
	}

	// 转换providerID为uint
	id, err := strconv.ParseUint(providerID, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "providerId参数格式错误"))
		return
	}

	userServiceInstance := userService.NewService()
	capabilities, err := userServiceInstance.GetProviderCapabilities(userID, uint(id))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, capabilities)
}

// GetProviderGPUs 获取Provider缓存的GPU/NPU检测结果
// @Summary 获取Provider GPU/NPU设备列表
// @Description 获取指定Provider最后一次GPU检测的缓存结果（持久化存储），供用户申请时选择GPU设备
// @Tags 用户管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path uint true "Provider ID"
// @Success 200 {object} common.Response{data=[]object} "GPU列表"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 404 {object} common.Response "Provider不存在或无缓存数据"
// @Router /user/provider/{id}/gpus [get]
func GetProviderGPUs(c *gin.Context) {
	providerID := c.Param("id")
	if providerID == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "providerId参数必填"))
		return
	}

	id, err := strconv.ParseUint(providerID, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "providerId参数格式错误"))
		return
	}

	var provider providerModel.Provider
	if err := global.APP_DB.Select("id, type, gpu_enabled, gpu_info").
		Where("id = ? AND (type = ? OR type = ?)", uint(id), "lxd", "incus").
		First(&provider).Error; err != nil {
		common.ResponseSuccess(c, map[string]interface{}{
			"gpus": []interface{}{},
			"info": "Provider 不存在或非 lxd/incus 类型，不支持GPU直通",
		})
		return
	}

	if provider.GpuInfo == "" {
		common.ResponseSuccess(c, map[string]interface{}{
			"gpus": []interface{}{},
			"info": "该节点尚未执行GPU检测，请联系管理员进行GPU检测",
		})
		return
	}

	var gpus []map[string]interface{}
	if err := json.Unmarshal([]byte(provider.GpuInfo), &gpus); err != nil {
		// 兼容旧格式（字符串列表）
		var oldFormat []string
		if err2 := json.Unmarshal([]byte(provider.GpuInfo), &oldFormat); err2 == nil {
			for i, name := range oldFormat {
				gpus = append(gpus, map[string]interface{}{
					"index": i,
					"name":  name,
				})
			}
		} else {
			common.ResponseSuccess(c, map[string]interface{}{
				"gpus": []interface{}{},
				"info": "GPU缓存数据格式异常，请联系管理员重新检测",
			})
			return
		}
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"gpus": gpus,
	})
}
