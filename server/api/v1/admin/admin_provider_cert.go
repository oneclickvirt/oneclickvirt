package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	adminProvider "oneclickvirt/service/admin/provider"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/service/provider"
	"oneclickvirt/service/taskgate"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GenerateProviderCert 为Provider生成证书或配置

// @Summary 生成Provider 证书
// @Description 创建生成Provider 证书
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "生成Provider 证书成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "生成Provider 证书失败"
// @Router /admin/providers/{id}/generate-cert [post]
func GenerateProviderCert(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(uint(providerID), ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
	}

	providerService := adminProvider.NewService()
	setupCommand, err := providerService.GenerateProviderCert(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, gin.H{
		"setupCommand": setupCommand,
	}, "证书生成成功")
}

// AutoConfigureProviderStream 实时自动配置Provider (SSE streaming)

// @Summary 自动配置Provider Stream
// @Description 创建自动配置Provider Stream
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "自动配置Provider Stream成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "自动配置Provider Stream失败"
// @Router /admin/providers/{id}/auto-configure-stream [post]
func AutoConfigureProviderStream(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(uint(providerID), ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
	}

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

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

// GetProviderDetail 获取单个Provider详情（含 Agent 状态字段）

// @Summary 获取Provider 详情
// @Description 获取获取Provider 详情
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取Provider 详情成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取Provider 详情失败"
// @Router /admin/providers/{id} [get]
func GetProviderDetail(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(uint(id), ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
	}

	var providerObj providerModel.Provider
	if err := global.APP_DB.First(&providerObj, id).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	resp := struct {
		providerModel.Provider
		AgentRuntimeStatus   string     `json:"agentRuntimeStatus,omitempty"`
		AgentControlLastSeen *time.Time `json:"agentControlLastSeen,omitempty"`
	}{
		Provider: providerObj,
	}

	if providerObj.ConnectionType == "agent" {
		runtimeHealth := agentService.GetHub().GetRuntimeHealth(providerObj.ID)
		resp.AgentRuntimeStatus = runtimeHealth.Status
		resp.AgentControlLastSeen = runtimeHealth.ControlLastSeen
	}

	common.ResponseSuccess(c, resp)
}

// CheckProviderHealth 保留旧健康检查路由，实际提交持久化后台任务。
// @Summary 提交Provider健康检查任务（兼容路由）
// @Description 兼容旧客户端；远端健康探测在管理员任务池中执行，请通过任务列表查看结果
// @Tags Provider管理
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=admin.Task} "健康检查任务已提交"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 404 {object} common.Response "Provider不存在"
// @Failure 409 {object} common.Response "已有同类任务"
// @Router /admin/providers/{id}/health-check [post]
func CheckProviderHealth(c *gin.Context) {
	// Keep the legacy route as an alias, but never perform remote SSH/API probes
	// in the HTTP request. Callers receive the persistent admin task and can
	// observe its terminal result from the task list.
	QueueProviderHealthCheck(c)
}

// GetProviderStatus 获取Provider状态详情

// @Summary 获取Provider 状态
// @Description 获取获取Provider 状态
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取Provider 状态成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取Provider 状态失败"
// @Router /admin/providers/{id}/status [get]

// @Summary 获取Provider 状态
// @Description 获取获取Provider 状态
// @Tags Provider管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取Provider 状态成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取Provider 状态失败"
// @Router /providers/{id}/status [get]
func GetProviderStatus(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(uint(providerID), ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
	}

	providerService := adminProvider.NewService()
	status, err := providerService.GetProviderStatus(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, status, "获取状态成功")
}

// ExportProviderConfigs 导出所有Provider配置

// @Summary 导出Provider 配置s
// @Description 创建导出Provider 配置s
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "导出Provider 配置s成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "导出Provider 配置s失败"
// @Router /admin/providers/export-configs [post]
func ExportProviderConfigs(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	var req struct {
		ProviderIDs []uint `json:"provider_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	req.ProviderIDs = uniqueNonZeroIDs(req.ProviderIDs)
	if len(req.ProviderIDs) > 500 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "单次最多导出500个Provider配置"))
		return
	}

	configService := &provider.ProviderConfigService{}
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if len(req.ProviderIDs) > 0 {
			if err := ensureProviderOwners(c, req.ProviderIDs); err != nil {
				common.ResponseWithError(c, err)
				return
			}
		} else {
			if err := global.APP_DB.Model(&providerModel.Provider{}).
				Where("owner_admin_id = ?", ownerAdminID).
				Pluck("id", &req.ProviderIDs).Error; err != nil {
				common.ResponseWithError(c, common.NewError(common.CodeDatabaseError, "查询可导出Provider失败"))
				return
			}
		}
	}
	exportDir := "exports"
	var err error
	if ownerAdminID > 0 {
		err = configService.ExportProviderConfigs(exportDir, req.ProviderIDs)
	} else if len(req.ProviderIDs) > 0 {
		err = configService.ExportProviderConfigs(exportDir, req.ProviderIDs)
	} else {
		err = configService.ExportAllConfigs(exportDir)
	}
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
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

// @Summary 检查Provider Name
// @Description 获取检查Provider Name
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "检查Provider Name成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "检查Provider Name失败"
// @Router /admin/providers/check-name [get]
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
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, map[string]bool{
		"exists": exists,
	}, "检查成功")
}

// CheckProviderEndpoint 检查Provider SSH地址和端口是否已存在

// @Summary 检查Provider Endpoint
// @Description 获取检查Provider Endpoint
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "检查Provider Endpoint成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "检查Provider Endpoint失败"
// @Router /admin/providers/check-endpoint [get]
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
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, map[string]bool{
		"exists": exists,
	}, "检查成功")
}
