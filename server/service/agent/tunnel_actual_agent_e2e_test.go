package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// TestActualRustAgentControllerPortForward runs only when an actual Rust
// Agent binary is supplied. The ordinary controller tunnel tests intentionally
// use a protocol peer; this test closes that evidence gap without requiring a
// Linux host or modifying a provider node during normal CI.
func TestActualRustAgentControllerPortForward(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("ONECLICKVIRT_AGENT_BINARY"))
	if binary == "" {
		t.Skip("set ONECLICKVIRT_AGENT_BINARY to run the real reverse-Agent E2E")
	}
	if info, err := os.Stat(binary); err != nil || info.IsDir() {
		t.Fatalf("ONECLICKVIRT_AGENT_BINARY is not an executable file: %v", err)
	}

	previousLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previousLog })

	accepted := make(chan *websocket.Conn, 4)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		select {
		case accepted <- conn:
		default:
			_ = conn.Close()
		}
	}))
	t.Cleanup(controller.Close)
	wsURL := "ws" + strings.TrimPrefix(controller.URL, "http") + "/api/v1/ws/agent"

	target, stopTarget := startEchoTarget(t)
	t.Cleanup(stopTarget)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"API_TOKEN=actual-e2e-token",
		"AGENT_SECRET=actual-e2e-secret",
		"WS_URL="+wsURL,
		"AGENT_API_ADDR=127.0.0.1:0",
		"TRAFFIC_COLLECT_METHOD=ipt",
		"TRAFFIC_COLLECT_INTERVAL=3600",
		"TRAFFIC_RECONCILE_INTERVAL=3600",
		"RESOURCE_COLLECT_INTERVAL=3600",
		"RUST_LOG=warn",
	)
	var processOutput bytes.Buffer
	cmd.Stdout = &processOutput
	cmd.Stderr = &processOutput
	if err := cmd.Start(); err != nil {
		t.Fatalf("start actual Rust Agent: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	connect := func() (*websocket.Conn, *TunnelManager) {
		t.Helper()
		var ws *websocket.Conn
		select {
		case ws = <-accepted:
		case <-time.After(15 * time.Second):
			t.Fatalf("actual Agent did not connect: %s", processOutput.String())
		}
		conn := newAgentConn(9901, ws, "actual-agent")
		manager := NewTunnelManager(conn)
		startActualAgentControllerReader(t, ws, manager)
		return ws, manager
	}

	controllerWS, manager := connect()
	listener, done, stop := serveTestController(t, manager, target)
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial controller listener through actual Agent: %v", err)
	}
	readEcho(t, client, "actual-agent-first-bind")
	_ = client.Close()
	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("first controller listener stopped with error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first controller listener did not stop")
	}

	// Closing the controller side forces the real Agent's reconnect loop. The
	// existing process remains alive, so this checks reconnect and rebind rather
	// than silently replacing the binary or the environment.
	_ = controllerWS.Close()
	controllerWS, manager = connect()
	listener2, done2, stop2 := serveTestController(t, manager, target)
	client2, err := net.DialTimeout("tcp", listener2.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial rebound controller listener after Agent reconnect: %v", err)
	}
	readEcho(t, client2, "actual-agent-reconnected-bind")
	_ = client2.Close()
	close(stop2)
	select {
	case err := <-done2:
		if err != nil {
			t.Fatalf("rebound controller listener stopped with error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rebound controller listener did not stop")
	}
	_ = controllerWS.Close()
}

func startActualAgentControllerReader(t *testing.T, ws *websocket.Conn, manager *TunnelManager) {
	t.Helper()
	go func() {
		for {
			kind, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if kind == websocket.BinaryMessage {
				if len(data) > 8 {
					manager.DeliverByHash(binary.BigEndian.Uint64(data[:8]), data[8:])
				}
				continue
			}
			if kind != websocket.TextMessage {
				continue
			}
			var message wsMessage
			if json.Unmarshal(data, &message) != nil {
				continue
			}
			switch message.Type {
			case msgTypeTunnelAck:
				var ack tunnelAckPayload
				if json.Unmarshal(message.Payload, &ack) == nil {
					manager.DeliverAck(ack)
				}
			case msgTypeTunnelClose:
				var closePayload tunnelClosePayload
				if json.Unmarshal(message.Payload, &closePayload) == nil {
					manager.CloseSession(closePayload.ConnID)
				}
			case msgTypeTunnelKeepalive:
				var keepalive tunnelKeepalivePayload
				if json.Unmarshal(message.Payload, &keepalive) == nil {
					manager.TouchSession(keepalive.ConnID)
				}
			}
		}
	}()
}
