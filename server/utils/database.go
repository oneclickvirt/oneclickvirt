package utils

import (
	"context"
	"errors"
	"strings"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// DBError 数据库错误类型
type DBError struct {
	Err       error
	IsTimeout bool
	IsLocked  bool
}

func (e *DBError) Error() string {
	return e.Err.Error()
}

// IsDeadlockError 检查是否是死锁错误
func IsDeadlockError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "database is locked") ||
		strings.Contains(errMsg, "deadlock") ||
		strings.Contains(errMsg, "database lock") ||
		strings.Contains(errMsg, "busy") ||
		strings.Contains(errMsg, "foreign key constraint failed") ||
		strings.Contains(errMsg, "constraint failed") ||
		strings.Contains(errMsg, "lock wait timeout exceeded") ||
		strings.Contains(errMsg, "error 1205")
}

// IsConnectionError 检查是否是连接错误
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "invalid connection") ||
		strings.Contains(errMsg, "bad connection") ||
		strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "broken pipe") ||
		strings.Contains(errMsg, "connection lost") ||
		errors.Is(err, gorm.ErrInvalidDB)
}

// IsRetryableError 检查是否是可重试的错误
func IsRetryableError(err error) bool {
	return IsDeadlockError(err) || IsConnectionError(err)
}

// RetryableDBOperation 可重试的数据库操作
func RetryableDBOperation(ctx context.Context, operation func() error, maxRetries int) error {
	if maxRetries <= 0 {
		maxRetries = 5 // 增加重试次数以应对WAL模式下的并发冲突
	}

	var lastErr error
	for i := 0; i <= maxRetries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 在重试前检查数据库连接健康
		if i > 0 {
			if err := CheckDBHealth(); err != nil {
				global.APP_LOG.Warn("数据库健康检查失败",
					zap.Int("retry", i),
					zap.Error(err))
				// 短暂等待后继续
				time.Sleep(time.Duration(i) * 100 * time.Millisecond)
			}
		}

		err := operation()
		if err == nil {
			if i > 0 {
				global.APP_LOG.Info("数据库操作重试成功", zap.Int("retry_count", i))
			}
			return nil
		}

		lastErr = err
		// 检查是否是可重试的错误
		if !IsRetryableError(err) {
			// 非可重试错误，直接返回
			return err
		}

		if i < maxRetries {
			// 指数退避策略
			delay := time.Duration(i+1) * 100 * time.Millisecond
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}

			errorType := "未知"
			if IsDeadlockError(err) {
				errorType = "死锁/锁等待超时"
			} else if IsConnectionError(err) {
				errorType = "连接错误"
			}

			global.APP_LOG.Warn("数据库操作失败，准备重试",
				zap.String("错误类型", errorType),
				zap.Error(err),
				zap.Int("retry", i+1),
				zap.Int("max_retries", maxRetries),
				zap.Duration("delay", delay))
			time.Sleep(delay)
		}
	}

	global.APP_LOG.Error("数据库操作最终失败", zap.Error(lastErr), zap.Int("max_retries", maxRetries))
	return lastErr
}

// SafeTransaction 安全的事务执行
func SafeTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return RetryableDBOperation(ctx, func() error {
		// MySQL 支持并发的数据库直接使用事务
		return global.APP_DB.Transaction(func(tx *gorm.DB) error {
			return fn(tx)
		})
	}, 5) // 增加重试次数
}

// SafeQuery 安全的查询操作
func SafeQuery(ctx context.Context, fn func() error) error {
	return RetryableDBOperation(ctx, fn, 5) // 增加重试次数
}

// GetDBStats 获取数据库连接池统计信息
func GetDBStats() map[string]interface{} {
	stats := make(map[string]interface{})

	if global.APP_DB != nil {
		sqlDB, err := global.APP_DB.DB()
		if err == nil {
			dbStats := sqlDB.Stats()
			stats["max_open_connections"] = dbStats.MaxOpenConnections
			stats["open_connections"] = dbStats.OpenConnections
			stats["in_use"] = dbStats.InUse
			stats["idle"] = dbStats.Idle
			stats["wait_count"] = dbStats.WaitCount
			stats["wait_duration"] = dbStats.WaitDuration
			stats["max_idle_closed"] = dbStats.MaxIdleClosed
			stats["max_idle_time_closed"] = dbStats.MaxIdleTimeClosed
			stats["max_lifetime_closed"] = dbStats.MaxLifetimeClosed
		}
	}

	return stats
}

// CheckDBHealth 检查数据库健康状态
func CheckDBHealth() error {
	if global.APP_DB == nil {
		return errors.New("数据库连接为空")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return SafeQuery(ctx, func() error {
		var result int
		return global.APP_DB.Raw("SELECT 1").Scan(&result).Error
	})
}
