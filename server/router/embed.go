//go:build embed
// +build embed

package router

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed dist
var webDist embed.FS

// embedEnabled 标记是否启用了前端嵌入
const embedEnabled = true

// setupStaticRoutes 设置静态文件路由（嵌入模式）
func setupStaticRoutes(router *gin.Engine) {
	// 获取嵌入的文件系统
	distFS, err := fs.Sub(webDist, "dist")
	if err != nil {
		panic("Failed to load embedded web files: " + err.Error())
	}

	// 使用自定义处理器来正确处理 SPA 路由
	router.NoRoute(func(c *gin.Context) {
		// 如果是 API 路径，返回 404
		if isAPIPath(c.Request.URL.Path) {
			c.JSON(http.StatusNotFound, gin.H{"error": "API endpoint not found"})
			return
		}

		// 尝试读取请求的文件
		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// 读取文件
		file, err := distFS.Open(path)
		if err != nil {
			// 如果文件不存在，返回 index.html（用于 SPA 路由）
			indexFile, indexErr := distFS.Open("index.html")
			if indexErr != nil {
				c.String(http.StatusNotFound, "404 page not found")
				return
			}
			defer indexFile.Close()

			stat, _ := indexFile.(fs.File)
			http.ServeContent(c.Writer, c.Request, "index.html", getModTime(stat), indexFile.(io.ReadSeeker))
			return
		}
		defer file.Close()

		// 获取文件信息
		stat, err := file.Stat()
		if err != nil {
			c.String(http.StatusInternalServerError, "Internal Server Error")
			return
		}

		// 如果是目录，返回 index.html
		if stat.IsDir() {
			indexFile, err := distFS.Open(path + "/index.html")
			if err != nil {
				// 目录下没有 index.html，返回根目录的 index.html
				indexFile, err = distFS.Open("index.html")
				if err != nil {
					c.String(http.StatusNotFound, "404 page not found")
					return
				}
			}
			defer indexFile.Close()

			stat, _ := indexFile.(fs.File)
			http.ServeContent(c.Writer, c.Request, "index.html", getModTime(stat), indexFile.(io.ReadSeeker))
			return
		}

		// 设置正确的 Content-Type
		contentType := getContentType(path)
		if contentType != "" {
			c.Writer.Header().Set("Content-Type", contentType)
		}

		// 返回文件内容
		http.ServeContent(c.Writer, c.Request, stat.Name(), getModTime(file.(fs.File)), file.(io.ReadSeeker))
	})
}

// getModTime 获取文件的修改时间
func getModTime(file fs.File) time.Time {
	if stat, err := file.Stat(); err == nil {
		return stat.ModTime()
	}
	return time.Time{}
}

// getContentType 根据文件扩展名返回 Content-Type
func getContentType(filePath string) string {
	ext := path.Ext(filePath)
	contentTypes := map[string]string{
		".html":  "text/html; charset=utf-8",
		".css":   "text/css; charset=utf-8",
		".js":    "application/javascript; charset=utf-8",
		".json":  "application/json; charset=utf-8",
		".png":   "image/png",
		".jpg":   "image/jpeg",
		".jpeg":  "image/jpeg",
		".gif":   "image/gif",
		".svg":   "image/svg+xml",
		".ico":   "image/x-icon",
		".woff":  "font/woff",
		".woff2": "font/woff2",
		".ttf":   "font/ttf",
		".eot":   "application/vnd.ms-fontobject",
	}

	if contentType, ok := contentTypes[ext]; ok {
		return contentType
	}
	return ""
}
