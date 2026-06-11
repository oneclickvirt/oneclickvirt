package system

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	authModel "oneclickvirt/model/auth"
	checkinModel "oneclickvirt/model/checkin"
	"oneclickvirt/model/config"
	domainModel "oneclickvirt/model/domain"
	firewallModel "oneclickvirt/model/firewall"
	kycModel "oneclickvirt/model/kyc"
	monitoringModel "oneclickvirt/model/monitoring"
	oauth2Model "oneclickvirt/model/oauth2"
	permissionModel "oneclickvirt/model/permission"
	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/utils"
	"oneclickvirt/utils/dbcompat"

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
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?charset=utf8mb4&parseTime=True&loc=Local&time_zone=%%27%%2B08%%3A00%%27",
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
	// Validate database name to prevent SQL injection (DDL cannot use parameterized queries)
	validDBName := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	if !validDBName.MatchString(config.Database) {
		return fmt.Errorf("非法数据库名称: %s", config.Database)
	}

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
	dsnWithDB := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local&time_zone=%%27%%2B08%%3A00%%27",
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

	// 与 initialize.RegisterTables 保持一致。初始化页会在空数据库上直接调用这里，
	// 因此不能只迁移旧版核心表，否则监控、OAuth2、KYC、签到等后续路径会缺表。
	if err := global.APP_DB.AutoMigrate(
		// 用户相关表
		&userModel.User{},
		&authModel.Role{},
		&userModel.UserRole{},

		// OAuth2相关表
		&oauth2Model.OAuth2Provider{},

		// 实例相关表
		&providerModel.Instance{},
		&providerModel.Provider{},
		&providerModel.AdminGroupSetting{},
		&providerModel.Port{},
		&providerModel.ProviderIPv4Pool{},
		&providerModel.InstanceShareLink{},
		&providerModel.InstanceSnapshot{},
		&providerModel.SnapshotSchedule{},
		&adminModel.Task{},

		// 资源管理表
		&resourceModel.ResourceReservation{},

		// 认证相关表
		&userModel.VerifyCode{},
		&userModel.PasswordReset{},
		&userModel.JWTBlacklistedToken{},

		// 系统配置表
		&adminModel.SystemConfig{},
		&systemModel.Announcement{},
		&systemModel.SystemImage{},
		&systemModel.Captcha{},
		&systemModel.JWTSecret{},

		// 邀请码/兑换码
		&systemModel.InviteCode{},
		&systemModel.InviteCodeUsage{},
		&systemModel.RedemptionCode{},

		// 权限管理表
		&permissionModel.UserPermission{},

		// 审计和硬件检测
		&adminModel.AuditLog{},
		&providerModel.PendingDeletion{},
		&providerModel.HardwareTestReport{},

		// 管理员配置任务表
		&adminModel.ConfigurationTask{},
		&adminModel.TrafficMonitorTask{},

		// 监控数据表
		&monitoringModel.PmacctTrafficRecord{},
		&monitoringModel.PmacctMonitor{},
		&monitoringModel.InstanceTrafficHistory{},
		&monitoringModel.ProviderTrafficHistory{},
		&monitoringModel.UserTrafficHistory{},
		&monitoringModel.PerformanceMetric{},
		&monitoringModel.AgentMonitor{},
		&monitoringModel.ResourceMetric{},
		&monitoringModel.MonitoringConfig{},

		// 防火墙/滥用屏蔽表
		&firewallModel.BlockRule{},
		&firewallModel.BlockRuleApplication{},

		// 域名绑定表
		&domainModel.Domain{},
		&domainModel.DomainConfig{},

		// 实名认证表
		&kycModel.KYCRecord{},

		// 签到续期表
		&checkinModel.CheckinConfig{},
		&checkinModel.CheckinRecord{},
		&checkinModel.CheckinVerification{},

		// API Token表
		&authModel.ApiToken{},
	); err != nil {
		global.APP_LOG.Error("数据库表结构迁移失败", zap.String("error", utils.FormatError(err)))
		return fmt.Errorf("表结构迁移失败: %v", err)
	}
	dbcompat.Init(global.APP_DB)

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
// applyMysqlConfigToGlobal 将 MySQL 配置直接写入 global.APP_CONFIG，
// 确保后续 Gorm() 调用可以读取到最新配置，不依赖 ConfigManager 回调。
func applyMysqlConfigToGlobal(dbConfig config.DatabaseConfig) {
	if dbConfig.Type != "mysql" && dbConfig.Type != "mariadb" {
		return
	}
	appCfg := global.GetAppConfig()
	appCfg.Mysql.Path = dbConfig.Host
	appCfg.Mysql.Port = strconv.Itoa(dbConfig.Port)
	appCfg.Mysql.Dbname = dbConfig.Database
	appCfg.Mysql.Username = dbConfig.Username
	appCfg.Mysql.Password = dbConfig.Password
	appCfg.Mysql.Config = "charset=utf8mb4&parseTime=True&loc=Local&time_zone=%27%2B08%3A00%27"
	appCfg.Mysql.Engine = "InnoDB"
	appCfg.Mysql.MaxIdleConns = 10
	appCfg.Mysql.MaxOpenConns = 100
	appCfg.Mysql.LogMode = "error"
	appCfg.Mysql.LogZap = false
	appCfg.Mysql.MaxLifetime = 3600
	appCfg.Mysql.AutoCreate = true
	global.SetAppConfig(appCfg)
}

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
			updates["mysql.config"] = "charset=utf8mb4&parseTime=True&loc=Local&time_zone=%27%2B08%3A00%27"
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

		if err := cm.UpdateConfig(updates); err != nil {
			return err
		}
		// ConfigManager 的回调不同步 mysql 节点到 global.APP_CONFIG，需要手动同步
		applyMysqlConfigToGlobal(dbConfig)
		return nil
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
		// 自动检测配置文件中使用的键名（mysql 或 mariadb）
		mysqlKey := detectMysqlKey(&node)
		mysqlUpdates := map[string]interface{}{
			mysqlKey + ".path":           dbConfig.Host,
			mysqlKey + ".port":           strconv.Itoa(dbConfig.Port),
			mysqlKey + ".db-name":        dbConfig.Database,
			mysqlKey + ".username":       dbConfig.Username,
			mysqlKey + ".password":       dbConfig.Password,
			mysqlKey + ".config":         "charset=utf8mb4&parseTime=True&loc=Local&time_zone=%27%2B08%3A00%27",
			mysqlKey + ".prefix":         "",
			mysqlKey + ".singular":       false,
			mysqlKey + ".engine":         "InnoDB",
			mysqlKey + ".max-idle-conns": 10,
			mysqlKey + ".max-open-conns": 100,
			mysqlKey + ".log-mode":       "error",
			mysqlKey + ".log-zap":        false,
			mysqlKey + ".max-lifetime":   3600,
			mysqlKey + ".auto-create":    true,
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

	global.APP_LOG.Info("数据库配置已成功写入文件",
		zap.String("host", dbConfig.Host),
		zap.Int("port", dbConfig.Port),
		zap.String("database", dbConfig.Database))

	// 立即重新加载配置到内存
	if err := s.reloadConfig(); err != nil {
		global.APP_LOG.Warn("重新加载配置失败", zap.Error(err))
		// 不返回错误，因为配置文件已经写入成功
	}
	// 确保 global.APP_CONFIG.Mysql 已更新（reloadConfig 通过 CM 回调可能不同步 mysql 节点）
	applyMysqlConfigToGlobal(dbConfig)

	return nil
}

// detectMysqlKey 检测配置文件中 MySQL/MariaDB 配置使用的键名
// 返回 "mysql" 或 "mariadb"，默认返回 "mysql"
func detectMysqlKey(node *yaml.Node) string {
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return "mysql"
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return "mysql"
	}
	for i := 0; i < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		if keyNode.Value == "mariadb" {
			return "mariadb"
		}
		if keyNode.Value == "mysql" {
			return "mysql"
		}
	}
	return "mysql"
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
		node.Style = 0
		node.Tag = "!!int"
		node.Value = fmt.Sprintf("%d", v)
	case int64:
		node.Kind = yaml.ScalarNode
		node.Style = 0
		node.Tag = "!!int"
		node.Value = fmt.Sprintf("%d", v)
	case float64:
		node.Kind = yaml.ScalarNode
		node.Style = 0
		if v == float64(int64(v)) {
			node.Tag = "!!int"
			node.Value = fmt.Sprintf("%d", int64(v))
		} else {
			node.Tag = "!!float"
			node.Value = fmt.Sprintf("%g", v)
		}
	case bool:
		node.Kind = yaml.ScalarNode
		node.Style = 0
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
	// 强制重新加载配置文件到 global.APP_CONFIG
	if err := s.reloadConfig(); err != nil {
		global.APP_LOG.Warn("重新加载配置失败，尝试从文件直接读取", zap.Error(err))
	}

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

	// 获取 MySQL 配置（兼容 mysql 和 mariadb 两种键名）
	mysqlConfig, ok := c["mysql"].(map[string]interface{})
	if !ok {
		// 向后兼容：部分旧版 install_full.sh 可能写入 mariadb 键名
		mysqlConfig, ok = c["mariadb"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("MySQL配置不存在")
		}
		global.APP_LOG.Warn("配置文件使用 'mariadb' 键名，已自动兼容；建议将键名改为 'mysql' 以匹配规范")
	}

	// 提取配置信息
	host, _ := mysqlConfig["path"].(string)
	dbname, _ := mysqlConfig["db-name"].(string)
	username, _ := mysqlConfig["username"].(string)
	password, _ := mysqlConfig["password"].(string)
	config, _ := mysqlConfig["config"].(string)

	// 记录读取到的数据库配置，用于调试
	global.APP_LOG.Debug("从配置文件读取到的数据库配置",
		zap.String("host", host),
		zap.String("dbname", dbname),
		zap.String("username", username))

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

// reloadConfig 重新加载配置文件到 global.APP_CONFIG
// 手动修改 config.yaml 后调用此方法，会：
// 1. 将 YAML 配置同步到数据库
// 2. 通过 ConfigManager 回调同步到 global.APP_CONFIG
// 3. 清除配置修改标志（因为现在 YAML 是最新的）
func (s *InitService) reloadConfig() error {
	configPath := "./config.yaml"
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %v", err)
	}

	// 兼容性预处理：将 YAML 中 !!str 类型的纯整数值（如 "1"）规范化为 !!int（如 1）。
	// 这类值可能由旧版本代码或 DB 回写路径写入，否则 yaml.Unmarshal 到强类型 struct 会报错。
	if normalized, normErr := normalizeYAMLStringInts(configData); normErr == nil {
		configData = normalized
	}

	// 先解析配置到临时变量（用于验证及 ConfigManager 不可用时的降级加载）
	var tempConfig configManager.Server
	if err := yaml.Unmarshal(configData, &tempConfig); err != nil {
		return fmt.Errorf("解析配置文件失败: %v", err)
	}

	// 使用 ConfigManager 重新加载配置
	// 这样可以确保：
	// 1. 配置被同步到数据库
	// 2. 触发回调同步到 global.APP_CONFIG
	// 3. 配置缓存被更新
	cm := configManager.GetConfigManager()
	if cm != nil {
		if err := cm.ReloadFromYAML(); err != nil {
			global.APP_LOG.Warn("通过ConfigManager重新加载配置失败", zap.Error(err))
			// 降级处理：直接加载到 global.APP_CONFIG
			global.SetAppConfig(tempConfig)
			global.APP_LOG.Warn("配置已直接加载到global.APP_CONFIG，但未同步到数据库")
		} else {
			global.APP_LOG.Info("配置已通过ConfigManager重新加载并同步到数据库")
		}
	} else {
		// ConfigManager 未初始化，直接加载
		global.SetAppConfig(tempConfig)
		global.APP_LOG.Warn("ConfigManager未初始化，配置仅加载到global.APP_CONFIG")
	}

	global.APP_LOG.Info("配置已从文件重新加载")
	return nil
}

// normalizeYAMLStringInts 将 YAML 中所有 !!str 类型的纯整数标量节点转换为 !!int。
// 例如：将 min-level-for-container: "1" 转换为 min-level-for-container: 1。
// 这解决了旧版本代码或 DB 回写路径将整数值写为带引号字符串的兼容性问题。
func normalizeYAMLStringInts(data []byte) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	normalizeYAMLNode(&root)
	return yaml.Marshal(&root)
}

func normalizeYAMLNode(node *yaml.Node) {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		if _, err := strconv.Atoi(node.Value); err == nil {
			node.Tag = "!!int"
			node.Style = 0
		}
	}
	for _, child := range node.Content {
		normalizeYAMLNode(child)
	}
}
