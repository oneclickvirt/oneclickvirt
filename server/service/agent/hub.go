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
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	resourcesSvc "oneclickvirt/service/resources"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

func init() {
	// 注入控制端端口转发函数，解决循环依赖
	resourcesSvc.ControllerPortForwardFunc = StartControllerPortForward
	resourcesSvc.StopControllerPortForwardFunc = StopControllerPortForward
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
}

// ──────────────────────────────────────────────────────────────────────────────
// AgentConn — 代表一个已连接的 Agent WebSocket 连接
// ──────────────────────────────────────────────────────────────────────────────

type AgentConn struct {
	ProviderID uint
	conn       *websocket.Conn
	remoteAddr string
	hostname   string

	mu      sync.Mutex
	pending map[string]chan execResponsePayload // reqID → response channel
}

func newAgentConn(providerID uint, conn *websocket.Conn, remoteAddr string) *AgentConn {
	return &AgentConn{
		ProviderID: providerID,
		conn:       conn,
		remoteAddr: remoteAddr,
		pending:    make(map[string]chan execResponsePayload),
	}
}

// NewAgentConn 导出的构造函数供 API handler 调用。
func NewAgentConn(providerID uint, conn *websocket.Conn, remoteAddr string) *AgentConn {
	return newAgentConn(providerID, conn, remoteAddr)
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

	a.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := a.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		return "", fmt.Errorf("写入 WebSocket 失败: %w", err)
	}

	select {
	case resp := <-respCh:
		if resp.Error != "" {
			return resp.Stdout, fmt.Errorf("agent 执行错误: %s", resp.Error)
		}
		return resp.Stdout, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("执行命令超时（%s）", timeout)
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

	global.APP_LOG.Info("Agent 已连接",
		zap.Uint("providerID", ac.ProviderID),
		zap.String("remoteAddr", ac.remoteAddr))

	// 异步更新数据库状态
	now := time.Now()
	go h.updateProviderAgentStatus(ac.ProviderID, "online", &now, ac.remoteAddr, "")

	// 启动读取循环
	go h.readLoop(ac)
}

// GetConn 返回指定 Provider 的 AgentConn（如果在线）。
func (h *AgentHub) GetConn(providerID uint) (*AgentConn, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ac, ok := h.conns[providerID]
	return ac, ok
}

// unregister 注销连接。
func (h *AgentHub) unregister(providerID uint) {
	h.mu.Lock()
	delete(h.conns, providerID)
	h.mu.Unlock()

	global.APP_LOG.Info("Agent 已断开", zap.Uint("providerID", providerID))
	go h.updateProviderAgentStatus(providerID, "offline", nil, "", "")
}

// readLoop 持续读取来自 Agent 的消息。
func (h *AgentHub) readLoop(ac *AgentConn) {
	defer func() {
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
		}
	}
}

// StartPingLoop 定期向所有在线 Agent 发送 ping 帧并更新 AgentLastSeen。
func (h *AgentHub) StartPingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for range ticker.C {
			h.mu.RLock()
			conns := make([]*AgentConn, 0, len(h.conns))
			for _, ac := range h.conns {
				conns = append(conns, ac)
			}
			h.mu.RUnlock()

			now := time.Now()
			for _, ac := range conns {
				pingMsg, _ := json.Marshal(wsMessage{Type: msgTypePing})
				ac.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := ac.conn.WriteMessage(websocket.TextMessage, pingMsg); err != nil {
					continue
				}
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

func (h *AgentHub) updateProviderAgentStatus(providerID uint, status string, lastSeen *time.Time, remoteAddr string, hostname string) {
	h.updateProviderAgentStatusWithVersion(providerID, status, lastSeen, remoteAddr, hostname, "")
}

func (h *AgentHub) updateProviderAgentStatusWithVersion(providerID uint, status string, lastSeen *time.Time, remoteAddr string, hostname string, version string) {
	updates := map[string]interface{}{
		"agent_status": status,
	}
	if lastSeen != nil {
		updates["agent_last_seen"] = lastSeen
	}
	if remoteAddr != "" {
		// 只保存 IP 部分
		if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
			updates["agent_remote_i_p"] = host
		} else {
			updates["agent_remote_i_p"] = remoteAddr
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
