package system

import (
	"context"
	"fmt"
	"oneclickvirt/model/common"
	"oneclickvirt/service/database"
	"oneclickvirt/service/images"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	systemModel "oneclickvirt/model/system"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SystemImageResponse 系统镜像响应结构
type SystemImageResponse struct {
	systemModel.SystemImage
}

// CreateSystemImageRequest 创建系统镜像请求
type CreateSystemImageRequest struct {
	Name         string `json:"name" binding:"required"`
	ProviderType string `json:"providerType" binding:"required,oneof=proxmox lxd incus docker podman containerd orbstack qemu kubevirt vmware virtualbox multipass vagrant"`
	InstanceType string `json:"instanceType" binding:"required,oneof=vm container"`
	Architecture string `json:"architecture" binding:"required,oneof=amd64 arm64 s390x"`
	URL          string `json:"url" binding:"required,url"`
	Checksum     string `json:"checksum"`
	Size         int64  `json:"size"`
	Description  string `json:"description"`
	OSType       string `json:"osType"`
	OSVersion    string `json:"osVersion"`
	Tags         string `json:"tags"`
	MinMemoryMB  int    `json:"minMemoryMB" binding:"required,min=1"`
	MinDiskMB    int    `json:"minDiskMB" binding:"required,min=1"`
	UseCDN       bool   `json:"useCdn"`
}

// UpdateSystemImageRequest 更新系统镜像请求
type UpdateSystemImageRequest struct {
	Name         string `json:"name"`
	ProviderType string `json:"providerType" binding:"omitempty,oneof=proxmox lxd incus docker podman containerd orbstack qemu kubevirt vmware virtualbox multipass vagrant"`
	InstanceType string `json:"instanceType" binding:"omitempty,oneof=vm container"`
	Architecture string `json:"architecture" binding:"omitempty,oneof=amd64 arm64 s390x"`
	URL          string `json:"url" binding:"omitempty,url"`
	Checksum     string `json:"checksum"`
	Size         int64  `json:"size"`
	Status       string `json:"status" binding:"omitempty,oneof=active inactive"`
	Description  string `json:"description"`
	OSType       string `json:"osType"`
	OSVersion    string `json:"osVersion"`
	Tags         string `json:"tags"`
	MinMemoryMB  *int   `json:"minMemoryMB" binding:"omitempty,min=1"`
	MinDiskMB    *int   `json:"minDiskMB" binding:"omitempty,min=1"`
	UseCDN       *bool  `json:"useCdn"`
}

// GetSystemImageList 获取系统镜像列表
// @Summary 获取系统镜像列表
// @Description 获取系统镜像列表，支持分页和过滤条件
// @Tags 系统镜像管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param providerType query string false "提供商类型" Enums(proxmox,lxd,incus,docker)
// @Param instanceType query string false "实例类型" Enums(vm,container)
// @Param architecture query string false "架构" Enums(amd64,arm64,s390x)
// @Param status query string false "状态" Enums(active,inactive)
// @Param search query string false "搜索关键字"
// @Param osType query string false "操作系统类型"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/system-images [get]
func GetSystemImageList(c *gin.Context) {
	// 分页参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	page, pageSize = common.NormalizePagination(page, pageSize, common.DefaultPageSize)

	// 过滤参数
	providerType := c.Query("providerType")
	instanceType := c.Query("instanceType")
	architecture := c.Query("architecture")
	osType := c.Query("osType")
	status := c.Query("status")
	search := c.Query("search")

	db := global.APP_DB.Model(&systemModel.SystemImage{})

	// 应用过滤条件
	if providerType != "" {
		db = db.Where("provider_type = ?", providerType)
	}
	if instanceType != "" {
		db = db.Where("instance_type = ?", instanceType)
	}
	if architecture != "" {
		db = db.Where("architecture = ?", architecture)
	}
	if osType != "" {
		// 使用小写匹配，支持主流Linux系统
		db = db.Where("LOWER(os_type) = LOWER(?)", osType)
	}
	if status != "" {
		db = db.Where("status = ?", status)
	}
	if search != "" {
		db = db.Where("name LIKE ? OR description LIKE ? OR os_type LIKE ?",
			"%"+search+"%", "%"+search+"%", "%"+search+"%")
	}

	// 计算总数
	var total int64
	db.Count(&total)

	// 分页查询
	var images []systemModel.SystemImage
	offset := (page - 1) * pageSize
	if err := db.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&images).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// 直接返回镜像列表
	var responses []SystemImageResponse
	for _, image := range images {
		response := SystemImageResponse{SystemImage: image}
		responses = append(responses, response)
	}

	common.ResponseSuccess(c, gin.H{
		"list":     responses,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// CreateSystemImage 创建系统镜像
// @Summary 创建系统镜像
// @Description 创建新的系统镜像配置
// @Tags 系统镜像管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateSystemImageRequest true "创建系统镜像请求参数"
// @Success 200 {object} common.Response "创建成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 401 {object} common.Response "认证失败"
// @Failure 409 {object} common.Response "镜像名称已存在"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/system-images [post]
func CreateSystemImage(c *gin.Context) {
	var req CreateSystemImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}

	// 验证文件扩展名
	if err := validateImageURL(req.ProviderType, req.InstanceType, req.URL); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权"))
		return
	}

	// 检查镜像名称是否已存在
	var existingImage systemModel.SystemImage
	if err := global.APP_DB.Where("name = ? AND provider_type = ? AND instance_type = ? AND architecture = ?",
		req.Name, req.ProviderType, req.InstanceType, req.Architecture).First(&existingImage).Error; err == nil {
		common.ResponseWithError(c, common.NewError(common.CodeConflict, "该镜像名称已存在"))
		return
	}
	// 同一 Provider 类型下镜像链接不可重复；不同 Provider 类型允许复用同一 URL。
	if err := global.APP_DB.Where("provider_type = ? AND url = ?", req.ProviderType, req.URL).First(&existingImage).Error; err == nil {
		common.ResponseWithError(c, common.NewError(common.CodeConflict, "同类型镜像链接已存在"))
		return
	}

	// 创建系统镜像
	image := systemModel.SystemImage{
		Name:         req.Name,
		ProviderType: req.ProviderType,
		InstanceType: req.InstanceType,
		Architecture: req.Architecture,
		URL:          req.URL,
		Checksum:     req.Checksum,
		Size:         req.Size,
		Status:       "active",
		Description:  req.Description,
		OSType:       req.OSType,
		OSVersion:    req.OSVersion,
		Tags:         req.Tags,
		MinMemoryMB:  req.MinMemoryMB,
		MinDiskMB:    req.MinDiskMB,
		UseCDN:       req.UseCDN,
		CreatedBy:    func() *uint { id := userID.(uint); return &id }(),
	}

	// 使用数据库抽象层创建
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&image).Error
	}); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, image, "创建成功")
}

// UpdateSystemImage 更新系统镜像
func UpdateSystemImage(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	var req UpdateSystemImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}

	// 查找系统镜像
	var image systemModel.SystemImage
	if err := global.APP_DB.First(&image, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, "系统镜像不存在"))
		} else {
			common.ResponseWithError(c, common.ClassifyError(err))
		}
		return
	}

	finalProviderType := image.ProviderType
	if req.ProviderType != "" {
		finalProviderType = req.ProviderType
	}
	finalInstanceType := image.InstanceType
	if req.InstanceType != "" {
		finalInstanceType = req.InstanceType
	}
	finalArchitecture := image.Architecture
	if req.Architecture != "" {
		finalArchitecture = req.Architecture
	}
	finalName := image.Name
	if req.Name != "" {
		finalName = req.Name
	}
	finalURL := image.URL
	if req.URL != "" {
		finalURL = req.URL
	}

	// 验证文件扩展名和唯一性（更新 Provider/URL 时同样适用）
	if err := validateImageURL(finalProviderType, finalInstanceType, finalURL); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	var duplicate systemModel.SystemImage
	if err := global.APP_DB.Where("provider_type = ? AND url = ? AND id <> ?", finalProviderType, finalURL, image.ID).First(&duplicate).Error; err == nil {
		common.ResponseWithError(c, common.NewError(common.CodeConflict, "同类型镜像链接已存在"))
		return
	}
	if err := global.APP_DB.Where("name = ? AND provider_type = ? AND instance_type = ? AND architecture = ? AND id <> ?",
		finalName, finalProviderType, finalInstanceType, finalArchitecture, image.ID).First(&duplicate).Error; err == nil {
		common.ResponseWithError(c, common.NewError(common.CodeConflict, "该镜像名称已存在"))
		return
	}

	// 更新字段
	updates := make(map[string]interface{})
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.ProviderType != "" {
		updates["provider_type"] = req.ProviderType
	}
	if req.InstanceType != "" {
		updates["instance_type"] = req.InstanceType
	}
	if req.Architecture != "" {
		updates["architecture"] = req.Architecture
	}
	if req.URL != "" {
		updates["url"] = req.URL
	}
	if req.Checksum != "" {
		updates["checksum"] = req.Checksum
	}
	if req.Size > 0 {
		updates["size"] = req.Size
	}
	if req.Status != "" {
		updates["status"] = req.Status
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.OSType != "" {
		updates["os_type"] = req.OSType
	}
	if req.OSVersion != "" {
		updates["os_version"] = req.OSVersion
	}
	if req.Tags != "" {
		updates["tags"] = req.Tags
	}
	if req.MinMemoryMB != nil {
		updates["min_memory_mb"] = *req.MinMemoryMB
	}
	if req.MinDiskMB != nil {
		updates["min_disk_mb"] = *req.MinDiskMB
	}
	if req.UseCDN != nil {
		updates["use_cdn"] = *req.UseCDN
	}
	updates["updated_at"] = time.Now()

	if err := global.APP_DB.Model(&image).Updates(updates).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, image, "更新成功")
}

// DeleteSystemImage 删除系统镜像
func DeleteSystemImage(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	// 查找系统镜像
	var image systemModel.SystemImage
	if err := global.APP_DB.First(&image, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, "系统镜像不存在"))
		} else {
			common.ResponseWithError(c, common.ClassifyError(err))
		}
		return
	}

	// 软删除
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Delete(&image).Error
	}); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "删除成功")
}

// BatchDeleteSystemImages 批量删除系统镜像
func BatchDeleteSystemImages(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required,min=1"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}

	if err := global.APP_DB.Where("id IN ?", req.IDs).Delete(&systemModel.SystemImage{}).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "批量删除成功")
}

// BatchUpdateSystemImageStatus 批量更新系统镜像状态
func BatchUpdateSystemImageStatus(c *gin.Context) {
	var req struct {
		IDs    []uint `json:"ids" binding:"required,min=1"`
		Status string `json:"status" binding:"required,oneof=active inactive"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}

	if err := global.APP_DB.Model(&systemModel.SystemImage{}).Where("id IN ?", req.IDs).
		Updates(map[string]interface{}{
			"status":     req.Status,
			"updated_at": time.Now(),
		}).Error; err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "批量更新状态成功")
}

// GetAvailableSystemImages 获取可用的系统镜像（用于实例创建）
func GetAvailableSystemImages(c *gin.Context) {
	providerType := c.Query("providerType")
	instanceType := c.Query("instanceType")
	architecture := c.Query("architecture")
	osType := c.Query("osType")

	imageService := images.ImageService{}
	images, err := imageService.GetAvailableImagesWithOS(providerType, instanceType, architecture, osType)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, images)
}

// validateImageURL 验证镜像URL的文件扩展名
func validateImageURL(providerType, instanceType, url string) error {
	switch providerType {
	case "proxmox":
		if instanceType == "vm" && !strings.HasSuffix(url, ".qcow2") {
			return fmt.Errorf("ProxmoxVE虚拟机镜像地址必须是.qcow2文件")
		}
		if instanceType == "container" && !strings.HasSuffix(url, ".tar.xz") {
			return fmt.Errorf("ProxmoxVE LXC容器镜像地址必须是.tar.xz文件")
		}
	case "lxd", "incus":
		if !strings.HasSuffix(url, ".zip") {
			return fmt.Errorf("LXD/Incus镜像地址必须是zip文件")
		}
	case "docker", "podman", "containerd", "orbstack":
		if instanceType == "container" && !strings.HasSuffix(url, ".tar.gz") {
			return fmt.Errorf("Docker/Podman/Containerd/Orbstack容器镜像地址必须是.tar.gz文件")
		}
	case "qemu":
		if instanceType == "vm" && !strings.HasSuffix(url, ".qcow2") {
			return fmt.Errorf("QEMU虚拟机镜像地址必须是.qcow2文件")
		}
	case "kubevirt":
		if instanceType == "vm" && !strings.HasSuffix(url, ".qcow2") {
			return fmt.Errorf("KubeVirt虚拟机镜像地址必须是.qcow2文件")
		}
		if instanceType == "container" && !strings.HasSuffix(url, ".tar.gz") {
			return fmt.Errorf("KubeVirt容器镜像归档地址必须是.tar.gz文件")
		}
	case "vmware":
		if instanceType == "vm" && !strings.HasSuffix(url, ".vmx") && !strings.HasSuffix(url, ".zip") && !strings.HasSuffix(url, ".tar.gz") {
			return fmt.Errorf("VMware虚拟机模板地址必须是.vmx、.zip或.tar.gz文件")
		}
	}
	return nil
}
