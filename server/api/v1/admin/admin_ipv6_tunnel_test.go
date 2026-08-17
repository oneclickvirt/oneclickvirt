package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"oneclickvirt/model/common"
	"oneclickvirt/service/ipv6tunnel"

	"github.com/gin-gonic/gin"
)

func TestIPv6TunnelRemoteFailureKeepsDiagnosticsOutsideCDNGatewayResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	responseIPv6TunnelError(ctx, &ipv6tunnel.RemoteCommandError{
		Operation: "识别隧道客户端IPv4失败",
		Output:    "route output has no src field",
		Cause:     errors.New("route source missing"),
	})

	if recorder.Code != common.CodeFailedDependency {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, common.CodeFailedDependency, recorder.Body.String())
	}
	if recorder.Code == http.StatusBadGateway {
		t.Fatal("remote node diagnostics must not be returned as a CDN-replaced 502")
	}

	var response struct {
		Code    int    `json:"code"`
		Details string `json:"details"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != common.CodeFailedDependency || !strings.Contains(response.Details, "route output has no src field") {
		t.Fatalf("diagnostic response = %#v", response)
	}
}
