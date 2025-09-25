package initialize

import (
	"oneclickvirt/router"

	"github.com/gin-gonic/gin"
)

// Routers 路由初始化 - 使用统一的router包
func Routers() *gin.Engine {
	return router.SetupRouter()
}
