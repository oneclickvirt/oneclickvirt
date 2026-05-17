package agent

// conn.go — AgentConn 方法集。
// 包含 WebSocket 写操作、命令执行、Shell 会话管理、noise/WS-ping 循环。

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"oneclickvirt/global"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

func newAgentConn(providerID uint, conn *websocket.Conn, remoteAddr string) *AgentConn {
	return &AgentConn{
		ProviderID:    providerID,
		conn:          conn,
		remoteAddr:    remoteAddr,
		pending:       make(map[string]chan execResponsePayload),
		shellSessions: make(map[string]*AgentShellSession),
		noiseStop:     make(chan struct{}),
		wsPingStop:    make(chan struct{}),
	}
}

// NewAgentConn 导出的构造函数供 API handler 调用。
func NewAgentConn(providerID uint, conn *websocket.Conn, remoteAddr string) *AgentConn {
	return newAgentConn(providerID, conn, remoteAddr)
}

// ── 底层写操作 ──────────────────────────────────────────────────────────────

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

// ── 命令执行 ────────────────────────────────────────────────────────────────

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

// ── Shell 会话 ──────────────────────────────────────────────────────────────

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

// ── Anti-DPI Noise 循环 ────────────────────────────────────────────────────

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

// ── WebSocket 协议层 Ping 循环 ──────────────────────────────────────────────

// StartWSPingLoop periodically sends WebSocket protocol-level Ping frames
// (RFC 6455 §5.5.2).  Protocol pings are handled by the peer's WebSocket
// stack directly — no JSON parsing, no application-level dispatch — and
// the peer responds with a Pong that refreshes the read deadline via
// SetPongHandler.  This provides a transport-layer keepalive that is
// immune to application-level deadlocks in the write path.
//
// Interval: 30 s (fixed, deliberately different from the ~15 s app-level
// ping to avoid harmonic alignment).
//
// NOTE: WriteControl is concurrent-safe per gorilla/websocket docs, so we
// do NOT hold writeMu here.  Holding writeMu would serialize the protocol
// ping behind potentially slow data writes (up to 10 s timeout), defeating
// the purpose of a transport-layer keepalive.  SetWriteDeadline is also
// safe without writeMu because it only affects the next write on this
// goroutine.
func (a *AgentConn) StartWSPingLoop() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-a.wsPingStop:
				return
			case <-ticker.C:
			}
			_ = a.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			err := a.conn.WriteControl(
				websocket.PingMessage,
				[]byte{},
				time.Now().Add(5*time.Second),
			)
			if err != nil {
				global.APP_LOG.Debug("WebSocket protocol ping failed",
					zap.Uint("providerID", a.ProviderID),
					zap.Error(err))
				// Don't take action here; the app-level ping loop
				// already tracks consecutive failures and will force
				// disconnect if the connection is truly dead.
			}
		}
	}()
}

// StopWSPingLoop signals the WebSocket ping goroutine to exit.
func (a *AgentConn) StopWSPingLoop() {
	select {
	case <-a.wsPingStop:
	default:
		close(a.wsPingStop)
	}
}
