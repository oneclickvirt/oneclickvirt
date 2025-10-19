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
	// 非嵌入模式下不需要设置静态路由
	// 前端将独立部署
	return nil
}
