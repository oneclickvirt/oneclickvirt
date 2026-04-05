package admin

import (
	"net/http"
	"strconv"

	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	adminProviderService "oneclickvirt/service/admin/provider"

	"github.com/gin-gonic/gin"
)

// SaveHardwareReport 保存硬件报告（通过粘贴板URL）
// @Summary 通过粘贴板URL保存Provider硬件报告
// @Tags Admin-Provider
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Param data body object true "请求体" example({"pasteUrl":"https://paste.spiritlhl.net/#/show/xxx.txt"})
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/hardware-report [post]
func SaveHardwareReport(c *gin.Context) {
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

	var req struct {
		PasteURL string `json:"pasteUrl" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "请提供粘贴板URL"})
		return
	}

	svc := adminProviderService.NewService()
	report, err := svc.SaveHardwareReport(c.Request.Context(), uint(providerID), authCtx.UserID, req.PasteURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 0, Data: report, Msg: "报告已保存"})
}

// GetHardwareTestReport 获取硬件测试报告
// @Summary 获取Provider硬件测试报告
// @Tags Admin-Provider
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/hardware-report [get]
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

// DeleteHardwareReport 删除硬件报告
// @Summary 删除Provider硬件报告
// @Tags Admin-Provider
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/hardware-report [delete]
func DeleteHardwareReport(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 40000, Msg: "参数错误"})
		return
	}

	svc := adminProviderService.NewService()
	if err := svc.DeleteHardwareReport(c.Request.Context(), uint(providerID)); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{Code: 50000, Msg: err.Error()})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 0, Msg: "报告已删除"})
}
