package middleware

import (
	"net/http"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
)

func CSRFProtection() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isUnsafeHTTPMethod(c.Request.Method) {
			c.Next()
			return
		}

		appConfig := global.GetAppConfig()
		frontendURL := appConfig.System.FrontendURL
		whitelist := appConfig.Cors.Whitelist

		if origin := c.GetHeader("Origin"); origin != "" && !utils.OriginAllowedForRequest(c.Request, origin, frontendURL, whitelist) {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "跨站请求已被拒绝"))
			c.Abort()
			return
		}

		c.Next()
	}
}

func isUnsafeHTTPMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
