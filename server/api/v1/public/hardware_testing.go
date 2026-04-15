package public

import (
	"strconv"

	"oneclickvirt/model/common"
	adminProviderService "oneclickvirt/service/admin/provider"

	"github.com/gin-gonic/gin"
)

// GetProviderHardwareReport 用户查看宿主机硬件报告
// @Summary 获取Provider硬件报告（用户端）
// @Tags Public
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response
// @Router /public/providers/{id}/hardware-report [get]
func GetProviderHardwareReport(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	svc := adminProviderService.NewService()
	report, err := svc.GetHardwareTestReport(c.Request.Context(), uint(providerID))
	if err != nil || report.ReportText == "" {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "暂无测试报告"))
		return
	}

	common.ResponseSuccess(c, gin.H{
		"providerId": report.ProviderID,
		"reportText": report.ReportText,
		"pasteUrl":   report.PasteURL,
		"updatedAt":  report.UpdatedAt,
	})
}
