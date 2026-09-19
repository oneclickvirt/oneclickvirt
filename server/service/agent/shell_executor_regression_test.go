package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newAgentWebSocketPair(t *testing.T) (control, peer *websocket.Conn, cleanup func()) {
	t.Helper()
	peers := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade test websocket: %v", err)
			return
		}
		peers <- conn
	}))
	control, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatalf("dial test websocket: %v", err)
	}
	select {
	case peer = <-peers:
	case <-time.After(time.Second):
		control.Close()
		server.Close()
		t.Fatal("timed out waiting for test websocket peer")
	}
	var once sync.Once
	cleanup = func() { once.Do(func() { control.Close(); peer.Close(); server.Close() }) }
	return control, peer, cleanup
}

func startAgentSimulator(t *testing.T, peer *websocket.Conn, handle func(wsMessage)) {
	t.Helper()
	go func() {
		for {
			_, raw, err := peer.ReadMessage()
			if err != nil {
				return
			}
			var msg wsMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Errorf("decode Agent frame: %v", err)
				return
			}
			handle(msg)
		}
	}()
}

// startControlRouter is the smallest control-side read loop needed by these
// tests. Production uses AgentHub.readLoop; this helper deliberately avoids
// its database/status side effects while preserving request/session routing.
func startControlRouter(t *testing.T, control *websocket.Conn, ac *AgentConn) {
	t.Helper()
	go func() {
		for {
			_, raw, err := control.ReadMessage()
			if err != nil {
				return
			}
			var msg wsMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Errorf("decode control frame: %v", err)
				return
			}
			ac.mu.Lock()
			switch msg.Type {
			case msgTypeShellReady:
				if session := ac.shellSessions[msg.ID]; session != nil {
					select {
					case session.ReadyCh <- struct{}{}:
					default:
					}
				}
			case msgTypeExecResponse:
				var response execResponsePayload
				if json.Unmarshal(msg.Payload, &response) == nil {
					if ch, ok := ac.pending[msg.ID]; ok {
						select {
						case ch <- response:
						default:
						}
					}
				}
			case msgTypeShellData:
				var payload shellDataPayload
				if json.Unmarshal(msg.Payload, &payload) == nil {
					if session, ok := ac.shellSessions[msg.ID]; ok {
						select {
						case session.OutputCh <- []byte(payload.Data):
						default:
						}
					}
				}
			case msgTypeShellClose:
				if session, ok := ac.shellSessions[msg.ID]; ok {
					delete(ac.shellSessions, msg.ID)
					session.safeClose()
				}
			}
			ac.mu.Unlock()
		}
	}()
}

func sendAgentFrame(t *testing.T, peer *websocket.Conn, msg wsMessage) {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("encode Agent frame: %v", err)
	}
	if err := peer.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("send Agent frame: %v", err)
	}
}

func TestCallAPITimeoutSendsRequestScopedCancel(t *testing.T) {
	control, peer, cleanup := newAgentWebSocketPair(t)
	defer cleanup()
	ac := newAgentConn(42, control, "test")

	frames := make(chan wsMessage, 2)
	go func() {
		for {
			_, raw, err := peer.ReadMessage()
			if err != nil {
				return
			}
			var msg wsMessage
			if json.Unmarshal(raw, &msg) == nil {
				frames <- msg
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ac.CallAPI("GET", "/api/v1/egress/capabilities", nil, nil, 20*time.Millisecond)
	}()

	request := <-frames
	if request.Type != msgTypeAPIRequest || request.ID == "" {
		t.Fatalf("first frame = %#v, want api request with an ID", request)
	}
	cancel := <-frames
	if cancel.Type != msgTypeAPICancel || cancel.ID != request.ID {
		t.Fatalf("cancel frame = %#v, want api_cancel for %q", cancel, request.ID)
	}
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("CallAPI error = %v, want timeout", err)
	}
}

func execResponseFrame(t *testing.T, id, output string) wsMessage {
	t.Helper()
	payload, err := json.Marshal(execResponsePayload{Stdout: output, ExitCode: 0})
	if err != nil {
		t.Fatalf("encode exec response: %v", err)
	}
	return wsMessage{Type: msgTypeExecResponse, ID: id, Payload: payload}
}

func shellDataFrame(t *testing.T, id, output string) wsMessage {
	t.Helper()
	payload, err := json.Marshal(shellDataPayload{Data: output})
	if err != nil {
		t.Fatalf("encode shell response: %v", err)
	}
	return wsMessage{Type: msgTypeShellData, ID: id, Payload: payload}
}

func TestAgentCommandTimeoutDoesNotCloseSharedConnection(t *testing.T) {
	control, peer, cleanup := newAgentWebSocketPair(t)
	defer cleanup()
	ac := newAgentConn(7, control, "test")
	executor := NewAgentShellExecutor(7, &AgentHub{conns: map[uint]*AgentConn{7: ac}})
	startControlRouter(t, control, ac)
	slowRequestID := make(chan string, 1)
	fastRequestID := make(chan string, 1)
	startAgentSimulator(t, peer, func(msg wsMessage) {
		if msg.Type != msgTypeExecRequest {
			return
		}
		var req execRequestPayload
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			t.Errorf("decode exec request: %v", err)
			return
		}
		if strings.Contains(req.Command, "slow-command") {
			slowRequestID <- msg.ID
			return
		}
		if strings.Contains(req.Command, "fast-command") {
			fastRequestID <- msg.ID
			return
		}
	})
	resultCh := make(chan error, 1)
	go func() { _, err := executor.ExecuteWithTimeout("slow-command", 120*time.Millisecond); resultCh <- err }()
	var slowID string
	select {
	case slowID = <-slowRequestID:
	case <-time.After(time.Second):
		t.Fatal("slow command was not sent")
	}
	if err := <-resultCh; err == nil || !strings.Contains(err.Error(), "执行命令超时") {
		t.Fatalf("slow command error = %v", err)
	}
	ac.mu.Lock()
	_, slowPending := ac.pending[slowID]
	ac.mu.Unlock()
	if slowPending {
		t.Fatal("timed-out command A is still pending")
	}

	type commandResult struct {
		output string
		err    error
	}
	resultB := make(chan commandResult, 1)
	go func() {
		output, err := executor.ExecuteWithTimeout("fast-command", time.Second)
		resultB <- commandResult{output: output, err: err}
	}()
	var fastID string
	select {
	case fastID = <-fastRequestID:
	case <-time.After(time.Second):
		t.Fatal("fast command was not sent")
	}
	ac.mu.Lock()
	_, fastPending := ac.pending[fastID]
	ac.mu.Unlock()
	if !fastPending {
		t.Fatal("command B was not pending before responses")
	}

	// A's delayed response must be ignored: it cannot consume B's result or
	// make the shared WebSocket unusable.
	sendAgentFrame(t, peer, execResponseFrame(t, slowID, "late-result"))
	sendAgentFrame(t, peer, execResponseFrame(t, fastID, "fast-result"))
	result := <-resultB
	if result.err != nil {
		t.Fatalf("command B failed after command A timeout: %v", result.err)
	}
	if result.output != "fast-result" {
		t.Fatalf("command B output = %q", result.output)
	}
}

func TestExecShellStartsAtomicallyAndWaitsForReady(t *testing.T) {
	control, peer, cleanup := newAgentWebSocketPair(t)
	defer cleanup()
	ac := newAgentConn(701, control, "test")
	startControlRouter(t, control, ac)
	opened := make(chan wsMessage, 1)
	startAgentSimulator(t, peer, func(msg wsMessage) { opened <- msg })
	type result struct {
		session *AgentShellSession
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		session, err := ac.StartExecShell(80, 24, "lxc exec tenant -- /bin/sh")
		resultCh <- result{session, err}
	}()
	var frame wsMessage
	select {
	case frame = <-opened:
	case <-time.After(time.Second):
		t.Fatal("missing shell_exec")
	}
	if frame.Type != msgTypeShellExec {
		t.Fatalf("unsafe non-atomic startup: %s", frame.Type)
	}
	var payload shellOpenPayload
	if err := json.Unmarshal(frame.Payload, &payload); err != nil || payload.Command != "lxc exec tenant -- /bin/sh" {
		t.Fatalf("unexpected startup payload: %s", frame.Payload)
	}
	select {
	case <-resultCh:
		t.Fatal("startup returned before ready")
	default:
	}
	sendAgentFrame(t, peer, wsMessage{Type: msgTypeShellReady, ID: frame.ID})
	select {
	case got := <-resultCh:
		if got.err != nil || got.session.ID != frame.ID {
			t.Fatalf("startup failed: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("ready did not release startup")
	}
	ac.closeAllSessions()
}

func TestCloseShellOnlyClosesRequestedSession(t *testing.T) {
	control, peer, cleanup := newAgentWebSocketPair(t)
	defer cleanup()
	ac := newAgentConn(8, control, "test")
	startControlRouter(t, control, ac)
	frames := make(chan wsMessage, 8)
	startAgentSimulator(t, peer, func(msg wsMessage) { frames <- msg })
	sessionA, err := ac.StartShell(80, 24)
	if err != nil {
		t.Fatalf("start shell A: %v", err)
	}
	sessionB, err := ac.StartShell(80, 24)
	if err != nil {
		t.Fatalf("start shell B: %v", err)
	}
	if err := ac.CloseShell(sessionA.ID); err != nil {
		t.Fatalf("close shell A: %v", err)
	}
	var closeFrame wsMessage
	deadline := time.After(time.Second)
	for closeFrame.Type != msgTypeShellClose {
		select {
		case frame := <-frames:
			if frame.Type == msgTypeShellClose {
				closeFrame = frame
			}
		case <-deadline:
			t.Fatal("timed out waiting for shellClose")
		}
	}
	if closeFrame.ID != sessionA.ID {
		t.Fatalf("shellClose ID = %q, want %q", closeFrame.ID, sessionA.ID)
	}
	select {
	case <-sessionA.DoneCh:
	case <-time.After(time.Second):
		t.Fatal("session A was not closed")
	}
	select {
	case <-sessionB.DoneCh:
		t.Fatal("closing session A closed session B")
	default:
	}
	if err := ac.WriteShellInput(sessionB.ID, []byte("still-alive")); err != nil {
		t.Fatalf("session B input failed: %v", err)
	}
	if err := ac.WriteShellInput(sessionA.ID, []byte("stale")); err == nil {
		t.Fatal("closed session accepted stale input")
	}
	if err := ac.ResizeShell(sessionA.ID, 100, 30); err == nil {
		t.Fatal("closed session accepted stale resize")
	}
	deadline = time.After(time.Second)
	for {
		select {
		case frame := <-frames:
			if frame.Type == msgTypeShellData && frame.ID == sessionB.ID {
				sendAgentFrame(t, peer, shellDataFrame(t, sessionB.ID, "session-b-output"))
				goto output
			}
		case <-deadline:
			t.Fatal("timed out waiting for session B input")
		}
	}
output:
	select {
	case value := <-sessionB.OutputCh:
		if string(value) != "session-b-output" {
			t.Fatalf("session B output = %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("session B did not receive output")
	}
}

func TestShellInputAndCloseNeverReorderAcrossSessionRetirement(t *testing.T) {
	control, peer, cleanup := newAgentWebSocketPair(t)
	defer cleanup()
	ac := newAgentConn(801, control, "test")
	frames := make(chan wsMessage, 4)
	startAgentSimulator(t, peer, func(msg wsMessage) { frames <- msg })

	session := &AgentShellSession{
		ID:       "ordered-session",
		OutputCh: make(chan []byte, 1),
		DoneCh:   make(chan struct{}),
		ReadyCh:  make(chan struct{}, 1),
	}
	ac.mu.Lock()
	ac.shellSessions[session.ID] = session
	ac.mu.Unlock()

	// Keep both operations queued at the per-session gate. Whichever operation
	// wins is valid, but a shell_data frame must never be emitted after the
	// shell_close frame that retires the same session.
	session.ioMu.Lock()
	inputDone := make(chan error, 1)
	go func() { inputDone <- ac.WriteShellInput(session.ID, []byte("input")) }()
	closeDone := make(chan error, 1)
	go func() { closeDone <- ac.CloseShell(session.ID) }()
	for i := 0; i < 10; i++ {
		time.Sleep(time.Millisecond)
	}
	session.ioMu.Unlock()

	select {
	case <-inputDone:
	case <-time.After(time.Second):
		t.Fatal("shell input did not complete")
	}
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("shell close did not complete")
	}

	var observed []wsMessage
	deadline := time.After(time.Second)
	for {
		select {
		case frame := <-frames:
			observed = append(observed, frame)
		case <-deadline:
			for i, frame := range observed {
				if frame.Type != msgTypeShellClose {
					continue
				}
				for _, later := range observed[i+1:] {
					if later.Type == msgTypeShellData && later.ID == session.ID {
						t.Fatalf("shell_data was emitted after shell_close: %+v", observed)
					}
				}
				return
			}
			t.Fatalf("timed out waiting for shell frames: %+v", observed)
		}
	}
}

func TestAgentShellCloseRetiresOnlyMatchingSession(t *testing.T) {
	control, _, cleanup := newAgentWebSocketPair(t)
	defer cleanup()
	ac := newAgentConn(802, control, "test")
	newSession := func(id string) *AgentShellSession {
		return &AgentShellSession{ID: id, OutputCh: make(chan []byte, 1), DoneCh: make(chan struct{}), ReadyCh: make(chan struct{}, 1)}
	}
	first, second := newSession("remote-close-a"), newSession("remote-close-b")
	ac.mu.Lock()
	ac.shellSessions[first.ID] = first
	ac.shellSessions[second.ID] = second
	ac.mu.Unlock()
	// Exercise the same object-identity retirement used by the production
	// AgentHub read loop when a remote PTY exits.
	ac.retireShellSession(first.ID)

	select {
	case <-first.DoneCh:
	case <-time.After(time.Second):
		t.Fatal("first shell was not retired")
	}
	select {
	case <-second.DoneCh:
		t.Fatal("retiring first shell closed second shell")
	default:
	}
	if err := ac.WriteShellInput(second.ID, []byte("still-open")); err != nil {
		t.Fatalf("second shell input failed after first retirement: %v", err)
	}
}

func TestRetireShellSessionDoesNotWaitForWriter(t *testing.T) {
	control, _, cleanup := newAgentWebSocketPair(t)
	defer cleanup()
	ac := newAgentConn(803, control, "test")
	session := &AgentShellSession{
		ID:       "blocked-writer-session",
		OutputCh: make(chan []byte, 1),
		DoneCh:   make(chan struct{}),
		ReadyCh:  make(chan struct{}, 1),
	}
	ac.mu.Lock()
	ac.shellSessions[session.ID] = session
	ac.mu.Unlock()

	// A controller-originated write can legitimately hold this gate while the
	// shared WebSocket is back-pressured. The Agent read loop must still retire
	// the remote session promptly so unrelated responses are not stalled.
	session.ioMu.Lock()
	retired := make(chan struct{})
	go func() {
		ac.retireShellSession(session.ID)
		close(retired)
	}()
	select {
	case <-retired:
	case <-time.After(100 * time.Millisecond):
		session.ioMu.Unlock()
		t.Fatal("retireShellSession waited for the per-session writer gate")
	}
	select {
	case <-session.DoneCh:
	default:
		session.ioMu.Unlock()
		t.Fatal("retireShellSession did not close the session")
	}
	session.ioMu.Unlock()
	ac.mu.Lock()
	_, stillPresent := ac.shellSessions[session.ID]
	ac.mu.Unlock()
	if stillPresent {
		t.Fatal("retired shell session remained in the active map")
	}
}

func TestCloseShellRejectsStaleSessionWithoutSendingFrame(t *testing.T) {
	control, peer, cleanup := newAgentWebSocketPair(t)
	defer cleanup()
	ac := newAgentConn(9, control, "test")
	frames := make(chan wsMessage, 2)
	startAgentSimulator(t, peer, func(msg wsMessage) { frames <- msg })
	if err := ac.CloseShell("stale-session"); err == nil {
		t.Fatal("stale shell close unexpectedly succeeded")
	}
	select {
	case frame := <-frames:
		t.Fatalf("stale close emitted frame: %+v", frame)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAgentCommandNonPositiveTimeoutUsesSafeDefault(t *testing.T) {
	control, peer, cleanup := newAgentWebSocketPair(t)
	defer cleanup()
	ac := newAgentConn(801, control, "test")
	startControlRouter(t, control, ac)
	request := make(chan wsMessage, 1)
	startAgentSimulator(t, peer, func(msg wsMessage) {
		if msg.Type == msgTypeExecRequest {
			request <- msg
		}
	})
	result := make(chan struct {
		output string
		err    error
	}, 1)
	go func() {
		output, err := ac.ExecuteWithTimeout("default-timeout", 0)
		result <- struct {
			output string
			err    error
		}{output, err}
	}()
	select {
	case frame := <-request:
		sendAgentFrame(t, peer, execResponseFrame(t, frame.ID, "ok"))
	case <-time.After(time.Second):
		t.Fatal("command was not sent with non-positive timeout")
	}
	select {
	case got := <-result:
		if got.err != nil || got.output != "ok" {
			t.Fatalf("default timeout request failed: output=%q err=%v", got.output, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("default timeout request did not complete")
	}
}

func TestWebSSHAndAgentCommandCanRunConcurrently(t *testing.T) {
	control, peer, cleanup := newAgentWebSocketPair(t)
	defer cleanup()
	ac := newAgentConn(9, control, "test")
	executor := NewAgentShellExecutor(9, &AgentHub{conns: map[uint]*AgentConn{9: ac}})
	startControlRouter(t, control, ac)
	startAgentSimulator(t, peer, func(msg wsMessage) {
		switch msg.Type {
		case msgTypeExecRequest:
			sendAgentFrame(t, peer, execResponseFrame(t, msg.ID, "agent-command-ok"))
		case msgTypeShellData:
			sendAgentFrame(t, peer, shellDataFrame(t, msg.ID, "webssh-ok"))
		}
	})
	session, err := ac.StartShell(80, 24)
	if err != nil {
		t.Fatalf("start WebSSH session: %v", err)
	}
	defer ac.CloseShell(session.ID)
	shellErr := make(chan error, 1)
	commandResult := make(chan struct {
		output string
		err    error
	}, 1)
	go func() { shellErr <- ac.WriteShellInput(session.ID, []byte("whoami")) }()
	go func() {
		output, err := executor.ExecuteWithTimeout("parallel-command", time.Second)
		commandResult <- struct {
			output string
			err    error
		}{output, err}
	}()
	if err := <-shellErr; err != nil {
		t.Fatalf("WebSSH input failed: %v", err)
	}
	result := <-commandResult
	if result.err != nil {
		t.Fatalf("Agent command failed: %v", result.err)
	}
	if result.output != "agent-command-ok" {
		t.Fatalf("Agent command output = %q", result.output)
	}
	select {
	case value := <-session.OutputCh:
		if string(value) != "webssh-ok" {
			t.Fatalf("WebSSH output = %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("WebSSH did not receive output")
	}
}

func TestStaleConnectionCleanupCannotRemoveReplacement(t *testing.T) {
	old := newAgentConn(10, nil, "old")
	replacement := newAgentConn(10, nil, "replacement")
	hub := &AgentHub{conns: map[uint]*AgentConn{10: replacement}, statusPersistMemo: make(map[uint]agentStatusPersistState), runtimeState: make(map[uint]agentRuntimeState)}
	hub.unregister(old)
	current, ok := hub.GetConn(10)
	if !ok || current != replacement {
		t.Fatal("stale cleanup removed the replacement connection")
	}
}
