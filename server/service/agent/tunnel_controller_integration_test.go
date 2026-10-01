package agent

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// protocolTestAgent is a small protocol peer used by the controller-side
// integration test. It models the Rust Agent's tunnel frames while keeping the
// test independent from a running controller or a cloud node.
type protocolTestAgent struct {
	ws         *websocket.Conn
	writeMu    sync.Mutex
	sessionsMu sync.Mutex
	sessions   map[uint64]net.Conn
	done       chan struct{}
}

func newProtocolTestAgent(t *testing.T) (*protocolTestAgent, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		accepted <- conn
	}))
	t.Cleanup(server.Close)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	controllerWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial protocol test agent: %v", err)
	}
	t.Cleanup(func() { _ = controllerWS.Close() })
	select {
	case agentWS := <-accepted:
		agent := &protocolTestAgent{
			ws:       agentWS,
			sessions: make(map[uint64]net.Conn),
			done:     make(chan struct{}),
		}
		t.Cleanup(func() {
			_ = agentWS.Close()
			select {
			case <-agent.done:
			case <-time.After(time.Second):
			}
		})
		return agent, controllerWS
	case <-time.After(time.Second):
		t.Fatal("protocol test agent did not accept WebSocket")
		return nil, nil
	}
}

func (a *protocolTestAgent) write(kind int, payload []byte) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_ = a.ws.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return a.ws.WriteMessage(kind, payload)
}

func (a *protocolTestAgent) run(t *testing.T) {
	t.Helper()
	go func() {
		defer close(a.done)
		defer func() {
			a.sessionsMu.Lock()
			for hash, conn := range a.sessions {
				_ = conn.Close()
				delete(a.sessions, hash)
			}
			a.sessionsMu.Unlock()
		}()

		for {
			kind, data, err := a.ws.ReadMessage()
			if err != nil {
				return
			}
			switch kind {
			case websocket.TextMessage:
				var message wsMessage
				if err := json.Unmarshal(data, &message); err != nil {
					t.Errorf("decode controller tunnel frame: %v", err)
					return
				}
				switch message.Type {
				case msgTypeTunnelOpen:
					var open tunnelOpenPayload
					if err := json.Unmarshal(message.Payload, &open); err != nil {
						t.Errorf("decode tunnel_open payload: %v", err)
						return
					}
					conn, err := net.DialTimeout("tcp", net.JoinHostPort(open.Host, fmt.Sprintf("%d", open.Port)), time.Second)
					ack := tunnelAckPayload{ConnID: open.ConnID, OK: err == nil}
					if err != nil {
						ack.Error = err.Error()
					} else {
						hash := hashString(open.ConnID)
						a.sessionsMu.Lock()
						a.sessions[hash] = conn
						a.sessionsMu.Unlock()
						go a.forwardTarget(hash, open.ConnID, conn)
					}
					payload, _ := json.Marshal(ack)
					frame, _ := json.Marshal(wsMessage{Type: msgTypeTunnelAck, Payload: payload})
					if err := a.write(websocket.TextMessage, frame); err != nil {
						t.Errorf("write tunnel_ack: %v", err)
						return
					}
				case msgTypeTunnelClose:
					var closePayload tunnelClosePayload
					if json.Unmarshal(message.Payload, &closePayload) == nil {
						hash := hashString(closePayload.ConnID)
						a.sessionsMu.Lock()
						if conn := a.sessions[hash]; conn != nil {
							_ = conn.Close()
							delete(a.sessions, hash)
						}
						a.sessionsMu.Unlock()
					}
				}
			case websocket.BinaryMessage:
				if len(data) <= 8 {
					continue
				}
				hash := binary.BigEndian.Uint64(data[:8])
				a.sessionsMu.Lock()
				conn := a.sessions[hash]
				if conn != nil {
					_, _ = conn.Write(data[8:])
				}
				a.sessionsMu.Unlock()
			}
		}
	}()
}

func (a *protocolTestAgent) forwardTarget(hash uint64, connID string, conn net.Conn) {
	defer func() {
		_ = conn.Close()
		a.sessionsMu.Lock()
		if a.sessions[hash] == conn {
			delete(a.sessions, hash)
		}
		a.sessionsMu.Unlock()
		payload, _ := json.Marshal(tunnelClosePayload{ConnID: connID})
		frame, _ := json.Marshal(wsMessage{Type: msgTypeTunnelClose, Payload: payload})
		_ = a.write(websocket.TextMessage, frame)
	}()

	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			frame := make([]byte, 8+n)
			binary.BigEndian.PutUint64(frame[:8], hash)
			copy(frame[8:], buf[:n])
			if a.write(websocket.BinaryMessage, frame) != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func startEchoTarget(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-stop:
					return
				default:
				}
				continue
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String(), func() {
		close(stop)
		_ = listener.Close()
	}
}

func serveTestController(t *testing.T, manager *TunnelManager, target string) (net.Listener, chan error, chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- manager.serveControllerPort(listener, listener.Addr().String(), target, func() (string, int, error) {
			host, port, err := net.SplitHostPort(target)
			if err != nil {
				return "", 0, err
			}
			var parsed int
			_, _ = fmt.Sscanf(port, "%d", &parsed)
			return host, parsed, nil
		}, stop)
	}()
	return listener, done, stop
}

func readEcho(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatalf("write through controller tunnel: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read through controller tunnel: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("tunnel echo = %q, want %q", got, payload)
	}
}

func startControllerTunnelReader(t *testing.T, ws *websocket.Conn, manager *TunnelManager) {
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
				return
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

func TestControllerPortForwardEndToEndLifecycle(t *testing.T) {
	previousLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previousLog })

	target, stopTarget := startEchoTarget(t)
	t.Cleanup(stopTarget)
	agent, controllerWS := newProtocolTestAgent(t)
	agent.run(t)
	conn := newAgentConn(9001, controllerWS, "test-agent")
	manager := NewTunnelManager(conn)
	startControllerTunnelReader(t, controllerWS, manager)

	listener, done, stop := serveTestController(t, manager, target)
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	readEcho(t, client, "controller-agent-roundtrip")
	_ = client.Close()

	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("controller listener returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("controller listener did not stop")
	}

	// The same control-plane port can be rebound after removal. This models a
	// delete followed by a recreate without depending on an OS-specific retry.
	listener2, done2, stop2 := serveTestController(t, manager, target)
	client2, err := net.DialTimeout("tcp", listener2.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	readEcho(t, client2, "controller-agent-rebind")
	_ = client2.Close()
	close(stop2)
	select {
	case err := <-done2:
		if err != nil {
			t.Fatalf("rebound controller listener returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rebound controller listener did not stop")
	}
}

func TestControllerPortForwardRecoveryAndHealLifecycle(t *testing.T) {
	previousDB, previousLog := global.APP_DB, global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB = previousDB
		global.APP_LOG = previousLog
	})

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:tunnel_controller_recovery_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE instances (
		id integer primary key, private_ip text, deleted_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE ports (
		id integer primary key, instance_id integer, provider_id integer,
		host_port integer, guest_port integer, status text, mapping_type text,
		mapping_method text, internal_host text, created_at datetime,
		updated_at datetime, deleted_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}

	target, stopTarget := startEchoTarget(t)
	t.Cleanup(stopTarget)
	targetHost, targetPortText, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	var targetPort int
	if _, err := fmt.Sscanf(targetPortText, "%d", &targetPort); err != nil {
		t.Fatal(err)
	}
	const providerID = uint(9002)
	const instanceID = uint(1)
	controllerPortProbe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	controllerPort := controllerPortProbe.Addr().(*net.TCPAddr).Port
	_ = controllerPortProbe.Close()
	if err := db.Exec(`INSERT INTO instances (id, private_ip) VALUES (?, ?)`, instanceID, targetHost).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ports (id, instance_id, provider_id, host_port, guest_port, status, mapping_type, mapping_method, internal_host)
		VALUES (?, ?, ?, ?, ?, 'active', 'controller', 'controller', ?)`, 1, instanceID, providerID, controllerPort, targetPort, targetHost).Error; err != nil {
		t.Fatal(err)
	}
	global.APP_DB = db

	agent, controllerWS := newProtocolTestAgent(t)
	agent.run(t)
	conn := newAgentConn(providerID, controllerWS, "test-agent")
	hub := GetHub()
	hub.mu.Lock()
	previousConn, hadPrevious := hub.conns[providerID]
	hub.conns[providerID] = conn
	hub.mu.Unlock()
	manager := NewTunnelManager(conn)
	tunnelMgrMu.Lock()
	previousManager := tunnelMgrs[providerID]
	tunnelMgrs[providerID] = manager
	tunnelMgrMu.Unlock()
	startControllerTunnelReader(t, controllerWS, manager)
	t.Cleanup(func() {
		StopControllerPortForward(1)
		RemoveTunnelManager(providerID)
		tunnelMgrMu.Lock()
		if previousManager != nil {
			tunnelMgrs[providerID] = previousManager
		} else {
			delete(tunnelMgrs, providerID)
		}
		tunnelMgrMu.Unlock()
		hub.mu.Lock()
		if hadPrevious {
			hub.conns[providerID] = previousConn
		} else {
			delete(hub.conns, providerID)
		}
		hub.mu.Unlock()
	})

	if err := StartControllerPortForward(1, providerID, controllerPort, targetHost, targetPort); err != nil {
		t.Fatalf("start controller port forward: %v", err)
	}
	var updatedAt sql.NullTime
	if err := db.Table("ports").Select("updated_at").Where("id = ?", 1).Scan(&updatedAt).Error; err != nil {
		t.Fatalf("read controller mapping update timestamp: %v", err)
	}
	if updatedAt.Valid {
		t.Fatalf("active controller mapping was rewritten during recovery: updated_at=%s", updatedAt.Time.Format(time.RFC3339Nano))
	}
	assertControllerEcho := func(payload string) {
		t.Helper()
		client, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", controllerPort), time.Second)
		if err != nil {
			t.Fatalf("dial recovered controller port: %v", err)
		}
		defer client.Close()
		readEcho(t, client, payload)
	}
	assertControllerEcho("controller-start")

	StopControllerPortForward(1)
	if IsControllerPortForwardRunning(1) {
		t.Fatal("controller listener remained after delete/stop")
	}
	EnsureControllerPortForwardsByProvider(providerID)
	if !IsControllerPortForwardRunning(1) {
		t.Fatal("controller listener was not restored by synchronization")
	}
	assertControllerEcho("controller-sync")

	if err := db.Model(&providerModel.Port{}).Where("id = ?", 1).Update("status", "deleting").Error; err != nil {
		t.Fatal(err)
	}
	CheckAndRepairControllerPortForwards()
	if IsControllerPortForwardRunning(1) {
		t.Fatal("health repair kept a deleted controller listener")
	}
	if err := db.Model(&providerModel.Port{}).Where("id = ?", 1).Update("status", "active").Error; err != nil {
		t.Fatal(err)
	}
	EnsureControllerPortForwardsByProvider(providerID)
	assertControllerEcho("controller-heal")

	RebuildControllerPortForwardsByProvider(providerID)
	if !IsControllerPortForwardRunning(1) {
		t.Fatal("forced controller rebuild did not restore listener")
	}
	assertControllerEcho("controller-rebuild")
}
