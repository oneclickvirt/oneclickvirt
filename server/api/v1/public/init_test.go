package public

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"oneclickvirt/config"
	"oneclickvirt/global"
	systemService "oneclickvirt/service/system"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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

func TestCheckInitWaitsForPersistentJWTSecret(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "init.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create users table: %v", err)
	}
	if err := db.Exec("INSERT INTO users (id) VALUES (1)").Error; err != nil {
		t.Fatalf("seed users table: %v", err)
	}

	oldDB, oldLog, oldSecret := global.APP_DB, global.APP_LOG, global.APP_JWT_SECRET
	oldConfigReady := global.CONFIG_MANAGER_READY.Load()
	global.APP_DB = db
	global.APP_LOG = zap.NewNop()
	global.APP_JWT_SECRET = ""
	global.CONFIG_MANAGER_READY.Store(true)
	config.PreInitializeConfigManager(db, global.APP_LOG, nil)
	t.Cleanup(func() {
		global.APP_DB = oldDB
		global.APP_LOG = oldLog
		global.APP_JWT_SECRET = oldSecret
		global.CONFIG_MANAGER_READY.Store(oldConfigReady)
	})

	gin.SetMode(gin.TestMode)
	waiting := callCheckInit(t)
	if waiting.Data.NeedInit || waiting.Data.Ready || waiting.Data.State != "starting" {
		t.Fatalf("JWT-not-ready status: %+v", waiting.Data)
	}

	global.APP_JWT_SECRET = "persistent-test-secret"
	ready := callCheckInit(t)
	if ready.Data.NeedInit || !ready.Data.Ready || ready.Data.State != "ready" {
		t.Fatalf("JWT-ready status: %+v", ready.Data)
	}
}

func TestPublicSystemConfigUsesCurrentLanguageAndDisablesCaching(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "config.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&config.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	oldDB, oldLog, oldConfig := global.APP_DB, global.APP_LOG, global.GetAppConfig()
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	global.UpdateAppConfig(func(cfg *config.Server) {
		cfg.Other.DefaultLanguage = "en-US"
	})
	t.Cleanup(func() {
		global.APP_DB, global.APP_LOG = oldDB, oldLog
		global.SetAppConfig(oldConfig)
	})
	gin.SetMode(gin.TestMode)

	readConfig := func() map[string]string {
		t.Helper()
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/public/system-config", nil)
		GetPublicSystemConfig(ctx)
		if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unexpected response: %d %v", recorder.Code, recorder.Header())
		}
		var response struct{ Data map[string]string }
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Data
	}
	if err := db.Create(&config.SystemConfig{Key: "other.site-name", Value: "Demo", IsPublic: true}).Error; err != nil {
		t.Fatal(err)
	}
	if result := readConfig(); result["default_language"] != "en-US" || result["site_name"] != "Demo" {
		t.Fatalf("unexpected config fallback: %v", result)
	}

	savedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rows := []config.SystemConfig{
		{Key: "default_language", Value: "zh", IsPublic: true},
		{Key: "other.default-language", Value: "", IsPublic: true, CreatedAt: savedAt.Add(-time.Hour), UpdatedAt: savedAt},
		{Key: "other.default-language", Value: "zh", IsPublic: true, CreatedAt: savedAt.Add(-time.Hour), UpdatedAt: savedAt.Add(-time.Minute)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if result := readConfig(); result["default_language"] != "" || result["default_language_revision"] != savedAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected saved language: %v", result)
	}
}
