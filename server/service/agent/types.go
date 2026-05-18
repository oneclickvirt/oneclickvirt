package agent

// types.go — 类型定义与消息协议常量。
// 包含 WebSocket 帧协议、健康状态、AgentConn 结构体等核心类型。

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// 仅用于兜底，真正的在线状态由 Agent 上行消息（pong/info/exec_resp 等）维护。
	// 提高到 120s，与 Agent 侧 ws_client.rs 的 120s 读超时对称，
	// 避免在 Agent 负载较高、noise 帧延迟时因读超时误判离线。
	readDeadlineWindow = 120 * time.Second
	// 限制 online 心跳落库频率，避免每次 ping 都写 DB 引发 N+1 写放大。
	heartbeatPersistInterval = 30 * time.Second
)

// ── 运行时状态与健康 ──────────────────────────────────────────────────────

type agentStatusPersistState struct {
	status      string
	lastPersist time.Time
}

type agentRuntimeState struct {
	connected   bool
	connectedAt time.Time
	lastInbound time.Time
}

type AgentRuntimeHealth struct {
	ProviderID      uint       `json:"providerId"`
	Connected       bool       `json:"connected"`
	Status          string     `json:"status"` // online / offline
	ControlLastSeen *time.Time `json:"controlLastSeen,omitempty"`
	ConnectedAt     *time.Time `json:"connectedAt,omitempty"`
}

// ── 消息协议（文本帧 JSON） ─────────────────────────────────────────────────

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
	closed   bool // 防止重复关闭 OutputCh 导致 panic
	closeMu  sync.Mutex
}

// safeClose 安全关闭会话通道，可多次调用不会 panic。
func (s *AgentShellSession) safeClose() {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	select {
	case <-s.DoneCh:
	default:
		close(s.DoneCh)
	}
	close(s.OutputCh)
}

// AgentConn — 代表一个已连接的 Agent WebSocket 连接
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
	wsPingStop    chan struct{} // 关闭时停止 WebSocket 协议层 ping
	doneCh        chan struct{} // WS 断开时关闭，通知所有等待中的 exec/shell 操作立即返回
}
