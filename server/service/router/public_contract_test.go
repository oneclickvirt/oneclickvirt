package router

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPublicShareConsoleRouteContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	InitPublicRouter(engine.Group("/api"))

	routes := make(map[string]struct{})
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, route := range []string{
		"GET /api/v1/public/instance-shares/:token/console",
		"POST /api/v1/public/instance-shares/:token/console/repair",
		"GET /api/v1/public/instance-shares/:token/console/ws",
		"GET /api/v1/public/instance-shares/:token/console/terminal/ws",
		"GET /api/v1/public/instance-shares/:token/console/spice-ws",
		"GET /api/v1/public/instance-shares/:token/console/spice/*path",
	} {
		if _, ok := routes[route]; !ok {
			t.Fatalf("required public share console route is not registered: %s", route)
		}
	}
}
