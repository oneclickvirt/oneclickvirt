package proxmox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

type proxmoxPasswordCaptureTransport struct {
	requests []string
	payloads map[string]map[string]interface{}
}

func (t *proxmoxPasswordCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	requestName := req.Method + " " + req.URL.Path
	t.requests = append(t.requests, requestName)
	if req.Body != nil && strings.HasSuffix(req.URL.Path, "/config") ||
		(req.Body != nil && strings.HasSuffix(req.URL.Path, "/exec")) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		if t.payloads == nil {
			t.payloads = make(map[string]map[string]interface{})
		}
		t.payloads[requestName] = payload
	}

	response := func(status int, body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	}

	if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/tasks/") {
		return response(http.StatusOK, `{"data":{"status":"stopped","exitstatus":"OK"}}`)
	}
	if strings.HasSuffix(req.URL.Path, "/config") {
		return response(http.StatusOK, `{"data":"UPID:password-config"}`)
	}
	if strings.HasSuffix(req.URL.Path, "/exec") {
		return response(http.StatusOK, `{"data":"UPID:password-exec"}`)
	}
	if strings.HasSuffix(req.URL.Path, "/status/reboot") {
		return response(http.StatusOK, `{"data":"UPID:password-reboot"}`)
	}
	return response(http.StatusNotFound, `{"message":"unexpected request"}`)
}

func TestProxmoxAPIContainerPasswordWaitsAndEncodesCredential(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	oldPoll := proxmoxAPITaskPollInterval
	proxmoxAPITaskPollInterval = 0
	t.Cleanup(func() { proxmoxAPITaskPollInterval = oldPoll })

	transport := &proxmoxPasswordCaptureTransport{}
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.config = providerNodeConfigForEndpointTest("2001:db8::40")
	p.node = "pve-node"
	p.apiClient = &http.Client{Transport: transport}

	password := "p@ss' word$with-chars"
	if err := p.apiSetContainerPassword(context.Background(), "100", password); err != nil {
		t.Fatalf("apiSetContainerPassword() error = %v", err)
	}

	requestName := "POST /api2/json/nodes/pve-node/lxc/100/exec"
	payload, ok := transport.payloads[requestName]
	if !ok {
		t.Fatalf("missing captured payload for %s", requestName)
	}
	command, ok := payload["command"].(string)
	if !ok {
		t.Fatalf("command payload = %#v, want string", payload["command"])
	}
	if strings.Contains(command, password) {
		t.Fatalf("command contains plaintext password: %q", command)
	}
	parts := strings.Fields(command)
	if len(parts) < 2 {
		t.Fatalf("unexpected command: %q", command)
	}
	encoded := strings.Trim(parts[1], "'")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode credential: %v", err)
	}
	if got, want := string(decoded), "root:"+password+"\n"; got != want {
		t.Fatalf("decoded credential = %q, want %q", got, want)
	}
	wantRequests := []string{
		"POST /api2/json/nodes/pve-node/lxc/100/exec",
		"GET /api2/json/nodes/pve-node/tasks/UPID:password-exec/status",
	}
	if strings.Join(transport.requests, ",") != strings.Join(wantRequests, ",") {
		t.Fatalf("requests = %v, want %v", transport.requests, wantRequests)
	}
}

func TestProxmoxAPIVMPasswordWaitsForConfigAndReboot(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	oldPoll := proxmoxAPITaskPollInterval
	proxmoxAPITaskPollInterval = 0
	t.Cleanup(func() { proxmoxAPITaskPollInterval = oldPoll })

	transport := &proxmoxPasswordCaptureTransport{}
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.config = providerNodeConfigForEndpointTest("2001:db8::41")
	p.node = "pve-node"
	p.apiClient = &http.Client{Transport: transport}

	if err := p.apiSetVMPassword(context.Background(), "101", "safe-password"); err != nil {
		t.Fatalf("apiSetVMPassword() error = %v", err)
	}

	wantRequests := []string{
		"PUT /api2/json/nodes/pve-node/qemu/101/config",
		"GET /api2/json/nodes/pve-node/tasks/UPID:password-config/status",
		"POST /api2/json/nodes/pve-node/qemu/101/status/reboot",
		"GET /api2/json/nodes/pve-node/tasks/UPID:password-reboot/status",
	}
	if strings.Join(transport.requests, ",") != strings.Join(wantRequests, ",") {
		t.Fatalf("requests = %v, want %v", transport.requests, wantRequests)
	}
	configPayload := transport.payloads["PUT /api2/json/nodes/pve-node/qemu/101/config"]
	if got := configPayload["cipassword"]; got != "safe-password" {
		t.Fatalf("cipassword = %v, want safe-password", got)
	}
}
