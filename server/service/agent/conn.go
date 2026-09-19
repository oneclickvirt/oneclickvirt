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
	"oneclickvirt/utils"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

func newAgentConn(providerID uint, conn *websocket.Conn, remoteAddr string) *AgentConn {
	return &AgentConn{
		ProviderID:    providerID,
		conn:          conn,
		remoteAddr:    remoteAddr,
		pending:       make(map[string]chan execResponsePayload),
		apiPending:    make(map[string]chan apiResponsePayload),
		fmPending:     make(map[string]chan fmRawResp),
		shellSessions: make(map[string]*AgentShellSession),
		noiseStop:     make(chan struct{}),
		wsPingStop:    make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// closeAllSessions 关闭所有挂起的 exec 请求和 shell 会话，在 WS 断开时调用。
// 通过关闭 doneCh 使 ExecuteWithTimeout 立即返回错误；
// 通过 safeClose() 关闭 shell 会话使 handleAgentShellTerminal 立即退出，
// 避免等待 30 分钟的上下文超时。
func (a *AgentConn) closeAllSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 关闭 doneCh（通知所有 ExecuteWithTimeout 立即返回）
	select {
	case <-a.doneCh:
		// 已关闭，跳过
	default:
		close(a.doneCh)
	}

	// 关闭所有 shell 会话（通知 handleAgentShellTerminal 退出）
	for id, session := range a.shellSessions {
		session.safeClose()
		delete(a.shellSessions, id)
	}

	// 清理 pending exec 请求（避免内存泄漏；ExecuteWithTimeout 通过 doneCh 已被通知）
	for id := range a.pending {
		delete(a.pending, id)
	}
	for id := range a.apiPending {
		delete(a.apiPending, id)
	}

	// 清理 pending fm 请求
	for id, ch := range a.fmPending {
		select {
		case ch <- fmRawResp{MsgType: msgTypeFMError, Payload: []byte(`{"message":"agent disconnected"}`)}:
		default:
		}
		delete(a.fmPending, id)
	}
}

// CallAPI sends an allow-listed Agent API request without involving a shell.
// In particular, WireGuard key material remains inside the authenticated
// WebSocket frame and cannot appear in ps output or command-execution logs.
func (a *AgentConn) CallAPI(method, path string, body interface{}, result interface{}, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	var rawBody json.RawMessage
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal typed Agent API request: %w", err)
		}
		rawBody = encoded
	}

	reqID := randomID()
	respCh := make(chan apiResponsePayload, 1)
	a.mu.Lock()
	a.apiPending[reqID] = respCh
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.apiPending, reqID)
		a.mu.Unlock()
	}()

	payload, err := json.Marshal(apiRequestPayload{Method: method, Path: path, Body: rawBody})
	if err != nil {
		return fmt.Errorf("marshal typed Agent API envelope: %w", err)
	}
	frame, err := json.Marshal(wsMessage{Type: msgTypeAPIRequest, ID: reqID, Payload: payload})
	if err != nil {
		return fmt.Errorf("marshal typed Agent API frame: %w", err)
	}
	if err := a.writeTextMessage(frame, 10*time.Second); err != nil {
		return fmt.Errorf("send typed Agent API request: %w", err)
	}

	select {
	case resp := <-respCh:
		if resp.Status < 200 || resp.Status >= 300 {
			var apiErr ErrorResponse
			if json.Unmarshal(resp.Body, &apiErr) == nil && apiErr.Error != "" {
				return fmt.Errorf("agent API error (status %d): %s", resp.Status, apiErr.Error)
			}
			return fmt.Errorf("agent API error (status %d)", resp.Status)
		}
		if result != nil {
			if len(resp.Body) == 0 {
				return fmt.Errorf("typed Agent API returned an empty response")
			}
			if err := json.Unmarshal(resp.Body, result); err != nil {
				return fmt.Errorf("decode typed Agent API response: %w", err)
			}
		}
		return nil
	case <-time.After(timeout):
		cancel, _ := json.Marshal(wsMessage{Type: msgTypeAPICancel, ID: reqID})
		// Cancellation is request-scoped. A congested write path must not turn
		// an API deadline into a Provider-wide WebSocket close.
		_ = a.writeTextMessage(cancel, time.Second)
		return fmt.Errorf("typed Agent API request timed out after %s", timeout)
	case <-a.doneCh:
		return fmt.Errorf("agent connection closed")
	}
}

// NewAgentConn 导出的构造函数供 API handler 调用。
func NewAgentConn(providerID uint, conn *websocket.Conn, remoteAddr string) *AgentConn {
	return newAgentConn(providerID, conn, remoteAddr)
}

// ── 底层写操作 ──────────────────────────────────────────────────────────────

func (a *AgentConn) writeTextMessage(payload []byte, timeout time.Duration) error {
	return a.writeMessage(websocket.TextMessage, payload, timeout)
}

func (a *AgentConn) writeBinaryMessage(payload []byte, timeout time.Duration) error {
	return a.writeMessage(websocket.BinaryMessage, payload, timeout)
}

func (a *AgentConn) writeMessage(kind int, payload []byte, timeout time.Duration) error {
	if a == nil || a.conn == nil {
		return fmt.Errorf("agent websocket is unavailable")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	a.writeGateOnce.Do(func() { a.writeGate = make(chan struct{}, 1) })
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case a.writeGate <- struct{}{}:
	case <-a.doneCh:
		return fmt.Errorf("agent connection closed")
	case <-timer.C:
		return fmt.Errorf("agent write queue timed out")
	}
	defer func() { <-a.writeGate }()
	select {
	case <-a.doneCh:
		return fmt.Errorf("agent connection closed")
	default:
	}
	if !time.Now().Before(deadline) {
		return fmt.Errorf("agent write queue timed out")
	}
	a.conn.SetWriteDeadline(deadline)
	err := a.conn.WriteMessage(kind, payload)
	if err != nil {
		// Actual Gorilla write failure poisons the transport; unlike queue or
		// command timeouts, this is a connection-level failure.
		_ = a.conn.Close()
	}
	return err
}

// ── 命令执行 ────────────────────────────────────────────────────────────────

// Execute 通过 WebSocket 在远端 Agent 上执行命令，返回 (stdout, error)。
// 超时默认 30 秒。
// 命令会自动添加完整的系统 PATH 前缀，确保 snap 安装的 lxc/lxd 等工具可被发现。
func (a *AgentConn) Execute(cmd string) (string, error) {
	return a.ExecuteWithTimeout(cmd, 30*time.Second)
}

// agentEnvPrefix 构建发送给 Agent 的命令环境前缀。
// Agent 通过 sh -c 执行命令，不会加载 bash profile/login shell 环境，
// 因此需要显式设置完整的标准 PATH 及扩展路径，
// 保证 snap LXD、/opt 下工具等在 Agent 侧也能被 command -v / which 发现。
// 不加载用户级配置（~/.bashrc）以避免交互式阻塞。
var agentEnvPrefix = "export PATH=\"" + utils.StandardExtendedPath + ":$PATH\"; "

// ExecuteWithTimeout 带自定义超时的命令执行。
// 命令会自动添加完整的系统 PATH 前缀。
func (a *AgentConn) ExecuteWithTimeout(cmd string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	// 为 Agent 侧添加完整 PATH 环境，确保 snap 等非标准路径下的命令可被发现
	cmd = agentEnvPrefix + cmd

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
		cancel, _ := json.Marshal(wsMessage{Type: "exec_cancel", ID: reqID})
		_ = a.writeTextMessage(cancel, time.Second)
		return "", fmt.Errorf("执行命令超时（%s）", timeout)
	case <-a.doneCh:
		return "", fmt.Errorf("agent 连接已断开")
	}
}

// ── Shell 会话 ──────────────────────────────────────────────────────────────

func (a *AgentConn) StartShell(cols, rows int) (*AgentShellSession, error) {
	return a.startShell(cols, rows, "")
}

// StartExecShell requires the atomic-exec protocol. Old agents ignore this
// message and time out safely instead of exposing their interactive host shell.
func (a *AgentConn) StartExecShell(cols, rows int, command string) (*AgentShellSession, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("empty terminal command")
	}
	session, err := a.startShell(cols, rows, command)
	if err != nil {
		return nil, err
	}
	select {
	case <-session.ReadyCh:
		return session, nil
	case <-session.DoneCh:
		err = fmt.Errorf("Agent terminal startup failed")
	case <-a.doneCh:
		err = fmt.Errorf("Agent disconnected during terminal startup")
	case <-time.After(10 * time.Second):
		err = fmt.Errorf("Agent does not support atomic terminal startup or did not respond; upgrade Agent")
	}
	_ = a.CloseShell(session.ID)
	return nil, err
}

func (a *AgentConn) startShell(cols, rows int, command string) (*AgentShellSession, error) {
	sessionID := randomID()
	session := &AgentShellSession{
		ID:       sessionID,
		OutputCh: make(chan []byte, 128),
		DoneCh:   make(chan struct{}),
		ReadyCh:  make(chan struct{}, 1),
	}
	payload, _ := json.Marshal(shellOpenPayload{Cols: cols, Rows: rows, Command: command})
	msg := wsMessage{Type: msgTypeShellOpen, ID: sessionID, Payload: payload}
	if command != "" {
		msg.Type = msgTypeShellExec
	}
	raw, _ := json.Marshal(msg)

	// Acquire the per-session gate before publishing the session in the map.
	// Otherwise CloseShell could observe the new entry, send shell_close, and
	// remove it before this goroutine gets a chance to write shell_open.
	session.ioMu.Lock()
	a.mu.Lock()
	select {
	case <-a.doneCh:
		a.mu.Unlock()
		session.ioMu.Unlock()
		return nil, fmt.Errorf("Agent disconnected")
	default:
	}
	a.shellSessions[sessionID] = session
	a.mu.Unlock()

	// Serialize the initial shell_open with any close/input/resize operation.
	if err := a.writeTextMessage(raw, 10*time.Second); err != nil {
		session.ioMu.Unlock()
		a.mu.Lock()
		delete(a.shellSessions, sessionID)
		session.safeClose()
		a.mu.Unlock()
		return nil, fmt.Errorf("启动 agent shell 失败: %w", err)
	}
	session.ioMu.Unlock()

	return session, nil
}

func (a *AgentConn) WriteShellInput(sessionID string, data []byte) error {
	session, err := a.lockShellSessionForIO(sessionID)
	if err != nil {
		return err
	}
	defer session.ioMu.Unlock()
	payload, _ := json.Marshal(shellDataPayload{Data: string(data)})
	msg := wsMessage{Type: msgTypeShellData, ID: sessionID, Payload: payload}
	raw, _ := json.Marshal(msg)
	if err := a.writeTextMessage(raw, 10*time.Second); err != nil {
		return fmt.Errorf("发送 shell 输入失败: %w", err)
	}
	return nil
}

func (a *AgentConn) ResizeShell(sessionID string, cols, rows int) error {
	session, err := a.lockShellSessionForIO(sessionID)
	if err != nil {
		return err
	}
	defer session.ioMu.Unlock()
	payload, _ := json.Marshal(shellResizePayload{Cols: cols, Rows: rows})
	msg := wsMessage{Type: msgTypeShellResize, ID: sessionID, Payload: payload}
	raw, _ := json.Marshal(msg)
	if err := a.writeTextMessage(raw, 10*time.Second); err != nil {
		return fmt.Errorf("发送 shell resize 失败: %w", err)
	}
	return nil
}

// lockShellSessionForIO prevents stale browser events from being sent after a
// session has already been closed. The per-session lock is held by the caller
// until its frame has been written, so CloseShell cannot overtake an input or
// resize frame that already passed the active-session check.
func (a *AgentConn) lockShellSessionForIO(sessionID string) (*AgentShellSession, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("shell session ID is empty")
	}
	a.mu.Lock()
	session, ok := a.shellSessions[sessionID]
	connectionClosed := false
	select {
	case <-a.doneCh:
		connectionClosed = true
	default:
	}
	a.mu.Unlock()
	if !ok {
		if connectionClosed {
			return nil, fmt.Errorf("agent connection closed")
		}
		return nil, fmt.Errorf("shell session %q is not active", sessionID)
	}
	session.ioMu.Lock()
	session.closeMu.Lock()
	closed := session.closed
	session.closeMu.Unlock()
	if closed {
		session.ioMu.Unlock()
		return nil, fmt.Errorf("shell session %q is not active", sessionID)
	}
	select {
	case <-a.doneCh:
		session.ioMu.Unlock()
		return nil, fmt.Errorf("agent connection closed")
	default:
		return session, nil
	}
}

func (a *AgentConn) CloseShell(sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("shell session ID is empty")
	}
	// Do not emit a late close frame for a session that this connection no
	// longer owns.  Browser reconnects can deliver stale cleanup events after
	// the session was already retired; sending them would be able to terminate
	// a future session if an ID were ever reused.
	a.mu.Lock()
	session, active := a.shellSessions[sessionID]
	connectionClosed := false
	select {
	case <-a.doneCh:
		connectionClosed = true
	default:
	}
	a.mu.Unlock()
	if !active {
		if connectionClosed {
			return fmt.Errorf("agent connection closed")
		}
		return fmt.Errorf("shell session %q is not active", sessionID)
	}
	session.ioMu.Lock()
	defer session.ioMu.Unlock()
	// A concurrent connection teardown or another CloseShell may have removed
	// this exact session while we waited for the per-session gate.
	a.mu.Lock()
	current, stillActive := a.shellSessions[sessionID]
	a.mu.Unlock()
	if !stillActive || current != session {
		return fmt.Errorf("shell session %q is not active", sessionID)
	}
	payload, _ := json.Marshal(shellClosePayload{})
	msg := wsMessage{Type: msgTypeShellClose, ID: sessionID, Payload: payload}
	raw, _ := json.Marshal(msg)
	// 使用较短的写超时（3秒），避免清理操作长时间阻塞新会话的建立
	err := a.writeTextMessage(raw, 3*time.Second)
	a.mu.Lock()
	if current, ok := a.shellSessions[sessionID]; ok && current == session {
		delete(a.shellSessions, sessionID)
		session.safeClose()
	}
	a.mu.Unlock()
	if err != nil {
		return fmt.Errorf("关闭 shell 会话失败: %w", err)
	}
	return nil
}

// retireShellSession removes a session after the Agent reports shell_close.
// It is separate from CloseShell because the remote side has already closed
// its PTY and no controller shell_close frame should be sent back. Object
// identity prevents an old close notification from retiring a newer session
// that happens to reuse the same ID; retirement itself is deliberately
// non-blocking so the shared Agent read loop remains responsive.
func (a *AgentConn) retireShellSession(sessionID string) {
	a.mu.Lock()
	session := a.shellSessions[sessionID]
	if session != nil {
		// The read loop owns this callback. Never wait for the per-session
		// writer gate here: a controller write may be blocked on the shared
		// WebSocket, and waiting would stall responses for every other request
		// on this Agent connection. Removing the object under a.mu prevents new
		// writes; an already-held writer may finish or fail independently.
		if current, ok := a.shellSessions[sessionID]; ok && current == session {
			delete(a.shellSessions, sessionID)
			session.safeClose()
		}
	}
	a.mu.Unlock()
}

// ── Anti-DPI Noise 循环 ────────────────────────────────────────────────────

// StartNoiseLoop periodically sends random-length noise frames (type "nop")
// to break DPI traffic-analysis signatures (message-size distribution,
// bidirectional symmetry, always-on silence patterns).
// Noise interval: 45-120s random, payload: 0-512 random bytes hex-encoded.
// The payload field key is randomised each cycle to prevent structural
// fingerprinting on a fixed {"h":"..."} pattern.
//
// IMPORTANT: The payload must be a valid JSON value so that the Rust agent's
// serde_json parser does not choke on the frame.  Raw bytes cannot be embedded
// directly via json.RawMessage because MarshalJSON returns them verbatim
// (no escaping), producing invalid JSON.  We hex-encode noise bytes into a
// {"<key>":"<hex>"} wrapper, with <key> randomly chosen from a small pool.
func (a *AgentConn) StartNoiseLoop() {
	if a == nil || a.conn == nil {
		return
	}
	// Field key pool — same set as the agent side.
	noiseKeys := []string{"d", "v", "p", "r", "c", "b", "m", "x", "e", "q"}
	go func() {
		for {
			select {
			case <-a.noiseStop:
				return
			default:
			}
			// Random sleep 45-120 s
			delay := time.Duration(45+rand.Intn(76)) * time.Second
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
				// Encode as hex string inside a JSON object.
				// Pick a random key each cycle to avoid structural fingerprinting.
				key := noiseKeys[rand.Intn(len(noiseKeys))]
				hexStr := fmt.Sprintf("%x", noise)
				payload, _ = json.Marshal(map[string]string{key: hexStr})
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
	if a == nil {
		return
	}
	a.noiseStopOnce.Do(func() { close(a.noiseStop) })
}

// ── WebSocket 协议层 Ping 循环 ──────────────────────────────────────────────

// StartWSPingLoop periodically sends WebSocket protocol-level Ping frames
// (RFC 6455 §5.5.2). Protocol pings are the primary keepalive/liveness path:
// the peer responds with a Pong that refreshes the read deadline and updates
// runtime health via SetPongHandler, without exposing a custom JSON heartbeat.
//
// Interval: randomized 35-55 s. This lowers traffic frequency and avoids a
// fixed heartbeat cadence that DPI can fingerprint, while still keeping the
// connection active through common 60-120 s idle middleboxes.
//
// NOTE: WriteControl is concurrent-safe per gorilla/websocket docs, so we
// do NOT hold writeMu here. Holding writeMu would serialize the protocol
// ping behind potentially slow data writes (up to 10 s timeout), defeating
// the purpose of a transport-layer keepalive. The deadline is passed
// directly to WriteControl via its deadline parameter — we must NOT call
// SetWriteDeadline without writeMu, as that would race with
// writeTextMessage's SetWriteDeadline+WriteMessage sequence and could
// prematurely truncate an in-progress data write's deadline.
func (a *AgentConn) StartWSPingLoop() {
	if a == nil || a.conn == nil {
		return
	}
	go func() {
		for {
			select {
			case <-a.wsPingStop:
				return
			default:
			}

			delay := time.Duration(35+rand.Intn(21)) * time.Second
			select {
			case <-a.wsPingStop:
				return
			case <-time.After(delay):
			}

			err := a.conn.WriteControl(
				websocket.PingMessage,
				[]byte{},
				time.Now().Add(5*time.Second),
			)
			if err != nil {
				a.mu.Lock()
				a.pingFailCount++
				failCount := a.pingFailCount
				a.mu.Unlock()

				if global.APP_LOG != nil {
					global.APP_LOG.Warn("WebSocket protocol ping failed",
						zap.Uint("providerID", a.ProviderID),
						zap.Int("consecutiveFailures", failCount),
						zap.Error(err))
				}
				if failCount >= 3 {
					if global.APP_LOG != nil {
						global.APP_LOG.Error("WebSocket protocol ping 连续失败超过阈值，强制断开",
							zap.Uint("providerID", a.ProviderID),
							zap.Int("consecutiveFailures", failCount))
					}
					a.conn.Close()
					return
				}
				continue
			}

			a.mu.Lock()
			a.pingFailCount = 0
			a.mu.Unlock()
		}
	}()
}

// StopWSPingLoop signals the WebSocket ping goroutine to exit.
func (a *AgentConn) StopWSPingLoop() {
	if a == nil {
		return
	}
	a.wsPingStopOnce.Do(func() { close(a.wsPingStop) })
}
