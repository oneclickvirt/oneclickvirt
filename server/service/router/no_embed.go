//go:build !embed
// +build !embed

package router

import (
	"github.com/gin-gonic/gin"
)

// embedEnabled 标记是否启用了前端嵌入
const embedEnabled = false

// setupStaticRoutes 设置静态文件路由（非嵌入模式，什么都不做）
func setupStaticRoutes(router *gin.Engine) error {
	// 非嵌入模式由独立前端处理静态资源，但 API 仍返回统一的诊断响应，
	// 避免版本错配时只得到无法定位来源的 "404 page not found"。
	router.NoRoute(apiNotFoundHandler)
	return nil
}
