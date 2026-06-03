package router

import (
	"net/http"
	"oneclickvirt/api/v1/admin"
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
	appConfig := global.GetAppConfig()
	frontendURL := appConfig.System.FrontendURL
	corsMode := appConfig.Cors.Mode
	corsWhitelist := appConfig.Cors.Whitelist

	// 空值兜底：默认使用白名单模式；生产环境禁止通配放行。
	if corsMode == "" {
		corsMode = "whitelist"
	}
	if strings.EqualFold(appConfig.System.Env, "production") && corsMode == "allow-all" {
		corsMode = "whitelist"
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("生产环境不允许 CORS allow-all，已降级为 whitelist")
		}
	}
	if global.APP_LOG != nil {
		global.APP_LOG.Info("CORS中间件配置",
			zap.String("mode", corsMode),
			zap.String("frontendURL", frontendURL),
			zap.Int("whitelistCount", len(corsWhitelist)))
	}

	allowedMethods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	allowedHeaders := []string{
		"Origin",
		"Content-Type",
		"Content-Length",
		"Accept",
		"Authorization",
		"X-Requested-With",
		middleware.RequestIDHeader,
	}
	var corsMiddleware gin.HandlerFunc
	if corsMode == "allow-all" {
		// allow-all 模式：反射实际 Origin，保留 Credentials 支持
		corsMiddleware = cors.New(cors.Config{
			AllowOriginFunc: func(origin string) bool {
				return true
			},
			AllowMethods:     allowedMethods,
			AllowHeaders:     allowedHeaders,
			ExposeHeaders:    []string{"Content-Length", "Authorization", middleware.RequestIDHeader},
			AllowCredentials: true,
		})
	} else {
		// whitelist 模式：仅允许白名单及配置的前端地址
		allowedOrigins := make(map[string]struct{}, len(corsWhitelist)+1)
		for _, o := range corsWhitelist {
			allowedOrigins[o] = struct{}{}
		}
		corsMiddleware = cors.New(cors.Config{
			AllowOriginFunc: func(origin string) bool {
				if frontendURL != "" && origin == frontendURL {
					return true
				}
				if _, ok := allowedOrigins[origin]; ok {
					return true
				}
				// 开发环境默认允许 localhost 和 127.0.0.1
				return strings.HasPrefix(origin, "http://localhost:") ||
					strings.HasPrefix(origin, "https://localhost:") ||
					strings.HasPrefix(origin, "http://127.0.0.1:") ||
					strings.HasPrefix(origin, "https://127.0.0.1:")
			},
			AllowMethods:     allowedMethods,
			AllowHeaders:     allowedHeaders,
			ExposeHeaders:    []string{"Content-Length", "Authorization", middleware.RequestIDHeader},
			AllowCredentials: true,
		})
	}
	Router.Use(corsMiddleware)
	Router.Use(middleware.RateLimit())           // API限流防护（防滥用）
	Router.Use(middleware.RequestIDMiddleware()) // 注入 X-Request-ID，必须在 Logger 前
	Router.Use(middleware.LoggerMiddleware())    // HTTP 访问日志
	Router.Use(middleware.ErrorHandler())        // panic 捕获与统一错误响应
	Router.Use(middleware.InputValidator())      // SQL注入/XSS 预处理

	// 健康检查——无需认证和数据库限制
	Router.GET("/health", public.HealthCheck)

	// Swagger 文档：开发环境公开，生产环境仅管理员可访问。
	if strings.EqualFold(appConfig.System.Env, "production") {
		Router.GET("/swagger/*any", middleware.RequireNormalAdmin(), ginSwagger.WrapHandler(swaggerFiles.Handler))
	} else {
		Router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	// API 路由组
	ApiGroup := Router.Group("/api")
	{
		// 健康检查在 /api 和 /api/v1 路径下保持一致
		ApiGroup.GET("/health", public.HealthCheck)
		ApiGroup.GET("/v1/health", public.HealthCheck)

		// 无数据库健康检查组：系统初始化完成前必需可访问的接口
		NoDBGroup := ApiGroup.Group("")
		NoDBGroup.Use(middleware.RequireAuth(authModel.AuthLevelPublic))
		{
			// 初始化相关公开 API
			InitPublicGroup := NoDBGroup.Group("v1/public")
			{
				InitPublicGroup.GET("init/check", public.CheckInit)                           // 检查初始化状态
				InitPublicGroup.POST("init", public.InitSystem)                               // 执行系统初始化
				InitPublicGroup.GET("init-progress", public.GetInitProgress)                  // 查询初始化进度
				InitPublicGroup.POST("test-db-connection", public.TestDatabaseConnection)     // 测试数据库连接
				InitPublicGroup.GET("recommended-db-type", public.GetRecommendedDatabaseType) // 获取推荐数据库类型
				InitPublicGroup.GET("register-config", public.GetRegisterConfig)              // 获取注册配置（从内存读取）
				InitPublicGroup.GET("system-config", public.GetPublicSystemConfig)            // 获取系统配置（优先从数据库读取）
				InitPublicGroup.GET("version", public.GetVersion)                             // 版本信息——不依赖数据库，DB 宕机时仍可访问
				InitPublicGroup.GET("build-info", public.GetBuildInfo)                        // 构建信息——不依赖数据库
				InitPublicGroup.GET("agent/install-agent.sh", public.DownloadAgentInstaller)  // 控制端安装脚本下载
				InitPublicGroup.GET("agent/releases/:filename", public.DownloadAgentRelease)  // 控制端Agent发布包下载
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

		// Agent WebSocket 连接入口（使用 AgentSecret 自鉴权，无 JWT 中间件）
		// 保留历史路径兼容，避免控制端重启后旧版本 agent 无法重连。
		ApiGroup.GET("/v1/ws/agent", admin.AgentWebSocket)
		ApiGroup.GET("/ws/agent", admin.AgentWebSocket)
	}

	// 设置静态文件路由（embed 构建模式下才生效）
	if err := setupStaticRoutes(Router); err != nil {
		// 日志已在 InitializeSystem 中完成初始化，这里可安全使用 global.APP_LOG
		global.APP_LOG.Error("设置静态文件路由失败，API服务仍正常运行", zap.Error(err))
	}

	return Router
}
