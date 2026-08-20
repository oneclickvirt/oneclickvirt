package admin

import (
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	providerModel "oneclickvirt/model/provider"

	"github.com/gorilla/websocket"
)

func TestFindProxmoxConsoleRuntimeID(t *testing.T) {
	resources := []proxmoxConsoleResource{
		{VMID: 100, Name: "pve-2485", Type: "qemu", Node: "pve"},
		{VMID: 101, Name: "pve-4db0", Type: "lxc", Node: "pve"},
		{VMID: 102, Name: "pve-4db0", Type: "lxc", Node: "other"},
		{VMID: 103, Name: "template", Type: "qemu", Node: "pve", Template: []byte("1")},
	}

	vmid, err := findProxmoxConsoleRuntimeID(resources, providerModel.Instance{
		Name: "pve-2485", ProviderVMID: "pve-2485", InstanceType: "vm",
	}, "pve")
	if err != nil || vmid != "100" {
		t.Fatalf("VM runtime ID = (%q, %v), want (100, nil)", vmid, err)
	}
	ctid, err := findProxmoxConsoleRuntimeID(resources, providerModel.Instance{
		Name: "pve-4db0", ProviderVMID: "pve-4db0", InstanceType: "container",
	}, "pve")
	if err != nil || ctid != "101" {
		t.Fatalf("CT runtime ID = (%q, %v), want (101, nil)", ctid, err)
	}
	if _, err := findProxmoxConsoleRuntimeID(resources, providerModel.Instance{
		Name: "template", ProviderVMID: "template", InstanceType: "vm",
	}, "pve"); err == nil {
		t.Fatal("template resource unexpectedly resolved as a runtime VM")
	}
}

func TestParseProxmoxConsoleResourcesAcceptsPveshAndAPIShapes(t *testing.T) {
	for _, raw := range []string{
		`[{"vmid":100,"name":"vm-a","type":"qemu"}]`,
		`{"data":[{"vmid":101,"name":"ct-a","type":"lxc"}]}`,
	} {
		resources, err := parseProxmoxConsoleResources(raw)
		if err != nil || len(resources) != 1 || resources[0].VMID <= 0 {
			t.Fatalf("parseProxmoxConsoleResources(%s) = %#v, %v", raw, resources, err)
		}
	}
}

func TestProxmoxVNCProxyCommandAndResponse(t *testing.T) {
	command, err := proxmoxVNCProxyCommand("pve-a", "100")
	if err != nil || !strings.Contains(command, "/nodes/pve-a/qemu/100/vncproxy") {
		t.Fatalf("proxmoxVNCProxyCommand() = (%q, %v)", command, err)
	}
	if _, err := proxmoxVNCProxyCommand("pve;unsafe", "100"); err == nil {
		t.Fatal("unsafe node name unexpectedly accepted")
	}
	if _, err := proxmoxVNCProxyCommand("pve", "pve-2485"); err == nil {
		t.Fatal("display name unexpectedly accepted as a VMID")
	}
	credentials, err := parseProxmoxVNCProxyResponse(`{"port":"5900","password":"temporary"}`)
	if err != nil || credentials.port != 5900 || credentials.password != "temporary" {
		t.Fatalf("parseProxmoxVNCProxyResponse() = %#v, %v", credentials, err)
	}
	credentials, err = parseProxmoxVNCProxyResponse(`{"port":5900,"ticket":"PVEVNC:temporary"}`)
	if err != nil || credentials.port != 5900 || credentials.password != "PVEVNC:temporary" {
		t.Fatalf("ticket-based PVE response = %#v, %v", credentials, err)
	}
	if _, err := parseProxmoxVNCProxyResponse(`{"port":70000,"password":"temporary"}`); err == nil {
		t.Fatal("invalid VNC proxy port unexpectedly accepted")
	}
}

func TestProxmoxNativeVNCDoesNotRequireGenericRawVNCSetting(t *testing.T) {
	target := buildProxmoxConsoleVNCTarget(
		providerModel.Instance{ID: 42, InstanceType: "vm"},
		providerModel.Provider{
			ConnectionType: "ssh",
			Endpoint:       "pve.example.test:22",
			Username:       "root",
			EnableVNC:      false,
		},
		"100",
		nil,
	)
	if !target.available || !target.proxmoxVNC || target.protocol != consoleProtocolVNC {
		t.Fatalf("PVE native VNC target = %#v, want available native VNC target", target)
	}
}

func TestProxmoxAgentVNCRequiresLiveControlChannel(t *testing.T) {
	target := buildProxmoxConsoleVNCTarget(
		providerModel.Instance{ID: 42, InstanceType: "vm"},
		providerModel.Provider{ID: ^uint(0) - 1, ConnectionType: "agent", AgentStatus: "online"},
		"100",
		nil,
	)
	if target.available || !strings.Contains(target.reason, "Agent 当前离线") {
		t.Fatalf("offline Agent PVE VNC target = %#v", target)
	}
}

func TestWriteVNCWebSocketFailureUsesRFBFailureHandshake(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		writeVNCWebSocketFailure(ws, "PVE VNC 连接失败: test diagnostic")
	}))
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
	if err != nil {
		t.Fatalf("dial test WebSocket: %v", err)
	}
	defer client.Close()
	if messageType, message, err := client.ReadMessage(); err != nil || messageType != websocket.BinaryMessage || string(message) != "RFB 003.008\n" {
		t.Fatalf("RFB version message = (%d, %q, %v)", messageType, message, err)
	}
	if err := client.WriteMessage(websocket.BinaryMessage, []byte("RFB 003.008\n")); err != nil {
		t.Fatalf("send client RFB version: %v", err)
	}
	messageType, failure, err := client.ReadMessage()
	if err != nil || messageType != websocket.BinaryMessage || len(failure) < 5 || failure[0] != 0 {
		t.Fatalf("RFB failure message = (%d, %q, %v)", messageType, failure, err)
	}
	length := binary.BigEndian.Uint32(failure[1:5])
	if int(length) != len(failure)-5 || !strings.Contains(string(failure[5:]), "test diagnostic") {
		t.Fatalf("RFB failure payload = %q", failure)
	}
}

func TestAuthenticateProxmoxVNCPassword(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	password := "temporary"
	done := make(chan error, 1)
	go func() {
		defer close(done)
		if _, err := server.Write([]byte("RFB 003.008\n")); err != nil {
			done <- err
			return
		}
		version := make([]byte, 12)
		if _, err := io.ReadFull(server, version); err != nil {
			done <- err
			return
		}
		if string(version) != "RFB 003.008\n" {
			done <- &testProtocolError{message: "unexpected client RFB version"}
			return
		}
		if _, err := server.Write([]byte{1, 2}); err != nil {
			done <- err
			return
		}
		selected := make([]byte, 1)
		if _, err := io.ReadFull(server, selected); err != nil {
			done <- err
			return
		}
		if selected[0] != 2 {
			done <- &testProtocolError{message: "client did not select VNC auth"}
			return
		}
		challenge := []byte("0123456789abcdef")
		if _, err := server.Write(challenge); err != nil {
			done <- err
			return
		}
		response := make([]byte, len(challenge))
		if _, err := io.ReadFull(server, response); err != nil {
			done <- err
			return
		}
		expected, err := vncChallengeResponse(challenge, password)
		if err != nil || string(response) != string(expected) {
			done <- &testProtocolError{message: "unexpected VNC challenge response"}
			return
		}
		var success [4]byte
		binary.BigEndian.PutUint32(success[:], 0)
		_, err = server.Write(success[:])
		done <- err
	}()

	if err := authenticateProxmoxVNC(client, password); err != nil {
		t.Fatalf("authenticateProxmoxVNC() error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("mock PVE VNC server error = %v", err)
	}
}

type testProtocolError struct {
	message string
}

func (e *testProtocolError) Error() string { return e.message }
