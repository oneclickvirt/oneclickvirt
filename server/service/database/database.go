package database

import (
	"context"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"gorm.io/gorm"
)

// DatabaseService 数据库服务抽象层 - 专为MySQL优化，内置指数退避重试
type DatabaseService struct {
	mutex sync.RWMutex
}

var (
	dbService     *DatabaseService
	dbServiceOnce sync.Once
)

// GetDatabaseService 获取数据库服务单例
func GetDatabaseService() *DatabaseService {
	dbServiceOnce.Do(func() {
		dbService = &DatabaseService{}
	})
	return dbService
}

// ExecuteInTransaction 在事务中执行操作（内部方法，不含重试）
func (ds *DatabaseService) ExecuteInTransaction(db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.Transaction(fn)
}

// ExecuteWithTimeout 带超时的数据库操作
func (ds *DatabaseService) ExecuteWithTimeout(db *gorm.DB, timeout time.Duration, fn func(tx *gorm.DB) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return db.WithContext(ctx).Transaction(fn)
}

// ExecuteTransaction 执行事务（带指数退避重试，避免嵌套事务）
func (ds *DatabaseService) ExecuteTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	db := ds.getDB()
	if db == nil {
		global.APP_LOG.Error("数据库连接不可用")
		return gorm.ErrInvalidDB
	}

	// 使用指数退避重试机制
	return utils.RetryableDBOperation(ctx, func() error {
		return ds.ExecuteInTransaction(db, fn)
	}, 8) // 最多重试8次，配合指数退避可以处理更复杂的并发场景
}

// ExecuteQuery 执行查询操作（带指数退避重试）
func (ds *DatabaseService) ExecuteQuery(ctx context.Context, fn func() error) error {
	return utils.RetryableDBOperation(ctx, fn, 6) // 查询操作重试6次
}

// getDB 获取数据库连接（内部使用）
func (ds *DatabaseService) getDB() *gorm.DB {
	// 导入全局包以获取数据库连接
	return global.APP_DB
}
