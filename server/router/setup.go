package router

import (
	"log"
	"net/http"
	"oneclickvirt/api/v1/public"
	"oneclickvirt/api/v1/system"
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

		// 系统初始化相关路由（无需数据库健康检查）
		// 这些端点在数据库未连接或初始化前必须可用
		NoDBGroup := ApiGroup.Group("")
		NoDBGroup.Use(middleware.RequireAuth(authModel.AuthLevelPublic))
		{
			// 1. 初始化相关API
			InitPublicGroup := NoDBGroup.Group("v1/public")
			{
				InitPublicGroup.GET("init/check", public.CheckInit)                           // 检查初始化状态
				InitPublicGroup.POST("init", public.InitSystem)                               // 执行系统初始化
				InitPublicGroup.POST("test-db-connection", public.TestDatabaseConnection)     // 测试数据库连接
				InitPublicGroup.GET("recommended-db-type", public.GetRecommendedDatabaseType) // 获取推荐数据库类型
				InitPublicGroup.GET("register-config", public.GetRegisterConfig)              // 获取注册配置（从内存读取）
				InitPublicGroup.GET("system-config", public.GetPublicSystemConfig)            // 获取系统配置（优先从数据库读取，降级到内存配置）
			}

			// 2. 静态文件服务（不需要数据库）
			StaticRouter := NoDBGroup.Group("v1/static")
			{
				StaticRouter.GET(":type/*path", system.ServeStaticFile) // 提供静态文件（头像等）
			}

			// 3. 认证相关API（登录、注册、验证码等需要数据库但在初始化前必须可用）
			// 这些API内部会查询数据库，但不应被DatabaseHealthCheck拦截
			// 因为它们需要在系统初始化过程中可用
			InitAuthRouter(NoDBGroup)

			// 4. OAuth2认证路由（第三方登录回调不依赖数据库健康检查）
			// 注意：OAuth2的管理和公开API会在下面单独配置
			InitOAuth2AuthRouter(NoDBGroup)
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
			InitPublicRouter(PublicGroup) // 公开路由（公告、统计、镜像列表）

			// OAuth2公开API（获取启用的提供商列表）
			InitOAuth2PublicRouter(PublicGroup)
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

		// OAuth2管理路由（需要数据库健康检查和管理员权限）
		OAuth2AdminGroup := ApiGroup.Group("")
		OAuth2AdminGroup.Use(middleware.DatabaseHealthCheck())
		InitOAuth2AdminRouter(OAuth2AdminGroup)

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
