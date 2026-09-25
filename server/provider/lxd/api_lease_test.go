package lxd

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"oneclickvirt/provider"
)

type lxdLeaseTransport struct {
	t                *testing.T
	stateUnavailable bool
}

func (tr lxdLeaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var payload string
	status := http.StatusOK
	switch req.URL.Path {
	case "/1.0/instances/guest/state":
		payload = `{"metadata":{"status":"Running","network":{}}}`
		if tr.stateUnavailable {
			status = http.StatusServiceUnavailable
		}
	case "/1.0/instances/guest":
		payload = `{"metadata":{"status":"Running","config":{"volatile.uplink.hwaddr":"00:16:3e:11:22:33"},"expanded_devices":{"uplink":{"type":"nic","network":"lxdbr0","name":"enp5s0"}},"devices":{},"profiles":["default"]}}`
	case "/1.0/networks/lxdbr0/leases":
		payload = `{"metadata":[{"address":"192.0.2.10","hwaddr":"00:16:3e:11:22:33","type":"dynamic"}]}`
	default:
		tr.t.Fatalf("unexpected API request %s", req.URL.Path)
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header), Request: req}, nil
}

func TestLXDVMIPv4UsesMatchingBridgeLeaseWithoutGuestAgent(t *testing.T) {
	for _, tc := range []struct {
		name             string
		stateUnavailable bool
	}{
		{name: "empty guest state"},
		{name: "guest state endpoint unavailable", stateUnavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewLXDProvider().(*LXDProvider)
			p.config = provider.NodeConfig{Host: "127.0.0.1", ExecutionRule: "api_only"}
			p.apiClient = &http.Client{Transport: lxdLeaseTransport{t: t, stateUnavailable: tc.stateUnavailable}}
			ip, metadata, err := p.apiWaitForInstanceIPv4(context.Background(), "guest")
			if err != nil || ip != "192.0.2.10" || metadata["_network_state"] == nil {
				t.Fatalf("wait for IPv4 = %q, %#v, %v", ip, metadata, err)
			}
			ip, err = p.GetInstanceIPv4(context.Background(), "guest")
			if err != nil || ip != "192.0.2.10" {
				t.Fatalf("GetInstanceIPv4 = %q, %v", ip, err)
			}
		})
	}
}
