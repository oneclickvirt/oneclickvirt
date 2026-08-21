package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func buildInstanceVNCInfo(instanceID uint, userID uint, admin bool) (gin.H, error) {
	// Keep the legacy response shape while using the same observed target as the
	// multi-protocol route. This avoids an old API hiding VNC merely because a
	// discovered container or imported VM has a stale instance_type value.
	target, err := resolveInstanceConsoleTargetForProtocol(instanceID, userID, admin, consoleProtocolVNC)
	if err != nil {
		return gin.H{"enabled": false, "reason": err.Error()}, nil
	}
	return gin.H{"enabled": target.available, "reason": target.reason}, nil
}

func parseVNCDiscoveredPort(raw string) int {
	if raw == "" {
		return 0
	}
	var obj map[string]interface{}
	if json.Unmarshal([]byte(raw), &obj) != nil {
		return 0
	}
	var parse func(value interface{}) int
	parse = func(value interface{}) int {
		switch x := value.(type) {
		case float64:
			return int(x)
		case json.Number:
			n, _ := strconv.Atoi(string(x))
			return n
		case int:
			return x
		case string:
			n, _ := strconv.Atoi(strings.TrimSpace(x))
			return n
		case map[string]interface{}:
			for _, key := range []string{"port", "vncPort", "vnc_port"} {
				if n := parse(x[key]); n > 0 {
					return n
				}
			}
		}
		return 0
	}
	for _, key := range []string{"vncPort", "vnc_port", "vnc"} {
		if n := parse(obj[key]); n > 0 {
			return n
		}
	}
	console, _ := obj["console"].(map[string]interface{})
	if console == nil {
		return 0
	}
	for _, key := range []string{"vncPort", "vnc_port", "vnc"} {
		if n := parse(console[key]); n > 0 {
			return n
		}
	}
	if strings.EqualFold(strings.TrimSpace(fmt.Sprint(console["protocol"])), consoleProtocolVNC) {
		return parse(console["port"])
	}
	return 0
}

var vncUpgrader = websocket.Upgrader{
	ReadBufferSize:  32768,
	WriteBufferSize: 32768,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		appConfig := global.GetAppConfig()
		return utils.OriginAllowedForRequest(r, origin, appConfig.System.FrontendURL, appConfig.Cors.Whitelist)
	},
}
