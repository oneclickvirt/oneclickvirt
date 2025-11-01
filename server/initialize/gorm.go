package initialize

import (
	"time"

	"oneclickvirt/global"
	"oneclickvirt/initialize/internal"
	adminModel "oneclickvirt/model/admin"
	authModel "oneclickvirt/model/auth"
	"oneclickvirt/model/config"
	monitoringModel "oneclickvirt/model/monitoring"
	oauth2Model "oneclickvirt/model/oauth2"
	permissionModel "oneclickvirt/model/permission"
	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// GormMysql 初始化数据库（支持MySQL和MariaDB）
func GormMysql() *gorm.DB {
	m := global.APP_CONFIG.Mysql
	dbType := global.APP_CONFIG.System.DbType
	if dbType == "" {
		dbType = "mysql" // 默认
	}

	mysqlConfig := config.MysqlConfig{
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
	if db, err := internal.GormMysql(mysqlConfig); err != nil {
		global.APP_LOG.Error("数据库初始化失败",
			zap.String("dbType", dbType),
			zap.Error(err))
		return nil
	} else {
		db.InstanceSet("gorm:table_options", "ENGINE="+m.Engine)
		global.APP_LOG.Info("数据库初始化成功",
			zap.String("dbType", dbType),
			zap.String("engine", m.Engine))
		return db
	}
}

// Gorm 初始化数据库并产生数据库全局变量
func Gorm() *gorm.DB {
	// 支持MySQL和MariaDB
	db := GormMysql()
	dbType := global.APP_CONFIG.System.DbType

	// 验证数据库连接
	if db != nil {
		if err := validateDatabaseConnection(db); err != nil {
			global.APP_LOG.Error("数据库连接验证失败", zap.Error(err))
			return nil
		}
		global.APP_LOG.Info("数据库连接验证成功", zap.String("dbType", dbType))

		// 自动迁移表结构（无论是否初始化都执行，确保表结构是最新的）
		global.APP_LOG.Info("开始数据库表结构自动迁移")
		RegisterTables(db)
		global.APP_LOG.Info("数据库表结构迁移完成")
	}

	return db
} // validateDatabaseConnection 验证数据库连接是否可用
func validateDatabaseConnection(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	// 测试连接
	if err := sqlDB.Ping(); err != nil {
		return err
	}

	// 简单的查询测试
	var result int
	if err := db.Raw("SELECT 1").Scan(&result).Error; err != nil {
		return err
	}

	// 检查连接池状态
	stats := sqlDB.Stats()
	global.APP_LOG.Info("数据库连接池状态",
		zap.Int("max_open_connections", stats.MaxOpenConnections),
		zap.Int("open_connections", stats.OpenConnections),
		zap.Int("in_use", stats.InUse),
		zap.Int("idle", stats.Idle))

	// 启动连接池监控（每5分钟检查一次）
	go monitorConnectionPool(db)

	return nil
}

// monitorConnectionPool 监控数据库连接池状态
func monitorConnectionPool(db *gorm.DB) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		if db == nil {
			return
		}

		sqlDB, err := db.DB()
		if err != nil {
			global.APP_LOG.Error("获取数据库连接池失败", zap.Error(err))
			continue
		}

		// 检查连接是否正常
		if err := sqlDB.Ping(); err != nil {
			global.APP_LOG.Error("数据库连接检查失败", zap.Error(err))
			continue
		}

		// 获取连接池统计信息
		stats := sqlDB.Stats()
		usagePercent := float64(stats.OpenConnections) / float64(stats.MaxOpenConnections) * 100

		// 如果使用率超过80%，记录警告
		if usagePercent > 80 {
			global.APP_LOG.Warn("数据库连接池使用率过高",
				zap.Int("open_connections", stats.OpenConnections),
				zap.Int("max_open_connections", stats.MaxOpenConnections),
				zap.Float64("usage_percent", usagePercent),
				zap.Int("in_use", stats.InUse),
				zap.Int("idle", stats.Idle),
				zap.Int64("wait_count", stats.WaitCount),
				zap.Duration("wait_duration", stats.WaitDuration))
		} else {
			// 正常情况下记录debug日志
			global.APP_LOG.Debug("数据库连接池状态",
				zap.Int("open_connections", stats.OpenConnections),
				zap.Int("max_open_connections", stats.MaxOpenConnections),
				zap.Float64("usage_percent", usagePercent))
		}
	}
}

// RegisterTables 注册数据库表专用
func RegisterTables(db *gorm.DB) {
	err := db.AutoMigrate(
		// 用户相关表
		&userModel.User{},          // 用户基础信息表
		&userModel.TrafficRecord{}, // 用户流量记录表
		&authModel.Role{},          // 角色管理表
		&userModel.UserRole{},      // 用户角色关联表

		// OAuth2相关表
		&oauth2Model.OAuth2Provider{}, // OAuth2提供商配置表

		// 实例相关表
		&providerModel.Instance{}, // 虚拟机/容器实例表
		&providerModel.Provider{}, // 服务提供商配置表
		&providerModel.Port{},     // 端口映射表
		&adminModel.Task{},        // 用户任务表

		// 资源管理表
		&resourceModel.ResourceReservation{}, // 资源预留表

		// 认证相关表
		&userModel.VerifyCode{},    // 验证码表（邮箱/短信）
		&userModel.PasswordReset{}, // 密码重置令牌表

		// 系统配置表
		&adminModel.SystemConfig{},  // 系统配置表
		&systemModel.Announcement{}, // 系统公告表
		&systemModel.SystemImage{},  // 系统镜像模板表
		&systemModel.Captcha{},      // 图形验证码表

		// 邀请码相关表
		&systemModel.InviteCode{},      // 邀请码表
		&systemModel.InviteCodeUsage{}, // 邀请码使用记录表

		// 权限管理表
		&permissionModel.UserPermission{}, // 用户权限组合表

		// 审计日志表
		&adminModel.AuditLog{},           // 操作审计日志表
		&providerModel.PendingDeletion{}, // 待删除资源表

		// 管理员配置任务表
		&adminModel.ConfigurationTask{}, // 管理员配置任务表

		// 监控数据表
		&monitoringModel.VnStatTrafficRecord{}, // vnStat流量记录表
		&monitoringModel.VnStatInterface{},     // vnStat网络接口表
	)
	if err != nil {
		global.APP_LOG.Error("register table failed", zap.Error(err))
		return
	}
	global.APP_LOG.Info("数据库表注册成功")
}
