package user

import (
	"strconv"

	"oneclickvirt/model/common"
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
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取可用节点失败"))
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
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取系统镜像失败"))
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
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
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
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, capabilities)
}
