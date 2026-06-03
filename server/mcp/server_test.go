package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMCPRegistersMetricsResourceAndPrompt(t *testing.T) {
	s := NewMCPServer("http://127.0.0.1:8888", "token")

	toolsResp := s.HandleRequest(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	if toolsResp.Error != nil {
		t.Fatalf("tools/list returned error: %v", toolsResp.Error)
	}
	toolsJSON := mustJSON(t, toolsResp.Result)
	if !strings.Contains(toolsJSON, "get_metrics") {
		t.Fatalf("tools/list missing get_metrics: %s", toolsJSON)
	}

	resourcesResp := s.HandleRequest(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: "resources/list"})
	resourcesJSON := mustJSON(t, resourcesResp.Result)
	if !strings.Contains(resourcesJSON, "oneclickvirt://config/system") {
		t.Fatalf("resources/list missing config/system: %s", resourcesJSON)
	}

	promptsResp := s.HandleRequest(JSONRPCRequest{JSONRPC: "2.0", ID: 3, Method: "prompts/list"})
	promptsJSON := mustJSON(t, promptsResp.Result)
	if !strings.Contains(promptsJSON, "quick_status_check") {
		t.Fatalf("prompts/list missing quick_status_check: %s", promptsJSON)
	}
}

func TestMCPPromptGetRendersTemplate(t *testing.T) {
	s := NewMCPServer("http://127.0.0.1:8888", "token")
	params := mustRawJSON(t, map[string]interface{}{
		"name": "troubleshoot_instance",
		"arguments": map[string]interface{}{
			"instance_id": 42,
		},
	})

	resp := s.HandleRequest(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "prompts/get", Params: params})
	if resp.Error != nil {
		t.Fatalf("prompts/get returned error: %v", resp.Error)
	}
	resultJSON := mustJSON(t, resp.Result)
	if !strings.Contains(resultJSON, "instance 42") || !strings.Contains(resultJSON, "get_metrics") {
		t.Fatalf("prompt template did not include expected guidance: %s", resultJSON)
	}
}

func TestMCPMetricsAndResourceReadUseExpectedAPIPaths(t *testing.T) {
	var paths []string
	s := NewMCPServer("http://oneclickvirt.test", "test-token")
	s.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.String())
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization header = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    r,
		}, nil
	})}

	metricsParams := mustRawJSON(t, map[string]interface{}{
		"name": "get_metrics",
		"arguments": map[string]interface{}{
			"provider_id": 7,
		},
	})
	metricsResp := s.HandleRequest(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: metricsParams})
	if metricsResp.Error != nil {
		t.Fatalf("tools/call get_metrics returned error: %v", metricsResp.Error)
	}

	resourceParams := mustRawJSON(t, map[string]interface{}{
		"uri": "oneclickvirt://health/status",
	})
	resourceResp := s.HandleRequest(JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: "resources/read", Params: resourceParams})
	if resourceResp.Error != nil {
		t.Fatalf("resources/read returned error: %v", resourceResp.Error)
	}

	joined := strings.Join(paths, "\n")
	if !strings.Contains(joined, "/api/v1/admin/providers/7/monitoring/resources") {
		t.Fatalf("missing provider metrics path, got:\n%s", joined)
	}
	if !strings.Contains(joined, "/api/v1/health") {
		t.Fatalf("missing health resource path, got:\n%s", joined)
	}
}

func mustJSON(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	return string(data)
}

func mustRawJSON(t *testing.T, value interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	return data
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
