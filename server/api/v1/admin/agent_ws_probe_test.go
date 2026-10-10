package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestAgentWebSocketAnonymousProbe(t *testing.T) {
	for _, tls := range []bool{false, true} {
		t.Run(map[bool]string{false: "ws", true: "wss"}[tls], func(t *testing.T) {
			router := gin.New()
			router.GET("/api/v1/ws/agent", AgentWebSocket)
			server := httptest.NewUnstartedServer(router)
			if tls {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			dialer := *websocket.DefaultDialer
			if tls {
				dialer.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig
			}
			url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws/agent?probe=1"
			conn, response, err := dialer.Dial(url, nil)
			if err != nil {
				t.Fatalf("probe handshake: %v", err)
			}
			defer conn.Close()
			if response.StatusCode != 101 {
				t.Fatalf("status = %d", response.StatusCode)
			}
			_, _, err = conn.ReadMessage()
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				t.Fatalf("probe should close without registering an Agent: %v", err)
			}
		})
	}
}

func TestAgentWebSocketStillRequiresSecret(t *testing.T) {
	router := gin.New()
	router.GET("/api/v1/ws/agent", AgentWebSocket)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/v1/ws/agent", nil))
	if !strings.Contains(recorder.Body.String(), "缺少 secret") {
		t.Fatalf("anonymous Agent registration was not rejected: %s", recorder.Body.String())
	}
}
