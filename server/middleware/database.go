package middleware

import (
	"oneclickvirt/global"
	"oneclickvirt/model/common"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const databaseUnavailableLogInterval = 30 * time.Second

var lastDatabaseUnavailableLogAt atomic.Int64

func shouldLogDatabaseUnavailable(now time.Time) bool {
	nowUnixNano := now.UnixNano()
	for {
		previous := lastDatabaseUnavailableLogAt.Load()
		if previous != 0 && nowUnixNano-previous < int64(databaseUnavailableLogInterval) {
			return false
		}
		if lastDatabaseUnavailableLogAt.CompareAndSwap(previous, nowUnixNano) {
			return true
		}
	}
}

func logDatabaseUnavailable(message, path string) {
	if global.APP_LOG == nil || !shouldLogDatabaseUnavailable(time.Now()) {
		return
	}
	global.APP_LOG.Warn(message, zap.String("path", path))
}

// DatabaseHealthCheck 是数据库健康检查中间件。
//
// 使用后台心跳统计信息判断连接状态，避免每次请求同步 Ping。
func DatabaseHealthCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		if global.APP_DB == nil {
			logDatabaseUnavailable("数据库实例未初始化", c.Request.URL.Path)
			common.ResponseWithError(c, common.NewError(common.CodeDatabaseError, "数据库服务暂时不可用，请稍后重试"))
			c.Abort()
			return
		}

		// 使用后台心跳的连接状态（由 DatabaseManager 定期更新）而非每次请求同步 Ping
		if stats := global.GetDBManagerStats(); stats != nil && !stats.Connected {
			logDatabaseUnavailable("数据库连接已断开（来自心跳监控）", c.Request.URL.Path)
			common.ResponseWithError(c, common.NewError(common.CodeDatabaseError, "数据库连接异常，请稍后重试"))
			c.Abort()
			return
		}

		c.Next()
	}
}
