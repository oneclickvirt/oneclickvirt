package agent

// hub.go — AgentHub 管理通过 WebSocket 反向连接到控制端的节点 Agent 连接。
//
// 连接建立流程：
//  1. Rust Agent 携带 agent_secret 作为查询参数请求 GET /ws/agent
//  2. 控制端校验 secret，找到对应 Provider，HTTP Upgrade → WebSocket
//  3. Hub 注册连接，更新 Provider.AgentStatus = "online"
//  4. 控制端可通过 AgentConn.Execute() 在 Agent 上执行 shell 命令
//  5. Agent 断开后 Hub 注销连接，Provider.AgentStatus = "offline"

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"
	resourcesSvc "oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"github.com/gorilla/websocket"
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
}

// OnAgentConnected 是 Agent 成功连接并完成资源同步后的回调。
// 由 service/admin/provider 包在初始化时注册，用于触发延迟的实例发现与导入。
var OnAgentConnected func(providerID uint)

// hubStartupTime AgentHub 启动时间，用于计算启动后的重连宽限期
var hubStartupTime time.Time

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

// ──────────────────────────────────────────────────────────────────────────────
// 消息协议（文本帧 JSON）
// ──────────────────────────────────────────────────────────────────────────────

const (
	msgTypeExecRequest  = "exec_req"  // 控制端 → Agent: 执行命令
	msgTypeExecResponse = "exec_resp" // Agent → 控制端: 命令结果
	msgTypePing         = "ping"      // 控制端 → Agent: 心跳
	msgTypePong         = "pong"      // Agent → 控制端: 心跳应答
	msgTypeInfo         = "info"      // Agent → 控制端: 上报自身信息
	msgTypeShellOpen    = "shell_open"
	msgTypeShellData    = "shell_data"
	msgTypeShellResize  = "shell_resize"
	msgTypeShellClose   = "shell_close"
	msgTypeNoise        = "nop" // anti-DPI noise frame (discarded silently)
)

type wsMessage struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"` // 请求/响应对应 ID
	Payload json.RawMessage `json:"payload,omitempty"`
}

type execRequestPayload struct {
	Command string `json:"command"`
}

type execResponsePayload struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

type infoPayload struct {
	Hostname string `json:"hostname"`
	Version  string `json:"version,omitempty"`
	Secret   string `json:"secret,omitempty"` // optional second-factor validation
}

type shellOpenPayload struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type shellDataPayload struct {
	Data string `json:"data"`
}

type shellResizePayload struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type shellClosePayload struct {
	Reason string `json:"reason,omitempty"`
}

type AgentShellSession struct {
	ID       string
	OutputCh chan []byte
	DoneCh   chan struct{}
}

// ──────────────────────────────────────────────────────────────────────────────
// AgentConn — 代表一个已连接的 Agent WebSocket 连接
// ──────────────────────────────────────────────────────────────────────────────

type AgentConn struct {
	ProviderID uint
	conn       *websocket.Conn
	remoteAddr string
	hostname   string

	mu            sync.Mutex
	writeMu       sync.Mutex
	pending       map[string]chan execResponsePayload // reqID → response channel
	shellSessions map[string]*AgentShellSession
	pingFailCount int           // 连续 ping 失败计数（用于检测连接僵死）
	noiseStop     chan struct{} // 关闭时停止 noise 帧发送
}

func newAgentConn(providerID uint, conn *websocket.Conn, remoteAddr string) *AgentConn {
	return &AgentConn{
		ProviderID:    providerID,
		conn:          conn,
		remoteAddr:    remoteAddr,
		pending:       make(map[string]chan execResponsePayload),
		shellSessions: make(map[string]*AgentShellSession),
		noiseStop:     make(chan struct{}),
	}
}

// NewAgentConn 导出的构造函数供 API handler 调用。
func NewAgentConn(providerID uint, conn *websocket.Conn, remoteAddr string) *AgentConn {
	return newAgentConn(providerID, conn, remoteAddr)
}

func (a *AgentConn) writeTextMessage(payload []byte, timeout time.Duration) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	a.conn.SetWriteDeadline(time.Now().Add(timeout))
	return a.conn.WriteMessage(websocket.TextMessage, payload)
}

func (a *AgentConn) writeBinaryMessage(payload []byte, timeout time.Duration) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	a.conn.SetWriteDeadline(time.Now().Add(timeout))
	return a.conn.WriteMessage(websocket.BinaryMessage, payload)
}

// Execute 通过 WebSocket 在远端 Agent 上执行命令，返回 (stdout, error)。
// 超时默认 30 秒。
func (a *AgentConn) Execute(cmd string) (string, error) {
	return a.ExecuteWithTimeout(cmd, 30*time.Second)
}

// ExecuteWithTimeout 带自定义超时的命令执行。
func (a *AgentConn) ExecuteWithTimeout(cmd string, timeout time.Duration) (string, error) {
	reqID := randomID()

	respCh := make(chan execResponsePayload, 1)
	a.mu.Lock()
	a.pending[reqID] = respCh
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, reqID)
		a.mu.Unlock()
	}()

	payload, _ := json.Marshal(execRequestPayload{Command: cmd})
	msg := wsMessage{Type: msgTypeExecRequest, ID: reqID, Payload: payload}
	raw, _ := json.Marshal(msg)

	if err := a.writeTextMessage(raw, 10*time.Second); err != nil {
		return "", fmt.Errorf("写入 WebSocket 失败: %w", err)
	}

	select {
	case resp := <-respCh:
		combined := resp.Stdout
		if resp.Stderr != "" {
			if combined != "" && !strings.HasSuffix(combined, "\n") {
				combined += "\n"
			}
			combined += resp.Stderr
		}
		if resp.Error != "" {
			return combined, fmt.Errorf("agent 执行错误: %s", resp.Error)
		}
		if resp.ExitCode != 0 {
			return combined, fmt.Errorf("agent command execution failed: exit code %d", resp.ExitCode)
		}
		return combined, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("执行命令超时（%s）", timeout)
	}
}

func (a *AgentConn) StartShell(cols, rows int) (*AgentShellSession, error) {
	sessionID := randomID()
	session := &AgentShellSession{
		ID:       sessionID,
		OutputCh: make(chan []byte, 128),
		DoneCh:   make(chan struct{}),
	}
	payload, _ := json.Marshal(shellOpenPayload{Cols: cols, Rows: rows})
	msg := wsMessage{Type: msgTypeShellOpen, ID: sessionID, Payload: payload}
	raw, _ := json.Marshal(msg)

	a.mu.Lock()
	a.shellSessions[sessionID] = session
	a.mu.Unlock()

	if err := a.writeTextMessage(raw, 10*time.Second); err != nil {
		a.mu.Lock()
		delete(a.shellSessions, sessionID)
		a.mu.Unlock()
		return nil, fmt.Errorf("启动 agent shell 失败: %w", err)
	}

	return session, nil
}

func (a *AgentConn) WriteShellInput(sessionID string, data []byte) error {
	payload, _ := json.Marshal(shellDataPayload{Data: string(data)})
	msg := wsMessage{Type: msgTypeShellData, ID: sessionID, Payload: payload}
	raw, _ := json.Marshal(msg)
	if err := a.writeTextMessage(raw, 10*time.Second); err != nil {
		return fmt.Errorf("发送 shell 输入失败: %w", err)
	}
	return nil
}

func (a *AgentConn) ResizeShell(sessionID string, cols, rows int) error {
	payload, _ := json.Marshal(shellResizePayload{Cols: cols, Rows: rows})
	msg := wsMessage{Type: msgTypeShellResize, ID: sessionID, Payload: payload}
	raw, _ := json.Marshal(msg)
	if err := a.writeTextMessage(raw, 10*time.Second); err != nil {
		return fmt.Errorf("发送 shell resize 失败: %w", err)
	}
	return nil
}

func (a *AgentConn) CloseShell(sessionID string) error {
	payload, _ := json.Marshal(shellClosePayload{})
	msg := wsMessage{Type: msgTypeShellClose, ID: sessionID, Payload: payload}
	raw, _ := json.Marshal(msg)
	err := a.writeTextMessage(raw, 10*time.Second)
	a.mu.Lock()
	if session, ok := a.shellSessions[sessionID]; ok {
		delete(a.shellSessions, sessionID)
		select {
		case <-session.DoneCh:
		default:
			close(session.DoneCh)
		}
		close(session.OutputCh)
	}
	a.mu.Unlock()
	if err != nil {
		return fmt.Errorf("关闭 shell 会话失败: %w", err)
	}
	return nil
}

// StartNoiseLoop periodically sends random-length noise frames (type "nop")
// to break DPI traffic-analysis signatures (message-size distribution,
// bidirectional symmetry, always-on silence patterns).
// Noise interval: 5-25s random, payload: 0-512 random bytes hex-encoded.
//
// IMPORTANT: The payload must be a valid JSON value so that the Rust agent's
// serde_json parser does not choke on the frame.  Raw bytes cannot be embedded
// directly via json.RawMessage because MarshalJSON returns them verbatim
// (no escaping), producing invalid JSON.  We hex-encode noise bytes into a
// {"h":"<hex>"} wrapper, matching the agent's own noise frame format.
func (a *AgentConn) StartNoiseLoop() {
	go func() {
		for {
			select {
			case <-a.noiseStop:
				return
			default:
			}
			// Random sleep 5-25 s
			delay := time.Duration(5+rand.Intn(21)) * time.Second
			select {
			case <-a.noiseStop:
				return
			case <-time.After(delay):
			}

			noiseLen := rand.Intn(513) // 0-512 random bytes
			var payload json.RawMessage
			if noiseLen > 0 {
				noise := make([]byte, noiseLen)
				rand.Read(noise)
				// Encode as hex string inside a JSON object, matching the
				// Rust agent's nop frame format: {"h":"<hex>"}.
				hexStr := fmt.Sprintf("%x", noise)
				payload, _ = json.Marshal(map[string]string{"h": hexStr})
			}
			// When noiseLen == 0, payload stays nil and is omitted via
			// omitempty, producing the valid frame {"type":"nop"}.
			msg, err := json.Marshal(wsMessage{
				Type:    msgTypeNoise,
				Payload: payload,
			})
			if err != nil || len(msg) == 0 {
				// Should never happen, but if it does, skip this cycle
				// instead of sending an empty frame that triggers
				// "EOF while parsing a value" on the agent.
				continue
			}
			// Best-effort, ignore errors (connection may be closing)
			_ = a.writeTextMessage(msg, 3*time.Second)
		}
	}()
}

// StopNoiseLoop signals the noise goroutine to exit.
func (a *AgentConn) StopNoiseLoop() {
	select {
	case <-a.noiseStop:
	default:
		close(a.noiseStop)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// AgentHub — 全局单例，管理所有 Agent 连接
// ──────────────────────────────────────────────────────────────────────────────

type AgentHub struct {
	mu    sync.RWMutex
	conns map[uint]*AgentConn // providerID → AgentConn
}

var (
	globalHub     *AgentHub
	globalHubOnce sync.Once
)

// GetHub 返回全局 AgentHub 单例。
func GetHub() *AgentHub {
	globalHubOnce.Do(func() {
		globalHub = &AgentHub{
			conns: make(map[uint]*AgentConn),
		}
	})
	return globalHub
}

// Register 注册一个新连接并启动读取协程。
func (h *AgentHub) Register(ac *AgentConn) {
	h.mu.Lock()
	// 如果已有旧连接，关闭它
	if old, ok := h.conns[ac.ProviderID]; ok {
		old.conn.Close()
	}
	h.conns[ac.ProviderID] = ac
	h.mu.Unlock()

	// 清理旧的 TunnelManager，确保后续端口转发使用新的 AgentConn
	RemoveTunnelManager(ac.ProviderID)

	global.APP_LOG.Info("Agent 已连接",
		zap.Uint("providerID", ac.ProviderID),
		zap.String("remoteAddr", ac.remoteAddr))

	// 记录重连信息：Provider 离线了多久，是否在宽限期内
	var provider providerModel.Provider
	if err := global.APP_DB.Select("name, agent_connected_at, agent_last_seen").
		Where("id = ?", ac.ProviderID).First(&provider).Error; err == nil {
		prevConnected := provider.AgentConnectedAt
		prevLastSeen := provider.AgentLastSeen
		if prevLastSeen != nil {
			offlineDuration := time.Since(*prevLastSeen)
			if offlineDuration > 30*time.Second {
				global.APP_LOG.Info("Agent 重连成功（曾离线较长时间）",
					zap.Uint("providerID", ac.ProviderID),
					zap.String("name", provider.Name),
					zap.Duration("offlineDuration", offlineDuration))
			}
		}
		_ = prevConnected
	}

	// 同步更新数据库状态，确保前端检测立即可见
	now := time.Now()
	h.updateProviderAgentStatus(ac.ProviderID, "online", &now, ac.remoteAddr, "")

	// Agent 重连成功后，如果 Provider 因健康检查连续失败被自动冻结，则自动解冻。
	// Agent 反向连接成功即证明节点可达，无需等待健康检查周期。
	// 同时确认因主控重启导致的 status 不一致（agent_status=online 但 status=inactive）。
	h.recoverProviderOnReconnect(ac.ProviderID, now)

	// 记录本次连接建立时间（用于前端显示在线时长）
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", ac.ProviderID).
		Update("agent_connected_at", now).Error; err != nil {
		global.APP_LOG.Warn("更新 Agent 连接时间失败", zap.Uint("providerID", ac.ProviderID), zap.Error(err))
	}

	// 异步同步节点硬件资源（CPU/内存/磁盘），解决 Agent 模式节点无 SSH 健康检查的资源同步问题
	go h.syncNodeResources(ac)

	// 启动读取循环（异步，包含后续心跳和 info 帧处理）
	go h.readLoop(ac)

	// 启动 anti-DPI 噪声帧发送（随机间隔 + 随机长度，打破流量指纹）
	ac.StartNoiseLoop()

	// Agent 重连后恢复该 Provider 的控制端端口转发（内网穿透）
	// 延迟执行，确保 WebSocket 连接已稳定
	go func() {
		time.Sleep(3 * time.Second)
		RecoverControllerPortForwardsByProvider(ac.ProviderID)
	}()

	// 触发延迟实例发现与导入（Agent 模式节点在创建时标记了 PendingDiscovery）
	go h.triggerPendingDiscovery(ac.ProviderID)

	// 异步确保 Provider 在 ProviderService 中已加载（解决主控重启后 Provider 内存缓存为空，
	// Agent 重连后 ProviderService.providers 中仍然缺失该 Provider，导致后续操作
	// 如 GetStoppedContainers/ExecuteSSHCommand 等需要先通过 EnsureProviderConnected
	// 重新加载的问题）。此处延迟 2 秒确保 Agent WebSocket 连接已稳定。
	go func() {
		time.Sleep(2 * time.Second)
		if _, err := providerService.GetProviderInstanceByID(ac.ProviderID); err != nil {
			global.APP_LOG.Warn("Agent 重连后加载 Provider 到内存缓存失败",
				zap.Uint("providerID", ac.ProviderID),
				zap.Error(err))
		} else {
			global.APP_LOG.Debug("Agent 重连后 Provider 内存缓存已同步",
				zap.Uint("providerID", ac.ProviderID))
		}
	}()
}

// triggerPendingDiscovery 检查 Provider 是否有待处理的实例发现任务，如有则触发。
func (h *AgentHub) triggerPendingDiscovery(providerID uint) {
	// 等待资源同步和 WebSocket 连接稳定
	time.Sleep(5 * time.Second)

	// 检查是否有待处理的发现任务
	var provider providerModel.Provider
	if err := global.APP_DB.Select("pending_discovery, discovery_owner_user_id, discovery_auto_adjust").
		Where("id = ?", providerID).First(&provider).Error; err != nil {
		global.APP_LOG.Warn("triggerPendingDiscovery: 查询 Provider 失败",
			zap.Uint("providerID", providerID), zap.Error(err))
		return
	}

	if !provider.PendingDiscovery {
		return
	}

	// 清除 PendingDiscovery 标记（无论成功与否，避免重复触发）
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", providerID).
		Update("pending_discovery", false).Error; err != nil {
		global.APP_LOG.Warn("triggerPendingDiscovery: 清除 PendingDiscovery 标记失败",
			zap.Uint("providerID", providerID), zap.Error(err))
	}

	global.APP_LOG.Info("Agent 连接后触发延迟实例发现",
		zap.Uint("providerID", providerID),
		zap.Uint("ownerUserID", provider.DiscoveryOwnerUserID),
		zap.Bool("autoAdjust", provider.DiscoveryAutoAdjust))

	// 调用注册的回调执行实例发现与导入
	if OnAgentConnected != nil {
		OnAgentConnected(providerID)
	}
}

// GetConn 返回指定 Provider 的 AgentConn（如果在线）。
func (h *AgentHub) GetConn(providerID uint) (*AgentConn, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ac, ok := h.conns[providerID]
	return ac, ok
}

// unregister 注销连接（同步更新 DB 状态以确保前端立即可见）。
func (h *AgentHub) unregister(providerID uint) {
	h.mu.Lock()
	if ac, ok := h.conns[providerID]; ok {
		ac.StopNoiseLoop()
	}
	delete(h.conns, providerID)
	h.mu.Unlock()

	// 清理该 Provider 的 TunnelManager，释放资源
	RemoveTunnelManager(providerID)

	// 停止该 Provider 的所有控制端端口转发监听器
	// Agent 断开后这些监听器无法转发流量，应释放端口资源
	StopControllerPortForwardsByProvider(providerID)

	global.APP_LOG.Info("Agent 已断开", zap.Uint("providerID", providerID))
	h.updateProviderAgentStatus(providerID, "offline", nil, "", "")
	// 清除连接建立时间（离线后无在线时长）
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", providerID).
		Update("agent_connected_at", nil).Error; err != nil {
		global.APP_LOG.Warn("清除 Agent 连接时间失败", zap.Uint("providerID", providerID), zap.Error(err))
	}
}

// readLoop 持续读取来自 Agent 的消息。
func (h *AgentHub) readLoop(ac *AgentConn) {
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Error("Agent readLoop panic",
				zap.Uint("providerID", ac.ProviderID),
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		ac.conn.Close()
		h.unregister(ac.ProviderID)
	}()

	ac.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	ac.conn.SetPongHandler(func(string) error {
		ac.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	for {
		msgType, data, err := ac.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				global.APP_LOG.Warn("Agent WebSocket 读取错误",
					zap.Uint("providerID", ac.ProviderID), zap.Error(err))
			}
			return
		}

		// 二进制帧：隧道数据 [8-byte connID hash][payload]
		if msgType == websocket.BinaryMessage {
			if len(data) <= 8 {
				continue
			}
			connHash := binary.BigEndian.Uint64(data[:8])
			payload := data[8:]
			tunnelMgrMu.RLock()
			mgr, hasMgr := tunnelMgrs[ac.ProviderID]
			tunnelMgrMu.RUnlock()
			if hasMgr {
				mgr.DeliverByHash(connHash, payload)
			}
			continue
		}

		var msg wsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			global.APP_LOG.Warn("Agent 消息解析失败", zap.Error(err))
			continue
		}

		switch msg.Type {
		case msgTypeExecResponse:
			var resp execResponsePayload
			if err := json.Unmarshal(msg.Payload, &resp); err == nil {
				ac.mu.Lock()
				if ch, ok := ac.pending[msg.ID]; ok {
					ch <- resp
				}
				ac.mu.Unlock()
			}

		case msgTypePong:
			ac.conn.SetReadDeadline(time.Now().Add(90 * time.Second))

		case msgTypeInfo:
			var info infoPayload
			if err := json.Unmarshal(msg.Payload, &info); err == nil && info.Hostname != "" {
				// Optional second-factor: validate secret in info frame.
				// If the agent sent a secret and it doesn't match the
				// registered provider secret, reject the connection.
				if info.Secret != "" {
					var p providerModel.Provider
					if err := global.APP_DB.Select("agent_secret").
						Where("id = ?", ac.ProviderID).First(&p).Error; err == nil {
						if p.AgentSecret != "" && info.Secret != p.AgentSecret {
							global.APP_LOG.Warn("Agent info 帧 secret 验证失败，断开连接",
								zap.Uint("providerID", ac.ProviderID))
							ac.conn.Close()
							return
						}
					}
				}
				ac.mu.Lock()
				ac.hostname = info.Hostname
				ac.mu.Unlock()
				now := time.Now()
				go h.updateProviderAgentStatusWithVersion(ac.ProviderID, "online", &now, ac.remoteAddr, info.Hostname, info.Version)
			}

		case msgTypeTunnelAck:
			var ack tunnelAckPayload
			if err := json.Unmarshal(msg.Payload, &ack); err == nil {
				tunnelMgrMu.RLock()
				mgr, hasMgr := tunnelMgrs[ac.ProviderID]
				tunnelMgrMu.RUnlock()
				if hasMgr {
					mgr.DeliverAck(ack)
				}
			}

		case msgTypeTunnelClose:
			var cl tunnelClosePayload
			if err := json.Unmarshal(msg.Payload, &cl); err == nil {
				tunnelMgrMu.RLock()
				mgr, hasMgr := tunnelMgrs[ac.ProviderID]
				tunnelMgrMu.RUnlock()
				if hasMgr {
					mgr.CloseSession(cl.ConnID)
				}
			}

		case msgTypeShellData:
			var payload shellDataPayload
			if err := json.Unmarshal(msg.Payload, &payload); err == nil {
				ac.mu.Lock()
				if session, ok := ac.shellSessions[msg.ID]; ok {
					select {
					case session.OutputCh <- []byte(payload.Data):
					default:
					}
				}
				ac.mu.Unlock()
			}

		case msgTypeShellClose:
			ac.mu.Lock()
			if session, ok := ac.shellSessions[msg.ID]; ok {
				delete(ac.shellSessions, msg.ID)
				select {
				case <-session.DoneCh:
				default:
					close(session.DoneCh)
				}
				close(session.OutputCh)
			}
			ac.mu.Unlock()

		case msgTypeNoise:
			// Anti-DPI noise frame — silently discarded.
			// Contains random-length payload to vary message sizes.
		}
	}
}

// StartPingLoop 定期向所有在线 Agent 发送 ping 帧并更新 AgentLastSeen。
// 检测连续 ping 失败次数，超过阈值（3次=约90s无响应）强制标记离线。
//
// 行为伪装（anti-DPI）：
//   - ping 间隔使用 ±35% 随机抖动（19.5-40.5s，基准30s），打破固定周期特征
//   - ping 帧附带随机长度的填充字段（noise），模拟 HTTP/2 PING 或浏览器 keepalive
func (h *AgentHub) StartPingLoop() {
	baseInterval := 30 * time.Second
	go func() {
		for {
			// Jitter: base ±35%, avoid exact multiples that DPI can fingerprint
			jitterRange := float64(baseInterval) * 0.35
			jitter := time.Duration(rand.Float64()*jitterRange*2 - jitterRange)
			interval := baseInterval + jitter
			if interval < 10*time.Second {
				interval = 10 * time.Second
			}
			time.Sleep(interval)

			h.mu.RLock()
			conns := make([]*AgentConn, 0, len(h.conns))
			for _, ac := range h.conns {
				conns = append(conns, ac)
			}
			h.mu.RUnlock()

			now := time.Now()
			for _, ac := range conns {
				// Add random noise payload to ping frame (0-64 bytes of base64 noise)
				noiseLen := rand.Intn(65)
				noise := make([]byte, noiseLen)
				if noiseLen > 0 {
					rand.Read(noise)
				}
				pingFrame := map[string]interface{}{
					"type":  msgTypePing,
					"noise": noise,
				}
				pingMsg, _ := json.Marshal(pingFrame)

				if err := ac.writeTextMessage(pingMsg, 5*time.Second); err != nil {
					// 写入失败：累计失败次数
					ac.mu.Lock()
					ac.pingFailCount++
					failCount := ac.pingFailCount
					ac.mu.Unlock()

					global.APP_LOG.Warn("Agent ping 写入失败",
						zap.Uint("providerID", ac.ProviderID),
						zap.Int("consecutiveFailures", failCount),
						zap.Error(err))

					// 连续 3 次（90s）写入失败 → 强制标记离线
					if failCount >= 3 {
						global.APP_LOG.Error("Agent 连续 ping 失败超过阈值，强制断开",
							zap.Uint("providerID", ac.ProviderID),
							zap.Int("consecutiveFailures", failCount))
						ac.conn.Close()
						h.unregister(ac.ProviderID)
					}
					continue
				}
				// 写入成功 → 重置失败计数
				ac.mu.Lock()
				ac.pingFailCount = 0
				ac.mu.Unlock()

				go h.updateProviderAgentStatus(ac.ProviderID, "online", &now, ac.remoteAddr, "")
			}
		}
	}()
}

// GenerateAgentSecret 生成并保存一个新的 AgentSecret 给指定 Provider。
func GenerateAgentSecret(providerID uint) (string, error) {
	secret := fmt.Sprintf("%s-%s", randomID(), randomID()) // ~44 字符随机串
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

// ──────────────────────────────────────────────────────────────────────────────
// 内部辅助函数
// ──────────────────────────────────────────────────────────────────────────────

// recoverProviderOnReconnect 在 Agent 重连成功后恢复 Provider 状态。
// 处理两种边界条件：
//  1. Provider 因健康检查连续失败被自动冻结 → 自动解冻
//  2. 主控重启后 agent_status 与 status 不一致（agent_status=online 但 status=inactive）→ 确认 status
func (h *AgentHub) recoverProviderOnReconnect(providerID uint, now time.Time) {
	var p providerModel.Provider
	if err := global.APP_DB.Select("id, is_frozen, frozen_reason, status, connection_type").
		Where("id = ?", providerID).First(&p).Error; err != nil {
		return
	}

	needsUpdate := false
	updates := map[string]interface{}{}

	// 如果 Provider 因健康检查连续失败被自动冻结，Agent 重连成功即证明节点可达，自动解冻
	if p.IsFrozen && p.FrozenReason != "" &&
		(p.FrozenReason == "expired" || strings.Contains(p.FrozenReason, "健康检查连续失败") || strings.Contains(p.FrozenReason, "Agent 反向连接连续断开")) {
		// expired 类型的冻结通过 Agent 重连无法解冻（需管理员手动设置新的过期时间）
		// 仅对健康检查自动冻结（含 Agent 断连自动冻结）进行解冻
		if strings.Contains(p.FrozenReason, "健康检查连续失败") || strings.Contains(p.FrozenReason, "Agent 反向连接连续断开") {
			updates["is_frozen"] = false
			updates["frozen_at"] = nil
			updates["frozen_reason"] = ""
			needsUpdate = true
			global.APP_LOG.Info("Agent 重连后自动解冻 Provider（此前因健康检查失败被冻结）",
				zap.Uint("providerID", providerID),
				zap.String("frozen_reason", p.FrozenReason))
		}
	}

	// Agent 重连后，若 general status 为 inactive，确认为 active
	// 避免主控重启后 agent_status=online 但 status=inactive 的不一致状态
	if p.Status == "inactive" && p.ConnectionType == "agent" {
		updates["status"] = "active"
		needsUpdate = true
		global.APP_LOG.Debug("Agent 重连后确认 Provider 状态不一致",
			zap.Uint("providerID", providerID),
			zap.String("old_status", p.Status))
	}

	if needsUpdate {
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("id = ?", providerID).
			Updates(updates).Error; err != nil {
			global.APP_LOG.Warn("Agent 重连后恢复 Provider 状态失败",
				zap.Uint("providerID", providerID), zap.Error(err))
		}
	}
}

func (h *AgentHub) updateProviderAgentStatus(providerID uint, status string, lastSeen *time.Time, remoteAddr string, hostname string) {
	h.updateProviderAgentStatusWithVersion(providerID, status, lastSeen, remoteAddr, hostname, "")
}

func (h *AgentHub) updateProviderAgentStatusWithVersion(providerID uint, status string, lastSeen *time.Time, remoteAddr string, hostname string, version string) {
	updates := map[string]interface{}{
		"agent_status": status,
	}
	remoteIP := ""
	if lastSeen != nil {
		updates["agent_last_seen"] = lastSeen
	}
	if remoteAddr != "" {
		// 只保存 IP 部分
		if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
			updates["agent_remote_ip"] = host
			remoteIP = host
		} else {
			updates["agent_remote_ip"] = remoteAddr
			remoteIP = remoteAddr
		}
	}
	if hostname != "" {
		updates["agent_hostname"] = hostname
	}
	if version != "" {
		updates["agent_version"] = version
	}
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", providerID).
		Updates(updates).Error; err != nil {
		global.APP_LOG.Warn("更新 Agent 状态失败", zap.Uint("providerID", providerID), zap.Error(err))
	}

	if status == "online" && remoteIP != "" {
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("id = ? AND connection_type = ? AND (endpoint IS NULL OR endpoint = '')", providerID, "agent").
			Update("endpoint", remoteIP).Error; err != nil {
			global.APP_LOG.Warn("回填 Agent 节点 endpoint 失败", zap.Uint("providerID", providerID), zap.Error(err))
		}
	}
}

// syncNodeResources 通过 Agent WebSocket 获取节点硬件资源（CPU/内存/磁盘），
// 更新 Provider 的 NodeCPUCores / NodeMemoryTotal / NodeDiskTotal。
// Agent 模式节点不依赖 SSH 健康检查，资源通过此函数在 Agent 上线时同步。
func (h *AgentHub) syncNodeResources(ac *AgentConn) {
	// 稍等片刻确保 WebSocket 连接稳定
	time.Sleep(2 * time.Second)

	providerID := ac.ProviderID

	// 检查 Provider 是否已有资源数据，已有则跳过（避免覆盖手动设置的值）
	var provider providerModel.Provider
	if err := global.APP_DB.Select("node_cpu_cores, node_memory_total, node_disk_total, resource_synced").
		Where("id = ?", providerID).First(&provider).Error; err != nil {
		global.APP_LOG.Warn("syncNodeResources: 查询 Provider 失败", zap.Uint("providerID", providerID), zap.Error(err))
		return
	}
	if provider.ResourceSynced && provider.NodeCPUCores > 0 && provider.NodeMemoryTotal > 0 && provider.NodeDiskTotal > 0 {
		return // 已同步过，无需重复
	}

	// 获取 CPU 核心数
	cpuOutput, cpuErr := ac.ExecuteWithTimeout("nproc 2>/dev/null || echo 0", 10*time.Second)
	cpuCores := 0
	if cpuErr == nil {
		cpuCores = parseFirstInt(strings.TrimSpace(cpuOutput))
	}

	// 获取总内存（字节）
	memOutput, memErr := ac.ExecuteWithTimeout("free -b 2>/dev/null | awk '/^Mem:/{print $2}' || echo 0", 10*time.Second)
	memTotal := int64(0)
	if memErr == nil {
		memTotal = parseInt64(strings.TrimSpace(memOutput))
	}

	// 获取根分区总磁盘（字节）
	diskOutput, diskErr := ac.ExecuteWithTimeout("df -B1 / 2>/dev/null | awk 'NR==2{print $2}' || echo 0", 10*time.Second)
	diskTotal := int64(0)
	if diskErr == nil {
		diskTotal = parseInt64(strings.TrimSpace(diskOutput))
	}

	if cpuCores == 0 && memTotal == 0 && diskTotal == 0 {
		global.APP_LOG.Warn("syncNodeResources: 未能获取节点资源信息",
			zap.Uint("providerID", providerID))
		return
	}

	// 转换为 MB（内存和磁盘原始值为字节）
	memMB := memTotal / (1024 * 1024)
	diskMB := diskTotal / (1024 * 1024)

	now := time.Now()
	updates := map[string]interface{}{
		"resource_synced":    true,
		"resource_synced_at": &now,
	}
	if cpuCores > 0 {
		updates["node_cpu_cores"] = cpuCores
	}
	if memMB > 0 {
		updates["node_memory_total"] = memMB
	}
	if diskMB > 0 {
		updates["node_disk_total"] = diskMB
	}

	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", providerID).
		Updates(updates).Error; err != nil {
		global.APP_LOG.Warn("syncNodeResources: 更新 Provider 资源失败",
			zap.Uint("providerID", providerID), zap.Error(err))
		return
	}

	global.APP_LOG.Info("Agent 节点资源同步完成",
		zap.Uint("providerID", providerID),
		zap.Int("cpuCores", cpuCores),
		zap.Int64("memoryMB", memMB),
		zap.Int64("diskMB", diskMB))
}

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
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}
