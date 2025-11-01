package system

import (
	"fmt"
	"os"
	"strconv"
	"strings"

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

	configManager "oneclickvirt/config"

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
	// 使用 ConfigManager 来更新配置，保持原有格式
	cm := configManager.GetConfigManager()
	if cm != nil {
		// 使用 ConfigManager 更新配置
		updates := make(map[string]interface{})

		// 更新系统配置
		updates["system.db-type"] = dbConfig.Type

		// 对于MySQL和MariaDB，都使用相同的配置结构
		if dbConfig.Type == "mysql" || dbConfig.Type == "mariadb" {
			updates["mysql.path"] = dbConfig.Host
			updates["mysql.port"] = strconv.Itoa(dbConfig.Port)
			updates["mysql.db-name"] = dbConfig.Database
			updates["mysql.username"] = dbConfig.Username
			updates["mysql.password"] = dbConfig.Password
			updates["mysql.config"] = "charset=utf8mb4&parseTime=True&loc=Local"
			updates["mysql.prefix"] = ""
			updates["mysql.singular"] = false
			updates["mysql.engine"] = "InnoDB"
			updates["mysql.max-idle-conns"] = 10
			updates["mysql.max-open-conns"] = 100
			updates["mysql.log-mode"] = "error"
			updates["mysql.log-zap"] = false
			updates["mysql.max-lifetime"] = 3600
			updates["mysql.auto-create"] = true
		}

		return cm.UpdateConfig(updates)
	}

	// 降级方案：直接操作文件（保持向后兼容）
	configPath := "./config.yaml"
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %v", err)
	}

	// 使用 Node API 解析，保持原有格式
	var node yaml.Node
	if err := yaml.Unmarshal(configData, &node); err != nil {
		return fmt.Errorf("解析配置文件失败: %v", err)
	}

	// 更新系统配置
	if err := updateYAMLNodeValue(&node, "system.db-type", dbConfig.Type); err != nil {
		global.APP_LOG.Warn("更新 system.db-type 失败", zap.Error(err))
	}

	// 对于MySQL和MariaDB，更新配置
	if dbConfig.Type == "mysql" || dbConfig.Type == "mariadb" {
		mysqlUpdates := map[string]interface{}{
			"mysql.path":           dbConfig.Host,
			"mysql.port":           strconv.Itoa(dbConfig.Port),
			"mysql.db-name":        dbConfig.Database,
			"mysql.username":       dbConfig.Username,
			"mysql.password":       dbConfig.Password,
			"mysql.config":         "charset=utf8mb4&parseTime=True&loc=Local",
			"mysql.prefix":         "",
			"mysql.singular":       "false",
			"mysql.engine":         "InnoDB",
			"mysql.max-idle-conns": "10",
			"mysql.max-open-conns": "100",
			"mysql.log-mode":       "error",
			"mysql.log-zap":        "false",
			"mysql.max-lifetime":   "3600",
			"mysql.auto-create":    "true",
		}

		for key, value := range mysqlUpdates {
			if err := updateYAMLNodeValue(&node, key, value); err != nil {
				global.APP_LOG.Warn("更新配置失败", zap.String("key", key), zap.Error(err))
			}
		}
	}

	// 序列化并保存
	newConfigData, err := yaml.Marshal(&node)
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

// updateYAMLNodeValue 更新YAML节点的值（辅助函数）
func updateYAMLNodeValue(node *yaml.Node, path string, value interface{}) error {
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return fmt.Errorf("invalid document node")
	}

	keys := strings.Split(path, ".")
	current := node.Content[0]

	for i := 0; i < len(keys); i++ {
		key := keys[i]
		if current.Kind != yaml.MappingNode {
			return fmt.Errorf("expected mapping node at key: %s", key)
		}

		found := false
		for j := 0; j < len(current.Content); j += 2 {
			keyNode := current.Content[j]
			valueNode := current.Content[j+1]

			if keyNode.Value == key {
				found = true
				if i == len(keys)-1 {
					// 到达目标节点，更新值
					if err := setYAMLNodeValue(valueNode, value); err != nil {
						return err
					}
					return nil
				} else {
					current = valueNode
				}
				break
			}
		}

		if !found {
			return fmt.Errorf("key not found: %s", key)
		}
	}

	return nil
}

// setYAMLNodeValue 设置YAML节点的值（类型安全）
func setYAMLNodeValue(node *yaml.Node, value interface{}) error {
	// 处理nil值
	if value == nil {
		node.Kind = yaml.ScalarNode
		node.Tag = "!!null"
		node.Value = ""
		return nil
	}

	switch v := value.(type) {
	case string:
		// 空字符串使用null表示
		if v == "" {
			node.Kind = yaml.ScalarNode
			node.Tag = "!!null"
			node.Value = ""
		} else {
			node.Kind = yaml.ScalarNode
			node.Tag = "!!str"
			node.Value = v
		}
	case int:
		node.Kind = yaml.ScalarNode
		node.Tag = "!!int"
		node.Value = fmt.Sprintf("%d", v)
	case int64:
		node.Kind = yaml.ScalarNode
		node.Tag = "!!int"
		node.Value = fmt.Sprintf("%d", v)
	case float64:
		node.Kind = yaml.ScalarNode
		if v == float64(int64(v)) {
			node.Tag = "!!int"
			node.Value = fmt.Sprintf("%d", int64(v))
		} else {
			node.Tag = "!!float"
			node.Value = fmt.Sprintf("%g", v)
		}
	case bool:
		node.Kind = yaml.ScalarNode
		node.Tag = "!!bool"
		if v {
			node.Value = "true"
		} else {
			node.Value = "false"
		}
	case map[string]interface{}:
		// 对于复杂类型，序列化为YAML子结构
		subYAML, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		var subNode yaml.Node
		if err := yaml.Unmarshal(subYAML, &subNode); err != nil {
			return err
		}
		if subNode.Kind == yaml.DocumentNode && len(subNode.Content) > 0 {
			*node = *subNode.Content[0]
		}
	default:
		// 其他类型尝试序列化
		subYAML, err := yaml.Marshal(v)
		if err != nil {
			return fmt.Errorf("unsupported value type: %T", v)
		}
		var subNode yaml.Node
		if err := yaml.Unmarshal(subYAML, &subNode); err != nil {
			return err
		}
		if subNode.Kind == yaml.DocumentNode && len(subNode.Content) > 0 {
			*node = *subNode.Content[0]
		}
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
