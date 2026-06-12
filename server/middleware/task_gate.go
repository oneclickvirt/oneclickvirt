package middleware

import (
	"net/http"
	"strings"

	"oneclickvirt/model/common"
	"oneclickvirt/service/taskgate"

	"github.com/gin-gonic/gin"
)

// TaskPoolAdmissionGate rejects new mutating operations while the task pool is
// closed. Read-only requests and maintenance controls needed to reopen or drain
// existing tasks are allowed through.
func TaskPoolAdmissionGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isTaskPoolControlledMethod(c.Request.Method) || isTaskPoolAdmissionExempt(c) {
			c.Next()
			return
		}
		if err := taskgate.EnsureAccepting(); err != nil {
			common.ResponseWithError(c, common.ClassifyError(err))
			c.Abort()
			return
		}
		c.Next()
	}
}

func isTaskPoolControlledMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isTaskPoolAdmissionExempt(c *gin.Context) bool {
	path := c.FullPath()
	if path == "" && c.Request != nil && c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	path = strings.TrimSpace(path)
	method := c.Request.Method

	if method == http.MethodPut && strings.HasSuffix(path, "/v1/admin/tasks/pool-status") {
		return true
	}
	if method == http.MethodPost {
		switch {
		case strings.HasSuffix(path, "/v1/admin/tasks/force-stop"):
			return true
		case strings.Contains(path, "/v1/admin/tasks/") && strings.HasSuffix(path, "/cancel"):
			return true
		case strings.Contains(path, "/v1/user/tasks/") && strings.HasSuffix(path, "/cancel"):
			return true
		case strings.Contains(path, "/v1/admin/configuration-tasks/") && strings.HasSuffix(path, "/cancel"):
			return true
		}
	}
	return false
}
