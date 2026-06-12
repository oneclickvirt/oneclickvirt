package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	adminProvider "oneclickvirt/service/admin/provider"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

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
	host = formatControllerURLHost(host, normalizeForwardedPort(c.GetHeader("X-Forwarded-Port")), global.GetAppConfig().System.Addr)
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
	installScript := fmt.Sprintf("%s/https://raw.githubusercontent.com/oneclickvirt/oneclickvirt/main/scripts/install_agent.sh", cdnBase)
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
//	@Summary		获取节点上可用于复制模式的源容器列表
//	@Description	通过SSH或Agent连接到LXD/Incus/Docker/Podman/Containerd/Orbstack节点，返回可作为复制源的容器名称列表
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

	if !utils.SupportsContainerCopyProvider(dbProvider.Type) {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "仅 LXD/Incus/Docker/Podman/Containerd/Orbstack 类型支持容器复制"))
		return
	}

	// 根据 provider 类型选择命令
	listCmd := "lxc list --format json 2>/dev/null"
	if dbProvider.Type == "incus" {
		listCmd = "incus list --format json 2>/dev/null"
	} else if utils.IsDockerFamilyProvider(dbProvider.Type) {
		cli := "docker"
		if dbProvider.Type == "podman" {
			cli = "podman"
		} else if dbProvider.Type == "containerd" {
			cli = "nerdctl"
		}
		listCmd = fmt.Sprintf("%s ps -a --format '{{.Names}}\t{{.Status}}' 2>/dev/null", cli)
	}

	execCtx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	var output string
	if dbProvider.ConnectionType == "agent" {
		if incompatible, minVersion := isAgentVersionIncompatible(dbProvider.AgentVersion); incompatible {
			msg := fmt.Sprintf("Agent版本过低或不兼容（当前: %s，最低要求: %s），请先升级Agent后再获取容器列表", dbProvider.AgentVersion, minVersion)
			common.ResponseWithError(c, common.NewError(common.CodeBadGateway, msg))
			return
		}
		exec := agentService.NewAgentShellExecutor(dbProvider.ID, agentService.GetHub())
		output, err = exec.ExecuteWithTimeout(listCmd, 60*time.Second)
	} else {
		providerInstance, connErr := provider.EnsureProviderConnected(execCtx, uint(id))
		if connErr != nil {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider连接不可用: "+connErr.Error()))
			return
		}
		output, err = providerInstance.ExecuteSSHCommand(execCtx, listCmd)
	}
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

	stopped := make([]string, 0)
	details := make([]containerDetail, 0)
	if utils.IsDockerFamilyProvider(dbProvider.Type) {
		for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
			parts := strings.SplitN(strings.TrimSpace(line), "\t", 2)
			if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
				continue
			}
			name := strings.TrimSpace(parts[0])
			if !utils.IsValidContainerRuntimeName(name) {
				continue
			}
			status := ""
			if len(parts) > 1 {
				status = strings.TrimSpace(parts[1])
			}
			stopped = append(stopped, name)
			details = append(details, containerDetail{Name: name, Status: status})
		}
	} else {
		records := make([]containerRecord, 0)
		if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &records); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "解析容器列表失败: "+err.Error()))
			return
		}
		details = make([]containerDetail, 0, len(records))
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
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"containers":       stopped,
		"containerDetails": details,
	}, "获取源容器列表成功")
}

// ExecOnProvider godoc
//
//	@Summary		在 Provider 节点上执行命令
//	@Description	通过 SSH（SSH模式）或 Agent WebSocket（Agent模式）在节点上执行 shell 命令
//	@Tags			admin/providers
//	@Param			id		path	int		true	"Provider ID"
//	@Param			command	body	string	true	"命令（JSON: {\"command\":\"...\",\"timeout\":30}）"
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

func formatControllerURLHost(host, forwardedPort string, defaultPort int) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if parsedHost, parsedPort, err := net.SplitHostPort(host); err == nil {
		parsedHost = strings.Trim(parsedHost, "[]")
		if parsedPort == "80" || parsedPort == "443" {
			return formatHostForURL(parsedHost)
		}
		return net.JoinHostPort(parsedHost, parsedPort)
	}

	unwrappedHost := strings.Trim(host, "[]")
	port := forwardedPort
	if port == "" && defaultPort > 0 {
		port = fmt.Sprintf("%d", defaultPort)
	}
	if port == "" || port == "80" || port == "443" {
		return formatHostForURL(unwrappedHost)
	}
	return net.JoinHostPort(unwrappedHost, port)
}

func formatHostForURL(host string) string {
	host = strings.Trim(host, "[]")
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
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
