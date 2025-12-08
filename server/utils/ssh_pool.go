package utils

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// SSHConnectionPool SSH连接池（Provider级别复用）
type SSHConnectionPool struct {
	conns          map[uint]*SSHClient // providerID -> SSH连接
	configs        map[uint]SSHConfig  // providerID -> SSH配置（用于检测配置变更）
	lastUsed       map[uint]time.Time  // providerID -> 最后使用时间
	mu             sync.RWMutex        // 读写锁
	maxIdleTime    time.Duration       // 最大空闲时间
	maxConnections int                 // 最大连接数（防止无限增长）
	maxAge         time.Duration       // 连接最大存活时间（强制过期）
	logger         *zap.Logger         // 日志记录器
	ctx            context.Context     // 生命周期控制
	cancel         context.CancelFunc  // 取消函数
}

const (
	defaultMaxConnections = 100             // 默认最大连接数
	defaultMaxAge         = 1 * time.Hour   // 默认连接最大存活 1 小时
	cleanupInterval       = 5 * time.Minute // 清理间隔
)

// NewSSHConnectionPool 创建SSH连接池
func NewSSHConnectionPool(maxIdleTime time.Duration, logger *zap.Logger) *SSHConnectionPool {
	ctx, cancel := context.WithCancel(context.Background())
	pool := &SSHConnectionPool{
		conns:          make(map[uint]*SSHClient),
		configs:        make(map[uint]SSHConfig),
		lastUsed:       make(map[uint]time.Time),
		maxIdleTime:    maxIdleTime,
		maxConnections: defaultMaxConnections,
		maxAge:         defaultMaxAge,
		logger:         logger,
		ctx:            ctx,
		cancel:         cancel,
	}

	// 启动后台清理goroutine
	go pool.cleanupIdleConnections()

	return pool
}

// GetOrCreate 获取或创建SSH连接（线程安全，支持配置变更检测）
// 重要：每个providerID对应一个独立的SSH连接，不会串用
func (p *SSHConnectionPool) GetOrCreate(providerID uint, config SSHConfig) (*SSHClient, error) {
	// 先尝试获取现有连接（读锁）
	p.mu.RLock()
	if client, exists := p.conns[providerID]; exists {
		// 检查配置是否变更
		oldConfig, configExists := p.configs[providerID]
		configChanged := !configExists || !p.isSameConfig(oldConfig, config)

		// 检查连接年龄
		createTime, hasTime := p.lastUsed[providerID]
		tooOld := hasTime && time.Since(createTime) > p.maxAge

		// 如果配置未变更且连接健康且未过期，复用
		if !configChanged && !tooOld && client.IsHealthy() {
			p.mu.RUnlock()
			// 更新最后使用时间（需要写锁）
			p.mu.Lock()
			p.lastUsed[providerID] = time.Now()
			p.mu.Unlock()
			if p.logger != nil {
				p.logger.Debug("复用现有SSH连接",
					zap.Uint("providerID", providerID))
			}
			return client, nil
		}

		// 配置变更、连接失效或过期，需要重建
		if p.logger != nil {
			if configChanged {
				p.logger.Info("检测到Provider配置变更，重建SSH连接",
					zap.Uint("providerID", providerID))
			} else if tooOld {
				p.logger.Info("SSH连接已过期，重建",
					zap.Uint("providerID", providerID),
					zap.Duration("age", time.Since(createTime)))
			} else {
				p.logger.Warn("SSH连接已失效，将重建",
					zap.Uint("providerID", providerID))
			}
		}
	}
	p.mu.RUnlock()

	// 需要创建新连接（写锁）
	p.mu.Lock()
	defer p.mu.Unlock()

	// 双重检查：可能其他goroutine已经创建了连接
	if client, exists := p.conns[providerID]; exists {
		oldConfig, configExists := p.configs[providerID]
		configChanged := !configExists || !p.isSameConfig(oldConfig, config)

		createTime, hasTime := p.lastUsed[providerID]
		tooOld := hasTime && time.Since(createTime) > p.maxAge

		if !configChanged && !tooOld && client.IsHealthy() {
			p.lastUsed[providerID] = time.Now()
			if p.logger != nil {
				p.logger.Debug("其他goroutine已创建连接，复用",
					zap.Uint("providerID", providerID))
			}
			return client, nil
		}
	}

	// 检查连接数限制
	if len(p.conns) >= p.maxConnections {
		// 达到上限，强制清理最旧的连接
		p.evictOldestConnection()
	}

	// 创建新连接
	client, err := NewSSHClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH client: %w", err)
	}

	// 关闭旧连接（如果存在）
	if oldClient, exists := p.conns[providerID]; exists {
		oldClient.Close()
		if p.logger != nil {
			p.logger.Info("关闭旧SSH连接（配置变更或失效）",
				zap.Uint("providerID", providerID))
		}
	}

	// 缓存新连接、配置和时间
	p.conns[providerID] = client
	p.configs[providerID] = config
	p.lastUsed[providerID] = time.Now()

	if p.logger != nil {
		p.logger.Info("创建新SSH连接",
			zap.Uint("providerID", providerID),
			zap.String("host", config.Host))
	}

	return client, nil
}

// evictOldestConnection 驱逐最旧的连接（需持有写锁，同步清理所有Map）
func (p *SSHConnectionPool) evictOldestConnection() {
	if len(p.conns) == 0 {
		return
	}

	var oldestID uint
	var oldestTime time.Time
	first := true

	for id, t := range p.lastUsed {
		if first || t.Before(oldestTime) {
			oldestID = id
			oldestTime = t
			first = false
		}
	}

	if client, exists := p.conns[oldestID]; exists {
		// 原子性地从所有Map中删除（必须在同一临界区内）
		delete(p.conns, oldestID)
		delete(p.configs, oldestID)
		delete(p.lastUsed, oldestID)
		p.mu.Unlock()

		// 释放锁后再关闭连接
		client.Close()
		p.mu.Lock()

		if p.logger != nil {
			p.logger.Warn("达到连接数上限，驱逐最旧连接并清理所有相关资源",
				zap.Uint("providerID", oldestID),
				zap.Duration("age", time.Since(oldestTime)))
		}
	}
}

// Remove 移除指定Provider的连接（完全原子性同步清理所有相关Map）
func (p *SSHConnectionPool) Remove(providerID uint) {
	// 先获取锁并提取client引用
	p.mu.Lock()
	client, hasClient := p.conns[providerID]
	_, hasConfig := p.configs[providerID]
	_, hasLastUsed := p.lastUsed[providerID]

	// 原子性删除：在同一临界区内从所有map中删除（防止孤立条目）
	delete(p.conns, providerID)
	delete(p.configs, providerID)
	delete(p.lastUsed, providerID)
	p.mu.Unlock()

	// 释放锁后再关闭连接（避免在持有锁时调用可能阻塞的Close）
	if hasClient && client != nil {
		client.Close()
	}

	if p.logger != nil {
		if hasClient || hasConfig || hasLastUsed {
			p.logger.Info("原子性移除SSH连接及所有相关资源",
				zap.Uint("providerID", providerID),
				zap.Bool("hadClient", hasClient),
				zap.Bool("hadConfig", hasConfig),
				zap.Bool("hadLastUsed", hasLastUsed))
		} else {
			p.logger.Debug("SSH连接不存在，已执行防御性清理",
				zap.Uint("providerID", providerID))
		}
	}
}

// CloseAll 关闭所有连接
func (p *SSHConnectionPool) CloseAll() {
	// 先取消context，停止后台清理goroutine
	if p.cancel != nil {
		p.cancel()
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for providerID, client := range p.conns {
		client.Close()
		if p.logger != nil {
			p.logger.Debug("关闭SSH连接",
				zap.Uint("providerID", providerID))
		}
	}

	p.conns = make(map[uint]*SSHClient)
	p.configs = make(map[uint]SSHConfig)
	p.lastUsed = make(map[uint]time.Time)

	if p.logger != nil {
		p.logger.Info("已关闭所有SSH连接")
	}
}

// GetStats 获取连接池统计信息
func (p *SSHConnectionPool) GetStats() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := map[string]interface{}{
		"total_connections": len(p.conns),
		"max_idle_time":     p.maxIdleTime.String(),
	}

	healthyCount := 0
	for _, client := range p.conns {
		if client.IsHealthy() {
			healthyCount++
		}
	}
	stats["healthy_connections"] = healthyCount

	return stats
}

// SSHPoolDetailedStats SSH连接池详细统计信息
type SSHPoolDetailedStats struct {
	TotalConnections     int           // 总连接数
	HealthyConnections   int           // 健康连接数
	UnhealthyConnections int           // 不健康连接数
	IdleConnections      int           // 空闲连接数（超过1分钟未使用）
	ActiveConnections    int           // 活跃连接数（1分钟内使用过）
	MaxConnections       int           // 最大连接数限制
	Utilization          float64       // 连接池利用率 (总连接数/最大连接数)
	MaxIdleTime          time.Duration // 最大空闲时间配置
	MaxAge               time.Duration // 连接最大存活时间配置
	OldestConnectionAge  time.Duration // 最老连接的年龄
	NewestConnectionAge  time.Duration // 最新连接的年龄
	AvgConnectionAge     time.Duration // 平均连接年龄
}

// GetDetailedStats 获取详细的连接池统计信息（用于性能监控）
func (p *SSHConnectionPool) GetDetailedStats() (totalConnections int, healthyConnections int) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	totalConnections = len(p.conns)
	healthyConnections = 0

	for _, client := range p.conns {
		if client.IsHealthy() {
			healthyConnections++
		}
	}

	return
}

// GetEnhancedStats 获取增强的连接池统计信息（更详细的监控数据）
func (p *SSHConnectionPool) GetEnhancedStats() SSHPoolDetailedStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	now := time.Now()
	stats := SSHPoolDetailedStats{
		TotalConnections:     len(p.conns),
		HealthyConnections:   0,
		UnhealthyConnections: 0,
		IdleConnections:      0,
		ActiveConnections:    0,
		MaxConnections:       p.maxConnections,
		MaxIdleTime:          p.maxIdleTime,
		MaxAge:               p.maxAge,
	}

	// 计算利用率
	if p.maxConnections > 0 {
		stats.Utilization = float64(len(p.conns)) / float64(p.maxConnections) * 100
	}

	// 统计各项指标
	var totalAge time.Duration
	var oldestAge time.Duration
	var newestAge time.Duration
	first := true

	for providerID, client := range p.conns {
		// 健康状态
		if client.IsHealthy() {
			stats.HealthyConnections++
		} else {
			stats.UnhealthyConnections++
		}

		// 活跃度和年龄统计
		if lastUse, exists := p.lastUsed[providerID]; exists {
			age := now.Sub(lastUse)
			totalAge += age

			// 空闲/活跃判断（1分钟为界）
			if age > time.Minute {
				stats.IdleConnections++
			} else {
				stats.ActiveConnections++
			}

			// 更新最老和最新连接
			if first {
				oldestAge = age
				newestAge = age
				first = false
			} else {
				if age > oldestAge {
					oldestAge = age
				}
				if age < newestAge {
					newestAge = age
				}
			}
		}
	}

	// 计算平均连接年龄
	if len(p.conns) > 0 {
		stats.AvgConnectionAge = totalAge / time.Duration(len(p.conns))
		stats.OldestConnectionAge = oldestAge
		stats.NewestConnectionAge = newestAge
	}

	return stats
}

// isSameConfig 比较两个SSH配置是否相同
func (p *SSHConnectionPool) isSameConfig(a, b SSHConfig) bool {
	return a.Host == b.Host &&
		a.Port == b.Port &&
		a.Username == b.Username &&
		a.Password == b.Password &&
		a.PrivateKey == b.PrivateKey
}

// cleanupIdleConnections 自适应清理空闲、不健康和过期的连接
func (p *SSHConnectionPool) cleanupIdleConnections() {
	// 确俟ticker在panic时也能停止，防止goroutine泄漏
	ticker := time.NewTicker(cleanupInterval)
	defer func() {
		ticker.Stop()
		if r := recover(); r != nil {
			if p.logger != nil {
				p.logger.Error("SSH连接池清理goroutine panic",
					zap.Any("panic", r),
					zap.Stack("stack"))
			}
		}
		if p.logger != nil {
			p.logger.Info("SSH连接池清理goroutine已停止")
		}
	}()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.cleanup()
		}
	}
}

// cleanup 执行清理操作
func (p *SSHConnectionPool) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	var toRemove []uint
	var toClose []*SSHClient
	now := time.Now()

	for providerID, client := range p.conns {
		shouldRemove := false
		reason := ""

		// 检查1: 连接健康性
		if !client.IsHealthy() {
			shouldRemove = true
			reason = "unhealthy"
		}

		// 检查2: 空闲时间
		if !shouldRemove {
			if lastUse, exists := p.lastUsed[providerID]; exists {
				if now.Sub(lastUse) > p.maxIdleTime {
					shouldRemove = true
					reason = "idle_timeout"
				}
			}
		}

		// 检查3: 连接年龄
		if !shouldRemove {
			if lastUse, exists := p.lastUsed[providerID]; exists {
				if now.Sub(lastUse) > p.maxAge {
					shouldRemove = true
					reason = "max_age"
				}
			}
		}

		if shouldRemove {
			toRemove = append(toRemove, providerID)
			toClose = append(toClose, client)
			if p.logger != nil {
				p.logger.Info("清理SSH连接",
					zap.Uint("providerID", providerID),
					zap.String("reason", reason))
			}
		}
	}

	// 原子性地从所有map中移除（必须在同一临界区内）
	for _, providerID := range toRemove {
		delete(p.conns, providerID)
		delete(p.configs, providerID)
		delete(p.lastUsed, providerID)
	}
	p.mu.Unlock()

	// 释放锁后再关闭连接（避免在持有锁时IO阻塞）
	for _, client := range toClose {
		if client != nil {
			client.Close()
		}
	}

	// 重新获取锁以执行孤立条目清理
	p.mu.Lock()

	if len(toRemove) > 0 && p.logger != nil {
		p.logger.Info("SSH连接池清理完成",
			zap.Int("cleaned", len(toRemove)),
			zap.Int("remaining", len(p.conns)))
	}

	// 额外检查：清理孤立的 lastUsed 和 configs 条目（防御性编程，防止内存泄漏）
	p.cleanupOrphanedEntries()
}

// cleanupOrphanedEntries 清理孤立的 map 条目（防御性编程，防止内存泄漏）
func (p *SSHConnectionPool) cleanupOrphanedEntries() {
	// 检查 lastUsed 中是否有 conns 中不存在的条目
	orphanedLastUsed := 0
	for id := range p.lastUsed {
		if _, exists := p.conns[id]; !exists {
			delete(p.lastUsed, id)
			orphanedLastUsed++
		}
	}

	// 检查 configs 中是否有 conns 中不存在的条目
	orphanedConfigs := 0
	for id := range p.configs {
		if _, exists := p.conns[id]; !exists {
			delete(p.configs, id)
			orphanedConfigs++
		}
	}

	if (orphanedLastUsed > 0 || orphanedConfigs > 0) && p.logger != nil {
		p.logger.Warn("发现并清理SSH连接池孤立条目（防止内存泄漏）",
			zap.Int("orphanedLastUsed", orphanedLastUsed),
			zap.Int("orphanedConfigs", orphanedConfigs))
	}
}

// RemoveProvider 移除指定Provider的SSH连接（用于Provider删除时自动清理，原子性同步删除所有Map）
func (p *SSHConnectionPool) RemoveProvider(providerID uint) {
	p.mu.Lock()
	client, exists := p.conns[providerID]

	// 原子性删除：在同一临界区内从所有map中删除（防止内存泄漏）
	delete(p.conns, providerID)
	delete(p.configs, providerID)
	delete(p.lastUsed, providerID)
	p.mu.Unlock()

	// 释放锁后再关闭连接
	if exists && client != nil {
		client.Close()
		if p.logger != nil {
			p.logger.Info("原子性清理已删除Provider的SSH连接及所有相关资源",
				zap.Uint("providerID", providerID))
		}
	}
}
