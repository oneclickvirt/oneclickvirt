package utils

import (
	"errors"
	"sync"
	"time"
)

// 验证码缓存配置常量
const (
	// MaxCaptchaItems 验证码缓存最大数量，防止内存耗尽
	MaxCaptchaItems = 5000
)

var (
	// ErrCacheFull 缓存已满错误
	ErrCacheFull = errors.New("验证码缓存已满，请稍后再试")
)

// CaptchaCache 验证码缓存接口
type CaptchaCache interface {
	// SetCaptcha 设置验证码
	SetCaptcha(id string, code string, expiration time.Duration) error
	// GetCaptcha 获取验证码
	GetCaptcha(id string) (string, bool)
	// DeleteCaptcha 删除验证码
	DeleteCaptcha(id string) error
}

// MemoryCaptchaCache 内存验证码缓存实现
type MemoryCaptchaCache struct {
	data     map[string]cacheItem
	mutex    sync.RWMutex
	maxItems int
	stopChan chan struct{} // 用于停止清理goroutine
	stopped  bool
}

type cacheItem struct {
	value      string
	expiration time.Time
	createdAt  time.Time // 用于LRU淘汰策略
}

// NewMemoryCaptchaCache 创建新的内存验证码缓存
func NewMemoryCaptchaCache() *MemoryCaptchaCache {
	cache := &MemoryCaptchaCache{
		data:     make(map[string]cacheItem),
		maxItems: MaxCaptchaItems,
		stopChan: make(chan struct{}),
		stopped:  false,
	}

	// 启动定期清理过期缓存的协程
	go cache.cleanupLoop()

	return cache
}

// SetCaptcha 设置验证码
func (c *MemoryCaptchaCache) SetCaptcha(id string, code string, expiration time.Duration) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// 检查缓存容量
	if len(c.data) >= c.maxItems {
		// 缓存已满，先尝试清理过期项
		c.cleanupExpiredLocked()

		// 清理后仍然满，则淘汰最旧的项
		if len(c.data) >= c.maxItems {
			c.evictOldestLocked()
		}

		// 如果还是满（极端情况），返回错误
		if len(c.data) >= c.maxItems {
			return ErrCacheFull
		}
	}

	now := time.Now()
	c.data[id] = cacheItem{
		value:      code,
		expiration: now.Add(expiration),
		createdAt:  now,
	}

	return nil
}

// GetCaptcha 获取验证码
func (c *MemoryCaptchaCache) GetCaptcha(id string) (string, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	item, exists := c.data[id]
	if !exists {
		return "", false
	}

	// 检查是否过期
	if time.Now().After(item.expiration) {
		return "", false
	}

	return item.value, true
}

// DeleteCaptcha 删除验证码
func (c *MemoryCaptchaCache) DeleteCaptcha(id string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	delete(c.data, id)
	return nil
}

// cleanupLoop 定期清理过期缓存
func (c *MemoryCaptchaCache) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanup()
		case <-c.stopChan:
			// 收到停止信号，退出goroutine
			return
		}
	}
}

// Stop 停止清理goroutine
func (c *MemoryCaptchaCache) Stop() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.stopped {
		c.stopped = true
		close(c.stopChan)
	}
}

// cleanup 清理过期缓存
func (c *MemoryCaptchaCache) cleanup() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.cleanupExpiredLocked()
}

// cleanupExpiredLocked 清理过期缓存（需要持有锁）
func (c *MemoryCaptchaCache) cleanupExpiredLocked() {
	now := time.Now()
	for key, item := range c.data {
		if now.After(item.expiration) {
			delete(c.data, key)
		}
	}
}

// evictOldestLocked 淘汰最旧的缓存项（需要持有锁）
func (c *MemoryCaptchaCache) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time

	// 找到最旧的项
	first := true
	for key, item := range c.data {
		if first || item.createdAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = item.createdAt
			first = false
		}
	}

	// 删除最旧的项
	if oldestKey != "" {
		delete(c.data, oldestKey)
	}
}
