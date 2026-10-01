package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReverseAgentClientDoesNotProbePublicHTTPWhenWebSocketIsOffline(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := &Client{
		baseURL:     server.URL,
		token:       "provider-token",
		httpClient:  server.Client(),
		providerID:  999999991,
		isAgentMode: true,
	}
	for _, path := range []string{"/api/v1/list", "/api/v1/domain-proxy"} {
		if err := client.doRequest(http.MethodGet, path, nil, nil); err == nil || !strings.Contains(err.Error(), "agent not connected") {
			t.Fatalf("offline reverse Agent request %s returned %v", path, err)
		}
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("reverse Agent probed public HTTP endpoint %d times", got)
	}
}
