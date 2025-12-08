package utils

import (
	"context"
	"sort"
	"sync"
	"time"
)

// LogRateLimiter 日志速率限制器
type LogRateLimiter struct {
	limits     map[string]*rateLimitEntry
	mu         sync.RWMutex
	maxEntries int // 最大条目数限制
	ctx        context.Context
	cancel     context.CancelFunc
	stopped    bool
}

type rateLimitEntry struct {
	lastLog   time.Time
	count     int64
	interval  time.Duration
	threshold int64
}

const maxLogRateLimitEntries = 1000 // 最多存储1000个不同的日志key

var globalLogRateLimiter = newLogRateLimiter()

func newLogRateLimiter() *LogRateLimiter {
	ctx, cancel := context.WithCancel(context.Background())
	l := &LogRateLimiter{
		limits:     make(map[string]*rateLimitEntry),
		maxEntries: maxLogRateLimitEntries,
		ctx:        ctx,
		cancel:     cancel,
		stopped:    false,
	}
	// 启动后台清理goroutine
	go l.cleanupLoop()
	return l
}

// GetLogRateLimiter 获取全局日志速率限制器
func GetLogRateLimiter() *LogRateLimiter {
	return globalLogRateLimiter
}

// Stop 停止清理goroutine
func (l *LogRateLimiter) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.stopped {
		l.stopped = true
		l.cancel()
	}
}

// ShouldLog 检查是否应该记录日志
func (l *LogRateLimiter) ShouldLog(key string, interval time.Duration, threshold int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 检查容量限制，防止内存无限增长
	if len(l.limits) >= l.maxEntries {
		// 强制清理最旧的50%条目
		type entryTime struct {
			key  string
			time time.Time
		}
		entries := make([]entryTime, 0, len(l.limits))
		for k, e := range l.limits {
			entries = append(entries, entryTime{key: k, time: e.lastLog})
		}
		// 按时间排序
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].time.Before(entries[j].time)
		})
		// 删除最旧的50%
		deleteCount := len(entries) / 2
		if deleteCount < 1 {
			deleteCount = 1
		}
		for i := 0; i < deleteCount && i < len(entries); i++ {
			delete(l.limits, entries[i].key)
		}
		// 如果清理后仍然满（不应该发生），拒绝新条目
		if len(l.limits) >= l.maxEntries {
			return false
		}
	}

	now := time.Now()
	entry, exists := l.limits[key]

	if !exists {
		l.limits[key] = &rateLimitEntry{
			lastLog:   now,
			count:     1,
			interval:  interval,
			threshold: threshold,
		}
		return true
	}

	// 检查是否在同一时间窗口内
	if now.Sub(entry.lastLog) < entry.interval {
		entry.count++
		return entry.count <= entry.threshold
	}

	// 新的时间窗口，重置计数
	entry.lastLog = now
	entry.count = 1
	return true
}

// ShouldLogWithMessage 检查包含消息内容的日志是否应该记录
func (l *LogRateLimiter) ShouldLogWithMessage(message string, interval time.Duration) bool {
	return l.ShouldLog(message, interval, 1) // 相同消息在间隔内只记录一次
}

// CleanupOldEntries 清理旧的限制条目（更积极的清理策略）
func (l *LogRateLimiter) CleanupOldEntries() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	var toDelete []string
	// 清理超过30分钟未使用的条目
	for key, entry := range l.limits {
		if now.Sub(entry.lastLog) > 30*time.Minute {
			toDelete = append(toDelete, key)
		}
	}

	for _, key := range toDelete {
		delete(l.limits, key)
	}

	// 如果清理后仍超过容量的80%，删除最旧的50%
	if len(l.limits) > int(float64(l.maxEntries)*0.8) {
		type entryWithTime struct {
			key  string
			time time.Time
		}

		entries := make([]entryWithTime, 0, len(l.limits))
		for k, e := range l.limits {
			entries = append(entries, entryWithTime{key: k, time: e.lastLog})
		}

		// 按时间排序
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].time.Before(entries[j].time)
		})

		// 删除最旧的一半
		deleteCount := len(entries) / 2
		for i := 0; i < deleteCount; i++ {
			delete(l.limits, entries[i].key)
		}
	}
}

// cleanupLoop 后台清理循环，定期清理过期条目
func (l *LogRateLimiter) cleanupLoop() {
	defer func() {
		if r := recover(); r != nil {
			// 静默失败，避免影响主程序
		}
	}()

	ticker := time.NewTicker(10 * time.Minute) // 每10分钟清理一次
	defer ticker.Stop()

	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			l.CleanupOldEntries()
		}
	}
}

// StartCleanupTask 启动清理任务（为了兼容性保留，实际上已经在init时启动）
func (l *LogRateLimiter) StartCleanupTask(ctx context.Context) {
	// 已经在newLogRateLimiter中启动，这里不做任何事情
}
