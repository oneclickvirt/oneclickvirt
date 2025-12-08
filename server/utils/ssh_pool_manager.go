package utils

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

var (
	globalSSHPool     *SSHConnectionPool
	globalSSHPoolOnce sync.Once
)

// GetGlobalSSHPool 获取全局SSH连接池
func GetGlobalSSHPool() *SSHConnectionPool {
	globalSSHPoolOnce.Do(func() {
		globalSSHPool = NewSSHConnectionPool(30*time.Minute, nil)
	})
	return globalSSHPool
}

// InitGlobalSSHPool 初始化全局SSH连接池（带日志）
func InitGlobalSSHPool(logger *zap.Logger) *SSHConnectionPool {
	globalSSHPoolOnce.Do(func() {
		globalSSHPool = NewSSHConnectionPool(30*time.Minute, logger)
		if logger != nil {
			logger.Info("全局SSH连接池已初始化")
		}
	})
	return globalSSHPool
}

// CloseGlobalSSHPool 关闭全局SSH连接池
func CloseGlobalSSHPool() {
	if globalSSHPool != nil {
		globalSSHPool.CloseAll()
	}
}
