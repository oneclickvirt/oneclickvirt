package router

import (
	"oneclickvirt/api/v1/auth"
	"oneclickvirt/middleware"
	authModel "oneclickvirt/model/auth"

	"github.com/gin-gonic/gin"
)

// InitAuthRouter 认证路由
func InitAuthRouter(Router *gin.RouterGroup) {
	AuthRouter := Router.Group("v1/auth")
	{
		AuthRouter.POST("login", auth.Login)
		AuthRouter.POST("register", auth.Register)
		AuthRouter.GET("captcha", auth.GetCaptcha)
		AuthRouter.POST("forgot-password", auth.ForgotPassword)
		AuthRouter.POST("reset-password", auth.ResetPassword)
		AuthRouter.POST("logout", middleware.RequireAuth(authModel.AuthLevelUser), auth.Logout)
	}
}
