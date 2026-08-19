package middleware

import (
	"fmt"
	"net/http"
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
	const prefix = "/api/v1/user/instances/"
	path := c.Request.URL.Path
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < 3 || parts[1] != "console" {
		return false
	}
	if _, err := strconv.ParseUint(parts[0], 10, 32); err != nil {
		return false
	}
	if parts[2] == "spice-ws" {
		return len(parts) == 3
	}
	return parts[2] == "spice" && len(parts) >= 4
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
	return strings.EqualFold(strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0]), "https")
}

// IssueConsoleSessionCookie grants only the browser resources of one instance
// a short, HttpOnly session. It is accepted only for SPICE assets and its
// WebSocket endpoint; all normal APIs still require the header/query token.
func IssueConsoleSessionCookie(c *gin.Context, instanceID uint) {
	token := requestAuthToken(c)
	if token == "" {
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(
		ConsoleSessionCookieName,
		token,
		int(consoleSessionLifetime.Seconds()),
		fmt.Sprintf("/api/v1/user/instances/%d/console", instanceID),
		"",
		consoleSessionCookieSecure(c),
		true,
	)
}
