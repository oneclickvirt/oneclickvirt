package middleware

import (
	"net/http"

	"oneclickvirt/global"
	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// DatabaseHealthCheck 是数据库健康检查中间件。
//
// 使用后台心跳统计信息判断连接状态，避免每次请求同步 Ping。
func DatabaseHealthCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		if global.APP_DB == nil {
			global.APP_LOG.Error("数据库实例未初始化",
				zap.String("path", c.Request.URL.Path),
			)
			c.JSON(http.StatusServiceUnavailable, common.NewError(
				common.CodeDatabaseError,
				"数据库服务暂时不可用，请稍后重试",
			))
			c.Abort()
			return
		}

		// 使用后台心跳的连接状态（由 DatabaseManager 定期更新）而非每次请求同步 Ping
		if stats := global.APP_DB_MANAGER_STATS; stats != nil && !stats.Connected {
			global.APP_LOG.Error("数据库连接已断开（来自心跳监控）",
				zap.String("path", c.Request.URL.Path),
			)
			c.JSON(http.StatusServiceUnavailable, common.NewError(
				common.CodeDatabaseError,
				"数据库连接异常，请稍后重试",
			))
			c.Abort()
			return
		}

		c.Next()
	}
}
