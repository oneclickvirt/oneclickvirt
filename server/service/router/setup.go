package router

import (
	"net/http"
	"oneclickvirt/api/v1/public"
	"oneclickvirt/global"
	"oneclickvirt/middleware"
	authModel "oneclickvirt/model/auth"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
)

// isAPIPath 判断路径是否属于 API 路径，用于嵌入模式下区分静态资源与动态接口。
func isAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/") ||
		strings.HasPrefix(path, "/swagger/") ||
		path == "/health"
}

// SetupRouter 初始化并返回全局 Gin 路由器。
//
// 中间件注册顺序（顺序很重要）：
//  1. CORS         — 跨域资源共享
//  2. RequestID    — 注入全链路追踪 ID
//  3. Logger       — HTTP 访问日志（依赖 RequestID）
//  4. ErrorHandler — panic 捕获与统一错误响应
//  5. Validator    — SQL注入/XSS 输入预检
func SetupRouter() *gin.Engine {
	// 禁用 gin.Default() 内置的 Logger 和 Recovery，改用自定义中间件
	Router := gin.New()

	// 信任所有上游代理（用于反向代理和 Cloudflare Tunnel）
	// nil 表示信任所有代理，可正确解析 X-Forwarded-For、X-Real-IP 等头
	Router.SetTrustedProxies(nil)
	Router.ForwardedByClientIP = true

	// 全局中间件排序：CORS → RequestID → Logger → ErrorHandler → InputValidator
	frontendURL := global.GetAppConfig().System.FrontendURL
	Router.Use(cors.New(cors.Config{
		AllowOriginFunc: func(origin string) bool {
			// 允许配置的前端地址
			if frontendURL != "" && origin == frontendURL {
				return true
			}
			// 允许 localhost 和 127.0.0.1（开发和本地部署）
			return strings.HasPrefix(origin, "http://localhost:") ||
				strings.HasPrefix(origin, "https://localhost:") ||
				strings.HasPrefix(origin, "http://127.0.0.1:") ||
				strings.HasPrefix(origin, "https://127.0.0.1:")
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"*"},
		ExposeHeaders:    []string{"Content-Length", "Authorization", middleware.RequestIDHeader},
		AllowCredentials: true,
	}))
	Router.Use(middleware.RequestIDMiddleware()) // 注入 X-Request-ID，必须在 Logger 前
	Router.Use(middleware.LoggerMiddleware())    // HTTP 访问日志
	Router.Use(middleware.ErrorHandler())        // panic 捕获与统一错误响应
	Router.Use(middleware.InputValidator())      // SQL注入/XSS 预处理

	// 健康检查——无需认证和数据库限制
	Router.GET("/health", public.HealthCheck)

	// Swagger文档路由
	Router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// API 路由组
	ApiGroup := Router.Group("/api")
	{
		// 健康检查在 /api 路径下保持一致
		ApiGroup.GET("/health", public.HealthCheck)

		// 无数据库健康检查组：系统初始化完成前必需可访问的接口
		NoDBGroup := ApiGroup.Group("")
		NoDBGroup.Use(middleware.RequireAuth(authModel.AuthLevelPublic))
		{
			// 初始化相关公开 API
			InitPublicGroup := NoDBGroup.Group("v1/public")
			{
				InitPublicGroup.GET("init/check", public.CheckInit)                           // 检查初始化状态
				InitPublicGroup.POST("init", public.InitSystem)                               // 执行系统初始化
				InitPublicGroup.POST("test-db-connection", public.TestDatabaseConnection)     // 测试数据库连接
				InitPublicGroup.GET("recommended-db-type", public.GetRecommendedDatabaseType) // 获取推荐数据库类型
				InitPublicGroup.GET("register-config", public.GetRegisterConfig)              // 获取注册配置（从内存读取）
				InitPublicGroup.GET("system-config", public.GetPublicSystemConfig)            // 获取系统配置（优先从数据库读取）
			}

			// 认证 API：登录、注册、验证码等——需要数据库但在初始化前就必须可用，不被 DatabaseHealthCheck 拦截
			InitAuthRouter(NoDBGroup)

			// OAuth2 认证回调路由——不依赖数据库健康检查
			InitOAuth2AuthRouter(NoDBGroup)
		}

		// 公开访问路由（需要数据库健康检查）
		PublicGroup := ApiGroup.Group("")
		PublicGroup.Use(middleware.DatabaseHealthCheck())
		PublicGroup.Use(middleware.RequireAuth(authModel.AuthLevelPublic))
		{
			PublicGroup.GET("/ping", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"message": "pong"})
			})

			InitPublicRouter(PublicGroup)
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

		// OAuth2 管理路由（需要数据库健康检查和管理员权限）
		OAuth2AdminGroup := ApiGroup.Group("")
		OAuth2AdminGroup.Use(middleware.DatabaseHealthCheck())
		InitOAuth2AdminRouter(OAuth2AdminGroup)

		// 资源和 Provider 路由（需要数据库健康检查）
		ResourceGroup := ApiGroup.Group("")
		ResourceGroup.Use(middleware.DatabaseHealthCheck())
		InitResourceRouter(ResourceGroup)
		InitProviderRouter(ResourceGroup)
	}

	// 设置静态文件路由（embed 构建模式下才生效）
	if err := setupStaticRoutes(Router); err != nil {
		// 日志已在 InitializeSystem 中完成初始化，这里可安全使用 global.APP_LOG
		global.APP_LOG.Error("设置静态文件路由失败，API服务仍正常运行", zap.Error(err))
	}

	return Router
}
