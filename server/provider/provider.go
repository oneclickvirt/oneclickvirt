package provider

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/provider/health"

	"go.uber.org/zap"
)

// 类型别名，使用model包中的结构体
type Instance = provider.ProviderInstance
type Image = provider.ProviderImage
type InstanceConfig = provider.ProviderInstanceConfig
type NodeConfig = provider.ProviderNodeConfig

// ProgressCallback 进度回调函数类型
type ProgressCallback func(percentage int, message string)

// Provider 统一接口
type Provider interface {
	// 基础信息
	GetType() string
	GetName() string
	GetSupportedInstanceTypes() []string // 获取支持的实例类型

	// 实例管理
	ListInstances(ctx context.Context) ([]Instance, error)
	CreateInstance(ctx context.Context, config InstanceConfig) error
	CreateInstanceWithProgress(ctx context.Context, config InstanceConfig, progressCallback ProgressCallback) error
	StartInstance(ctx context.Context, id string) error
	StopInstance(ctx context.Context, id string) error
	RestartInstance(ctx context.Context, id string) error
	DeleteInstance(ctx context.Context, id string) error
	GetInstance(ctx context.Context, id string) (*Instance, error)

	// 镜像管理
	ListImages(ctx context.Context) ([]Image, error)
	PullImage(ctx context.Context, image string) error
	DeleteImage(ctx context.Context, id string) error

	// 连接管理
	Connect(ctx context.Context, config NodeConfig) error
	Disconnect(ctx context.Context) error
	IsConnected() bool

	// 健康检查 - 使用新的health包
	HealthCheck(ctx context.Context) (*health.HealthResult, error)
	GetHealthChecker() health.HealthChecker

	// 密码管理
	SetInstancePassword(ctx context.Context, instanceID, password string) error
	ResetInstancePassword(ctx context.Context, instanceID string) (string, error)

	// SSH命令执行
	ExecuteSSHCommand(ctx context.Context, command string) (string, error)
}

// Registry Provider 注册表
type Registry struct {
	providers map[string]func() Provider
	mu        sync.RWMutex
}

var globalRegistry = &Registry{
	providers: make(map[string]func() Provider),
}

// ProviderCacheEntry Provider 缓存条目
type ProviderCacheEntry struct {
	Provider   Provider
	LastAccess time.Time
	CreatedAt  time.Time // 添加创建时间
	ConfigHash string    // 配置哈希，用于检测配置变更
}

// ProviderCache Provider 实例缓存
type ProviderCache struct {
	cache       sync.Map // map[uint]*ProviderCacheEntry (providerID -> entry)
	maxAge      time.Duration
	maxLifetime time.Duration // 最大存活时间（强制过期）
	ctx         context.Context
	cancel      context.CancelFunc
}

var (
	globalProviderCache     *ProviderCache
	globalProviderCacheOnce sync.Once
)

const (
	defaultProviderCacheMaxAge = 30 * time.Minute // Provider 实例缓存 30 分钟
	defaultProviderMaxLifetime = 2 * time.Hour    // Provider 实例最大存活 2 小时（强制过期）
	cacheCleanupInterval       = 5 * time.Minute  // 清理间隔
)

// 初始化health包的Transport清理管理器引用（避免循环依赖）
func init() {
	health.GetTransportCleanupManager = func() interface {
		RegisterTransport(*http.Transport)
		RegisterTransportWithProvider(*http.Transport, uint)
	} {
		return GetTransportCleanupManager()
	}
}

// GetProviderCache 获取全局 Provider 缓存
func GetProviderCache() *ProviderCache {
	globalProviderCacheOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		globalProviderCache = &ProviderCache{
			maxAge:      defaultProviderCacheMaxAge,
			maxLifetime: defaultProviderMaxLifetime,
			ctx:         ctx,
			cancel:      cancel,
		}
		// 启动后台清理
		go globalProviderCache.cleanupLoop()
	})
	return globalProviderCache
}

// cleanupLoop 定期清理过期的 Provider 实例
func (pc *ProviderCache) cleanupLoop() {
	// 确俟ticker在panic时也能停止，防止goroutine泄漏
	ticker := time.NewTicker(cacheCleanupInterval)
	defer func() {
		ticker.Stop()
		if r := recover(); r != nil && global.APP_LOG != nil {
			global.APP_LOG.Error("Provider缓存清理goroutine panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
	}()

	for {
		select {
		case <-pc.ctx.Done():
			return
		case <-ticker.C:
			pc.cleanupExpired()
		}
	}
}

// cleanupExpired 清理过期的缓存条目
func (pc *ProviderCache) cleanupExpired() {
	now := time.Now()
	var toRemove []uint

	pc.cache.Range(func(key, value interface{}) bool {
		providerID := key.(uint)
		entry := value.(*ProviderCacheEntry)

		shouldRemove := false
		reason := ""

		// 检查1: 空闲时间过期（最后访问时间）
		if now.Sub(entry.LastAccess) > pc.maxAge {
			shouldRemove = true
			reason = "idle_timeout"
		}

		// 检查2: 强制过期（创建时间，防止活跃对象永不过期）
		if !shouldRemove && now.Sub(entry.CreatedAt) > pc.maxLifetime {
			shouldRemove = true
			reason = "max_lifetime"
		}

		if shouldRemove {
			toRemove = append(toRemove, providerID)
			if global.APP_LOG != nil {
				global.APP_LOG.Debug("Provider缓存过期",
					zap.Uint("providerID", providerID),
					zap.String("reason", reason),
					zap.Duration("idleTime", now.Sub(entry.LastAccess)),
					zap.Duration("lifetime", now.Sub(entry.CreatedAt)))
			}
		}
		return true
	})

	// 批量删除过期条目
	for _, id := range toRemove {
		if value, loaded := pc.cache.LoadAndDelete(id); loaded {
			entry := value.(*ProviderCacheEntry)
			if entry.Provider != nil {
				// 使用超时context调用Disconnect，防止阻塞
				disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := entry.Provider.Disconnect(disconnectCtx)
				disconnectCancel()

				if err != nil && global.APP_LOG != nil {
					// Disconnect失败不阻塞清理，只记录日志
					global.APP_LOG.Warn("Provider Disconnect失败（已继续清理缓存）",
						zap.Uint("providerID", id),
						zap.Error(err))
				}
			}

			// 立即清理SSH连接（即使Disconnect失败）
			if global.APP_SSH_POOL != nil {
				if pool, ok := global.APP_SSH_POOL.(interface{ Remove(uint) }); ok {
					pool.Remove(id)
				}
			}
		}
	}

	if len(toRemove) > 0 && global.APP_LOG != nil {
		global.APP_LOG.Info("Provider缓存清理完成",
			zap.Int("cleaned", len(toRemove)))
	}
}

// Get 获取缓存的 Provider 实例
func (pc *ProviderCache) Get(providerID uint, configHash string) (Provider, bool) {
	value, ok := pc.cache.Load(providerID)
	if !ok {
		return nil, false
	}

	entry := value.(*ProviderCacheEntry)

	// 检查配置是否变更
	if entry.ConfigHash != configHash {
		// 配置变更，删除旧实例
		pc.cache.Delete(providerID)
		if entry.Provider != nil {
			entry.Provider.Disconnect(context.Background())
		}
		return nil, false
	}

	// 更新访问时间
	entry.LastAccess = time.Now()
	return entry.Provider, true
}

// Set 设置缓存的 Provider 实例
func (pc *ProviderCache) Set(providerID uint, provider Provider, configHash string) {
	now := time.Now()
	entry := &ProviderCacheEntry{
		Provider:   provider,
		LastAccess: now,
		CreatedAt:  now, // 记录创建时间
		ConfigHash: configHash,
	}
	pc.cache.Store(providerID, entry)
}

// Delete 删除缓存的 Provider 实例
func (pc *ProviderCache) Delete(providerID uint) {
	if value, loaded := pc.cache.LoadAndDelete(providerID); loaded {
		entry := value.(*ProviderCacheEntry)
		if entry.Provider != nil {
			// 使用超时context，防止Disconnect阻塞
			disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := entry.Provider.Disconnect(disconnectCtx)
			disconnectCancel()

			if err != nil && global.APP_LOG != nil {
				global.APP_LOG.Warn("Provider Disconnect失败（已删除缓存）",
					zap.Uint("providerID", providerID),
					zap.Error(err))
			}
		}
	}
}

// Clear 清空所有缓存
func (pc *ProviderCache) Clear() {
	var disconnectErrors int
	pc.cache.Range(func(key, value interface{}) bool {
		providerID := key.(uint)
		entry := value.(*ProviderCacheEntry)
		if entry.Provider != nil {
			// 使用超时context，防止单个Disconnect阻塞整个清理过程
			disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := entry.Provider.Disconnect(disconnectCtx)
			disconnectCancel()

			if err != nil {
				disconnectErrors++
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("Provider Disconnect失败（继续清理）",
						zap.Uint("providerID", providerID),
						zap.Error(err))
				}
			}
		}
		pc.cache.Delete(key)
		return true
	})

	if disconnectErrors > 0 && global.APP_LOG != nil {
		global.APP_LOG.Warn("Provider缓存清空完成，但有Disconnect失败",
			zap.Int("disconnectErrors", disconnectErrors))
	}
}

// Stop 停止缓存管理器
func (pc *ProviderCache) Stop() {
	if pc.cancel != nil {
		pc.cancel()
	}
	pc.Clear()
}

// RegisterProvider 注册 Provider
func RegisterProvider(name string, factory func() Provider) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.providers[name] = factory
}

// GetProvider 获取 Provider 实例
// 返回的是工厂创建的新实例，不是单例
// 每次调用都会创建新的Provider实例，避免并发问题
// 注意：这个方法不使用缓存，推荐使用 GetProviderWithCache
func GetProvider(name string) (Provider, error) {
	globalRegistry.mu.RLock()
	factory, exists := globalRegistry.providers[name]
	globalRegistry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("provider %s not registered", name)
	}

	// 每次都创建新实例，避免并发竞态条件
	instance := factory()
	return instance, nil
}

// GetProviderWithCache 获取带缓存的 Provider 实例
// providerID: 数据库中的 Provider ID
// providerType: Provider 类型 (docker/lxd/incus/proxmox)
// configHash: 配置的哈希值，用于检测配置变更
func GetProviderWithCache(providerID uint, providerType string, configHash string) (Provider, error) {
	cache := GetProviderCache()

	// 先尝试从缓存获取
	if cached, ok := cache.Get(providerID, configHash); ok {
		return cached, nil
	}

	// 缓存未命中，创建新实例
	globalRegistry.mu.RLock()
	factory, exists := globalRegistry.providers[providerType]
	globalRegistry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("provider %s not registered", providerType)
	}

	instance := factory()

	// 存入缓存
	cache.Set(providerID, instance, configHash)

	return instance, nil
}

// InvalidateProviderCache 使指定 Provider 的缓存失效
// 同时清理所有相关资源（SSH连接、Transport等）
func InvalidateProviderCache(providerID uint) {
	if global.APP_LOG != nil {
		global.APP_LOG.Debug("使Provider缓存失效", zap.Uint("providerID", providerID))
	}

	cache := GetProviderCache()
	cache.Delete(providerID)

	// 注意：不在此处清理SSH和Transport，由调用方统一协调清理顺序
	// 避免重复清理和清理顺序问题
}

// ListProviders 列出所有已注册的 Provider
func ListProviders() []string {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	var names []string
	for name := range globalRegistry.providers {
		names = append(names, name)
	}
	return names
}

// GetAllProviders 获取所有 Provider 类型的工厂函数
// 不再返回单例实例，而是返回可以创建Provider的工厂函数
func GetAllProviders() map[string]func() Provider {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	result := make(map[string]func() Provider)
	for name, factory := range globalRegistry.providers {
		result[name] = factory
	}
	return result
}
