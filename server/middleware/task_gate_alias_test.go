package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAliasEditsAreMetadataWhileTaskOperationsStayControlled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodPut, "/api/v1/user/instances/:id/alias", true},
		{http.MethodPut, "/api/v1/admin/instances/:id/alias", true},
		{http.MethodPut, "/api/v1/admin/instances/:id", false},
		{http.MethodPost, "/api/v1/admin/instances/:id/action", false},
		{http.MethodPost, "/api/v1/user/instances/action", false},
		{http.MethodPost, "/api/v1/user/instances/:id/alias", false},
	} {
		engine := gin.New()
		engine.Handle(tc.method, tc.path, func(c *gin.Context) {
			if got := isTaskPoolAdmissionExempt(c); got != tc.want {
				t.Errorf("%s %s exempt=%v", tc.method, tc.path, got)
			}
		})
		engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))
	}
}
