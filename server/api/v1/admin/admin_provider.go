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
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	adminProvider "oneclickvirt/service/admin/provider"
	agentService "oneclickvirt/service/agent"

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
		common.ResponseWithError(c, common.ClassifyError(err))
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
		common.ResponseWithError(c, common.ClassifyError(err))
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
		common.ResponseWithError(c, common.ClassifyError(err))
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
		common.ResponseWithError(c, common.ClassifyError(err))
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
		common.ResponseWithError(c, common.ClassifyError(err))
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
		common.ResponseWithError(c, common.ClassifyError(err))
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
		common.ResponseWithError(c, common.ClassifyError(err))
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

// GetProviderDetail 获取单个Provider详情（含 Agent 状态字段）
func GetProviderDetail(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if err := adminProvider.CheckProviderOwnership(uint(id), ownerAdminID); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
		return
	}

	var providerObj providerModel.Provider
	if err := global.APP_DB.First(&providerObj, id).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	common.ResponseSuccess(c, providerObj)
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
		} else if strings.Contains(err.Error(), "不存在") {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, err.Error()))
			return
		} else {
			errorMsg = "健康检查失败: " + err.Error()
		}

		common.ResponseWithError(c, common.NewError(common.CodeValidationError, errorMsg))
		return
	}

	status, err := providerService.GetProviderStatus(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
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
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, status, "获取状态成功")
}

// ExportProviderConfigs 导出所有Provider配置
func ExportProviderConfigs(c *gin.Context) {
	var req struct {
		ProviderIDs []uint `json:"provider_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		// 绑定失败时导出所有
	}

	configService := &provider.ProviderConfigService{}

	exportDir := "exports"
	var err error
	if len(req.ProviderIDs) > 0 {
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

// DetectGPUs 检测Provider节点上的GPU设备
// 通过SSH执行 lxc info --resources 并解析GPU部分
func DetectGPUs(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	providerService := adminProvider.NewService()
	p, err := providerService.GetProviderByID(uint(id), middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	if p.Type != "lxd" && p.Type != "incus" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "GPU检测仅支持 lxd 和 incus 类型的Provider"))
		return
	}

	sshConf := utils.SSHConfig{
		Host:       p.Endpoint,
		Port:       p.SSHPort,
		Username:   p.Username,
		Password:   p.Password,
		PrivateKey: p.SSHKey,
	}

	client, err := utils.NewSSHClient(sshConf)
	if err != nil {
		global.APP_LOG.Error("GPU检测：SSH连接失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "SSH连接失败: "+err.Error()))
		return
	}
	defer client.Close()

	output, err := client.Execute("lxc info --resources 2>/dev/null | awk '/^GPU:/,/^[A-Z]/' | head -60")
	if err != nil {
		global.APP_LOG.Warn("GPU检测：执行命令失败", zap.Error(err))
		common.ResponseSuccess(c, map[string]interface{}{
			"gpus":    []interface{}{},
			"rawInfo": "",
		}, "节点无可用GPU或不支持GPU检测")
		return
	}

	// 解析GPU信息，返回原始文本和解析后的列表供前端展示
	gpus := parseGPUInfo(output)

	common.ResponseSuccess(c, map[string]interface{}{
		"gpus":    gpus,
		"rawInfo": strings.TrimSpace(output),
	}, "GPU检测完成")
}

// parseGPUInfo 解析 lxc info --resources 中的GPU部分
// 示例输出:
//
//	GPU:
//	  Card 0 (DRM 0):
//	    ID: 0
//	    Device: 0000:01:00.0
//	    Product: GP104 [GeForce GTX 1080]
//	    Vendor: NVIDIA Corporation
func parseGPUInfo(raw string) []map[string]string {
	var gpus []map[string]string
	current := map[string]string{}

	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "GPU:" {
			continue
		}
		if strings.HasPrefix(trimmed, "Card ") {
			if len(current) > 0 {
				gpus = append(gpus, current)
			}
			current = map[string]string{"card": trimmed}
			continue
		}
		if idx := strings.Index(trimmed, ": "); idx != -1 {
			key := strings.ToLower(strings.TrimSpace(trimmed[:idx]))
			val := strings.TrimSpace(trimmed[idx+2:])
			current[key] = val
		}
	}
	if len(current) > 0 {
		gpus = append(gpus, current)
	}
	return gpus
}

// GenerateAgentSecret godoc
//
//	@Summary		生成或刷新 Provider 的 Agent 密钥
//	@Description	为 agent 连接模式的 Provider 生成新的鉴权密钥，并返回 WebSocket 连接地址和安装命令
//	@Tags			admin/providers
//	@Param			id	path	int	true	"Provider ID"
//	@Produce		json
//	@Router			/v1/admin/providers/{id}/agent-secret [post]
func GenerateAgentSecret(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("id = ?", id).First(&dbProvider).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	agentSvc := agentService.GetHub()
	_ = agentSvc

	secret, err := agentService.GenerateAgentSecret(uint(id))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "生成密钥失败: "+err.Error()))
		return
	}

	// 更新 ConnectionType 为 agent
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", id).
		Update("connection_type", "agent").Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "更新连接类型失败: "+err.Error()))
		return
	}

	// 构造控制端 WebSocket 地址（从请求头获取）
	scheme := "wss"
	if c.Request.TLS == nil {
		// 检查是否有反向代理传来的 X-Forwarded-Proto
		if proto := c.GetHeader("X-Forwarded-Proto"); proto == "http" {
			scheme = "ws"
		}
	}
	host := c.Request.Host
	if forwardedHost := c.GetHeader("X-Forwarded-Host"); forwardedHost != "" {
		host = forwardedHost
	}
	wsURL := fmt.Sprintf("%s://%s/api/v1/ws/agent", scheme, host)

	// 构造 CDN 加速安装命令（使用 sh 以保证最广兼容性）
	cdnBase := "https://cdn.spiritlhl.net"
	installScript := fmt.Sprintf("%s/https://raw.githubusercontent.com/oneclickvirt/oneclickvirt/main/scripts/install_agent.sh", cdnBase)
	installCmd := fmt.Sprintf(
		"curl -fsSL %s | sh -s -- --ws-url %s --secret %s",
		installScript, wsURL, secret,
	)

	common.ResponseSuccess(c, map[string]interface{}{
		"agentSecret": secret,
		"wsPath":      "/api/v1/ws/agent",
		"wsURL":       wsURL,
		"installCmd":  installCmd,
		"hint":        fmt.Sprintf("在 Agent 节点运行安装命令，或手动执行: oneclickvirt-agent --ws-url %s --secret %s", wsURL, secret),
	}, "Agent 密钥已生成")
}

// GetStoppedContainers godoc
//
//	@Summary		获取节点上已停止的容器列表（用于复制模式）
//	@Description	通过SSH连接到LXD/Incus节点，返回所有Stopped状态的容器名称列表
//	@Tags			admin/providers
//	@Param			id	path	int	true	"Provider ID"
//	@Produce		json
//	@Router			/v1/admin/providers/{id}/stopped-containers [get]
func GetStoppedContainers(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("id = ?", id).First(&dbProvider).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	if dbProvider.Type != "lxd" && dbProvider.Type != "incus" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "仅 LXD/Incus 类型支持容器复制"))
		return
	}

	sshConf := utils.SSHConfig{
		Host:       dbProvider.Endpoint,
		Port:       dbProvider.SSHPort,
		Username:   dbProvider.Username,
		Password:   dbProvider.Password,
		PrivateKey: dbProvider.SSHKey,
	}
	client, err := utils.NewSSHClient(sshConf)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "SSH连接失败: "+err.Error()))
		return
	}
	defer client.Close()

	// 根据 provider 类型选择命令
	listCmd := "lxc list --format csv -c n,s 2>/dev/null"
	if dbProvider.Type == "incus" {
		listCmd = "incus list --format csv -c n,s 2>/dev/null"
	}

	output, err := client.Execute(listCmd)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取容器列表失败: "+err.Error()))
		return
	}

	var stopped []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) == "STOPPED" {
			stopped = append(stopped, strings.TrimSpace(parts[0]))
		}
	}
	if stopped == nil {
		stopped = []string{}
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"containers": stopped,
	}, "获取停止容器列表成功")
}

// ExecOnProvider godoc
//
//	@Summary		在 Provider 节点上执行命令
//	@Description	通过 SSH（SSH模式）或 Agent WebSocket（Agent模式）在节点上执行 shell 命令
//	@Tags			admin/providers
//	@Param			id		path	int					true	"Provider ID"
//	@Param			body	body	admin.ExecCommandRequest	true	"执行命令请求"
//	@Produce		json
//	@Router			/v1/admin/providers/{id}/exec [post]
func ExecOnProvider(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	var req struct {
		Command string `json:"command" binding:"required"`
		Timeout int    `json:"timeout"` // 超时秒数，默认30
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+err.Error()))
		return
	}
	if req.Timeout <= 0 || req.Timeout > 300 {
		req.Timeout = 30
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("id = ?", id).First(&dbProvider).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	var stdout, stderr string

	if dbProvider.ConnectionType == "agent" {
		// Agent 模式：通过 WebSocket 发送命令
		hub := agentService.GetHub()
		conn, ok := hub.GetConn(dbProvider.ID)
		if !ok || conn == nil {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "Agent 节点未连接，请检查节点是否在线"))
			return
		}
		timeout := time.Duration(req.Timeout) * time.Second
		output, execErr := conn.ExecuteWithTimeout(req.Command, timeout)
		if execErr != nil {
			stderr = execErr.Error()
		} else {
			stdout = output
		}
	} else {
		// SSH 模式：通过 SSH 连接执行命令
		sshConf := utils.SSHConfig{
			Host:       dbProvider.Endpoint,
			Port:       dbProvider.SSHPort,
			Username:   dbProvider.Username,
			Password:   dbProvider.Password,
			PrivateKey: dbProvider.SSHKey,
		}
		client, sshErr := utils.NewSSHClient(sshConf)
		if sshErr != nil {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "SSH连接失败: "+sshErr.Error()))
			return
		}
		defer client.Close()

		output, execErr := client.Execute(req.Command)
		if execErr != nil {
			stderr = execErr.Error()
		} else {
			stdout = output
		}
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"stdout":     stdout,
		"stderr":     stderr,
		"command":    req.Command,
		"providerID": id,
	}, "命令执行完成")
}
