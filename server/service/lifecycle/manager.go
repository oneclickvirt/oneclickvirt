package lifecycle

import (
	"sync"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// Service 可关闭的服务接口
type Service interface {
	Stop()
}

// ServiceWithTimeout 支持超时关闭的服务接口
type ServiceWithTimeout interface {
	Stop(timeout time.Duration) error
}

// LifecycleManager 服务生命周期管理器
type LifecycleManager struct {
	services []serviceEntry
	mu       sync.RWMutex
}

type serviceEntry struct {
	name    string
	service interface{}
}

var (
	manager     *LifecycleManager
	managerOnce sync.Once
)

// GetManager 获取生命周期管理器单例
func GetManager() *LifecycleManager {
	managerOnce.Do(func() {
		manager = &LifecycleManager{
			services: make([]serviceEntry, 0),
		}
	})
	return manager
}

// Register 注册需要在关闭时清理的服务
func (m *LifecycleManager) Register(name string, service interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.services = append(m.services, serviceEntry{
		name:    name,
		service: service,
	})

	global.APP_LOG.Debug("服务已注册到生命周期管理器",
		zap.String("name", name))
}

// ShutdownAll 关闭所有已注册的服务（按注册顺序的逆序）
func (m *LifecycleManager) ShutdownAll(timeout time.Duration) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	global.APP_LOG.Info("开始关闭所有已注册服务",
		zap.Int("count", len(m.services)))

	// 逆序关闭（后注册的先关闭）
	for i := len(m.services) - 1; i >= 0; i-- {
		entry := m.services[i]

		global.APP_LOG.Info("正在关闭服务",
			zap.String("name", entry.name))

		// 尝试不同的关闭接口
		switch svc := entry.service.(type) {
		case ServiceWithTimeout:
			if err := svc.Stop(timeout); err != nil {
				global.APP_LOG.Error("服务关闭失败",
					zap.String("name", entry.name),
					zap.Error(err))
			} else {
				global.APP_LOG.Info("服务已关闭",
					zap.String("name", entry.name))
			}
		case Service:
			svc.Stop()
			global.APP_LOG.Info("服务已关闭",
				zap.String("name", entry.name))
		case interface{ Close() }:
			svc.Close()
			global.APP_LOG.Info("服务已关闭",
				zap.String("name", entry.name))
		case interface{ Close() error }:
			if err := svc.Close(); err != nil {
				global.APP_LOG.Error("服务关闭失败",
					zap.String("name", entry.name),
					zap.Error(err))
			} else {
				global.APP_LOG.Info("服务已关闭",
					zap.String("name", entry.name))
			}
		case interface{ Shutdown() }:
			svc.Shutdown()
			global.APP_LOG.Info("服务已关闭",
				zap.String("name", entry.name))
		case interface{ StopCleanup() }:
			svc.StopCleanup()
			global.APP_LOG.Info("服务已关闭",
				zap.String("name", entry.name))
		case interface{ CloseAll() }:
			// 支持SSH连接池的CloseAll方法
			svc.CloseAll()
			global.APP_LOG.Info("服务已关闭",
				zap.String("name", entry.name))
		default:
			global.APP_LOG.Warn("服务没有实现已知的关闭接口",
				zap.String("name", entry.name))
		}
	}

	global.APP_LOG.Info("所有服务关闭完成")
}
