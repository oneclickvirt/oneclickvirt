package lxd

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"oneclickvirt/provider"
)

type lxdOperationCaptureTransport struct {
	requests []string
	hosts    []string
}

func (t *lxdOperationCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, req.Method+" "+req.URL.Path)
	t.hosts = append(t.hosts, req.URL.Host)
	response := func(status int, payload string) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(payload)),
			Request:    req,
		}, nil
	}

	switch {
	case req.Method == http.MethodPut && req.URL.Path == "/1.0/instances/guest/state":
		return response(http.StatusAccepted, `{"type":"async","status":"Operation created","status_code":100,"operation":"/1.0/operations/start-guest"}`)
	case req.Method == http.MethodGet && req.URL.Path == "/1.0/operations/start-guest":
		return response(http.StatusOK, `{"type":"async","status":"Success","status_code":200}`)
	default:
		return response(http.StatusNotFound, `{"error":"unexpected request"}`)
	}
}

func TestLXDAPIMutationWaitsForOperationAndBracketsIPv6Host(t *testing.T) {
	oldPoll := lxdAPIOperationPollInterval
	lxdAPIOperationPollInterval = 0
	t.Cleanup(func() { lxdAPIOperationPollInterval = oldPoll })

	capture := &lxdOperationCaptureTransport{}
	p := NewLXDProvider().(*LXDProvider)
	p.config = provider.NodeConfig{Host: "2001:db8::20"}
	p.apiClient = &http.Client{Transport: capture}

	if err := p.apiStartInstance(context.Background(), "guest"); err != nil {
		t.Fatalf("apiStartInstance() error = %v", err)
	}
	wantRequests := []string{
		"PUT /1.0/instances/guest/state",
		"GET /1.0/operations/start-guest",
	}
	if strings.Join(capture.requests, ",") != strings.Join(wantRequests, ",") {
		t.Fatalf("requests = %v, want %v", capture.requests, wantRequests)
	}
	for _, host := range capture.hosts {
		if host != "[2001:db8::20]:8443" {
			t.Fatalf("request host = %q, want bracketed IPv6 API host", host)
		}
	}
}
