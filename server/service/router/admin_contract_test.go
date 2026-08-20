package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAdminRouteContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	InitAdminRouter(engine.Group("/api"))

	routes := make(map[string]struct{})
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	required := []string{
		"POST /api/v1/admin/providers/:id/health-check-task",
		"GET /api/v1/admin/providers/:id/ipv6-pool",
		"GET /api/v1/admin/providers/:id/ipv6-tunnels",
		"POST /api/v1/admin/providers/:id/ipv6-tunnels/detect-local-ipv4",
		"POST /api/v1/admin/port-mappings/repair",
		"GET /api/v1/admin/instances/:id/console",
		"POST /api/v1/admin/instances/:id/console/repair",
		"GET /api/v1/admin/instances/:id/console/ws",
		"GET /api/v1/admin/instances/:id/console/terminal/ws",
		"GET /api/v1/admin/instances/:id/console/spice-ws",
		"GET /api/v1/admin/instances/:id/console/spice/*path",
	}
	for _, route := range required {
		if _, ok := routes[route]; !ok {
			t.Fatalf("required admin route is not registered: %s", route)
		}
	}
}

func TestMissingAPIRouteReturnsStructuredBuildDiagnostic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(buildMetadataHeaders())
	engine.NoRoute(apiNotFoundHandler)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/missing-route", nil)
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if got := recorder.Header().Get("X-OneClickVirt-API-Contract"); got == "" {
		t.Fatal("missing X-OneClickVirt-API-Contract response header")
	}
	var response map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["message"] != "API endpoint not found" {
		t.Fatalf("message = %v, want API endpoint not found", response["message"])
	}
	if response["api_contract"] == "" || response["build_commit"] == "" {
		t.Fatalf("missing build diagnostic fields: %v", response)
	}
}
