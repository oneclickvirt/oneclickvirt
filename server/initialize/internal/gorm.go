package internal

import (
	"context"
	"log"
	"os"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/config"
	"oneclickvirt/utils/dbconnect"

	"go.uber.org/zap"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// GormMysql shares server detection and option repair with the initialization API.
func GormMysql(m config.MysqlConfig) (*gorm.DB, error) {
	db, _, err := GormMysqlWithInfo(m)
	return db, err
}

// GormMysqlWithInfo is the startup/reconnect entry point that also exposes the
// server metadata and any repaired connection options.  Keeping this result
// with the connection prevents callers from having to probe a second time (or
// to infer the engine from the configured label).
func GormMysqlWithInfo(m config.MysqlConfig) (*gorm.DB, dbconnect.Info, error) {
	db, info, err := dbconnect.Open(context.Background(), m, gormConfig(m.LogMode, m.LogZap))
	if err != nil {
		return nil, info, err
	}
	global.ReconcileDatabaseConfig(m, info.Type, info.Params)
	if global.APP_LOG != nil {
		global.APP_LOG.Info("已检测数据库服务端", zap.String("type", info.Type),
			zap.String("version", info.Version), zap.Strings("repairs", info.Repairs))
	}
	return db, info, nil
}

// gormConfig 根据配置决定是否开启日志
func gormConfig(mod string, useZap bool) (config *gorm.Config) {
	config = &gorm.Config{DisableForeignKeyConstraintWhenMigrating: false}
	// IgnoreRecordNotFoundError=true 避免将期望的"查不到记录"行为打印成错误日志
	switch mod {
	case "silent", "Silent":
		config.Logger = logger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             200 * time.Millisecond,
				LogLevel:                  logger.Silent,
				IgnoreRecordNotFoundError: true,
			},
		)
	case "error", "Error":
		config.Logger = logger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             200 * time.Millisecond,
				LogLevel:                  logger.Error,
				IgnoreRecordNotFoundError: true,
			},
		)
	case "warn", "Warn":
		config.Logger = logger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             200 * time.Millisecond,
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
			},
		)
	case "info", "Info":
		config.Logger = logger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             200 * time.Millisecond,
				LogLevel:                  logger.Info,
				IgnoreRecordNotFoundError: true,
			},
		)
	default:
		if useZap {
			config.Logger = logger.New(
				log.New(os.Stdout, "\r\n", log.LstdFlags),
				logger.Config{
					SlowThreshold:             time.Second,
					LogLevel:                  logger.Info,
					IgnoreRecordNotFoundError: true,
					Colorful:                  true,
				},
			)
		} else {
			config.Logger = logger.New(
				log.New(os.Stdout, "\r\n", log.LstdFlags),
				logger.Config{
					SlowThreshold:             200 * time.Millisecond,
					LogLevel:                  logger.Info,
					IgnoreRecordNotFoundError: true,
				},
			)
		}
	}
	return
}
