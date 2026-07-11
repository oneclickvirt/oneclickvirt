package config

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	configpkg "oneclickvirt/config"
	authModel "oneclickvirt/model/auth"

	"github.com/gin-gonic/gin"
)

type stubConfigGetter struct {
	values map[string]interface{}
}

func TestUpdateUnifiedConfigRejectsMalformedUnifiedPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
	}{
		{name: "scope is not a string", body: `{"scope":123,"config":{}}`},
		{name: "config is not an object", body: `{"scope":"admin","config":[]}`},
		{name: "scope is empty", body: `{"scope":"","config":{}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Set("auth_context", &authModel.AuthContext{UserID: 1})
			ctx.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/config", bytes.NewBufferString(tt.body))
			ctx.Request.Header.Set("Content-Type", "application/json")

			UpdateUnifiedConfig(ctx)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; response=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
		})
	}
}

func (s stubConfigGetter) GetConfig(key string) (interface{}, bool) {
	value, exists := s.values[key]
	return value, exists
}

func TestGetConfigBool(t *testing.T) {
	tests := []struct {
		name     string
		getter   configGetter
		key      string
		fallback bool
		want     bool
	}{
		{name: "nil getter uses fallback", getter: nil, key: "captcha.enabled", fallback: false, want: false},
		{name: "typed nil getter uses fallback", getter: (*configpkg.ConfigManager)(nil), key: "captcha.enabled", fallback: true, want: true},
		{name: "getter overrides fallback", getter: stubConfigGetter{values: map[string]interface{}{"captcha.enabled": true}}, key: "captcha.enabled", fallback: false, want: true},
		{name: "missing key uses fallback", getter: stubConfigGetter{values: map[string]interface{}{}}, key: "captcha.enabled", fallback: true, want: true},
		{name: "wrong type uses fallback", getter: stubConfigGetter{values: map[string]interface{}{"captcha.enabled": "true"}}, key: "captcha.enabled", fallback: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getConfigBool(tt.getter, tt.key, tt.fallback); got != tt.want {
				t.Fatalf("getConfigBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetConfigString(t *testing.T) {
	tests := []struct {
		name     string
		getter   configGetter
		key      string
		fallback string
		want     string
	}{
		{name: "nil getter uses fallback", getter: nil, key: "kyc.method", fallback: "manual", want: "manual"},
		{name: "typed nil getter uses fallback", getter: (*configpkg.ConfigManager)(nil), key: "kyc.method", fallback: "manual", want: "manual"},
		{name: "getter overrides fallback", getter: stubConfigGetter{values: map[string]interface{}{"kyc.method": "alipay"}}, key: "kyc.method", fallback: "manual", want: "alipay"},
		{name: "missing key uses fallback", getter: stubConfigGetter{values: map[string]interface{}{}}, key: "kyc.method", fallback: "manual", want: "manual"},
		{name: "wrong type uses fallback", getter: stubConfigGetter{values: map[string]interface{}{"kyc.method": true}}, key: "kyc.method", fallback: "manual", want: "manual"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getConfigString(tt.getter, tt.key, tt.fallback); got != tt.want {
				t.Fatalf("getConfigString() = %q, want %q", got, tt.want)
			}
		})
	}
}
