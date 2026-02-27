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
// 在需要数据库的路由组前进行快速共三步检查：
//  1. 判断全局数据库实例是否已初始化；
//  2. 获取底层 *sql.DB 连接对象；
//  3. 执行 Ping 验证连接可用性。
//
// 任一步骤失败将返回 503，防止请求进入业务层。
func DatabaseHealthCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 步骤 1：检查数据库实例是否存在
		if global.APP_DB == nil {
			global.APP_LOG.Error("数据库实例未初始化",
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method),
			)
			c.JSON(http.StatusServiceUnavailable, common.NewError(
				common.CodeDatabaseError,
				"数据库服务暂时不可用，请稍后重试",
			))
			c.Abort()
			return
		}

		// 步骤 2：获取底层 SQL 连接
		sqlDB, err := global.APP_DB.DB()
		if err != nil {
			global.APP_LOG.Error("获取数据库底层连接失败",
				zap.Error(err),
				zap.String("path", c.Request.URL.Path),
			)
			c.JSON(http.StatusServiceUnavailable, common.NewError(
				common.CodeDatabaseError,
				"数据库服务暂时不可用，请稍后重试",
			))
			c.Abort()
			return
		}

		// 步骤 3：快速 Ping 测试连接是否活跃
		if err := sqlDB.Ping(); err != nil {
			global.APP_LOG.Error("数据库连接 Ping 失败",
				zap.Error(err),
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
