package agent

// init.go — 包初始化、Agent 鉴权、启动/关闭辅助函数与通用工具。

import (
	cryptoRand "crypto/rand"
	"encoding/base64"
	"fmt"
	mathRand "math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"
	resourcesSvc "oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func init() {
	// 注入控制端端口转发函数，解决循环依赖
	resourcesSvc.ControllerPortForwardFunc = StartControllerPortForward
	resourcesSvc.StopControllerPortForwardFunc = StopControllerPortForward
	// 注入 Agent模式执行器工厂，使 provider.LoadProvider 能为 agent 节点注入 WebSocket 执行器
	providerService.AgentExecutorFactory = func(providerID uint) utils.ShellExecutor {
		return NewAgentShellExecutor(providerID, GetHub())
	}
	providerService.AgentClientCleanupFunc = RemoveClient
}

// OnAgentConnected 是 Agent 成功连接并完成资源同步后的回调。
// 由 service/admin/provider 包在初始化时注册，用于触发延迟的实例发现与导入。
var OnAgentConnected func(providerID uint)

type AgentReconnectHook func(providerID uint)

var (
	agentReconnectHooksMu sync.RWMutex
	agentReconnectHooks   []AgentReconnectHook
)

// RegisterAgentReconnectHook registers a best-effort hook that runs after an
// agent WebSocket connection is established and the core recovery tasks have had
// a short stabilization window. Hooks must be idempotent: they can run after
// agent restart, controller restart, or duplicate reconnect events.
func RegisterAgentReconnectHook(hook AgentReconnectHook) {
	if hook == nil {
		return
	}
	agentReconnectHooksMu.Lock()
	agentReconnectHooks = append(agentReconnectHooks, hook)
	agentReconnectHooksMu.Unlock()
}

func runAgentReconnectHooks(providerID uint) {
	agentReconnectHooksMu.RLock()
	hooks := append([]AgentReconnectHook(nil), agentReconnectHooks...)
	agentReconnectHooksMu.RUnlock()

	for _, hook := range hooks {
		func(h AgentReconnectHook) {
			defer func() {
				if r := recover(); r != nil && global.APP_LOG != nil {
					global.APP_LOG.Error("Agent 重连 hook panic",
						zap.Uint("providerID", providerID),
						zap.Any("panic", r),
						zap.Stack("stack"))
				}
			}()
			h(providerID)
		}(hook)
	}
}

// hubStartupTime AgentHub 启动时间，用于计算启动后的重连宽限期
var hubStartupTime time.Time

// ── 启动 / 关闭 ──────────────────────────────────────────────────────────────

// MarkAgentProvidersOfflineOnStartup 在主控启动时将所有 agent 模式 Provider 标记为 offline。
// 同时记录启动时间，Agent 在 2 分钟宽限期内重连视为正常恢复。
// 注意：保留 agent_last_seen 不置为 nil，以便 CheckProviderHealth 中的宽限期逻辑生效。
func MarkAgentProvidersOfflineOnStartup() {
	hubStartupTime = time.Now()

	if global.APP_DB == nil {
		return
	}

	result := global.APP_DB.Model(&providerModel.Provider{}).
		Where("connection_type = ? AND agent_status = ?", "agent", "online").
		Updates(map[string]interface{}{
			"agent_status": "offline",
		})

	if result.Error != nil {
		global.APP_LOG.Warn("标记 Agent Provider 离线失败", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		global.APP_LOG.Info("主控启动：已标记 Agent Provider 为离线，等待重连",
			zap.Int64("count", result.RowsAffected))
	}
}

// IsInStartupGracePeriod 检查当前是否在主控启动后的重连宽限期内（2分钟）
func IsInStartupGracePeriod() bool {
	return time.Since(hubStartupTime) < 2*time.Minute
}

// ── Agent 鉴权 ──────────────────────────────────────────────────────────────

// GenerateAgentSecret 生成并保存一个新的 AgentSecret 给指定 Provider。
func GenerateAgentSecret(providerID uint) (string, error) {
	secret, err := secureRandomID(32)
	if err != nil {
		return "", err
	}
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", providerID).
		Update("agent_secret", secret).Error; err != nil {
		return "", err
	}
	return secret, nil
}

// LookupProviderBySecret 根据 AgentSecret 查找 Provider ID。
func LookupProviderBySecret(secret string) (uint, error) {
	if secret == "" {
		return 0, fmt.Errorf("agent_secret 为空")
	}
	var provider providerModel.Provider
	if err := global.APP_DB.Select("id").
		Where("agent_secret = ? AND connection_type = ?", secret, "agent").
		First(&provider).Error; err != nil {
		return 0, fmt.Errorf("无效的 agent_secret")
	}
	return provider.ID, nil
}

// ── 工具函数 ────────────────────────────────────────────────────────────────

// parseFirstInt 从字符串中提取第一个整数
func parseFirstInt(s string) int {
	s = strings.TrimSpace(s)
	// 提取连续数字
	var numStr strings.Builder
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			numStr.WriteRune(ch)
		} else if numStr.Len() > 0 {
			break
		}
	}
	if numStr.Len() == 0 {
		return 0
	}
	val, err := strconv.Atoi(numStr.String())
	if err != nil {
		return 0
	}
	return val
}

// parseInt64 将字符串解析为 int64
func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0
	}
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return val
}

// randomID 生成一个短随机字符串（用于请求 ID 和 secret 生成）。
func randomID() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 22)
	for i := range b {
		b[i] = chars[mathRand.Intn(len(chars))]
	}
	return string(b)
}

func secureRandomID(byteLen int) (string, error) {
	if byteLen <= 0 {
		byteLen = 32
	}
	buf := make([]byte, byteLen)
	if _, err := cryptoRand.Read(buf); err != nil {
		return "", fmt.Errorf("generate secure random id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
