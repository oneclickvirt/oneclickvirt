package utils

import (
	"container/list"
	"errors"
	"sync"
	"time"
)

// 验证码缓存配置常量
const (
	// MaxCaptchaItems 验证码缓存最大数量
	MaxCaptchaItems = 3000
	// CaptchaCleanupInterval 验证码清理间隔
	CaptchaCleanupInterval = 3 * time.Minute
)

var (
	// ErrCacheFull 缓存已满错误
	ErrCacheFull = errors.New("验证码缓存已满，请稍后再试")
)

// CaptchaCache 验证码缓存接口
type CaptchaCache interface {
	// Set 设置验证码
	Set(id string, code string) error
	// Get 获取验证码（验证后可选清除）
	Get(id string, clear bool) string
	// Verify 验证验证码
	Verify(id string, code string, clear bool) bool
}

// lruCacheItem LRU缓存项
type lruCacheItem struct {
	key        string
	value      string
	expiration time.Time
}

// LRUCaptchaCache 基于LRU的验证码缓存实现
type LRUCaptchaCache struct {
	capacity int
	items    map[string]*list.Element // key -> list element
	lruList  *list.List               // 双向链表维护LRU顺序
	mutex    sync.RWMutex
	stopChan chan struct{}
	stopped  bool
}

// NewLRUCaptchaCache 创建新的LRU验证码缓存
func NewLRUCaptchaCache(capacity int) *LRUCaptchaCache {
	if capacity <= 0 {
		capacity = MaxCaptchaItems
	}

	cache := &LRUCaptchaCache{
		capacity: capacity,
		items:    make(map[string]*list.Element, capacity),
		lruList:  list.New(),
		stopChan: make(chan struct{}),
		stopped:  false,
	}

	// 启动定期清理过期缓存
	go cache.cleanupLoop()

	return cache
}

// Set 设置验证码（实现base64Captcha.Store接口）
func (c *LRUCaptchaCache) Set(id string, value string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// 如果key已存在，更新值并移到前面
	if elem, ok := c.items[id]; ok {
		c.lruList.MoveToFront(elem)
		item := elem.Value.(*lruCacheItem)
		item.value = value
		item.expiration = time.Now().Add(10 * time.Minute) // 验证码10分钟过期
		return nil
	}

	// 如果缓存已满，移除最久未使用的项
	if c.lruList.Len() >= c.capacity {
		c.evictOldest()
	}

	// 添加新项到前面
	item := &lruCacheItem{
		key:        id,
		value:      value,
		expiration: time.Now().Add(10 * time.Minute),
	}
	elem := c.lruList.PushFront(item)
	c.items[id] = elem

	return nil
}

// Get 获取验证码（实现base64Captcha.Store接口）
func (c *LRUCaptchaCache) Get(id string, clear bool) string {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	elem, ok := c.items[id]
	if !ok {
		return ""
	}

	item := elem.Value.(*lruCacheItem)

	// 检查是否过期
	if time.Now().After(item.expiration) {
		c.removeElement(elem)
		return ""
	}

	// 移到前面（最近使用）
	c.lruList.MoveToFront(elem)

	value := item.value

	// 如果需要清除，删除该项
	if clear {
		c.removeElement(elem)
	}

	return value
}

// Verify 验证验证码（实现base64Captcha.Store接口）
func (c *LRUCaptchaCache) Verify(id, answer string, clear bool) bool {
	value := c.Get(id, clear)
	if value == "" {
		return false
	}
	return value == answer
}

// evictOldest 移除最久未使用的项（需要持有锁）
func (c *LRUCaptchaCache) evictOldest() {
	elem := c.lruList.Back()
	if elem != nil {
		c.removeElement(elem)
	}
}

// removeElement 移除指定元素（需要持有锁）
func (c *LRUCaptchaCache) removeElement(elem *list.Element) {
	c.lruList.Remove(elem)
	item := elem.Value.(*lruCacheItem)
	delete(c.items, item.key)
}

// cleanupLoop 定期清理过期缓存
func (c *LRUCaptchaCache) cleanupLoop() {
	// 确俟ticker在panic时也能停止，防止goroutine泄漏
	ticker := time.NewTicker(CaptchaCleanupInterval)
	defer func() {
		ticker.Stop()
		if r := recover(); r != nil {
			// 静默失败，不记录日志（避免循环依赖）
		}
	}()

	for {
		select {
		case <-ticker.C:
			c.cleanupExpired()
		case <-c.stopChan:
			return
		}
	}
}

// cleanupExpired 清理过期项（从后向前遍历，LRU链表最旧的在后面）
func (c *LRUCaptchaCache) cleanupExpired() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	now := time.Now()
	cleanedCount := 0
	maxCleanup := 100 // 每次最多清理100个

	// 从后向前遍历（最旧的在后面）
	for elem := c.lruList.Back(); elem != nil && cleanedCount < maxCleanup; {
		item := elem.Value.(*lruCacheItem)
		prev := elem.Prev() // 先保存前一个元素

		if now.After(item.expiration) {
			c.removeElement(elem)
			cleanedCount++
		} else {
			// 如果遇到未过期的，后面的都是更新的，可以停止
			break
		}

		elem = prev
	}
}

// Stop 停止清理goroutine
func (c *LRUCaptchaCache) Stop() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.stopped {
		c.stopped = true
		close(c.stopChan)
	}
}

// Len 返回当前缓存项数量
func (c *LRUCaptchaCache) Len() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.lruList.Len()
}

// StatsCache 统计数据缓存
type StatsCache struct {
	data       interface{}
	mutex      sync.RWMutex
	expiration time.Time
	updateFunc func() (interface{}, error) // 更新函数
}

// NewStatsCache 创建新的统计数据缓存
func NewStatsCache(updateFunc func() (interface{}, error)) *StatsCache {
	return &StatsCache{
		updateFunc: updateFunc,
	}
}

// Get 获取缓存的统计数据，如果缓存过期则自动更新
func (c *StatsCache) Get() (interface{}, error) {
	c.mutex.RLock()
	// 检查缓存是否有效
	if c.data != nil && time.Now().Before(c.expiration) {
		data := c.data
		c.mutex.RUnlock()
		return data, nil
	}
	c.mutex.RUnlock()

	// 缓存无效，需要更新
	return c.Update()
}

// Update 强制更新缓存
func (c *StatsCache) Update() (interface{}, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// 调用更新函数获取新数据
	data, err := c.updateFunc()
	if err != nil {
		return nil, err
	}

	// 更新缓存
	c.data = data
	c.expiration = time.Now().Add(5 * time.Minute) // 5分钟过期

	return data, nil
}

// IsExpired 检查缓存是否过期
func (c *StatsCache) IsExpired() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.data == nil || time.Now().After(c.expiration)
}
