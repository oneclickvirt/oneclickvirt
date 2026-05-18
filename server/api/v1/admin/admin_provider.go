package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	providerPkg "oneclickvirt/provider"
	adminProvider "oneclickvirt/service/admin/provider"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// gpuCacheEntry GPU/NPU 检测缓存条目
type gpuCacheEntry struct {
	gpus         []map[string]string
	npus         []map[string]string
	accelerators []map[string]string
	rawInfo      string
	cachedAt     time.Time
}

// gpuDetectionCache GPU 检测结果缓存（5分钟有效期）
var gpuDetectionCache sync.Map

var normalizedPCIBusRegex = regexp.MustCompile(`(?i)([0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7])$`)

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

// CheckProviderHealth 检查Provider健康状态
func CheckProviderHealth(c *gin.Context) {
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

// DetectGPUs 检测Provider节点上的GPU/NPU设备
// 支持SSH与Agent模式，优先使用lxc/incus资源信息并结合nvidia-smi/lspci/npu-smi等多源检测
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

	// 检查缓存：优先内存缓存（5分钟TTL），其次持久化DB缓存
	forceRefresh := c.DefaultQuery("forceRefresh", "false") == "true"
	if !forceRefresh {
		// 1. 内存缓存（5分钟TTL）
		if cached, ok := gpuDetectionCache.Load(uint(id)); ok {
			if entry, ok2 := cached.(gpuCacheEntry); ok2 && time.Since(entry.cachedAt) < 5*time.Minute {
				normalizedAccelerators := normalizeDetectedAccelerators(entry.accelerators)
				normalizedGPUs, normalizedNPUs := splitStringGPUsNPUs(normalizedAccelerators)
				global.APP_LOG.Debug("GPU检测：命中内存缓存",
					zap.Uint("providerID", uint(id)),
					zap.Duration("age", time.Since(entry.cachedAt)))
				common.ResponseSuccess(c, map[string]interface{}{
					"gpus":         normalizedGPUs,
					"npus":         normalizedNPUs,
					"accelerators": normalizedAccelerators,
					"rawInfo":      entry.rawInfo,
					"cached":       true,
				}, "GPU/NPU检测完成（缓存）")
				return
			}
		}
		// 2. 持久化DB缓存（跨重启有效）
		if p.GpuInfo != "" {
			var cachedGpus []map[string]string
			if json.Unmarshal([]byte(p.GpuInfo), &cachedGpus) == nil && len(cachedGpus) > 0 {
				normalizedGpus := normalizeDetectedAccelerators(cachedGpus)
				if len(normalizedGpus) != len(cachedGpus) {
					if gpuInfoBytes, err := json.Marshal(normalizedGpus); err == nil {
						global.APP_DB.Model(&providerModel.Provider{}).
							Where("id = ?", uint(id)).
							Update("gpu_info", string(gpuInfoBytes))
					}
				}
				global.APP_LOG.Debug("GPU检测：命中DB持久化缓存",
					zap.Uint("providerID", uint(id)))
				common.ResponseSuccess(c, map[string]interface{}{
					"gpus":         normalizedGpus,
					"npus":         []map[string]string{},
					"accelerators": normalizedGpus,
					"rawInfo":      "",
					"cached":       true,
				}, "GPU/NPU检测完成（持久化缓存）")
				return
			}
		}
	}

	execCtx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	providerInstance, err := provider.EnsureProviderConnected(execCtx, uint(id))
	if err != nil {
		global.APP_LOG.Error("GPU检测：Provider连接不可用", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider连接不可用: "+err.Error()))
		return
	}

	accelerators, rawInfo, detectErr := detectAccelerators(execCtx, providerInstance, p.Type)
	if detectErr != nil {
		global.APP_LOG.Warn("GPU/NPU检测失败", zap.Error(detectErr), zap.Uint("providerID", p.ID), zap.String("providerType", p.Type))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "设备检测失败: "+detectErr.Error()))
		return
	}

	accelerators = normalizeDetectedAccelerators(accelerators)
	gpus, npus := splitStringGPUsNPUs(accelerators)

	// 写入内存缓存
	gpuDetectionCache.Store(uint(id), gpuCacheEntry{
		gpus:         gpus,
		npus:         npus,
		accelerators: accelerators,
		rawInfo:      strings.TrimSpace(rawInfo),
		cachedAt:     time.Now(),
	})

	// 持久化到 Provider 表，供用户端免检测直接展示 GPU 选项
	gpuInfoBytes, _ := json.Marshal(gpus)
	global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", uint(id)).
		Update("gpu_info", string(gpuInfoBytes))

	common.ResponseSuccess(c, map[string]interface{}{
		"gpus":         gpus,
		"npus":         npus,
		"accelerators": accelerators,
		"rawInfo":      strings.TrimSpace(rawInfo),
		"cached":       false,
	}, "GPU/NPU检测完成")
}

func splitStringGPUsNPUs(devices []map[string]string) ([]map[string]string, []map[string]string) {
	gpus := make([]map[string]string, 0)
	npus := make([]map[string]string, 0)
	for _, d := range devices {
		if strings.EqualFold(strings.TrimSpace(d["kind"]), "npu") {
			npus = append(npus, d)
		} else {
			gpus = append(gpus, d)
		}
	}
	return gpus, npus
}

func normalizeDetectedAccelerators(devices []map[string]string) []map[string]string {
	merged := make([]map[string]string, 0, len(devices))
	mergedIndex := make(map[string]int)
	for _, raw := range devices {
		device := map[string]string{}
		for key, value := range raw {
			device[key] = strings.TrimSpace(value)
		}
		if strings.TrimSpace(device["kind"]) == "" {
			device["kind"] = "gpu"
		}
		if strings.TrimSpace(device["product"]) == "" && strings.TrimSpace(device["name"]) != "" {
			device["product"] = device["name"]
		}
		if strings.TrimSpace(device["source"]) == "" {
			device["source"] = "unknown"
		}

		key := acceleratorMergeKey(device)
		if idx, ok := mergedIndex[key]; ok {
			mergeAcceleratorRecord(merged[idx], device)
			continue
		}
		merged = append(merged, device)
		mergedIndex[key] = len(merged) - 1
	}

	sort.SliceStable(merged, func(i, j int) bool {
		a := merged[i]
		b := merged[j]
		if a["kind"] != b["kind"] {
			return a["kind"] < b["kind"]
		}
		if a["id"] != b["id"] {
			return a["id"] < b["id"]
		}
		if normalizePCIBus(a["bus"]) != normalizePCIBus(b["bus"]) {
			return normalizePCIBus(a["bus"]) < normalizePCIBus(b["bus"])
		}
		return a["name"] < b["name"]
	})

	return merged
}

func detectAccelerators(ctx context.Context, providerInstance providerPkg.Provider, providerType string) ([]map[string]string, string, error) {
	devices := make([]map[string]string, 0)
	rawSections := make([]string, 0)
	mergedIndex := make(map[string]int)
	hadAnySource := false

	addDevice := func(kind, id, name, vendor, bus, source, card string) {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind == "" {
			kind = "gpu"
		}
		id = strings.TrimSpace(id)
		name = strings.TrimSpace(name)
		vendor = strings.TrimSpace(vendor)
		bus = strings.TrimSpace(bus)
		source = strings.TrimSpace(source)
		card = strings.TrimSpace(card)
		if name == "" {
			name = card
		}
		if source == "" {
			source = "unknown"
		}

		d := map[string]string{
			"kind":    kind,
			"id":      id,
			"name":    name,
			"product": name,
			"vendor":  vendor,
			"bus":     bus,
			"source":  source,
		}
		if card != "" {
			d["card"] = card
		}

		key := acceleratorMergeKey(d)
		if idx, ok := mergedIndex[key]; ok {
			mergeAcceleratorRecord(devices[idx], d)
			return
		}

		devices = append(devices, d)
		mergedIndex[key] = len(devices) - 1
	}

	appendRaw := func(title, output string) {
		output = strings.TrimSpace(output)
		if output == "" {
			return
		}
		rawSections = append(rawSections, title+"\n"+output)
	}

	resourceCmd := "lxc info --resources 2>/dev/null | awk '/^GPU:/,/^[A-Z]/' | head -120"
	if providerType == "incus" {
		resourceCmd = "incus info --resources 2>/dev/null | awk '/^GPU:/,/^[A-Z]/' | head -120"
	}
	if output, err := providerInstance.ExecuteSSHCommand(ctx, resourceCmd); err == nil {
		hadAnySource = true
		appendRaw("[lxc/incus resources]", output)
		for _, d := range parseLXDGPUInfo(output) {
			addDevice("gpu", d["id"], d["name"], d["vendor"], d["device"], "lxc-resources", d["card"])
		}
	} else {
		global.APP_LOG.Debug("GPU检测：lxc/incus资源命令执行失败", zap.Error(err))
	}

	if output, err := providerInstance.ExecuteSSHCommand(ctx, "nvidia-smi --query-gpu=index,name,pci.bus_id --format=csv,noheader 2>/dev/null || true"); err == nil {
		hadAnySource = true
		appendRaw("[nvidia-smi]", output)
		for _, d := range parseNvidiaSMI(output) {
			addDevice("gpu", d["id"], d["name"], "NVIDIA", d["bus"], "nvidia-smi", "")
		}
	}

	if output, err := providerInstance.ExecuteSSHCommand(ctx, "lspci -Dnn 2>/dev/null || true"); err == nil {
		hadAnySource = true
		appendRaw("[lspci]", output)
		for _, d := range parseLspciAccelerators(output) {
			addDevice(d["kind"], d["id"], d["name"], d["vendor"], d["bus"], "lspci", "")
		}
	}

	if output, err := providerInstance.ExecuteSSHCommand(ctx, "npu-smi info 2>/dev/null || true"); err == nil {
		hadAnySource = true
		appendRaw("[npu-smi]", output)
		for _, d := range parseNPUSmiInfo(output) {
			addDevice("npu", d["id"], d["name"], d["vendor"], d["bus"], "npu-smi", "")
		}
	}

	if !hadAnySource {
		return nil, strings.Join(rawSections, "\n\n"), fmt.Errorf("未能执行任何检测命令，请检查节点连接状态与命令执行权限")
	}

	sort.SliceStable(devices, func(i, j int) bool {
		a := devices[i]
		b := devices[j]
		if a["kind"] != b["kind"] {
			return a["kind"] < b["kind"]
		}
		if a["id"] != b["id"] {
			return a["id"] < b["id"]
		}
		if a["bus"] != b["bus"] {
			return a["bus"] < b["bus"]
		}
		return a["name"] < b["name"]
	})

	return devices, strings.Join(rawSections, "\n\n"), nil
}

func acceleratorMergeKey(device map[string]string) string {
	kind := strings.ToLower(strings.TrimSpace(device["kind"]))
	vendor := strings.ToLower(strings.TrimSpace(device["vendor"]))
	id := strings.ToLower(strings.TrimSpace(device["id"]))
	name := normalizeAcceleratorName(device["name"])
	bus := normalizePCIBus(device["bus"])

	switch {
	case bus != "":
		return kind + "|bus|" + bus
	case vendor != "" && id != "":
		return kind + "|vendor-id|" + vendor + "|" + id
	case vendor != "" && name != "":
		return kind + "|vendor-name|" + vendor + "|" + name
	case name != "":
		return kind + "|name|" + name
	default:
		return kind + "|raw|" + strings.ToLower(strings.TrimSpace(device["source"])) + "|" + strings.ToLower(strings.TrimSpace(device["card"]))
	}
}

func mergeAcceleratorRecord(dst, src map[string]string) {
	for _, field := range []string{"id", "name", "product", "vendor", "bus", "card"} {
		if strings.TrimSpace(dst[field]) == "" && strings.TrimSpace(src[field]) != "" {
			dst[field] = strings.TrimSpace(src[field])
		}
	}

	if normalizePCIBus(dst["bus"]) == "" && normalizePCIBus(src["bus"]) != "" {
		dst["bus"] = strings.TrimSpace(src["bus"])
	}

	if acceleratorSourceRank(src["source"]) > acceleratorSourceRank(dst["source"]) {
		dst["source"] = strings.TrimSpace(src["source"])
	}
}

func acceleratorSourceRank(source string) int {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "lxc-resources":
		return 4
	case "nvidia-smi", "npu-smi":
		return 3
	case "lspci":
		return 2
	case "unknown":
		return 1
	default:
		return 0
	}
}

func normalizeAcceleratorName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.Join(strings.Fields(name), " ")
	return name
}

func normalizePCIBus(bus string) string {
	bus = strings.ToLower(strings.TrimSpace(bus))
	if bus == "" {
		return ""
	}
	if matched := normalizedPCIBusRegex.FindStringSubmatch(bus); len(matched) > 1 {
		return matched[1]
	}
	return bus
}

// splitGPUsNPUs 从缓存的设备列表中分离 GPU 和 NPU
func splitGPUsNPUs(devices []map[string]interface{}) ([]map[string]interface{}, []map[string]interface{}) {
	gpus := make([]map[string]interface{}, 0)
	npus := make([]map[string]interface{}, 0)
	for _, d := range devices {
		kind, _ := d["kind"].(string)
		if strings.EqualFold(strings.TrimSpace(kind), "npu") {
			npus = append(npus, d)
		} else {
			gpus = append(gpus, d)
		}
	}
	return gpus, npus
}

// parseLXDGPUInfo 解析 lxc/incus info --resources 中的GPU片段
func parseLXDGPUInfo(raw string) []map[string]string {
	gpus := make([]map[string]string, 0)
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
			if key == "product" {
				current["name"] = val
			}
		}
	}
	if len(current) > 0 {
		gpus = append(gpus, current)
	}

	return gpus
}

func parseNvidiaSMI(raw string) []map[string]string {
	devices := make([]map[string]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		bus := ""
		if len(parts) >= 3 {
			bus = strings.TrimSpace(parts[2])
		}
		devices = append(devices, map[string]string{
			"id":   id,
			"name": name,
			"bus":  bus,
		})
	}
	return devices
}

func parseLspciAccelerators(raw string) []map[string]string {
	devices := make([]map[string]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)

		isNPU := containsAny(lower,
			" npu",
			"neural",
			"habanalabs",
			"gaudi",
			"ascend",
			"cambricon",
			"kunlun",
			"mlu",
			"processing accelerators",
		)
		isGPU := containsAny(lower,
			"vga compatible controller",
			"3d controller",
			"display controller",
		)
		if !isGPU && !isNPU {
			continue
		}

		kind := "gpu"
		if isNPU {
			kind = "npu"
		}

		bus := ""
		name := line
		if idx := strings.Index(line, " "); idx > 0 {
			bus = strings.TrimSpace(line[:idx])
		}
		if idx := strings.Index(line, ": "); idx >= 0 {
			name = strings.TrimSpace(line[idx+2:])
		}

		vendor := inferVendor(name)
		devices = append(devices, map[string]string{
			"kind":   kind,
			"id":     "",
			"name":   name,
			"vendor": vendor,
			"bus":    bus,
		})
	}
	return devices
}

func parseNPUSmiInfo(raw string) []map[string]string {
	devices := make([]map[string]string, 0)
	idRegex := regexp.MustCompile(`\b([0-9]{1,2})\b`)

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			continue
		}
		lower := strings.ToLower(line)
		if !containsAny(lower, "npu", "ascend") {
			continue
		}

		id := ""
		if m := idRegex.FindStringSubmatch(line); len(m) > 1 {
			id = m[1]
		}
		devices = append(devices, map[string]string{
			"id":     id,
			"name":   line,
			"vendor": inferVendor(line),
			"bus":    "",
		})
	}

	return devices
}

func containsAny(s string, keywords ...string) bool {
	for _, k := range keywords {
		if strings.Contains(s, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func inferVendor(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "nvidia"):
		return "NVIDIA"
	case strings.Contains(lower, "advanced micro devices"), strings.Contains(lower, " amd "), strings.HasPrefix(lower, "amd"):
		return "AMD"
	case strings.Contains(lower, "intel"):
		return "Intel"
	case strings.Contains(lower, "huawei"), strings.Contains(lower, "ascend"):
		return "Huawei"
	case strings.Contains(lower, "cambricon"), strings.Contains(lower, "mlu"):
		return "Cambricon"
	case strings.Contains(lower, "habanalabs"), strings.Contains(lower, "gaudi"):
		return "Habana"
	case strings.Contains(lower, "kunlun"):
		return "Baidu"
	default:
		return ""
	}
}

// GenerateAgentSecret godoc
//
//	@Summary		生成或获取 Provider 的 Agent 密钥
//	@Description	为 agent 连接模式的 Provider 生成新的鉴权密钥（仅首次），后续调用只返回已有密钥。密钥一旦创建即写死，需删除Provider重建才能刷新。
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

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(uint(id), ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("id = ?", id).First(&dbProvider).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	// 如果已有 agent_secret，直接返回现有密钥（密钥一旦创建即写死，不支持刷新）
	var secret string
	if dbProvider.AgentSecret != "" {
		secret = dbProvider.AgentSecret
	} else {
		secret, err = agentService.GenerateAgentSecret(uint(id))
		if err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "生成密钥失败: "+err.Error()))
			return
		}
	}

	// 更新 ConnectionType 为 agent（如果还不是）
	if dbProvider.ConnectionType != "agent" {
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("id = ?", id).
			Update("connection_type", "agent").Error; err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "更新连接类型失败: "+err.Error()))
			return
		}
	}

	// 构造控制端 WebSocket 地址
	// 默认 wss（加密）。仅当直接 HTTP 无代理且无 TLS 时才降级为 ws。
	scheme := "wss"
	forwardedProto := normalizeForwardedProto(c.GetHeader("X-Forwarded-Proto"))
	if c.Request.TLS == nil {
		if forwardedProto == "" {
			// 无反向代理且无 TLS → 回退 ws（开发/测试环境）
			scheme = "ws"
		} else if forwardedProto == "http" {
			scheme = "ws"
		}
	}
	host := strings.TrimSpace(c.Request.Host)
	if forwardedHost := normalizeForwardedHost(c.GetHeader("X-Forwarded-Host")); forwardedHost != "" {
		host = forwardedHost
	}
	if host == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无法解析控制端主机地址"))
		return
	}
	// nginx $host 不含端口，需从 X-Forwarded-Port 或 server config 补充端口
	if !strings.Contains(host, ":") {
		port := normalizeForwardedPort(c.GetHeader("X-Forwarded-Port"))
		if port == "" {
			port = fmt.Sprintf("%d", global.GetAppConfig().System.Addr)
		}
		if port != "" && port != "80" && port != "443" {
			host = host + ":" + port
		}
	}
	wsURL := fmt.Sprintf("%s://%s/api/v1/ws/agent", scheme, host)
	httpScheme := "https"
	if c.Request.TLS == nil {
		if forwardedProto == "http" || forwardedProto == "" {
			httpScheme = "http"
		}
	}
	controllerAPIBase := fmt.Sprintf("%s://%s/api/v1/public/agent", httpScheme, host)

	// 构造 CDN 加速安装命令（使用 sh 以保证最广兼容性）
	cdnBase := "https://cdn.spiritlhl.net"
	installScript := fmt.Sprintf("%s/https://raw.githubusercontent.com/oneclickvirt/oneclickvirt/main/install_agent.sh", cdnBase)
	installCmdGithub := fmt.Sprintf(
		"curl -fsSL %s | sh -s -- --ws-url %s --secret %s --agent-source github",
		installScript, wsURL, secret,
	)
	installCmdController := fmt.Sprintf(
		"curl -fsSL %s/install-agent.sh | sh -s -- --ws-url %s --secret %s --agent-source controller --controller-base-url %s/releases",
		controllerAPIBase, wsURL, secret, controllerAPIBase,
	)
	responseData := map[string]interface{}{
		"agentSecret":             secret,
		"wsPath":                  "/api/v1/ws/agent",
		"wsURL":                   wsURL,
		"installCmdController":    installCmdController,
		"installCmdGithub":        installCmdGithub,
		"controllerInstallScript": fmt.Sprintf("%s/install-agent.sh", controllerAPIBase),
		"controllerReleaseBase":   fmt.Sprintf("%s/releases", controllerAPIBase),
		"defaultInstallSource":    "controller",
	}

	// 判断是否为已有密钥（返回不同提示）
	if dbProvider.AgentSecret != "" {
		responseData["isExisting"] = true
		responseData["hint"] = fmt.Sprintf("密钥已存在（不可刷新）。在 Agent 节点运行安装命令，或手动执行: oneclickvirt-agent --ws-url %s --secret %s", wsURL, secret)
		common.ResponseSuccess(c, responseData, "Agent 密钥已存在（密钥一旦创建即写死，不支持刷新）")
	} else {
		responseData["isExisting"] = false
		responseData["hint"] = fmt.Sprintf("在 Agent 节点运行安装命令，或手动执行: oneclickvirt-agent --ws-url %s --secret %s", wsURL, secret)
		common.ResponseSuccess(c, responseData, "Agent 密钥已生成")
	}
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

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(uint(id), ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
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

	execCtx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	providerInstance, err := provider.EnsureProviderConnected(execCtx, uint(id))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider连接不可用: "+err.Error()))
		return
	}

	// 根据 provider 类型选择命令
	listCmd := "lxc list --format json 2>/dev/null"
	if dbProvider.Type == "incus" {
		listCmd = "incus list --format json 2>/dev/null"
	}

	output, err := providerInstance.ExecuteSSHCommand(execCtx, listCmd)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取容器列表失败: "+err.Error()))
		return
	}

	type containerRecord struct {
		Name            string                            `json:"name"`
		Status          string                            `json:"status"`
		Devices         map[string]map[string]interface{} `json:"devices"`
		ExpandedDevices map[string]map[string]interface{} `json:"expanded_devices"`
	}
	type containerDetail struct {
		Name         string `json:"name"`
		Status       string `json:"status"`
		HasGPU       bool   `json:"hasGpu"`
		GpuDeviceIDs string `json:"gpuDeviceIds"`
	}

	hasGPUDevices := func(devices map[string]map[string]interface{}) (bool, string) {
		if len(devices) == 0 {
			return false, ""
		}
		hasGPU := false
		ids := make([]string, 0)
		seen := make(map[string]struct{})
		for _, dev := range devices {
			devType, _ := dev["type"].(string)
			if strings.ToLower(strings.TrimSpace(devType)) != "gpu" {
				continue
			}
			hasGPU = true
			for _, key := range []string{"id", "pci", "pciid", "address"} {
				if raw, ok := dev[key]; ok {
					if s, ok := raw.(string); ok {
						s = strings.TrimSpace(s)
						if s != "" {
							if _, exists := seen[s]; !exists {
								seen[s] = struct{}{}
								ids = append(ids, s)
							}
						}
					}
				}
			}
		}
		return hasGPU, strings.Join(ids, ",")
	}

	records := make([]containerRecord, 0)
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &records); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "解析容器列表失败: "+err.Error()))
		return
	}

	stopped := make([]string, 0)
	details := make([]containerDetail, 0, len(records))
	for _, rec := range records {
		status := strings.TrimSpace(rec.Status)
		if !strings.EqualFold(status, "stopped") {
			continue
		}
		hasGPU, gpuIDs := hasGPUDevices(rec.Devices)
		if !hasGPU {
			hasGPU, gpuIDs = hasGPUDevices(rec.ExpandedDevices)
		}
		stopped = append(stopped, rec.Name)
		details = append(details, containerDetail{
			Name:         rec.Name,
			Status:       status,
			HasGPU:       hasGPU,
			GpuDeviceIDs: gpuIDs,
		})
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"containers":       stopped,
		"containerDetails": details,
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

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(uint(id), ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
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

	stdout := ""
	stderr := ""
	execFailed := false
	if dbProvider.ConnectionType == "agent" {
		if incompatible, minVersion := isAgentVersionIncompatible(dbProvider.AgentVersion); incompatible {
			msg := fmt.Sprintf("Agent版本过低或不兼容（当前: %s，最低要求: %s），请先升级Agent后再执行命令", dbProvider.AgentVersion, minVersion)
			common.ResponseWithError(c, common.NewError(common.CodeBadGateway, msg))
			return
		}

		hub := agentService.GetHub()
		exec := agentService.NewAgentShellExecutor(dbProvider.ID, hub)

		if !execFailed {
			output, execErr := exec.ExecuteWithTimeout(req.Command, time.Duration(req.Timeout)*time.Second)
			stdout = output
			if execErr != nil {
				stderr = execErr.Error()
				execFailed = true
			}
		}
	} else {
		execCtx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(req.Timeout)*time.Second)
		defer cancel()

		providerInstance, err := provider.EnsureProviderConnected(execCtx, uint(id))
		if err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "Provider连接不可用: "+err.Error()))
			return
		}

		output, execErr := providerInstance.ExecuteSSHCommand(execCtx, req.Command)
		stdout = output
		if execErr != nil {
			stderr = execErr.Error()
			execFailed = true
		}
	}

	if execFailed {
		if dbProvider.ConnectionType == "agent" {
			errLower := strings.ToLower(stderr)
			looksDisconnected := strings.Contains(errLower, "agent not connected") ||
				strings.Contains(errLower, "节点未连接") ||
				strings.Contains(errLower, "connection closed") ||
				strings.Contains(errLower, "websocket")
			if looksDisconnected {
				if err := global.APP_DB.Model(&providerModel.Provider{}).
					Where("id = ?", dbProvider.ID).
					Updates(map[string]interface{}{
						"agent_status":       "offline",
						"agent_connected_at": nil,
					}).Error; err != nil {
					global.APP_LOG.Warn("执行失败后回写 Agent 离线状态失败",
						zap.Uint("providerID", dbProvider.ID), zap.Error(err))
				}
			}
		}

		msg := "命令执行失败"
		if stderr != "" {
			msg = msg + ": " + stderr
		}
		common.ResponseWithError(c, common.NewError(common.CodeBadGateway, msg))
		return
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"stdout":     stdout,
		"stderr":     stderr,
		"command":    req.Command,
		"providerID": id,
	}, "命令执行完成")
}

func isAgentVersionIncompatible(agentVersion string) (bool, string) {
	version := strings.TrimSpace(agentVersion)
	minVersion := strings.TrimSpace(constant.CompatibleAgentVersion)
	if version == "" || minVersion == "" {
		return false, minVersion
	}
	cmp := compareVersionForCompatibility(version, minVersion)
	return cmp == -1, minVersion
}

// compareVersionForCompatibility returns:
// -1: a < b, 0: a == b, 1: a > b, -2: incomparable format.
func compareVersionForCompatibility(a, b string) int {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	if a == b {
		return 0
	}

	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	aIsSemver := len(aParts) >= 2
	bIsSemver := len(bParts) >= 2

	if aIsSemver && bIsSemver {
		maxLen := len(aParts)
		if len(bParts) > maxLen {
			maxLen = len(bParts)
		}
		for i := 0; i < maxLen; i++ {
			var aNum, bNum int
			if i < len(aParts) {
				aNum, _ = strconv.Atoi(strings.Split(aParts[i], "-")[0])
			}
			if i < len(bParts) {
				bNum, _ = strconv.Atoi(strings.Split(bParts[i], "-")[0])
			}
			if aNum < bNum {
				return -1
			}
			if aNum > bNum {
				return 1
			}
		}
		return 0
	}

	if aIsSemver != bIsSemver {
		return -2
	}

	if a < b {
		return -1
	}
	return 1
}

func firstForwardedValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, ","); idx >= 0 {
		value = value[:idx]
	}
	return strings.TrimSpace(value)
}

func normalizeForwardedProto(value string) string {
	value = strings.ToLower(firstForwardedValue(value))
	if value == "http" || value == "https" {
		return value
	}
	return ""
}

func normalizeForwardedHost(value string) string {
	value = firstForwardedValue(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "https://")
	if idx := strings.Index(value, "/"); idx >= 0 {
		value = value[:idx]
	}
	return strings.TrimSpace(value)
}

func normalizeForwardedPort(value string) string {
	value = firstForwardedValue(value)
	if value == "" {
		return ""
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return value
}
