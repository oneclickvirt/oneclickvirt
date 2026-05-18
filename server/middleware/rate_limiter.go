package middleware

import (
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// RateLimiter 基于令牌桶的内存限流器
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	config  RateLimitConfig
}

// RateLimitConfig 限流配置
type RateLimitConfig struct {
	Enabled         bool          // 是否启用
	GlobalRPS       int           // 全局每秒请求数（所有用户共享）
	PerUserRPS      int           // 每用户每秒请求数
	PerIPRPS        int           // 每IP每秒请求数
	BurstMultiplier int           // 突发倍数（允许瞬时超过RPS的倍数）
	CleanupInterval time.Duration // 清理过期桶的间隔
	BucketTTL       time.Duration // 桶的过期时间
}

// tokenBucket 令牌桶
type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
	rate       float64 // 令牌/秒
	burst      float64 // 最大令牌数
}

// DefaultRateLimitConfig 默认限流配置
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Enabled:         true,
		GlobalRPS:       200, // 全局200 QPS
		PerUserRPS:      30,  // 每用户30 QPS
		PerIPRPS:        20,  // 每IP 20 QPS
		BurstMultiplier: 3,   // 允许3倍突发
		CleanupInterval: 5 * time.Minute,
		BucketTTL:       10 * time.Minute,
	}
}

var (
	globalRateLimiter     *RateLimiter
	globalRateLimiterOnce sync.Once
)

// GetRateLimiter 获取全局限流器
func GetRateLimiter() *RateLimiter {
	globalRateLimiterOnce.Do(func() {
		config := DefaultRateLimitConfig()
		// 从系统配置读取（如果有的话）
		rl := &RateLimiter{
			buckets: make(map[string]*tokenBucket),
			config:  config,
		}
		// 启动定期清理
		go rl.cleanupLoop()
		globalRateLimiter = rl
	})
	return globalRateLimiter
}

// RateLimit 限流中间件
func RateLimit() gin.HandlerFunc {
	limiter := GetRateLimiter()

	return func(c *gin.Context) {
		if !limiter.config.Enabled {
			c.Next()
			return
		}

		// 跳过WebSocket连接（WebSocket有自己的流控）
		if c.GetHeader("Upgrade") == "websocket" {
			c.Next()
			return
		}

		clientIP := c.ClientIP()
		userKey := ""
		if authCtx, ok := GetAuthContext(c); ok {
			userKey = fmt.Sprintf("%d", authCtx.UserID)
		}

		// 1. 检查IP级别限流
		if !limiter.allow("ip:"+clientIP, limiter.config.PerIPRPS) {
			global.APP_LOG.Warn("IP级别限流触发",
				zap.String("ip", clientIP),
				zap.String("path", c.Request.URL.Path))
			common.ResponseWithError(c, common.NewError(common.CodeTooManyRequests, "请求过于频繁，请稍后重试"))
			c.Abort()
			return
		}

		// 2. 检查用户级别限流
		if userKey != "" {
			if !limiter.allow("user:"+userKey, limiter.config.PerUserRPS) {
				global.APP_LOG.Warn("用户级别限流触发",
					zap.String("userKey", userKey),
					zap.String("ip", clientIP),
					zap.String("path", c.Request.URL.Path))
				common.ResponseWithError(c, common.NewError(common.CodeTooManyRequests, "操作过于频繁，请稍后重试"))
				c.Abort()
				return
			}
		}

		// 3. 检查全局限流
		if !limiter.allow("global", limiter.config.GlobalRPS) {
			global.APP_LOG.Warn("全局限流触发",
				zap.String("path", c.Request.URL.Path))
			common.ResponseWithError(c, common.NewError(common.CodeTooManyRequests, "系统繁忙，请稍后重试"))
			c.Abort()
			return
		}

		c.Next()
	}
}

// allow 检查是否允许通过
func (rl *RateLimiter) allow(key string, rps int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	bucket, exists := rl.buckets[key]

	if !exists {
		// 创建新桶
		rate := float64(rps)
		burst := rate * float64(rl.config.BurstMultiplier)
		bucket = &tokenBucket{
			tokens:     burst, // 新桶初始满令牌
			lastRefill: now,
			rate:       rate,
			burst:      burst,
		}
		rl.buckets[key] = bucket
	}

	// 补充令牌
	elapsed := now.Sub(bucket.lastRefill).Seconds()
	bucket.tokens += elapsed * bucket.rate
	if bucket.tokens > bucket.burst {
		bucket.tokens = bucket.burst
	}
	bucket.lastRefill = now

	// 检查是否有可用令牌
	if bucket.tokens >= 1.0 {
		bucket.tokens -= 1.0
		return true
	}

	return false
}

// cleanupLoop 定期清理过期的桶
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.config.CleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		rl.cleanup()
	}
}

// cleanup 清理过期的桶
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for key, bucket := range rl.buckets {
		if now.Sub(bucket.lastRefill) > rl.config.BucketTTL {
			delete(rl.buckets, key)
		}
	}
}

// GetRateLimiterStats 获取限流统计信息（用于调试）
func (rl *RateLimiter) GetRateLimiterStats() map[string]interface{} {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	return map[string]interface{}{
		"activeBuckets": len(rl.buckets),
		"config":        rl.config,
	}
}
