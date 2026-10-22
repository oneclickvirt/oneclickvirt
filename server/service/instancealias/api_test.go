package instancealias_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminAPI "oneclickvirt/api/v1/admin"
	userAPI "oneclickvirt/api/v1/user"
	"oneclickvirt/global"
	"oneclickvirt/middleware"
	authModel "oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	userInstance "oneclickvirt/service/user/instance"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAliasHTTPPermissionsClearAndOriginalNameSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	pool.SetMaxOpenConns(1)
	previousDB, previousLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() { global.APP_DB, global.APP_LOG = previousDB, previousLog; _ = pool.Close() })
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		"CREATE TABLE providers (id INTEGER PRIMARY KEY, owner_admin_id INTEGER, name TEXT, type TEXT, status TEXT, port_ip TEXT, endpoint TEXT, connection_type TEXT, network_type TEXT, traffic_quota_visible BOOLEAN, traffic_limited BOOLEAN, deleted_at DATETIME)",
		"CREATE TABLE ports (id INTEGER PRIMARY KEY, instance_id INTEGER, status TEXT, is_ssh BOOLEAN, created_at DATETIME, deleted_at DATETIME)",
		"CREATE TABLE tasks (id INTEGER PRIMARY KEY, instance_id INTEGER, status TEXT, created_at DATETIME, deleted_at DATETIME)",
		"CREATE TABLE users (id INTEGER PRIMARY KEY, traffic_limited BOOLEAN, deleted_at DATETIME)",
		"INSERT INTO providers (id, owner_admin_id, name, type, status) VALUES (7, 11, 'node', 'proxmox', 'active')",
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	instance := providerModel.Instance{ID: 3, Name: "host-vm", Provider: "node", ProviderID: 7, UserID: 9, ProviderVMID: "101", Status: "running"}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	request := func(userID uint, userType, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		engine := gin.New()
		engine.Use(func(c *gin.Context) {
			if userID > 0 {
				c.Set("auth_context", &authModel.AuthContext{UserID: userID, UserType: userType})
			}
		})
		engine.PUT("/api/v1/user/instances/:id/alias", middleware.TaskPoolAdmissionGate(), userAPI.UpdateUserInstanceAlias)
		engine.PUT("/api/v1/admin/instances/:id/alias", middleware.TaskPoolAdmissionGate(), adminAPI.UpdateAdminInstanceAlias)
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		return recorder
	}
	for _, tc := range []struct {
		id               uint
		role, path, body string
		code             int
	}{
		{9, "user", "/api/v1/user/instances/3/alias", `{"alias":"展示名称", "name":"wrong-host", "status":"deleted", "userId":8}`, 200},
		{8, "user", "/api/v1/user/instances/3/alias", `{"alias":"other"}`, 404},
		{0, "", "/api/v1/user/instances/3/alias", `{"alias":"anonymous"}`, 401},
		{9, "user", "/api/v1/user/instances/3/alias", `{}`, 400},
		{9, "user", "/api/v1/user/instances/3/alias", `{"alias":null}`, 400},
		{9, "user", "/api/v1/user/instances/3/alias", `{"alias":123}`, 400},
		{9, "user", "/api/v1/user/instances/0/alias", `{"alias":"x"}`, 400},
		{9, "user", "/api/v1/user/instances/invalid/alias", `{"alias":"x"}`, 400},
		{22, "normal_admin", "/api/v1/admin/instances/3/alias", `{"alias":"other admin"}`, 404},
	} {
		response := request(tc.id, tc.role, tc.path, tc.body)
		if response.Code != tc.code {
			t.Fatalf("%s %s: %d %s", tc.path, tc.body, response.Code, response.Body.String())
		}
	}
	var after providerModel.Instance
	if err := db.First(&after, 3).Error; err != nil {
		t.Fatal(err)
	}
	if after.Alias != "展示名称" || after.Name != "host-vm" || after.Status != "running" || after.UserID != 9 || after.ProviderVMID != "101" {
		t.Fatalf("alias request changed core fields: %+v", after)
	}
	service := userInstance.NewService()
	for name, want := range map[string]int64{"host-vm": 1, "展示名称": 0} {
		rows, total, err := service.GetUserInstances(9, userModel.UserInstanceListRequest{PageInfo: common.PageInfo{Page: 1, PageSize: 20}, Name: name})
		if err != nil || total != want {
			t.Fatalf("search %s: %d %v", name, total, err)
		}
		if want == 1 && (len(rows) != 1 || rows[0].Alias != "展示名称" || rows[0].Name != "host-vm") {
			t.Fatalf("list lost alias or name: %v", rows)
		}
	}
	detail, err := service.GetInstanceDetail(9, 3)
	if err != nil || detail.Alias != "展示名称" || detail.Name != "host-vm" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	for _, tc := range []struct {
		id          uint
		role, alias string
	}{{11, "normal_admin", "管理员备注"}, {1, "admin", "超级管理员备注"}, {9, "user", ""}} {
		path := "/api/v1/admin/instances/3/alias"
		if tc.role == "user" {
			path = "/api/v1/user/instances/3/alias"
		}
		body, _ := json.Marshal(map[string]string{"alias": tc.alias})
		response := request(tc.id, tc.role, path, string(body))
		if response.Code != 200 {
			t.Fatalf("update/clear failed: %s", response.Body.String())
		}
		var result struct {
			Data providerModel.InstanceAliasResponse `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Data.Alias != tc.alias {
			t.Fatalf("response: %s %v", response.Body.String(), err)
		}
	}
}
