package utils

import (
	"context"
	"net/http"
	"sync"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// HTTPClientManager HTTP客户端管理器，用于优雅关闭和定期清理
type HTTPClientManager struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

var (
	httpClientManager     *HTTPClientManager
	httpClientManagerOnce sync.Once
)

// GetHTTPClientManager 获取HTTP客户端管理器单例
func GetHTTPClientManager() *HTTPClientManager {
	httpClientManagerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		httpClientManager = &HTTPClientManager{
			ctx:    ctx,
			cancel: cancel,
		}
		// 启动定期清理
		go httpClientManager.periodicCleanup()
	})
	return httpClientManager
}

// periodicCleanup 定期清理空闲连接
func (m *HTTPClientManager) periodicCleanup() {
	// 确俟ticker在panic时也能停止，防止goroutine泄漏
	ticker := time.NewTicker(10 * time.Minute)
	defer func() {
		ticker.Stop()
		if r := recover(); r != nil {
			global.APP_LOG.Error("HTTP Transport定期清理goroutine panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Info("HTTP Transport定期清理已停止")
	}()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanupIdleConnections()
		}
	}
}

// cleanupIdleConnections 清理空闲连接
func (m *HTTPClientManager) cleanupIdleConnections() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 清理共享Transport的空闲连接
	if sharedTransport != nil {
		sharedTransport.CloseIdleConnections()
	}

	// 清理不安全Transport的空闲连接
	if insecureTransport != nil {
		insecureTransport.CloseIdleConnections()
	}

	// 清理默认客户端的空闲连接
	if defaultHTTPClient != nil && defaultHTTPClient.Transport != nil {
		if transport, ok := defaultHTTPClient.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}

	global.APP_LOG.Debug("HTTP Transport空闲连接已清理")
}

// Stop 停止并清理所有HTTP客户端资源
func (m *HTTPClientManager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 关闭共享Transport的空闲连接
	if sharedTransport != nil {
		sharedTransport.CloseIdleConnections()
	}

	// 关闭不安全Transport的空闲连接
	if insecureTransport != nil {
		insecureTransport.CloseIdleConnections()
	}

	// 关闭默认客户端的空闲连接
	if defaultHTTPClient != nil && defaultHTTPClient.Transport != nil {
		if transport, ok := defaultHTTPClient.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}

	global.APP_LOG.Info("HTTP Transport已全部清理")
}

// Close 实现Close接口
func (m *HTTPClientManager) Close() {
	m.Stop()
}
