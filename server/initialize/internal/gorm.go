package internal

import (
	"fmt"
	"log"
	"os"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/config"

	"go.uber.org/zap"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// GormMysql 初始化数据库（支持MySQL和MariaDB），支持自动创建数据库
func GormMysql(m config.MysqlConfig) (*gorm.DB, error) {
	// 检查基本参数
	if m.Username == "" {
		return nil, fmt.Errorf("数据库用户名不能为空")
	}
	if m.Path == "" {
		m.Path = "127.0.0.1"
	}
	if m.Port == "" {
		m.Port = "3306"
	}
	if m.Config == "" {
		m.Config = "charset=utf8mb4&parseTime=True&loc=Local&time_zone=%27%2B08%3A00%27"
	}

	// 如果没有指定数据库名且需要自动创建，则设置默认数据库名
	if m.Dbname == "" && m.AutoCreate {
		m.Dbname = "oneclickvirt" // 默认数据库名
	}

	// 如果启用自动创建且有权限，尝试创建数据库
	if m.AutoCreate && m.Username == "root" && m.Dbname != "" {
		if err := createDatabaseIfNotExists(m); err != nil {
			global.APP_LOG.Warn("自动创建数据库失败", zap.Error(err))
		} else {
			global.APP_LOG.Info("数据库检查/创建完成", zap.String("database", m.Dbname))
		}
	}

	// 构建DSN
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?%s",
		m.Username, m.Password, m.Path, m.Port, m.Dbname, m.Config)

	mysqlConfig := mysql.Config{
		DSN:                       dsn,
		DefaultStringSize:         191,   // string 类型字段的默认长度
		SkipInitializeWithVersion: false, // 根据版本自动配置
	}

	gormConfig := gormConfig(m.LogMode, m.LogZap)

	db, err := gorm.Open(mysql.New(mysqlConfig), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	// 设置表选项为InnoDB引擎
	db.InstanceSet("gorm:table_options", "ENGINE=InnoDB")

	// 配置连接池
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取数据库连接池失败: %w", err)
	}

	// 设置连接池参数
	maxIdle := m.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 20 // 默认空闲连接数
	}
	maxOpen := m.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 200 // 默认最大连接数
	}
	maxLifetime := m.MaxLifetime
	if maxLifetime <= 0 {
		maxLifetime = 1800 // 默认30分钟，避免MySQL 8小时超时
	}

	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetConnMaxLifetime(time.Duration(maxLifetime) * time.Second)
	// 设置连接最大空闲时间，避免空闲连接被MySQL服务器关闭
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

	return db, nil
}

// createDatabaseIfNotExists 创建数据库（如果不存在）
func createDatabaseIfNotExists(m config.MysqlConfig) error {
	// 连接数据库服务器（不指定数据库）
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/?%s",
		m.Username, m.Password, m.Path, m.Port, m.Config)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("连接数据库服务器失败: %w", err)
	}

	// 检查数据库是否存在
	var count int64
	err = db.Raw("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", m.Dbname).Scan(&count).Error
	if err != nil {
		return fmt.Errorf("检查数据库是否存在失败: %w", err)
	}

	// 如果数据库不存在则创建
	if count == 0 {
		createSQL := fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", m.Dbname)
		err = db.Exec(createSQL).Error
		if err != nil {
			return fmt.Errorf("创建数据库失败: %w", err)
		}
		global.APP_LOG.Info("自动创建数据库成功", zap.String("database", m.Dbname))
	}

	return nil
}

// gormConfig 根据配置决定是否开启日志
func gormConfig(mod string, zap bool) (config *gorm.Config) {
	config = &gorm.Config{DisableForeignKeyConstraintWhenMigrating: false}
	switch mod {
	case "silent", "Silent":
		config.Logger = logger.Default.LogMode(logger.Silent)
	case "error", "Error":
		config.Logger = logger.Default.LogMode(logger.Error)
	case "warn", "Warn":
		config.Logger = logger.Default.LogMode(logger.Warn)
	case "info", "Info":
		config.Logger = logger.Default.LogMode(logger.Info)
	default:
		if zap {
			config.Logger = logger.New(
				log.New(os.Stdout, "\r\n", log.LstdFlags), // io writer
				logger.Config{
					SlowThreshold: time.Second, // 慢 SQL 阈值
					LogLevel:      logger.Info, // Log level
					Colorful:      true,        // 启用彩色打印
				},
			)
		} else {
			config.Logger = logger.Default.LogMode(logger.Info)
		}
	}
	return
}
