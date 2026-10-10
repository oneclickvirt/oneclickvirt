package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestSelectAgentWebSocketURLFallsBackToPlainWSWithoutLeakingQuery(t *testing.T) {
	router := gin.New()
	router.GET("/api/v1/ws/agent", func(c *gin.Context) {
		if c.Query("probe") != "1" || c.Request.URL.RawQuery != "probe=1" {
			t.Fatalf("unexpected probe query: %q", c.Request.URL.RawQuery)
		}
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(c.Writer, c.Request, nil)
		if err == nil {
			_ = conn.Close()
		}
	})
	server := httptest.NewServer(router)
	defer server.Close()

	inferred := "wss" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws/agent?secret=must-not-leak"
	selected := selectAgentWebSocketURL(t.Context(), inferred, nil)
	wantPrefix := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws/agent"
	if selected != wantPrefix {
		t.Fatalf("selected URL = %q, want %q", selected, wantPrefix)
	}
}

func TestSelectAgentWebSocketURLRemovesQueryWhenBothProbesFail(t *testing.T) {
	selected := selectAgentWebSocketURL(t.Context(), "wss://127.0.0.1:1/api/v1/ws/agent?secret=must-not-leak", nil)
	if selected != "wss://127.0.0.1:1/api/v1/ws/agent" {
		t.Fatalf("selected URL = %q, want a credential-free fallback URL", selected)
	}
}
