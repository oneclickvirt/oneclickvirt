package middleware

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const ConsoleSessionCookieName = "oneclickvirt_console_session"

const consoleSessionLifetime = 5 * time.Minute

func requestAuthToken(c *gin.Context) string {
	token := strings.TrimSpace(c.GetHeader("Authorization"))
	if token == "" {
		token = strings.TrimSpace(c.Query("token"))
	}
	if after, ok := strings.CutPrefix(token, "Bearer "); ok {
		return after
	}
	return token
}

func isConsoleSessionCookieRequest(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet {
		return false
	}
	path := c.Request.URL.Path
	for _, prefix := range []string{
		"/api/v1/user/instances/",
		"/api/v1/admin/instances/",
	} {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
		if len(parts) < 3 || parts[1] != "console" {
			return false
		}
		if _, err := strconv.ParseUint(parts[0], 10, 32); err != nil {
			return false
		}
		switch parts[2] {
		case "ws", "spice-ws":
			return len(parts) == 3
		case "terminal":
			return len(parts) == 4 && parts[3] == "ws"
		case "spice":
			return len(parts) >= 4
		default:
			return false
		}
	}
	return false
}

func consoleSessionCookieToken(c *gin.Context) string {
	if !isConsoleSessionCookieRequest(c) {
		return ""
	}
	token, err := c.Cookie(ConsoleSessionCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(token)
}

func consoleSessionCookieSecure(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0]), "https") {
		return true
	}
	// A TLS-terminating proxy can legitimately reach the controller over HTTP
	// and overwrite X-Forwarded-Proto with that internal hop. FRONTEND_URL is
	// operator-controlled configuration, so it is a safe production fallback
	// while local HTTP deployments keep a non-Secure cookie.
	frontendURL, err := url.Parse(strings.TrimSpace(os.Getenv("FRONTEND_URL")))
	return err == nil && strings.EqualFold(frontendURL.Scheme, "https")
}

func consoleSessionCookiePath(c *gin.Context, instanceID uint) (string, bool) {
	path := c.Request.URL.Path
	if strings.HasPrefix(path, "/api/v1/admin/instances/") {
		return fmt.Sprintf("/api/v1/admin/instances/%d/console", instanceID), true
	}
	if strings.HasPrefix(path, "/api/v1/user/instances/") {
		return fmt.Sprintf("/api/v1/user/instances/%d/console", instanceID), true
	}
	return "", false
}

// IssueConsoleSessionCookie grants only the browser resources of one instance
// a short, HttpOnly session. It is accepted only for the selected console
// WebSockets and SPICE assets; all normal APIs still require the header/query
// token.
func IssueConsoleSessionCookie(c *gin.Context, instanceID uint) {
	token := requestAuthToken(c)
	if token == "" {
		return
	}
	cookiePath, ok := consoleSessionCookiePath(c, instanceID)
	if !ok {
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(
		ConsoleSessionCookieName,
		token,
		int(consoleSessionLifetime.Seconds()),
		cookiePath,
		"",
		consoleSessionCookieSecure(c),
		true,
	)
}
