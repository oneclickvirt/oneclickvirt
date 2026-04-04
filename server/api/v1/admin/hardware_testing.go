package admin

import (
	"net/http"
	"strconv"

	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	adminProviderService "oneclickvirt/service/admin/provider"

	"github.com/gin-gonic/gin"
)

// RunHardwareTest 运行硬件测试
// @Summary 运行Provider硬件测试
// @Tags Admin-Provider
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/hardware-test [post]
func RunHardwareTest(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "参数错误"})
		return
	}

	authCtx, ok := middleware.GetAuthContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, common.Response{Code: 40100, Msg: "未授权"})
		return
	}

	svc := adminProviderService.NewService()

	if err := svc.RunHardwareTest(c.Request.Context(), uint(providerID), authCtx.UserID); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: "硬件测试已启动"})
}

// GetHardwareTestReport 获取硬件测试报告
// @Summary 获取Provider硬件测试报告
// @Tags Admin-Provider
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/hardware-test [get]
func GetHardwareTestReport(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "参数错误"})
		return
	}

	svc := adminProviderService.NewService()
	report, err := svc.GetHardwareTestReport(c.Request.Context(), uint(providerID))
	if err != nil {
		c.JSON(http.StatusOK, common.Response{Code: 0, Data: nil, Msg: "暂无测试报告"})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 0, Data: report})
}
