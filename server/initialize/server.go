package initialize

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"oneclickvirt/global"

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

		// 停止调度器
		if global.APP_SCHEDULER != nil {
			global.APP_SCHEDULER.StopScheduler()
		}

		// 停止监控调度器
		if global.APP_MONITORING_SCHEDULER != nil {
			global.APP_MONITORING_SCHEDULER.Stop()
		}

		// 停止Provider健康检查调度器
		if global.APP_PROVIDER_HEALTH_SCHEDULER != nil {
			global.APP_PROVIDER_HEALTH_SCHEDULER.Stop()
		}

		// 关闭数据库连接
		if global.APP_DB != nil {
			if sqlDB, err := global.APP_DB.DB(); err == nil {
				sqlDB.Close()
			}
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
