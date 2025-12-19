package initialize

import (
	"context"
	"net/http"
	"oneclickvirt/provider"
	"oneclickvirt/service/auth"
	"oneclickvirt/service/cache"
	oauth2Service "oneclickvirt/service/oauth2"
	"oneclickvirt/service/resources"
	"os"
	"os/signal"
	"syscall"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/service/lifecycle"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func InitServer(address string, router *gin.Engine) *http.Server {
	s := &http.Server{
		Addr:           address,
		Handler:        router,
		ReadTimeout:    20 * time.Second,
		WriteTimeout:   20 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		global.APP_LOG.Info("Shutdown Server ...")

		// 触发系统级别的关闭信号，停止所有后台goroutine
		if global.APP_SHUTDOWN_CANCEL != nil {
			global.APP_SHUTDOWN_CANCEL()
		}

		// 关闭顺序
		// 1. 先停止调度器和任务处理（避免新任务启动）
		// 2. 再清理Provider缓存（断开Provider连接）
		// 3. 然后清理SSH连接池和HTTP Transport（释放网络资源）
		// 4. 最后关闭数据库连接
		// 这个顺序确保：
		//   - 不会有新任务在清理期间启动
		//   - 所有活跃连接在资源释放前正确关闭
		//   - 避免资源竞争和泄漏

		// 使用生命周期管理器统一关闭所有服务
		lifecycleMgr := lifecycle.GetManager()
		lifecycleMgr.ShutdownAll(30 * time.Second)

		// 关闭日志限制器
		utils.GetLogRateLimiter().Stop()

		// 关闭SSH连接池
		if global.APP_SSH_POOL != nil {
			global.APP_SSH_POOL.CloseAll()
		}

		// 停止各种服务的清理goroutine
		if captchaCache, ok := global.APP_CAPTCHA_STORE.(*utils.LRUCaptchaCache); ok {
			captchaCache.Stop()
		}

		// 停止认证服务的清理任务
		authBlacklist := auth.GetJWTBlacklistService()
		if authBlacklist != nil {
			authBlacklist.Stop()
		}

		// 停止OAuth2服务的清理任务
		oauth2Service.StopOAuth2Service()

		// 停止资源服务的清理任务
		resources.StopDashboardCache()
		resources.GetResourceReservationService().StopCleanup()

		// 停止用户缓存服务
		cache.GetUserCacheService().Shutdown()

		// 清理HTTP Transport的空闲连接
		utils.CleanupHTTPTransports()

		// 停止HTTP Client Manager的定期清理goroutine
		utils.GetHTTPClientManager().Stop()

		// 停止并清理所有Provider Transport连接
		provider.GetTransportCleanupManager().Stop()

		// 关闭数据库连接管理器（包含心跳检测和连接池）
		if dbManager := GetDatabaseManager(); dbManager != nil {
			dbManager.Shutdown()
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			global.APP_LOG.Error("Server Shutdown failed", zap.Error(err))
		} else {
			global.APP_LOG.Info("Server shutdown completed")
		}
	}()

	return s
}
