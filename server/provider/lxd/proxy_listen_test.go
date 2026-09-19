package lxd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

func TestLXDVMPortRangesAlwaysUseNATProxyMode(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp", "both"} {
		t.Run(protocol, func(t *testing.T) {
			executor := &recordingLXDIPv6Executor{}
			p := NewLXDProvider().(*LXDProvider)
			p.config = provider.NodeConfig{PortIP: "198.51.100.10"}
			p.sshClient = utils.NewSafeShellExecutor(executor)
			if err := p.setupDeviceProxyRangeMapping("guest", 22000, 22009, protocol, "192.0.2.10"); err != nil {
				t.Fatal(err)
			}
			want := 1
			if protocol == "both" {
				want = 2
			}
			var deviceCommands []string
			for _, command := range executor.commands {
				if strings.Contains(command, "config device add") {
					deviceCommands = append(deviceCommands, command)
				}
			}
			if len(deviceCommands) != want {
				t.Fatalf("range commands = %v", executor.commands)
			}
			for _, command := range deviceCommands {
				if !strings.Contains(command, "nat=true") || !strings.Contains(command, "198.51.100.10:22000-22009") || !strings.Contains(command, "192.0.2.10:22000-22009") {
					t.Fatalf("VM range proxy violates runtime NAT requirements: %s", command)
				}
			}
		})
	}
}

type natListenTransport struct {
	t         *testing.T
	addresses []string
}

func (tr *natListenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.URL.Path != "/1.0" {
		tr.t.Fatalf("unexpected host discovery request: %s %s", req.Method, req.URL.Path)
	}
	if _, ok := req.Context().Deadline(); !ok {
		tr.t.Fatal("host discovery must have a timeout")
	}
	body, err := json.Marshal(map[string]interface{}{"metadata": map[string]interface{}{"environment": map[string]interface{}{"addresses": tr.addresses}}})
	if err != nil {
		tr.t.Fatal(err)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
}

func TestLXDProxyDiscoversMissingHostAddressFamily(t *testing.T) {
	for _, tt := range []struct {
		name, portIP, output, want string
		ipv6                       bool
	}{
		{"IPv6 port IP with IPv4 guest", "2001:db8::1", "192.0.2.1/24\n", "192.0.2.1", false},
		{"IPv4 port IP with IPv6 guest", "192.0.2.1", "2001:db8::1/64\n", "2001:db8::1", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			executor := &recordingLXDIPv6Executor{outputs: []string{tt.output}}
			p := NewLXDProvider().(*LXDProvider)
			p.config = provider.NodeConfig{Host: tt.portIP, PortIP: tt.portIP}
			p.sshClient = utils.NewSafeShellExecutor(executor)
			got, err := p.getNATProxyListenIP(context.Background(), tt.ipv6)
			if err != nil || got != tt.want || len(executor.commands) != 1 {
				t.Fatalf("host discovery = %q, %v, commands %v", got, err, executor.commands)
			}
		})
	}
}

func TestLXDProxyUsesAPIHostDiscoveryAndRejectsMissingFamily(t *testing.T) {
	p := NewLXDProvider().(*LXDProvider)
	p.config = provider.NodeConfig{Host: "127.0.0.1"}
	p.apiClient = &http.Client{Transport: &natListenTransport{t: t, addresses: []string{"0.0.0.0:8443", "[2001:db8::1]:8443", "192.0.2.1:8443"}}}
	for _, ipv6 := range []bool{false, true} {
		want := "192.0.2.1"
		if ipv6 {
			want = "2001:db8::1"
		}
		if got, err := p.getNATProxyListenIP(context.Background(), ipv6); err != nil || got != want {
			t.Fatalf("API host discovery = %q, %v; want %q", got, err, want)
		}
	}
	p.apiClient = &http.Client{Transport: &natListenTransport{t: t, addresses: []string{"0.0.0.0:8443", "[::]:8443", "192.0.2.1:8443"}}}
	if _, err := p.getNATProxyListenIP(context.Background(), true); err == nil {
		t.Fatal("missing host IPv6 silently fell back to a wildcard")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.getNATProxyListenIP(ctx, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discovery returned %v", err)
	}
}
