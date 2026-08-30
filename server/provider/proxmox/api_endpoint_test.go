package proxmox

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

func TestProxmoxAPIEndpointBracketsIPv6Host(t *testing.T) {
	p := &ProxmoxProvider{config: providerNodeConfigForEndpointTest("2001:db8::10")}
	if got, want := p.apiEndpoint("/api2/json/cluster/resources?type=vm"), "https://[2001:db8::10]:8006/api2/json/cluster/resources?type=vm"; got != want {
		t.Fatalf("apiEndpoint() = %q, want %q", got, want)
	}
}

func TestProxmoxGuestEndpointSelectsResourceByInstanceType(t *testing.T) {
	p := &ProxmoxProvider{config: providerNodeConfigForEndpointTest("pve.test"), node: "pve-node"}
	tests := []struct {
		instanceType string
		want         string
	}{
		{instanceType: "container", want: "https://pve.test:8006/api2/json/nodes/pve-node/lxc/100/status/stop"},
		{instanceType: "vm", want: "https://pve.test:8006/api2/json/nodes/pve-node/qemu/101/status/reboot"},
	}

	for _, test := range tests {
		t.Run(test.instanceType, func(t *testing.T) {
			vmid, suffix := "100", "status/stop"
			if test.instanceType == "vm" {
				vmid, suffix = "101", "status/reboot"
			}
			got, err := p.apiGuestEndpoint(test.instanceType, vmid, suffix)
			if err != nil {
				t.Fatalf("apiGuestEndpoint() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("apiGuestEndpoint() = %q, want %q", got, test.want)
			}
		})
	}

	if _, err := p.apiGuestEndpoint("unknown", "100", "status/start"); err == nil {
		t.Fatal("apiGuestEndpoint() accepted an unknown instance type")
	}
}

type proxmoxLifecycleMutationTransport struct {
	requests []string
}

func (t *proxmoxLifecycleMutationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, req.Method+" "+req.URL.Path)
	if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/tasks/UPID:mutation/status") {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":{"status":"stopped","exitstatus":"OK"}}`)),
			Request:    req,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"data":"UPID:mutation"}`)),
		Request:    req,
	}, nil
}

func TestProxmoxLifecycleMutationsUseCorrectGuestResourceAndWait(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	oldPoll := proxmoxAPITaskPollInterval
	proxmoxAPITaskPollInterval = 0
	t.Cleanup(func() { proxmoxAPITaskPollInterval = oldPoll })

	tests := []struct {
		name        string
		identifier  string
		listOutput  string
		call        func(*ProxmoxProvider) error
		wantRequest string
	}{
		{
			name:       "container stop",
			identifier: "100",
			listOutput: "VMID Status Lock Name\n100 running - guest\n",
			call: func(p *ProxmoxProvider) error {
				return p.apiStopInstance(context.Background(), "100")
			},
			wantRequest: "POST /api2/json/nodes/pve-node/lxc/100/status/stop",
		},
		{
			name:       "vm restart",
			identifier: "101",
			listOutput: " VMID NAME STATUS MEM(MB) BOOTDISK(GB) PID\n101 guest running 512 8 1\n",
			call: func(p *ProxmoxProvider) error {
				return p.apiRestartInstance(context.Background(), "101")
			},
			wantRequest: "POST /api2/json/nodes/pve-node/qemu/101/status/reboot",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &proxmoxLifecycleMutationTransport{}
			p := NewProxmoxProvider().(*ProxmoxProvider)
			p.config = providerNodeConfigForEndpointTest("pve.test")
			p.node = "pve-node"
			p.apiClient = &http.Client{Transport: transport}
			p.sshClient.SetExecutor(&ipv6CommandExecutor{output: func(command string) string {
				if strings.HasPrefix(command, "pct list") && test.identifier == "100" {
					return test.listOutput
				}
				if strings.HasPrefix(command, "qm list") && test.identifier == "101" {
					return test.listOutput
				}
				return "VMID Status Lock Name\n"
			}})

			if err := test.call(p); err != nil {
				t.Fatalf("lifecycle mutation error = %v", err)
			}
			if len(transport.requests) != 2 {
				t.Fatalf("requests = %v, want mutation and task-status poll", transport.requests)
			}
			if transport.requests[0] != test.wantRequest {
				t.Fatalf("first request = %q, want %q", transport.requests[0], test.wantRequest)
			}
			if transport.requests[1] != "GET /api2/json/nodes/pve-node/tasks/UPID:mutation/status" {
				t.Fatalf("task status request = %q", transport.requests[1])
			}
		})
	}
}

type proxmoxKnownStartTransport struct {
	requests           []string
	currentStatus      []string
	startResponseCodes []int
	startContentTypes  []string
	startBodies        []string
}

func (t *proxmoxKnownStartTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, req.Method+" "+req.URL.Path)
	response := func(payload string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(payload)),
			Request:    req,
		}, nil
	}
	switch {
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/status/current"):
		status := "running"
		if len(t.currentStatus) > 0 {
			status = t.currentStatus[0]
			t.currentStatus = t.currentStatus[1:]
		}
		return response(`{"data":{"status":"` + status + `"}}`)
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/status/start"):
		var body []byte
		if req.Body != nil {
			body, _ = io.ReadAll(req.Body)
		}
		t.startContentTypes = append(t.startContentTypes, req.Header.Get("Content-Type"))
		t.startBodies = append(t.startBodies, string(body))
		if len(t.startResponseCodes) > 0 {
			statusCode := t.startResponseCodes[0]
			t.startResponseCodes = t.startResponseCodes[1:]
			if statusCode != http.StatusOK {
				return &http.Response{
					StatusCode: statusCode,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"data":null,"message":"can't lock file '/run/lock/lxc/pve-config-100.lock' - got timeout"}`)),
					Request:    req,
				}, nil
			}
		}
		return response(`{"data":"UPID:known-start"}`)
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/tasks/UPID:known-start/status"):
		return response(`{"data":{"status":"stopped","exitstatus":"OK"}}`)
	default:
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":null,"message":"unexpected request"}`)),
			Request:    req,
		}, nil
	}
}

func TestProxmoxKnownLXCStartDoesNotRediscoverNewGuest(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	oldPoll := proxmoxAPITaskPollInterval
	proxmoxAPITaskPollInterval = 0
	t.Cleanup(func() { proxmoxAPITaskPollInterval = oldPoll })

	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.config = providerNodeConfigForEndpointTest("pve.test")
	p.node = "pve-node"
	transport := &proxmoxKnownStartTransport{currentStatus: []string{"stopped", "running"}}
	p.apiClient = &http.Client{Transport: transport}
	ssh := &ipv6CommandExecutor{}
	p.sshClient.SetExecutor(ssh)

	if err := p.apiStartKnownInstance(context.Background(), "100", "container"); err != nil {
		t.Fatalf("apiStartKnownInstance() error = %v", err)
	}
	if len(ssh.commands) != 0 {
		t.Fatalf("apiStartKnownInstance() performed SSH discovery: %v", ssh.commands)
	}
	want := []string{
		"GET /api2/json/nodes/pve-node/lxc/100/status/current",
		"POST /api2/json/nodes/pve-node/lxc/100/status/start",
		"GET /api2/json/nodes/pve-node/tasks/UPID:known-start/status",
		"GET /api2/json/nodes/pve-node/lxc/100/status/current",
	}
	if got := strings.Join(transport.requests, ","); got != strings.Join(want, ",") {
		t.Fatalf("request order = %q, want %q", got, strings.Join(want, ","))
	}
	if len(transport.startContentTypes) != 1 || transport.startContentTypes[0] != "" {
		t.Fatalf("start Content-Type = %v, want an empty header", transport.startContentTypes)
	}
	if len(transport.startBodies) != 1 || transport.startBodies[0] != "" {
		t.Fatalf("start body = %q, want an empty body", transport.startBodies)
	}
}

func TestProxmoxKnownLXCStartRetriesTransientGuestLock(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	oldPoll, oldDelay := proxmoxAPITaskPollInterval, proxmoxAPIConfigLockRetryDelay
	proxmoxAPITaskPollInterval = 0
	proxmoxAPIConfigLockRetryDelay = 0
	t.Cleanup(func() {
		proxmoxAPITaskPollInterval = oldPoll
		proxmoxAPIConfigLockRetryDelay = oldDelay
	})

	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.config = providerNodeConfigForEndpointTest("pve.test")
	p.node = "pve-node"
	transport := &proxmoxKnownStartTransport{
		currentStatus:      []string{"stopped", "stopped", "running"},
		startResponseCodes: []int{http.StatusInternalServerError, http.StatusOK},
	}
	p.apiClient = &http.Client{Transport: transport}
	ssh := &ipv6CommandExecutor{}
	p.sshClient.SetExecutor(ssh)

	if err := p.apiStartKnownInstance(context.Background(), "100", "container"); err != nil {
		t.Fatalf("apiStartKnownInstance() error = %v", err)
	}
	if len(ssh.commands) != 0 {
		t.Fatalf("apiStartKnownInstance() performed SSH discovery: %v", ssh.commands)
	}
	startRequests := 0
	for _, request := range transport.requests {
		if request == "POST /api2/json/nodes/pve-node/lxc/100/status/start" {
			startRequests++
		}
	}
	if startRequests != 2 {
		t.Fatalf("start requests = %d, want 2 after a transient PVE lock: %v", startRequests, transport.requests)
	}
}

func TestProxmoxKnownLXCStartFailsWhenGuestRemainsStopped(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	oldPoll := proxmoxAPITaskPollInterval
	proxmoxAPITaskPollInterval = 0
	t.Cleanup(func() { proxmoxAPITaskPollInterval = oldPoll })

	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.config = providerNodeConfigForEndpointTest("pve.test")
	p.node = "pve-node"
	transport := &proxmoxKnownStartTransport{currentStatus: []string{"stopped", "stopped"}}
	p.apiClient = &http.Client{Transport: transport}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := p.apiStartKnownInstance(ctx, "100", "container")
	if err == nil {
		t.Fatal("apiStartKnownInstance() accepted a guest that remained stopped")
	}
	if !strings.Contains(err.Error(), "最后状态: stopped") {
		t.Fatalf("apiStartKnownInstance() error = %q, want final stopped status", err)
	}
}

func providerNodeConfigForEndpointTest(host string) provider.NodeConfig {
	return provider.NodeConfig{Host: host}
}
