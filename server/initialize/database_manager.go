package initialize

import (
	"context"
	"math/rand"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/initialize/internal"
	"oneclickvirt/model/config"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	dbManager     *DatabaseManager
	dbManagerOnce sync.Once
)

// DatabaseManager 数据库连接管理器
type DatabaseManager struct {
	mu                   sync.RWMutex
	db                   *gorm.DB
	config               config.MysqlConfig
	heartbeatTicker      *time.Ticker
	heartbeatStop        chan struct{}
	reconnecting         bool
	lastError            string
	lastErrorAt          time.Time
	maxReconnectRetry    int
	reconnectInterval    time.Duration
	onConnectionRestored func(*gorm.DB)
	ctx                  context.Context
	cancel               context.CancelFunc
}

// GetDatabaseManager 获取数据库管理器单例
func GetDatabaseManager() *DatabaseManager {
	dbManagerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		dbManager = &DatabaseManager{
			heartbeatStop:     make(chan struct{}),
			maxReconnectRetry: 5,
			reconnectInterval: 5 * time.Second,
			ctx:               ctx,
			cancel:            cancel,
		}
	})
	return dbManager
}

// Initialize 初始化数据库连接管理器
func (dm *DatabaseManager) Initialize(cfg config.MysqlConfig) (*gorm.DB, error) {
	dm.mu.Lock()
	dm.config = cfg
	dm.mu.Unlock()

	// 首次连接
	db, err := dm.connect()
	if err != nil {
		global.APP_LOG.Error("数据库初始化失败", zap.Error(err))
		dm.mu.Lock()
		dm.db = nil
		dm.mu.Unlock()
		// Initial connection failures previously left the process permanently in
		// partial-init mode. Keep the heartbeat alive and retry immediately; later
		// heartbeat ticks continue retrying if the database starts slowly.
		dm.startHeartbeat()
		dm.updateGlobalStats()
		go dm.reconnect()
		return nil, err
	}

	dm.mu.Lock()
	dm.db = db
	dm.mu.Unlock()

	// 启动心跳检测
	dm.startHeartbeat()

	// 更新全局统计信息
	dm.updateGlobalStats()

	global.APP_LOG.Info("数据库连接管理器初始化成功")
	return db, nil
}

// SetConnectionRestoredHandler registers the system-level recovery hook. The
// callback runs after a replacement connection has become globally visible.
func (dm *DatabaseManager) SetConnectionRestoredHandler(handler func(*gorm.DB)) {
	dm.mu.Lock()
	dm.onConnectionRestored = handler
	dm.mu.Unlock()
}

func (dm *DatabaseManager) notifyConnectionRestored(db *gorm.DB) {
	dm.mu.RLock()
	handler := dm.onConnectionRestored
	dm.mu.RUnlock()
	if handler == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			global.APP_LOG.Error("数据库恢复回调发生panic", zap.Any("panic", recovered))
		}
	}()
	handler(db)
}

// GetDB 获取当前数据库连接
func (dm *DatabaseManager) GetDB() *gorm.DB {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.db
}

// GetStats 获取数据库管理器统计信息
func (dm *DatabaseManager) GetStats() global.DBManagerStats {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	return global.DBManagerStats{
		Connected:         dm.db != nil,
		Reconnecting:      dm.reconnecting,
		HeartbeatActive:   dm.heartbeatTicker != nil,
		MaxReconnectRetry: dm.maxReconnectRetry,
		ReconnectInterval: dm.reconnectInterval.String(),
		LastError:         dm.lastError,
		LastErrorAt: func() string {
			if dm.lastErrorAt.IsZero() {
				return ""
			}
			return dm.lastErrorAt.Format(time.RFC3339)
		}(),
	}
}

// updateGlobalStats 更新全局统计信息（供性能监控使用）
func (dm *DatabaseManager) updateGlobalStats() {
	stats := dm.GetStats()
	global.SetDBManagerStats(stats)
}

// connect 建立数据库连接
func (dm *DatabaseManager) connect() (*gorm.DB, error) {
	global.APP_LOG.Info("正在连接数据库...")

	connectionConfig := dm.refreshConnectionConfig()
	db, err := GormMysqlConnect(connectionConfig)
	if err != nil {
		dm.recordConnectionError(err)
		return nil, err
	}

	// 验证连接
	if err := validateDatabaseConnection(db); err != nil {
		dm.recordConnectionError(err)
		return nil, err
	}

	dm.clearConnectionError()
	global.APP_LOG.Info("数据库连接成功")
	return db, nil
}

func (dm *DatabaseManager) refreshConnectionConfig() config.MysqlConfig {
	runtimeConfig := global.GetAppConfig().Mysql
	latest := config.MysqlConfig{
		Path:         strings.TrimSpace(runtimeConfig.Path),
		Port:         strings.TrimSpace(runtimeConfig.Port),
		Config:       runtimeConfig.Config,
		Dbname:       runtimeConfig.Dbname,
		Username:     runtimeConfig.Username,
		Password:     runtimeConfig.Password,
		MaxIdleConns: runtimeConfig.MaxIdleConns,
		MaxOpenConns: runtimeConfig.MaxOpenConns,
		LogMode:      runtimeConfig.LogMode,
		LogZap:       runtimeConfig.LogZap,
		MaxLifetime:  runtimeConfig.MaxLifetime,
		AutoCreate:   runtimeConfig.AutoCreate,
	}

	dm.mu.Lock()
	defer dm.mu.Unlock()
	if hasRuntimeDatabaseConfig(latest) {
		dm.config = latest
	}
	return dm.config
}

func hasRuntimeDatabaseConfig(cfg config.MysqlConfig) bool {
	return cfg.Path != "" || cfg.Port != "" || cfg.Config != "" || cfg.Dbname != "" ||
		cfg.Username != "" || cfg.Password != "" || cfg.MaxIdleConns != 0 ||
		cfg.MaxOpenConns != 0 || cfg.LogMode != "" || cfg.LogZap || cfg.MaxLifetime != 0 || cfg.AutoCreate
}

func (dm *DatabaseManager) recordConnectionError(err error) {
	dm.mu.Lock()
	dm.lastError = err.Error()
	dm.lastErrorAt = time.Now()
	dm.mu.Unlock()
	dm.updateGlobalStats()
}

func (dm *DatabaseManager) clearConnectionError() {
	dm.mu.Lock()
	dm.lastError = ""
	dm.lastErrorAt = time.Time{}
	dm.mu.Unlock()
}

// startHeartbeat 启动心跳检测
func (dm *DatabaseManager) startHeartbeat() {
	// 如果已经在运行，先停止（通过dm.mu保护，防止并发双关闭）
	dm.mu.Lock()
	dm.stopHeartbeat()
	dm.heartbeatTicker = time.NewTicker(30 * time.Second) // 每30秒检测一次
	// 捕获当前 channel 和 ticker 引用（goroutine启动后字段可能被替换，必须提前捕获）
	stopChan := dm.heartbeatStop
	ticker := dm.heartbeatTicker
	dm.mu.Unlock()

	go func() {
		global.APP_LOG.Info("数据库心跳检测已启动")
		for {
			select {
			case <-dm.ctx.Done():
				global.APP_LOG.Info("数据库心跳检测已停止（系统关闭）")
				return
			case <-stopChan:
				global.APP_LOG.Info("数据库心跳检测已停止")
				return
			case <-ticker.C:
				dm.performHeartbeat()
			}
		}
	}()
}

// stopHeartbeat 停止心跳检测
func (dm *DatabaseManager) stopHeartbeat() {
	if dm.heartbeatTicker != nil {
		dm.heartbeatTicker.Stop()
		dm.heartbeatTicker = nil // 清除指针，防止重复调用
		close(dm.heartbeatStop)
		dm.heartbeatStop = make(chan struct{})
	}
}

// performHeartbeat 执行心跳检测
func (dm *DatabaseManager) performHeartbeat() {
	db := dm.GetDB()
	if db == nil {
		global.APP_LOG.Warn("数据库连接为空，尝试重连")
		dm.reconnect()
		return
	}

	sqlDB, err := db.DB()
	if err != nil {
		global.APP_LOG.Warn("获取数据库连接失败，尝试重连", zap.Error(err))
		dm.reconnect()
		return
	}

	// Ping测试
	if err := sqlDB.Ping(); err != nil {
		global.APP_LOG.Warn("数据库心跳检测失败，尝试重连", zap.Error(err))
		dm.reconnect()
		return
	}

	// 简单查询测试
	var result int
	if err := db.Raw("SELECT 1").Scan(&result).Error; err != nil {
		global.APP_LOG.Warn("数据库查询测试失败，尝试重连", zap.Error(err))
		dm.reconnect()
		return
	}

	// 检查连接池状态
	stats := sqlDB.Stats()
	usagePercent := float64(stats.OpenConnections) / float64(stats.MaxOpenConnections) * 100

	if usagePercent > 80 {
		global.APP_LOG.Warn("数据库连接池使用率较高",
			zap.Float64("usage_percent", usagePercent),
			zap.Int("open_connections", stats.OpenConnections),
			zap.Int("max_open_connections", stats.MaxOpenConnections),
			zap.Int("in_use", stats.InUse),
			zap.Int("idle", stats.Idle))
	}

	// 更新全局统计信息
	dm.updateGlobalStats()
}

// reconnect 重连数据库
func (dm *DatabaseManager) reconnect() {
	dm.mu.Lock()
	if dm.reconnecting {
		dm.mu.Unlock()
		return
	}
	dm.reconnecting = true
	dm.mu.Unlock()
	dm.updateGlobalStats()

	defer func() {
		dm.mu.Lock()
		dm.reconnecting = false
		dm.mu.Unlock()
		dm.updateGlobalStats()
	}()

	global.APP_LOG.Warn("开始数据库重连...")

	for i := 0; i < dm.maxReconnectRetry; i++ {
		select {
		case <-dm.ctx.Done():
			global.APP_LOG.Info("数据库重连中止（系统关闭）")
			return
		default:
		}

		global.APP_LOG.Debug("尝试重连数据库", zap.Int("attempt", i+1), zap.Int("max", dm.maxReconnectRetry))

		// 尝试连接
		newDB, err := dm.connect()
		if err != nil {
			global.APP_LOG.Warn("数据库重连失败",
				zap.Int("attempt", i+1),
				zap.Error(err))

			// 如果不是最后一次尝试，等待后再试
			if i < dm.maxReconnectRetry-1 {
				if !dm.waitForReconnect(i) {
					return
				}
			}
			continue
		}

		// 关闭旧连接
		dm.mu.Lock()
		oldDB := dm.db
		dm.db = newDB
		dm.mu.Unlock()

		if oldDB != nil {
			if sqlDB, err := oldDB.DB(); err == nil {
				sqlDB.Close()
			}
		}

		// 更新全局变量
		global.APP_DB = newDB

		// 更新全局统计信息
		dm.updateGlobalStats()

		global.APP_LOG.Info("数据库重连成功", zap.Int("attempt", i+1))
		dm.notifyConnectionRestored(newDB)
		return
	}

	global.APP_LOG.Error("数据库重连失败，已达到最大重试次数", zap.Int("max_retry", dm.maxReconnectRetry))
}

func (dm *DatabaseManager) waitForReconnect(attempt int) bool {
	delay := dm.reconnectDelay(attempt)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-dm.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (dm *DatabaseManager) reconnectDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	shift := attempt
	if shift > 3 {
		shift = 3
	}
	base := dm.reconnectInterval * time.Duration(1<<shift)
	if base > time.Minute {
		base = time.Minute
	}
	jitterWindow := base / 2
	if jitterWindow <= 0 {
		return base
	}
	return base + time.Duration(rand.Int63n(int64(jitterWindow)+1))
}

// Shutdown 关闭数据库连接管理器
func (dm *DatabaseManager) Shutdown() {
	global.APP_LOG.Info("正在关闭数据库连接管理器...")

	// 停止心跳检测（先取锁再关闭，未避免并发双关闭）
	dm.cancel()
	dm.mu.Lock()
	dm.stopHeartbeat()
	dm.mu.Unlock()

	// 关闭数据库连接
	dm.mu.Lock()
	defer dm.mu.Unlock()

	if dm.db != nil {
		if sqlDB, err := dm.db.DB(); err == nil {
			if err := sqlDB.Close(); err != nil {
				global.APP_LOG.Error("关闭数据库连接失败", zap.Error(err))
			} else {
				global.APP_LOG.Info("数据库连接已关闭")
			}
		}
		dm.db = nil
	}
}

// GormMysqlConnect 直接连接数据库（不经过管理器）
func GormMysqlConnect(cfg config.MysqlConfig) (*gorm.DB, error) {
	m := global.GetAppConfig().Mysql
	dbType := global.GetAppConfig().System.DbType
	if dbType == "" {
		dbType = "mysql"
	}

	// 使用传入的配置或全局配置
	if cfg.Path == "" {
		cfg = config.MysqlConfig{
			Path:         m.Path,
			Port:         m.Port,
			Config:       m.Config,
			Dbname:       m.Dbname,
			Username:     m.Username,
			Password:     m.Password,
			MaxIdleConns: m.MaxIdleConns,
			MaxOpenConns: m.MaxOpenConns,
			LogMode:      m.LogMode,
			LogZap:       m.LogZap,
			MaxLifetime:  m.MaxLifetime,
			AutoCreate:   m.AutoCreate,
		}
	}

	db, err := internal.GormMysql(cfg)
	if err != nil {
		return nil, err
	}

	db.InstanceSet("gorm:table_options", "ENGINE="+m.Engine)
	return db, nil
}
