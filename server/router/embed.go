//go:build embed
// +build embed

package router

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed dist
var FS embed.FS

// embedEnabled 标记是否启用了前端嵌入
const embedEnabled = true

// setupStaticRoutes 设置静态文件路由（嵌入模式）
func setupStaticRoutes(router *gin.Engine) error {
	// 获取嵌入的文件系统，去掉 dist 前缀
	staticFS, err := fs.Sub(FS, "dist")
	if err != nil {
		return err
	}

	// 创建 http.FileServer 来处理静态文件
	fileServer := http.FileServer(http.FS(staticFS))

	// NoRoute 处理 SPA 路由
	// 这是最后的兜底处理，所有未匹配的路由都返回 index.html
	router.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// 如果是 API 路径，返回 404 JSON 响应
		if isAPIPath(path) {
			c.JSON(http.StatusNotFound, gin.H{
				"code": 404,
				"msg":  "API endpoint not found",
			})
			return
		}

		// 去掉开头的斜杠
		path = strings.TrimPrefix(path, "/")

		// 尝试打开文件
		if f, err := staticFS.Open(path); err == nil {
			// 检查是否是文件
			if stat, err := f.Stat(); err == nil && !stat.IsDir() {
				f.Close()
				// 文件存在，使用 fileServer 处理
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
			f.Close()
		}

		// 文件不存在或是目录，返回 index.html（用于 SPA 路由）
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})

	return nil
}
