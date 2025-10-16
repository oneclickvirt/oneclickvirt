package middleware

import (
	"net/http"
	"strconv"

	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// UploadSizeLimit 文件上传大小限制中间件
func UploadSizeLimit(maxSize int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 检查Content-Length头
		if c.Request.Header.Get("Content-Type") != "" &&
			(c.Request.Method == "POST" || c.Request.Method == "PUT") {

			contentLengthStr := c.Request.Header.Get("Content-Length")
			if contentLengthStr != "" {
				contentLength, err := strconv.ParseInt(contentLengthStr, 10, 64)
				if err == nil && contentLength > maxSize {
					common.ResponseWithError(c, common.NewError(common.CodeRequestTooLarge, "上传文件过大"))
					c.Abort()
					return
				}
			}

			// 设置请求体大小限制
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSize)
		}

		c.Next()
	}
}

// AvatarUploadLimit 头像上传限制中间件 (固定2MB)
func AvatarUploadLimit() gin.HandlerFunc {
	return UploadSizeLimit(2 * 1024 * 1024) // 2MB
}
