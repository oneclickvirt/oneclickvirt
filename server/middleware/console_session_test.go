package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newConsoleSessionContext(method, target string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(method, target, nil)
	return c
}

func TestConsoleSessionCookieRequestIsStrictlyScoped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{name: "spice asset", method: http.MethodGet, path: "/api/v1/user/instances/42/console/spice/spice_auto.html", want: true},
		{name: "spice websocket", method: http.MethodGet, path: "/api/v1/user/instances/42/console/spice-ws", want: true},
		{name: "generic console websocket", method: http.MethodGet, path: "/api/v1/user/instances/42/console/ws", want: false},
		{name: "other instance api", method: http.MethodGet, path: "/api/v1/user/instances/42/ports", want: false},
		{name: "mutation", method: http.MethodPost, path: "/api/v1/user/instances/42/console/spice/spice_auto.html", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isConsoleSessionCookieRequest(newConsoleSessionContext(tc.method, tc.path)); got != tc.want {
				t.Fatalf("isConsoleSessionCookieRequest() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRequestAuthTokenUsesScopedConsoleCookieOnlyForSpice(t *testing.T) {
	c := newConsoleSessionContext(http.MethodGet, "/api/v1/user/instances/42/console/spice/spice_auto.html")
	c.Request.AddCookie(&http.Cookie{Name: ConsoleSessionCookieName, Value: "console-token"})
	if got := requestAuthToken(c); got != "" {
		t.Fatalf("requestAuthToken() = %q, want empty before cookie fallback", got)
	}
	if got := consoleSessionCookieToken(c); got != "console-token" {
		t.Fatalf("consoleSessionCookieToken() = %q, want scoped cookie", got)
	}

	other := newConsoleSessionContext(http.MethodGet, "/api/v1/user/instances/42/ports")
	other.Request.AddCookie(&http.Cookie{Name: ConsoleSessionCookieName, Value: "console-token"})
	if got := consoleSessionCookieToken(other); got != "" {
		t.Fatalf("consoleSessionCookieToken() = %q, want empty for unrelated endpoint", got)
	}
}
