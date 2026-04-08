package public

import (
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// HealthCheck 系统健康检查
// @Tags Health
// @Summary 系统健康检查
// @Description 检查数据库连接和系统状态
// @Produce json
// @Success 200 {object} common.Response{data=map[string]interface{}}
// @Router /health [get]
func HealthCheck(c *gin.Context) {
	healthStatus := make(map[string]interface{})

	// 检查数据库健康状态
	dbHealthy := true
	dbError := ""

	if err := utils.CheckDBHealth(); err != nil {
		dbHealthy = false
		dbError = err.Error()
		global.APP_LOG.Error("数据库健康检查失败", zap.Error(err))
	}

	// 获取数据库连接统计
	dbStats := utils.GetDBStats()

	healthStatus["database"] = map[string]interface{}{
		"healthy": dbHealthy,
		"error":   dbError,
		"stats":   dbStats,
	}

	// 系统信息
	healthStatus["system"] = map[string]interface{}{
		"timestamp": time.Now(),
		"version":   "1.0.0",
		"uptime":    time.Since(time.Now().Add(-time.Hour)), // 示例运行时间
	}

	// 总体健康状态
	healthStatus["healthy"] = dbHealthy

	common.ResponseSuccess(c, healthStatus, "健康检查完成")
}

// DatabaseStatsAPI 数据库统计信息API
// @Tags Health
// @Summary 数据库统计信息
// @Description 获取详细的数据库连接池统计信息
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} common.Response{data=map[string]interface{}}
// @Router /admin/database/stats [get]
func DatabaseStatsAPI(c *gin.Context) {
	stats := utils.GetDBStats()

	common.ResponseSuccess(c, stats, "数据库统计信息获取成功")
}
