package system

import (
	"fmt"
	"os"
	"strconv"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/config"
	permissionModel "oneclickvirt/model/permission"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/resource"
	"oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// InitService 初始化服务
type InitService struct{}

// CheckDatabaseConnection 检查数据库连接状态
func (s *InitService) CheckDatabaseConnection() error {
	if global.APP_DB == nil {
		return fmt.Errorf("数据库连接不存在")
	}

	sqlDB, err := global.APP_DB.DB()
	if err != nil {
		return fmt.Errorf("获取数据库实例失败: %v", err)
	}

	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("数据库连接测试失败: %v", err)
	}

	return nil
}

// TestDatabaseConnection 测试数据库连接（不需要全局DB连接）
func (s *InitService) TestDatabaseConnection(config config.DatabaseConfig) error {
	if config.Type != "mysql" && config.Type != "mariadb" {
		return fmt.Errorf("不支持的数据库类型: %s，仅支持mysql和mariadb", config.Type)
	}

	// 构建DSN，先不指定数据库名
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?charset=utf8mb4&parseTime=True&loc=Local",
		config.Username, config.Password, config.Host, config.Port)

	// 尝试连接数据库服务器（MySQL或MariaDB使用相同的连接方式）
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("连接%s服务器失败: %v", config.Type, err)
	}

	// 测试连接
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取数据库实例失败: %v", err)
	}
	defer sqlDB.Close()

	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("数据库连接测试失败: %v", err)
	}

	// 检查数据库是否存在，如果不存在则创建
	var count int64
	err = db.Raw("SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", config.Database).Scan(&count).Error
	if err != nil {
		return fmt.Errorf("检查数据库是否存在失败: %v", err)
	}

	if count == 0 {
		// 数据库不存在，尝试创建
		err = db.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", config.Database)).Error
		if err != nil {
			return fmt.Errorf("创建数据库失败: %v", err)
		}
		global.APP_LOG.Info("数据库不存在，已自动创建", zap.String("database", config.Database))
	}

	// 测试连接到具体数据库
	dsnWithDB := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		config.Username, config.Password, config.Host, config.Port, config.Database)

	dbWithDB, err := gorm.Open(mysql.Open(dsnWithDB), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("连接到数据库失败: %v", err)
	}

	sqlDBWithDB, err := dbWithDB.DB()
	if err != nil {
		return fmt.Errorf("获取数据库实例失败: %v", err)
	}
	defer sqlDBWithDB.Close()

	if err := sqlDBWithDB.Ping(); err != nil {
		return fmt.Errorf("数据库连接测试失败: %v", err)
	}

	return nil
}

// AutoMigrateTables 自动迁移所有表结构
func (s *InitService) AutoMigrateTables() error {
	if global.APP_DB == nil {
		return fmt.Errorf("数据库连接不存在")
	}

	global.APP_LOG.Debug("开始执行数据库表结构自动迁移")

	// 执行表结构迁移
	err := global.APP_DB.AutoMigrate(
		// 用户相关表
		&userModel.User{},          // 用户基础信息表
		&userModel.TrafficRecord{}, // 用户流量记录表
		&auth.Role{},               // 角色管理表
		&userModel.UserRole{},      // 用户角色关联表

		// 实例相关表
		&provider.Instance{}, // 虚拟机/容器实例表
		&provider.Provider{}, // 服务提供商配置表
		&provider.Port{},     // 端口映射表
		&adminModel.Task{},   // 用户任务表

		// 资源管理表
		&resource.ResourceReservation{}, // 资源预留表

		// 认证相关表
		&userModel.VerifyCode{},    // 验证码表（邮箱/短信）
		&userModel.PasswordReset{}, // 密码重置令牌表
		&auth.JWTBlacklist{},       // JWT黑名单表

		// 系统配置表
		&adminModel.SystemConfig{}, // 系统配置表
		&system.Announcement{},     // 系统公告表
		&system.SystemImage{},      // 系统镜像模板表
		&system.Captcha{},          // 图形验证码表

		// 邀请码相关表
		&system.InviteCode{},      // 邀请码表
		&system.InviteCodeUsage{}, // 邀请码使用记录表

		// 权限管理表
		&permissionModel.UserPermission{}, // 用户权限组合表

		// 审计日志表
		&adminModel.AuditLog{},      // 操作审计日志表
		&provider.PendingDeletion{}, // 待删除资源表

		// 管理员配置任务表
		&adminModel.ConfigurationTask{}, // 管理员配置任务表
	)

	if err != nil {
		global.APP_LOG.Error("数据库表结构迁移失败", zap.String("error", utils.FormatError(err)))
		return fmt.Errorf("表结构迁移失败: %v", err)
	}

	global.APP_LOG.Debug("数据库表结构自动迁移完成")
	return nil
}

// EnsureDatabase 确保数据库和表结构存在
func (s *InitService) EnsureDatabase(dbConfig config.DatabaseConfig) error {
	// 更新数据库配置
	if err := s.UpdateDatabaseConfig(dbConfig); err != nil {
		return fmt.Errorf("更新数据库配置失败: %v", err)
	}

	// 重新初始化数据库连接
	if err := s.ReinitializeDatabase(); err != nil {
		return fmt.Errorf("重新初始化数据库失败: %v", err)
	}

	// 执行表结构迁移
	if err := s.AutoMigrateTables(); err != nil {
		return fmt.Errorf("表结构迁移失败: %v", err)
	}

	return nil
}

// UpdateDatabaseConfig 更新数据库配置
func (s *InitService) UpdateDatabaseConfig(dbConfig config.DatabaseConfig) error {
	// 读取当前配置文件
	configPath := "./config.yaml"
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %v", err)
	}

	// 解析配置
	var c map[string]interface{}
	if err := yaml.Unmarshal(configData, &c); err != nil {
		return fmt.Errorf("解析配置文件失败: %v", err)
	}

	// 更新系统配置
	if system, ok := c["system"].(map[string]interface{}); ok {
		system["db-type"] = dbConfig.Type
	} else {
		c["system"] = map[string]interface{}{
			"db-type": dbConfig.Type,
		}
	}

	// 对于MySQL和MariaDB，都使用相同的配置结构（因为它们兼容MySQL协议）
	if dbConfig.Type == "mysql" || dbConfig.Type == "mariadb" {
		c["mysql"] = map[string]interface{}{
			"path":           dbConfig.Host,
			"port":           strconv.Itoa(dbConfig.Port),
			"db-name":        dbConfig.Database,
			"username":       dbConfig.Username,
			"password":       dbConfig.Password,
			"config":         "charset=utf8mb4&parseTime=True&loc=Local",
			"prefix":         "",
			"singular":       false,
			"engine":         "InnoDB",
			"max-idle-conns": 10,
			"max-open-conns": 100,
			"log-mode":       "error",
			"log-zap":        false,
			"max-lifetime":   3600,
			"auto-create":    true,
		}
	}

	// 保存配置文件
	newConfigData, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %v", err)
	}

	// 备份原配置文件
	backupPath := configPath + ".backup"
	if err := os.WriteFile(backupPath, configData, 0644); err != nil {
		global.APP_LOG.Debug("备份配置文件失败", zap.String("error", utils.FormatError(err)))
	}

	// 写入新配置
	if err := os.WriteFile(configPath, newConfigData, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %v", err)
	}

	return nil
}

// ReinitializeDatabase 重新初始化数据库连接
func (s *InitService) ReinitializeDatabase() error {
	// 读取配置文件获取最新的数据库配置
	configPath := "./config.yaml"
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %v", err)
	}

	var c map[string]interface{}
	if err := yaml.Unmarshal(configData, &c); err != nil {
		return fmt.Errorf("解析配置文件失败: %v", err)
	}

	// 获取 MySQL 配置
	mysqlConfig, ok := c["mysql"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("MySQL配置不存在")
	}

	// 提取配置信息
	host, _ := mysqlConfig["path"].(string)
	dbname, _ := mysqlConfig["db-name"].(string)
	username, _ := mysqlConfig["username"].(string)
	password, _ := mysqlConfig["password"].(string)
	config, _ := mysqlConfig["config"].(string)

	// 处理端口字段，支持字符串和数字两种类型
	var portStr string
	if portVal, exists := mysqlConfig["port"]; exists {
		switch v := portVal.(type) {
		case string:
			portStr = v
		case int:
			portStr = fmt.Sprintf("%d", v)
		case float64:
			portStr = fmt.Sprintf("%.0f", v)
		default:
			portStr = "3306" // 默认端口
		}
	} else {
		portStr = "3306" // 默认端口
	}

	// 如果端口为空，设置默认值
	if portStr == "" {
		portStr = "3306"
	}

	if host == "" || username == "" || dbname == "" {
		return fmt.Errorf("数据库配置不完整")
	}

	// 构建DSN并连接数据库
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?%s",
		username, password, host, portStr, dbname, config)

	mysqlDriverConfig := mysql.Config{
		DSN:                       dsn,
		DefaultStringSize:         191,
		SkipInitializeWithVersion: false,
	}

	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	}

	db, err := gorm.Open(mysql.New(mysqlDriverConfig), gormConfig)
	if err != nil {
		return fmt.Errorf("重新连接数据库失败: %v", err)
	}

	// 更新全局数据库连接
	global.APP_DB = db
	global.APP_LOG.Info("数据库连接已更新")

	return nil
}
