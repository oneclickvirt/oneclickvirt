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
		{name: "admin spice asset", method: http.MethodGet, path: "/api/v1/admin/instances/42/console/spice/spice_auto.html", want: true},
		{name: "admin spice websocket", method: http.MethodGet, path: "/api/v1/admin/instances/42/console/spice-ws", want: true},
		{name: "VNC websocket", method: http.MethodGet, path: "/api/v1/user/instances/42/console/ws", want: true},
		{name: "admin VNC websocket", method: http.MethodGet, path: "/api/v1/admin/instances/42/console/ws", want: true},
		{name: "terminal websocket", method: http.MethodGet, path: "/api/v1/user/instances/42/console/terminal/ws", want: true},
		{name: "terminal endpoint without websocket suffix", method: http.MethodGet, path: "/api/v1/user/instances/42/console/terminal", want: false},
		{name: "console repair", method: http.MethodGet, path: "/api/v1/user/instances/42/console/repair", want: false},
		{name: "share spice websocket is token scoped", method: http.MethodGet, path: "/api/v1/public/instance-shares/token/console/spice-ws", want: false},
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

func TestIssueConsoleSessionCookieUsesAdminConsolePath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/instances/77/console", nil)
	c.Request.Header.Set("Authorization", "Bearer test-token")

	IssueConsoleSessionCookie(c, 77)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	if cookies[0].Path != "/api/v1/admin/instances/77/console" {
		t.Fatalf("cookie path = %q", cookies[0].Path)
	}
	if !cookies[0].HttpOnly {
		t.Fatal("console cookie must be HttpOnly")
	}
}

func TestConsoleSessionCookieSecureUsesFrontendURLFallback(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://panel.example.test")
	c := newConsoleSessionContext(http.MethodGet, "/api/v1/admin/instances/77/console")
	if !consoleSessionCookieSecure(c) {
		t.Fatal("HTTPS FRONTEND_URL must make the console cookie Secure")
	}

	t.Setenv("FRONTEND_URL", "http://localhost:5173")
	if consoleSessionCookieSecure(c) {
		t.Fatal("HTTP FRONTEND_URL must preserve local non-Secure cookie support")
	}

	c.Request.Header.Set("X-Forwarded-Proto", "https")
	if !consoleSessionCookieSecure(c) {
		t.Fatal("HTTPS X-Forwarded-Proto must make the console cookie Secure")
	}
}

func TestRequestAuthTokenUsesScopedConsoleCookieOnlyForConsoleResources(t *testing.T) {
	for _, path := range []string{
		"/api/v1/user/instances/42/console/spice/spice_auto.html",
		"/api/v1/user/instances/42/console/ws",
		"/api/v1/user/instances/42/console/terminal/ws",
	} {
		c := newConsoleSessionContext(http.MethodGet, path)
		c.Request.AddCookie(&http.Cookie{Name: ConsoleSessionCookieName, Value: "console-token"})
		if got := requestAuthToken(c); got != "" {
			t.Fatalf("requestAuthToken(%q) = %q, want empty before cookie fallback", path, got)
		}
		if got := consoleSessionCookieToken(c); got != "console-token" {
			t.Fatalf("consoleSessionCookieToken(%q) = %q, want scoped cookie", path, got)
		}
	}

	other := newConsoleSessionContext(http.MethodGet, "/api/v1/user/instances/42/ports")
	other.Request.AddCookie(&http.Cookie{Name: ConsoleSessionCookieName, Value: "console-token"})
	if got := consoleSessionCookieToken(other); got != "" {
		t.Fatalf("consoleSessionCookieToken() = %q, want empty for unrelated endpoint", got)
	}
}
