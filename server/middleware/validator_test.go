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
