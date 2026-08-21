package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestInputValidatorAllowsBase64URLPathTokenWithDoubleDash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(InputValidator())
	router.GET("/api/v1/public/instance-shares/:token", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, token := range []string{
		"yft4IHFtl4utv5YthjYgXDP-pvpaBlVddQdwao7s--g",
		"valid-token--",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/public/instance-shares/"+token, nil)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("token %q returned status %d, want %d; body=%s", token, recorder.Code, http.StatusNoContent, recorder.Body.String())
		}
	}
}

func TestInputValidatorRejectsEncodedBooleanSQLExpression(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(InputValidator())
	router.GET("/probe", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/probe?name=%27%20OR%201%3D1%20--", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("encoded SQL expression returned status %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestInputValidatorEventHandlerBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(InputValidator())
	router.GET("/probe", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "allows ordinary query identifier containing on",
			path:       "/probe?skipConflictCheck=true",
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "rejects event handler query parameter",
			path:       "/probe?onload=alert(1)",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "rejects encoded event handler attribute",
			path:       "/probe?name=%20onload%3Dalert(1)",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("path %q returned status %d, want %d; body=%s", test.path, recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}
