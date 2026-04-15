package public

import (
	"oneclickvirt/model/common"
	"oneclickvirt/service/resources"

	"github.com/gin-gonic/gin"
)

// GetDashboardStats 获取仪表板统计数据
// @Summary 获取仪表板统计数据
// @Description 获取系统总体统计信息，包括用户数、实例数等
// @Tags 仪表板
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 500 {object} common.Response "获取失败"
// @Router /dashboard/stats [get]
func GetDashboardStats(c *gin.Context) {
	dashboardService := resources.DashboardService{}
	stats, err := dashboardService.GetDashboardStats()
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, stats)
}
