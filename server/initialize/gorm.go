package initialize

import (
	"oneclickvirt/global"
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
	"oneclickvirt/service/database"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Gorm 初始化数据库并产生数据库全局变量
// 使用DatabaseManager实现连接管理、自动重连和心跳检测
func Gorm() *gorm.DB {
	dbType := global.APP_CONFIG.System.DbType
	if dbType == "" {
		dbType = "mysql"
	}

	// 获取数据库管理器
	dbManager := GetDatabaseManager()

	// 初始化数据库连接（包含自动重连和心跳检测）
	mysqlConfig := config.MysqlConfig{
		Path:         global.APP_CONFIG.Mysql.Path,
		Port:         global.APP_CONFIG.Mysql.Port,
		Config:       global.APP_CONFIG.Mysql.Config,
		Dbname:       global.APP_CONFIG.Mysql.Dbname,
		Username:     global.APP_CONFIG.Mysql.Username,
		Password:     global.APP_CONFIG.Mysql.Password,
		MaxIdleConns: global.APP_CONFIG.Mysql.MaxIdleConns,
		MaxOpenConns: global.APP_CONFIG.Mysql.MaxOpenConns,
		LogMode:      global.APP_CONFIG.Mysql.LogMode,
		LogZap:       global.APP_CONFIG.Mysql.LogZap,
		MaxLifetime:  global.APP_CONFIG.Mysql.MaxLifetime,
		AutoCreate:   global.APP_CONFIG.Mysql.AutoCreate,
	}

	db, err := dbManager.Initialize(mysqlConfig)
	if err != nil {
		global.APP_LOG.Warn("数据库连接失败，系统将以待初始化模式运行",
			zap.String("dbType", dbType),
			zap.Error(err))
		return nil
	}

	global.APP_LOG.Info("数据库连接成功",
		zap.String("dbType", dbType),
		zap.String("engine", global.APP_CONFIG.Mysql.Engine))

	// 只有在数据库连接成功时才进行表结构迁移
	global.APP_LOG.Info("开始数据库表结构自动迁移")
	RegisterTables(db)
	global.APP_LOG.Info("数据库表结构迁移完成")

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

	return nil
}

// RegisterTables 注册数据库表专用
func RegisterTables(db *gorm.DB) {
	// 在AutoMigrate之前先修复可能存在的重复数据
	// 这样可以避免在添加唯一索引时因重复数据导致错误
	dbService := database.GetDatabaseService()
	if fixErr := dbService.FixAllDuplicateData(); fixErr != nil {
		global.APP_LOG.Warn("修复重复数据时出现警告（可忽略，如果是新数据库）", zap.Error(fixErr))
	}

	err := db.AutoMigrate(
		// 用户相关表
		&userModel.User{},     // 用户基础信息表
		&authModel.Role{},     // 角色管理表
		&userModel.UserRole{}, // 用户角色关联表

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
		&systemModel.JWTSecret{},    // JWT密钥表

		// 邀请码相关表
		&systemModel.InviteCode{},      // 邀请码表
		&systemModel.InviteCodeUsage{}, // 邀请码使用记录表

		// 权限管理表
		&permissionModel.UserPermission{}, // 用户权限组合表

		// 审计日志表
		&adminModel.AuditLog{},           // 操作审计日志表
		&providerModel.PendingDeletion{}, // 待删除资源表

		// 管理员配置任务表
		&adminModel.ConfigurationTask{},  // 管理员配置任务表
		&adminModel.TrafficMonitorTask{}, // 流量监控操作任务表

		// 监控数据表
		&monitoringModel.PmacctTrafficRecord{},    // pmacct流量记录表（原始数据，5分钟粒度）
		&monitoringModel.PmacctMonitor{},          // pmacct监控配置表
		&monitoringModel.InstanceTrafficHistory{}, // 实例流量历史表
		&monitoringModel.ProviderTrafficHistory{}, // Provider流量历史表
		&monitoringModel.UserTrafficHistory{},     // 用户流量历史表
		&monitoringModel.PerformanceMetric{},      // 性能指标历史表
	)
	if err != nil {
		global.APP_LOG.Error("register table failed", zap.Error(err))
		return
	}
	global.APP_LOG.Info("数据库表注册成功")
}
