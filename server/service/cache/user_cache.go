package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// UserCacheService 用户数据缓存服务
type UserCacheService struct {
	cache       sync.Map // key: string -> *CacheEntry
	ctx         context.Context
	cancel      context.CancelFunc
	cleanupOnce sync.Once
}

// CacheEntry 缓存条目
type CacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
	CreatedAt time.Time
}

var (
	userCacheInstance     *UserCacheService
	userCacheInstanceOnce sync.Once
)

// GetUserCacheService 获取用户缓存服务单例
func GetUserCacheService() *UserCacheService {
	userCacheInstanceOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		userCacheInstance = &UserCacheService{
			ctx:    ctx,
			cancel: cancel,
		}
		// 启动后台清理
		go userCacheInstance.cleanupLoop()
	})
	return userCacheInstance
}

// cleanupLoop 定期清理过期缓存
func (s *UserCacheService) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer func() {
		ticker.Stop()
		if r := recover(); r != nil && global.APP_LOG != nil {
			global.APP_LOG.Error("用户缓存清理goroutine panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.cleanupExpired()
		}
	}
}

// cleanupExpired 清理过期缓存
func (s *UserCacheService) cleanupExpired() {
	now := time.Now()
	var toRemove []string

	s.cache.Range(func(key, value interface{}) bool {
		entry := value.(*CacheEntry)
		if now.After(entry.ExpiresAt) {
			toRemove = append(toRemove, key.(string))
		}
		return true
	})

	for _, key := range toRemove {
		s.cache.Delete(key)
	}

	if len(toRemove) > 0 && global.APP_LOG != nil {
		global.APP_LOG.Debug("用户缓存清理完成",
			zap.Int("cleaned", len(toRemove)))
	}
}

// Get 获取缓存数据
func (s *UserCacheService) Get(key string) (interface{}, bool) {
	value, ok := s.cache.Load(key)
	if !ok {
		return nil, false
	}

	entry := value.(*CacheEntry)
	if time.Now().After(entry.ExpiresAt) {
		s.cache.Delete(key)
		return nil, false
	}

	return entry.Data, true
}

// Set 设置缓存数据
func (s *UserCacheService) Set(key string, data interface{}, ttl time.Duration) {
	now := time.Now()
	entry := &CacheEntry{
		Data:      data,
		ExpiresAt: now.Add(ttl),
		CreatedAt: now,
	}
	s.cache.Store(key, entry)
}

// Delete 删除缓存
func (s *UserCacheService) Delete(key string) {
	s.cache.Delete(key)
}

// DeleteByPrefix 删除指定前缀的所有缓存
func (s *UserCacheService) DeleteByPrefix(prefix string) {
	var toRemove []string
	s.cache.Range(func(key, value interface{}) bool {
		keyStr := key.(string)
		if len(keyStr) >= len(prefix) && keyStr[:len(prefix)] == prefix {
			toRemove = append(toRemove, keyStr)
		}
		return true
	})

	for _, key := range toRemove {
		s.cache.Delete(key)
	}
}

// Shutdown 关闭缓存服务
func (s *UserCacheService) Shutdown() {
	s.cleanupOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
}

// CacheKeys 缓存键常量
const (
	// Dashboard缓存 - 1分钟
	KeyUserDashboard = "user:dashboard:%d" // userID
	TTLUserDashboard = 1 * time.Minute

	// 流量汇总缓存 - 3分钟
	KeyUserTrafficSummary = "user:traffic:summary:%d:%d:%d" // userID, year, month
	TTLUserTrafficSummary = 3 * time.Minute

	// 流量概览缓存 - 2分钟
	KeyUserTrafficOverview = "user:traffic:overview:%d" // userID
	TTLUserTrafficOverview = 2 * time.Minute

	// 实例流量详情缓存 - 2分钟
	KeyInstanceTrafficDetail = "instance:traffic:detail:%d" // instanceID
	TTLInstanceTrafficDetail = 2 * time.Minute
)

// MakeUserDashboardKey 生成用户Dashboard缓存键
func MakeUserDashboardKey(userID uint) string {
	return fmt.Sprintf(KeyUserDashboard, userID)
}

// MakeUserTrafficSummaryKey 生成用户流量汇总缓存键
func MakeUserTrafficSummaryKey(userID uint, year, month int) string {
	return fmt.Sprintf(KeyUserTrafficSummary, userID, year, month)
}

// MakeUserTrafficOverviewKey 生成用户流量概览缓存键
func MakeUserTrafficOverviewKey(userID uint) string {
	return fmt.Sprintf(KeyUserTrafficOverview, userID)
}

// MakeInstanceTrafficDetailKey 生成实例流量详情缓存键
func MakeInstanceTrafficDetailKey(instanceID uint) string {
	return fmt.Sprintf(KeyInstanceTrafficDetail, instanceID)
}

// InvalidateUserCache 使用户所有缓存失效
func (s *UserCacheService) InvalidateUserCache(userID uint) {
	// 删除Dashboard缓存
	s.Delete(MakeUserDashboardKey(userID))

	// 删除所有流量相关缓存
	s.DeleteByPrefix(fmt.Sprintf("user:traffic:summary:%d:", userID))
	s.Delete(MakeUserTrafficOverviewKey(userID))
}

// InvalidateInstanceCache 使实例缓存失效
func (s *UserCacheService) InvalidateInstanceCache(instanceID uint) {
	s.Delete(MakeInstanceTrafficDetailKey(instanceID))
}

// GetOrSet 获取缓存或执行函数并缓存结果
func (s *UserCacheService) GetOrSet(key string, ttl time.Duration, fn func() (interface{}, error)) (interface{}, error) {
	// 先尝试从缓存获取
	if data, ok := s.Get(key); ok {
		return data, nil
	}

	// 缓存未命中，执行函数
	data, err := fn()
	if err != nil {
		return nil, err
	}

	// 缓存结果
	s.Set(key, data, ttl)
	return data, nil
}

// SerializableWrapper 可序列化包装器，用于复杂类型的缓存
type SerializableWrapper struct {
	Data json.RawMessage
}

// WrapForCache 包装数据用于缓存
func WrapForCache(data interface{}) (*SerializableWrapper, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &SerializableWrapper{Data: jsonData}, nil
}

// UnwrapFromCache 从缓存中解包数据
func UnwrapFromCache(wrapper *SerializableWrapper, target interface{}) error {
	return json.Unmarshal(wrapper.Data, target)
}
