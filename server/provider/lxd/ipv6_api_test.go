package lxd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"oneclickvirt/provider"
)

type lxdIPv6Transport struct {
	t       *testing.T
	calls   *int
	payload string
	status  int
}

func (tr lxdIPv6Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	*tr.calls++
	if req.URL.Path != "/1.0/instances/guest/state" {
		tr.t.Fatalf("unexpected resource: %s", req.URL.Path)
	}
	return &http.Response{StatusCode: tr.status, Body: io.NopCloser(strings.NewReader(tr.payload)), Header: make(http.Header), Request: req}, nil
}

func TestLXDIPv6APIOnlyAddressQueriesAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, payload, want string
		status              int
		wantErr             bool
		public              bool
	}{
		{name: "dual-stack", status: 200, want: "2606:4700::10", payload: `{"metadata":{"network":{"eth0":{"addresses":[{"family":"inet6","scope":"global","address":"fd00::10"}]},"eth1":{"addresses":[{"family":"inet6","scope":"global","address":"2606:4700::10"}]}}}}`},
		{name: "public", status: 200, want: "2606:4700::10", public: true, payload: `{"metadata":{"network":{"eth0":{"addresses":[{"family":"inet6","scope":"global","address":"fd00::10"}]},"eth1":{"addresses":[{"family":"inet6","scope":"global","address":"2606:4700::10"}]}}}}`},
		{name: "private fallback", status: 200, want: "fd00::10", payload: `{"metadata":{"network":{"eth0":{"addresses":[{"family":"inet6","scope":"global","address":"fd00::10"}]}}}}`},
		{name: "private is not public", status: 200, public: true, wantErr: true, payload: `{"metadata":{"network":{"eth0":{"addresses":[{"family":"inet6","scope":"global","address":"fd00::10"}]}}}}`},
		{name: "missing lease", status: 200, wantErr: true, payload: `{"metadata":{"network":{}}}`},
		{name: "API unavailable", status: 503, wantErr: true, payload: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewLXDProvider().(*LXDProvider)
			p.config = provider.NodeConfig{Host: "127.0.0.1", ExecutionRule: "api_only"}
			p.sshClient = nil
			calls := 0
			p.apiClient = &http.Client{Transport: lxdIPv6Transport{t: t, calls: &calls, payload: tc.payload, status: tc.status}}
			var address string
			var err error
			if tc.public {
				address, err = p.GetInstancePublicIPv6Context(context.Background(), "guest")
			} else {
				address, err = p.GetInstanceIPv6Context(context.Background(), "guest")
			}
			if address != tc.want || (err != nil) != tc.wantErr || calls != 1 {
				t.Fatalf("IPv6 query = %q, %v, requests=%d", address, err, calls)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = p.GetInstanceIPv6Context(ctx, "guest")
			if !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("cancelled IPv6 lookup issued I/O: error=%v requests=%d", err, calls)
			}
		})
	}
}
