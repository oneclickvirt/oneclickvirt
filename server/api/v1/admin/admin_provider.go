package admin

import (
	"context"
	"fmt"
	"net/http"
	"oneclickvirt/middleware"
	"oneclickvirt/service/provider"
	"oneclickvirt/utils"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	adminProvider "oneclickvirt/service/admin/provider"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetProviderList 获取提供商列表
func GetProviderList(c *gin.Context) {
	var req admin.ProviderListRequest
	req.Page = 1
	req.PageSize = 10

	if err := c.ShouldBindQuery(&req); err != nil {
		global.APP_LOG.Warn("Provider列表查询参数绑定失败，使用默认值", zap.Error(err))
	}

	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > 100 {
		req.PageSize = 10
	}

	providerService := adminProvider.NewService()
	providers, total, err := providerService.GetProviderList(req, middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取提供商列表失败"))
		return
	}

	common.ResponseSuccessWithPagination(c, providers, total, req.Page, req.PageSize)
}

// CreateProvider 创建提供商
func CreateProvider(c *gin.Context) {
	var req admin.CreateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("CreateProvider参数绑定失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	providerService := adminProvider.NewService()
	providerObj, err := providerService.CreateProvider(req, middleware.GetOwnerAdminID(c))
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "已存在") || strings.Contains(errMsg, "已被") {
			common.ResponseWithError(c, common.NewError(common.CodeConflict, errMsg))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, errMsg))
		}
		return
	}

	common.ResponseSuccess(c, providerObj, "创建提供商成功")
}

// UpdateProvider 更新提供商
func UpdateProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	var req admin.UpdateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("UpdateProvider参数绑定失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	req.ID = uint(id)

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(req.ID, ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "无权操作该Provider"))
			return
		}
	}

	providerService := adminProvider.NewService()
	if err := providerService.UpdateProvider(req); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "已存在") || strings.Contains(errMsg, "已被") {
			common.ResponseWithError(c, common.NewError(common.CodeConflict, errMsg))
		} else if strings.Contains(errMsg, "不存在") {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, errMsg))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, errMsg))
		}
		return
	}

	if err := provider.GetProviderService().ReloadProvider(req.ID); err != nil {
		global.APP_LOG.Warn("Provider缓存刷新失败，新配置将在下次重启后生效",
			zap.Uint("providerID", req.ID),
			zap.Error(err))
	}

	common.ResponseSuccess(c, nil, "更新提供商成功")
}

// DeleteProvider 删除提供商
func DeleteProvider(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的提供商ID"))
		return
	}

	forceDelete := c.Query("force") == "true"

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(uint(providerID), ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "无权操作该Provider"))
			return
		}
	}

	providerService := adminProvider.NewService()
	err = providerService.DeleteProvider(uint(providerID), forceDelete)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "运行中的实例") || strings.Contains(errMsg, "running instance") {
			common.ResponseWithError(c, common.NewError(common.CodeProviderHasInstances, errMsg))
		} else if strings.Contains(errMsg, "不存在") {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, errMsg))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, errMsg))
		}
		return
	}

	common.ResponseSuccess(c, nil, "删除提供商成功")
}

// FreezeProvider 冻结提供商
func FreezeProvider(c *gin.Context) {
	var req admin.FreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	providerService := adminProvider.NewService()
	if err := providerService.FreezeProvider(req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "提供商已冻结")
}

// UnfreezeProvider 解冻提供商
func UnfreezeProvider(c *gin.Context) {
	var req admin.UnfreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	providerService := adminProvider.NewService()
	if err := providerService.UnfreezeProvider(req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "提供商已解冻")
}

// GenerateProviderCert 为Provider生成证书或配置
func GenerateProviderCert(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	providerService := adminProvider.NewService()
	setupCommand, err := providerService.GenerateProviderCert(uint(providerID))
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "不支持") {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, errMsg))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "生成证书失败: "+errMsg))
		}
		return
	}

	common.ResponseSuccess(c, gin.H{
		"setupCommand": setupCommand,
	}, "证书生成成功")
}

// AutoConfigureProviderStream 实时自动配置Provider (SSE streaming)
func AutoConfigureProviderStream(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	outputChan := make(chan string, 100)
	errorChan := make(chan error, 1)

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	go func() {
		defer close(outputChan)
		defer close(errorChan)

		providerService := adminProvider.NewService()
		err := providerService.AutoConfigureProviderWithStreamContext(ctx, uint(providerID), outputChan)
		if err != nil {
			select {
			case errorChan <- err:
			case <-ctx.Done():
			}
		}
	}()

	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "服务器不支持实时输出"))
		return
	}

	for {
		select {
		case output, ok := <-outputChan:
			if !ok {
				c.Writer.WriteString("\n\n=== 配置完成 ===\n")
				flusher.Flush()
				return
			}
			c.Writer.WriteString(output + "\n")
			flusher.Flush()

		case err := <-errorChan:
			if err != nil {
				c.Writer.WriteString(fmt.Sprintf("\n\n=== 错误: %s ===\n", err.Error()))
				flusher.Flush()
				return
			}

		case <-ctx.Done():
			c.Writer.WriteString("\n\n=== 连接已断开 ===\n")
			flusher.Flush()
			return
		}
	}
}

// CheckProviderHealth 检查Provider健康状态
func CheckProviderHealth(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	forceRefresh := c.DefaultQuery("forceRefresh", "true") == "true"

	providerService := adminProvider.NewService()
	err = providerService.CheckProviderHealthWithOptions(uint(providerID), forceRefresh)
	if err != nil {
		errorMsg := "健康检查失败"
		if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "i/o timeout") {
			errorMsg = "健康检查超时，请检查网络连接或服务器状态"
		} else if strings.Contains(err.Error(), "connection refused") {
			errorMsg = "无法连接到服务器，请检查服务器状态和网络配置"
		} else if strings.Contains(err.Error(), "handshake failed") {
			errorMsg = "SSH握手失败，请检查认证信息和服务器配置"
		} else {
			errorMsg = "健康检查失败: " + err.Error()
		}

		common.ResponseWithError(c, common.NewError(common.CodeInternalError, errorMsg))
		return
	}

	status, err := providerService.GetProviderStatus(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取状态失败: "+err.Error()))
		return
	}

	common.ResponseSuccess(c, status, "健康检查完成")
}

// GetProviderStatus 获取Provider状态详情
func GetProviderStatus(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	providerService := adminProvider.NewService()
	status, err := providerService.GetProviderStatus(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取状态失败: "+err.Error()))
		return
	}

	common.ResponseSuccess(c, status, "获取状态成功")
}

// ExportProviderConfigs 导出所有Provider配置
func ExportProviderConfigs(c *gin.Context) {
	configService := &provider.ProviderConfigService{}

	exportDir := "exports"
	err := configService.ExportAllConfigs(exportDir)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "导出配置失败: "+err.Error()))
		return
	}

	common.ResponseSuccess(c, gin.H{
		"exportDir": exportDir,
	}, "配置导出成功，文件保存在 "+exportDir+" 目录")
}

// TestSSHConnection 测试SSH连接延迟
func TestSSHConnection(c *gin.Context) {
	var req admin.TestSSHConnectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	if req.TestCount <= 0 {
		req.TestCount = 3
	}
	if req.TestCount > 10 {
		req.TestCount = 10
	}

	global.APP_LOG.Debug("开始测试SSH连接",
		zap.String("host", req.Host),
		zap.Int("port", req.Port),
		zap.String("username", req.Username),
		zap.Int("testCount", req.TestCount))

	if req.Password == "" && req.SSHKey == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "必须提供SSH密码或SSH密钥其中一种认证方式"))
		return
	}

	sshConfig := utils.SSHConfig{
		Host:       req.Host,
		Port:       req.Port,
		Username:   req.Username,
		Password:   req.Password,
		PrivateKey: req.SSHKey,
	}

	minLatency, maxLatency, avgLatency, err := utils.TestSSHConnectionLatency(sshConfig, req.TestCount)
	if err != nil {
		global.APP_LOG.Error("SSH连接测试失败",
			zap.String("host", req.Host),
			zap.Int("port", req.Port),
			zap.Error(err))

		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "SSH连接测试失败: "+err.Error()))
		return
	}

	recommendedTimeout := int((maxLatency * 2).Seconds())
	if recommendedTimeout < 10 {
		recommendedTimeout = 10
	}

	response := admin.TestSSHConnectionResponse{
		Success:            true,
		MinLatency:         minLatency.Milliseconds(),
		MaxLatency:         maxLatency.Milliseconds(),
		AvgLatency:         avgLatency.Milliseconds(),
		RecommendedTimeout: recommendedTimeout,
		TestCount:          req.TestCount,
	}

	global.APP_LOG.Debug("SSH连接测试成功",
		zap.String("host", req.Host),
		zap.Int("port", req.Port),
		zap.Int64("minLatency", response.MinLatency),
		zap.Int64("maxLatency", response.MaxLatency),
		zap.Int64("avgLatency", response.AvgLatency),
		zap.Int("recommendedTimeout", response.RecommendedTimeout))

	common.ResponseSuccess(c, response, "SSH连接测试成功")
}

// CheckProviderName 检查Provider名称是否已存在
func CheckProviderName(c *gin.Context) {
	name := c.Query("name")
	if name == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "名称参数不能为空"))
		return
	}

	excludeIdStr := c.Query("excludeId")
	var excludeId *uint
	if excludeIdStr != "" {
		id, err := strconv.ParseUint(excludeIdStr, 10, 32)
		if err == nil {
			uid := uint(id)
			excludeId = &uid
		}
	}

	providerService := adminProvider.NewService()
	exists, err := providerService.CheckProviderNameExists(name, excludeId)
	if err != nil {
		global.APP_LOG.Error("检查Provider名称失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "检查失败"))
		return
	}

	common.ResponseSuccess(c, map[string]bool{
		"exists": exists,
	}, "检查成功")
}

// CheckProviderEndpoint 检查Provider SSH地址和端口是否已存在
func CheckProviderEndpoint(c *gin.Context) {
	endpoint := c.Query("endpoint")
	if endpoint == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "endpoint参数不能为空"))
		return
	}

	sshPortStr := c.Query("sshPort")
	if sshPortStr == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "sshPort参数不能为空"))
		return
	}

	sshPort, err := strconv.Atoi(sshPortStr)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "sshPort参数格式错误"))
		return
	}

	excludeIdStr := c.Query("excludeId")
	var excludeId *uint
	if excludeIdStr != "" {
		id, err := strconv.ParseUint(excludeIdStr, 10, 32)
		if err == nil {
			uid := uint(id)
			excludeId = &uid
		}
	}

	providerService := adminProvider.NewService()
	exists, err := providerService.CheckProviderEndpointExists(endpoint, sshPort, excludeId)
	if err != nil {
		global.APP_LOG.Error("检查Provider SSH地址失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "检查失败"))
		return
	}

	common.ResponseSuccess(c, map[string]bool{
		"exists": exists,
	}, "检查成功")
}
