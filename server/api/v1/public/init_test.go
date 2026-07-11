package public

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"oneclickvirt/global"
	systemService "oneclickvirt/service/system"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type initStatusResponse struct {
	Data struct {
		NeedInit bool   `json:"needInit"`
		Ready    bool   `json:"ready"`
		State    string `json:"state"`
	} `json:"data"`
}

func callCheckInit(t *testing.T) initStatusResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/public/init/check", nil)
	CheckInit(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; response=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var response initStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

func TestCheckInitRequiresSetupWithoutDatabaseOrMarker(t *testing.T) {
	t.Chdir(t.TempDir())
	oldDB, oldLog := global.APP_DB, global.APP_LOG
	global.APP_DB = nil
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB = oldDB
		global.APP_LOG = oldLog
	})

	gin.SetMode(gin.TestMode)
	response := callCheckInit(t)
	if !response.Data.NeedInit || response.Data.Ready || response.Data.State != "database_unavailable" {
		t.Fatalf("unexpected init status: %+v", response.Data)
	}
}

func TestCheckInitDoesNotReinitializeExistingDeploymentDuringDatabaseOutage(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := systemService.EnsureSystemInitializedMarker(); err != nil {
		t.Fatalf("create marker: %v", err)
	}

	oldDB, oldLog := global.APP_DB, global.APP_LOG
	global.APP_DB = nil
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB = oldDB
		global.APP_LOG = oldLog
	})

	gin.SetMode(gin.TestMode)
	response := callCheckInit(t)
	if response.Data.NeedInit || response.Data.Ready || response.Data.State != "database_unavailable" {
		t.Fatalf("unexpected init status: %+v", response.Data)
	}
}
