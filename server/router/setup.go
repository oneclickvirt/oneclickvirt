package router

import (
	"log"
	"net/http"
	"oneclickvirt/api/v1/public"
	"oneclickvirt/middleware"
	authModel "oneclickvirt/model/auth"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// isAPIPath 检查路径是否为API路径
func isAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/") ||
		strings.HasPrefix(path, "/swagger/") ||
		path == "/health"
}

// SetupRouter 统一的路由设置入口
func SetupRouter() *gin.Engine {
	Router := gin.Default()

	// 信任所有代理（用于反向代理和Cloudflare Tunnel）
	// 这样可以正确处理 X-Forwarded-* headers
	Router.SetTrustedProxies(nil) // nil 表示信任所有代理
	Router.ForwardedByClientIP = true

	// CORS配置
	Router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"*"},
		ExposeHeaders:    []string{"Content-Length", "Authorization"},
		AllowCredentials: true,
	}))

	// 全局中间件
	Router.Use(middleware.ErrorHandler())
	Router.Use(middleware.InputValidator())

	// 健康检查 - 使用public包中的标准健康检查
	Router.GET("/health", public.HealthCheck)

	// Swagger文档路由
	Router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// API路由组
	ApiGroup := Router.Group("/api")
	{
		// 健康检查也在API路径下，保持与前端一致
		ApiGroup.GET("/health", public.HealthCheck)

		// 系统初始化相关路由（不需要数据库健康检查）
		// 这些端点在系统初始化前必须可用
		NoDBGroup := ApiGroup.Group("")
		NoDBGroup.Use(middleware.RequireAuth(authModel.AuthLevelPublic))
		{
			// 初始化相关API
			InitPublicGroup := NoDBGroup.Group("v1/public")
			{
				InitPublicGroup.GET("init/check", public.CheckInit)
				InitPublicGroup.POST("init", public.InitSystem)
				InitPublicGroup.POST("test-db-connection", public.TestDatabaseConnection)
				InitPublicGroup.GET("recommended-db-type", public.GetRecommendedDatabaseType)
				InitPublicGroup.GET("register-config", public.GetRegisterConfig)
			}

			// 认证相关API（登录、注册、验证码等在系统初始化后但数据库可能不稳定时也需要可用）
			InitAuthRouter(NoDBGroup)

			// OAuth2路由也应该在初始化前可用
			InitOAuth2Router(NoDBGroup)
		}

		// 公开访问路由（需要数据库健康检查）
		PublicGroup := ApiGroup.Group("")
		PublicGroup.Use(middleware.DatabaseHealthCheck()) // 添加数据库健康检查
		PublicGroup.Use(middleware.RequireAuth(authModel.AuthLevelPublic))
		{
			PublicGroup.GET("/ping", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"message": "pong"})
			})

			// 需要数据库的公开路由
			InitPublicRouter(PublicGroup) // 公开路由（已从中移除了初始化相关API）
		}

		// 配置路由（需要数据库健康检查）
		ConfigGroup := ApiGroup.Group("")
		ConfigGroup.Use(middleware.DatabaseHealthCheck())
		InitConfigRouter(ConfigGroup)

		// 用户路由（需要数据库健康检查）
		UserGroup := ApiGroup.Group("")
		UserGroup.Use(middleware.DatabaseHealthCheck())
		InitUserRouter(UserGroup)

		// 管理员路由（需要数据库健康检查）
		AdminGroup := ApiGroup.Group("")
		AdminGroup.Use(middleware.DatabaseHealthCheck())
		InitAdminRouter(AdminGroup)

		// 资源和Provider路由（需要数据库健康检查）
		ResourceGroup := ApiGroup.Group("")
		ResourceGroup.Use(middleware.DatabaseHealthCheck())
		InitResourceRouter(ResourceGroup)
		InitProviderRouter(ResourceGroup)
	}

	// 设置静态文件路由（如果启用了嵌入模式）
	if err := setupStaticRoutes(Router); err != nil {
		log.Printf("[ERROR] 设置静态文件路由失败: %v\n", err)
		// 返回错误但不影响API服务启动
	}

	return Router
}
