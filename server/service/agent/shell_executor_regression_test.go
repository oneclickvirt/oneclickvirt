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
