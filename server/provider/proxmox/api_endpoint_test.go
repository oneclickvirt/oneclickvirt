package proxmox

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

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

func providerNodeConfigForEndpointTest(host string) provider.NodeConfig {
	return provider.NodeConfig{Host: host}
}
