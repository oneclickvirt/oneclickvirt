package middleware

import (
	"net/http"

	"oneclickvirt/global"
	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// DatabaseHealthCheck 数据库健康检查中间件
// 在需要数据库的路由前执行，确保数据库连接可用
func DatabaseHealthCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 检查数据库实例是否为空
		if global.APP_DB == nil {
			global.APP_LOG.Error("数据库未初始化或连接已断开")
			c.JSON(http.StatusServiceUnavailable, common.NewError(
				common.CodeDatabaseError,
				"数据库服务暂时不可用，请稍后重试",
			))
			c.Abort()
			return
		}

		// 获取底层SQL连接并进行Ping测试
		sqlDB, err := global.APP_DB.DB()
		if err != nil {
			global.APP_LOG.Error("获取数据库连接失败: " + err.Error())
			c.JSON(http.StatusServiceUnavailable, common.NewError(
				common.CodeDatabaseError,
				"数据库服务暂时不可用，请稍后重试",
			))
			c.Abort()
			return
		}

		// 快速Ping测试数据库连接
		if err := sqlDB.Ping(); err != nil {
			global.APP_LOG.Error("数据库连接检查失败: " + err.Error())
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
