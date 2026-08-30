package proxmox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubmitProxmoxAPIRequestPayloadEncoding(t *testing.T) {
	tests := []struct {
		name            string
		method          string
		path            string
		payload         []byte
		wantContentType string
	}{
		{
			name:            "empty lifecycle mutation",
			method:          http.MethodPost,
			path:            "/api2/json/nodes/pve-node/lxc/100/status/start",
			wantContentType: "",
		},
		{
			name:            "JSON config mutation",
			method:          http.MethodPut,
			path:            "/api2/json/nodes/pve-node/lxc/100/config",
			payload:         []byte(`{"memory":1024}`),
			wantContentType: "application/json",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != test.method {
					t.Errorf("method = %q, want %q", req.Method, test.method)
				}
				if req.URL.Path != test.path {
					t.Errorf("path = %q, want %q", req.URL.Path, test.path)
				}

				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				if got := req.Header.Get("Content-Type"); got != test.wantContentType {
					t.Errorf("Content-Type = %q, want %q", got, test.wantContentType)
				}
				if got := string(body); got != string(test.payload) {
					t.Errorf("request body = %q, want %q", got, test.payload)
				}

				// This models the PVE parser: declaring JSON while sending no JSON
				// causes its API layer to reject the lifecycle command at EOF.
				if len(test.payload) == 0 && req.Header.Get("Content-Type") != "" {
					http.Error(w, "malformed JSON string", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":null}`))
			}))
			defer server.Close()

			p := NewProxmoxProvider().(*ProxmoxProvider)
			p.apiClient = server.Client()
			data, err := p.submitProxmoxAPIRequest(context.Background(), test.method, server.URL+test.path, test.payload)
			if err != nil {
				t.Fatalf("submitProxmoxAPIRequest() error = %v", err)
			}
			if got := string(data); got != "null" {
				t.Fatalf("response data = %q, want null", got)
			}
		})
	}
}
