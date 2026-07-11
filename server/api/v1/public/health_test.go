package public

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"oneclickvirt/global"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestHealthCheckReturnsServiceUnavailableWhenDatabaseIsMissing(t *testing.T) {
	oldDB := global.APP_DB
	oldLog := global.APP_LOG
	global.APP_DB = nil
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB = oldDB
		global.APP_LOG = oldLog
	})

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)

	HealthCheck(ctx)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	var response struct {
		Data struct {
			Healthy bool `json:"healthy"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Data.Healthy {
		t.Fatal("health response unexpectedly reported healthy")
	}
}
