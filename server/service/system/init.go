package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

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
	"oneclickvirt/utils/dbconnect"

	configManager "oneclickvirt/config"

	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// InitService 初始化服务
type InitService struct{}

var databaseConfigFileMu sync.Mutex

// ResolveDatabaseConfigCredentials reuses the already loaded deployment
// password when the initialization form leaves it blank. All-in-one images can
// generate and persist an internal password, and no-db images commonly receive
// it through DB_PASSWORD; neither secret should need to be exposed back to the
// browser. The fallback is deliberately restricted to the exact configured
// endpoint and account so a blank password for another server never borrows a
// credential accidentally.
func ResolveDatabaseConfigCredentials(dbConfig config.DatabaseConfig) config.DatabaseConfig {
	if dbConfig.Password != "" {
		return dbConfig
	}

	current := global.GetAppConfig().Mysql
	currentPort, err := strconv.Atoi(strings.TrimSpace(current.Port))
	if err != nil {
		return dbConfig
	}

	if normalizeDatabaseHost(dbConfig.Host) == normalizeDatabaseHost(current.Path) &&
		dbConfig.Port == currentPort &&
		dbConfig.Database == current.Dbname &&
		dbConfig.Username == current.Username {
		dbConfig.Password = current.Password
	}
	return dbConfig
}

func normalizeDatabaseHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "localhost", "::1", "[::1]":
		return "127.0.0.1"
	default:
		return host
	}
}

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

// TestDatabaseConnection validates the same options used at startup and recovery.
func (s *InitService) TestDatabaseConnection(dbConfig config.DatabaseConfig) error {
	_, err := s.DetectDatabaseConnection(dbConfig)
	return err
}

// DetectDatabaseConnection returns only safe server metadata, never credentials.
func (s *InitService) DetectDatabaseConnection(dbConfig config.DatabaseConfig) (dbconnect.Info, error) {
	dbConfig = ResolveDatabaseConfigCredentials(dbConfig)
	m, err := databaseRequestConfig(dbConfig)
	if err != nil {
		return dbconnect.Info{}, err
	}
	db, info, err := dbconnect.Open(context.Background(), m, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return info, err
	}
	pool, err := db.DB()
	if err == nil {
		pool.Close()
	}
	return info, err
}

func databaseRequestConfig(dbConfig config.DatabaseConfig) (config.MysqlConfig, error) {
	m := global.GetAppConfig().Mysql.ConnectionConfig()
	dbConfig.Type = configManager.NormalizeDatabaseType(dbConfig.Type)
	if !configManager.IsSupportedDatabaseType(dbConfig.Type) {
		return m, fmt.Errorf("不支持的数据库类型: %s，仅支持mysql和mariadb", dbConfig.Type)
	}
	m.Path, m.Port, m.Dbname = dbConfig.Host, strconv.Itoa(dbConfig.Port), dbConfig.Database
	m.Username, m.Password, m.AutoCreate = dbConfig.Username, dbConfig.Password, true
	if m.Config == "" {
		m.Config = dbconnect.DefaultParams
	}
	if dbConfig.SSLMode != "" {
		params, err := url.ParseQuery(m.Config)
		if err != nil {
			return m, fmt.Errorf("数据库高级连接参数格式错误")
		}
		switch dbConfig.SSLMode {
		case "true", "require", "verify-full":
			params.Set("tls", "true")
		case "false", "disable":
			params.Set("tls", "false")
		case "skip-verify", "preferred":
			params.Set("tls", dbConfig.SSLMode)
		default:
			return m, fmt.Errorf("不支持的数据库 TLS 模式")
		}
		m.Config = params.Encode()
	}
	return m, nil
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
		&providerModel.ProviderIPv6Pool{},
		&providerModel.ProviderIPv6Tunnel{},
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
		&monitoringModel.EgressDesiredProfile{},
		&monitoringModel.EgressDesiredBinding{},

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
	dbConfig = ResolveDatabaseConfigCredentials(dbConfig)
	info, err := s.DetectDatabaseConnection(dbConfig)
	if err != nil {
		return err
	}
	dbConfig.Type = info.Type
	// 更新数据库配置
	if err := s.updateDatabaseConfig(dbConfig, info.Params); err != nil {
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

// UpdateDatabaseConfig 更新数据库配置。数据库连接参数是启动级配置，
// 只能持久化到 YAML，不能通过 ConfigManager 的运行时 API 修改。
func (s *InitService) UpdateDatabaseConfig(dbConfig config.DatabaseConfig) error {
	return s.updateDatabaseConfig(dbConfig, "")
}

// PersistDetectedDatabaseConfig writes only the engine label and connection
// options that the server probe actually repaired.  Startup and reconnects
// must be able to correct a stale mysql/mariadb hint without copying a second
// section's credentials or overwriting user tuning.  A missing config file is
// normal for environment-only deployments and is therefore ignored.
func (s *InitService) PersistDetectedDatabaseConfig(source config.MysqlConfig, configuredType string, info dbconnect.Info) error {
	actualType := strings.ToLower(strings.TrimSpace(info.Type))
	if actualType != "mysql" && actualType != "mariadb" {
		return fmt.Errorf("数据库探测返回了不支持的类型: %s", info.Type)
	}
	configuredType = strings.ToLower(strings.TrimSpace(configuredType))
	// A reconnect may finish after another request has switched the endpoint.
	// Never let that stale probe rewrite the newer deployment configuration.
	current := global.GetAppConfig().Mysql
	if strings.TrimSpace(current.Path) != strings.TrimSpace(source.Path) ||
		strings.TrimSpace(current.Port) != strings.TrimSpace(source.Port) ||
		current.Dbname != source.Dbname || current.Username != source.Username ||
		current.Password != source.Password ||
		(current.Config != source.Config && current.Config != info.Params) {
		return nil
	}

	databaseConfigFileMu.Lock()
	defer databaseConfigFileMu.Unlock()

	configPath := databaseConfigPath()
	configData, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil // DB_* environment variables are the complete deployment config.
	}
	if err != nil {
		return fmt.Errorf("读取数据库配置文件失败: %w", err)
	}

	var node yaml.Node
	if err := yaml.Unmarshal(configData, &node); err != nil {
		return fmt.Errorf("解析数据库配置文件失败: %w", err)
	}
	section := detectMysqlKey(&node)
	changed := false
	if configuredType != actualType || !yamlHasPath(&node, "system.db-type") || yamlScalarValue(&node, "system.db-type") != actualType {
		if err := updateYAMLNodeValue(&node, "system.db-type", actualType); err != nil {
			return fmt.Errorf("更新数据库类型失败: %w", err)
		}
		changed = true
	}
	if len(info.Repairs) != 0 && strings.TrimSpace(info.Params) != "" && yamlScalarValue(&node, section+".config") != info.Params {
		if err := updateYAMLNodeValue(&node, section+".config", info.Params); err != nil {
			return fmt.Errorf("更新数据库兼容参数失败: %w", err)
		}
		changed = true
	}
	if !changed {
		return nil
	}

	newConfigData, err := yaml.Marshal(&node)
	if err != nil {
		return fmt.Errorf("序列化数据库配置失败: %w", err)
	}
	// Keep one recoverable copy before an automatic repair.  Do not replace an
	// existing backup on every heartbeat/reconnect; it remains the last known
	// pre-repair configuration.
	backupPath := configPath + ".backup"
	if _, statErr := os.Stat(backupPath); os.IsNotExist(statErr) {
		if err := os.WriteFile(backupPath, configData, 0600); err != nil {
			return fmt.Errorf("备份数据库配置失败: %w", err)
		}
		_ = os.Chmod(backupPath, 0600)
	}
	if err := writeDatabaseConfigFile(configPath, newConfigData); err != nil {
		return fmt.Errorf("写入修复后的数据库配置失败: %w", err)
	}

	// Keep Viper's in-memory precedence aligned with the atomic file update.
	// The global copy was already reconciled by GormMysqlWithInfo; this avoids a
	// delayed fsnotify event reintroducing the stale hint during startup.
	if global.APP_VP != nil {
		global.APP_VP.Set("system.db-type", actualType)
		if len(info.Repairs) != 0 && strings.TrimSpace(info.Params) != "" {
			global.APP_VP.Set(section+".config", info.Params)
		}
	}
	return nil
}

func (s *InitService) updateDatabaseConfig(dbConfig config.DatabaseConfig, detectedParams string) error {
	databaseConfigFileMu.Lock()
	defer databaseConfigFileMu.Unlock()
	dbConfig = ResolveDatabaseConfigCredentials(dbConfig)
	dbConfig.Type = configManager.NormalizeDatabaseType(dbConfig.Type)
	if !configManager.IsSupportedDatabaseType(dbConfig.Type) {
		return fmt.Errorf("不支持的数据库类型: %s，仅支持mysql和mariadb", dbConfig.Type)
	}

	// 数据库连接参数属于系统级启动配置。ConfigManager.UpdateConfig 明确禁止
	// 修改这些键，而且它可能仍绑定在切换前的数据库上，因此初始化流程必须直接
	// 更新 config.yaml，不能通过运行时配置 API 绕过该安全边界。
	configPath := databaseConfigPath()
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %v", err)
	}

	// 使用 Node API 解析，保持原有格式
	var node yaml.Node
	if err := yaml.Unmarshal(configData, &node); err != nil {
		return fmt.Errorf("解析配置文件失败: %v", err)
	}

	// 自动检测配置文件中使用的键名（mysql 或 mariadb）
	mysqlKey := detectMysqlKey(&node)
	updates := []struct {
		key   string
		value interface{}
	}{
		{key: "system.db-type", value: dbConfig.Type},
		{key: mysqlKey + ".path", value: dbConfig.Host},
		{key: mysqlKey + ".port", value: strconv.Itoa(dbConfig.Port)},
		{key: mysqlKey + ".db-name", value: dbConfig.Database},
		{key: mysqlKey + ".username", value: dbConfig.Username},
		{key: mysqlKey + ".password", value: dbConfig.Password},
		{key: mysqlKey + ".config", value: "charset=utf8mb4&parseTime=True&loc=Local&time_zone=%27%2B08%3A00%27"},
		{key: mysqlKey + ".prefix", value: ""},
		{key: mysqlKey + ".singular", value: false},
		{key: mysqlKey + ".engine", value: "InnoDB"},
		{key: mysqlKey + ".max-idle-conns", value: 10},
		{key: mysqlKey + ".max-open-conns", value: 100},
		{key: mysqlKey + ".log-mode", value: "error"},
		{key: mysqlKey + ".log-zap", value: false},
		{key: mysqlKey + ".max-lifetime", value: 3600},
		{key: mysqlKey + ".auto-create", value: true},
	}

	for _, update := range updates {
		// Do not overwrite user tuning, timeout or TLS options with form defaults.
		if strings.HasPrefix(update.key, mysqlKey+".") {
			field := strings.TrimPrefix(update.key, mysqlKey+".")
			switch field {
			case "path", "port", "db-name", "username", "password":
			case "config":
				if detectedParams != "" {
					update.value = detectedParams
				} else if yamlHasPath(&node, update.key) {
					continue
				}
			default:
				if yamlHasPath(&node, update.key) {
					continue
				}
			}
		}
		if err := updateYAMLNodeValue(&node, update.key, update.value); err != nil {
			return fmt.Errorf("更新配置 %s 失败: %v", update.key, err)
		}
	}

	// 序列化并保存
	newConfigData, err := yaml.Marshal(&node)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %v", err)
	}

	// 备份原配置文件
	backupPath := configPath + ".backup"
	if err := os.WriteFile(backupPath, configData, 0600); err != nil {
		global.APP_LOG.Debug("备份配置文件失败", zap.String("error", utils.FormatError(err)))
	} else if err := os.Chmod(backupPath, 0600); err != nil {
		global.APP_LOG.Debug("收紧配置备份文件权限失败", zap.String("error", utils.FormatError(err)))
	}

	// 写入新配置
	if err := writeDatabaseConfigFile(configPath, newConfigData); err != nil {
		return fmt.Errorf("写入配置文件失败: %v", err)
	}

	global.APP_LOG.Info("数据库配置已成功写入文件",
		zap.String("host", dbConfig.Host),
		zap.Int("port", dbConfig.Port),
		zap.String("database", dbConfig.Database))

	// 直接更新启动级内存配置。此处不能调用 ConfigManager.ReloadFromYAML，
	// 否则会把业务配置写入切换前的数据库，甚至在其 DB 句柄为空时触发 panic。
	if err := loadDatabaseSettings(newConfigData); err != nil {
		return err
	}

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
	foundMariaDB := false
	for i := 0; i < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		if keyNode.Value == "mysql" {
			return "mysql"
		}
		if keyNode.Value == "mariadb" {
			foundMariaDB = true
		}
	}
	if foundMariaDB {
		return "mariadb"
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
			keyNode := &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: key,
			}
			if i == len(keys)-1 {
				valueNode := &yaml.Node{}
				if err := setYAMLNodeValue(valueNode, value); err != nil {
					return err
				}
				current.Content = append(current.Content, keyNode, valueNode)
				return nil
			}

			mappingNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			current.Content = append(current.Content, keyNode, mappingNode)
			current = mappingNode
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
		node.Kind = yaml.ScalarNode
		node.Style = 0
		node.Tag = "!!str"
		node.Value = v
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

// ReinitializeDatabase uses the same decoding and connection path as startup.
func (s *InitService) ReinitializeDatabase() error {
	data, err := os.ReadFile(databaseConfigPath())
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}
	cfg, err := readDatabaseSettings(data)
	if err != nil {
		return err
	}
	m := cfg.Mysql.ConnectionConfig()
	db, info, err := dbconnect.Open(context.Background(), m, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("重新连接数据库失败: %w", err)
	}
	configuredType := cfg.System.DbType
	cfg.System.DbType, cfg.Mysql.Config = info.Type, info.Params
	global.UpdateAppConfig(func(current *configManager.Server) {
		current.Mysql, current.System.DbType = cfg.Mysql, cfg.System.DbType
	})
	// Reinitialization is also a configuration entry point. Persist the engine
	// detected by the validated connection and any compatibility parameters so
	// the next process start does not repeat a stale MySQL/MariaDB mismatch.
	if repairErr := s.PersistDetectedDatabaseConfig(m, configuredType, info); repairErr != nil && global.APP_LOG != nil {
		global.APP_LOG.Warn("数据库已重连，但自动持久化引擎兼容修复失败", zap.Error(repairErr))
	}
	// The initialization page can replace the endpoint while the process is
	// already serving requests. Publish the replacement to the connection
	// manager at the same point as APP_DB, so the next heartbeat cannot mistake
	// the retired pool for the current endpoint. The manager closes its own old
	// pool; if startup was running without a manager, close the previous global
	// pool here instead.
	previousDB := global.APP_DB
	global.APP_DB = db
	var managedPrevious *gorm.DB
	if adopter := global.APP_DB_CONNECTION_ADOPTER; adopter != nil {
		managedPrevious = adopter.AdoptConnection(db)
	}
	if previousDB != nil && previousDB != db && previousDB != managedPrevious {
		if previousPool, poolErr := previousDB.DB(); poolErr == nil {
			if closeErr := previousPool.Close(); closeErr != nil && global.APP_LOG != nil {
				global.APP_LOG.Warn("关闭旧数据库连接失败", zap.Error(closeErr))
			}
		}
	}
	if global.APP_LOG != nil {
		global.APP_LOG.Info("数据库连接已更新", zap.String("type", info.Type),
			zap.String("version", info.Version), zap.Strings("repairs", info.Repairs))
	}
	return nil
}

func databaseConfigPath() string {
	if global.APP_VP != nil && global.APP_VP.ConfigFileUsed() != "" {
		return global.APP_VP.ConfigFileUsed()
	}
	for _, name := range []string{"config.yaml", "config.yml"} {
		if _, err := os.Stat(name); err == nil {
			return name
		}
	}
	return "config.yaml"
}

func readDatabaseSettings(data []byte) (configManager.Server, error) {
	var cfg configManager.Server
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return cfg, fmt.Errorf("解析配置文件失败: %w", err)
	}
	if len(v.GetStringMap("mysql")) == 0 && len(v.GetStringMap("mariadb")) == 0 && os.Getenv("DB_HOST") == "" {
		return cfg, fmt.Errorf("数据库配置不完整")
	}
	v.SetDefault("mysql.auto-create", true)
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, fmt.Errorf("解析配置文件失败: %w", err)
	}
	if err := configManager.DecodeDatabase(v, &cfg); err != nil {
		return cfg, fmt.Errorf("解析数据库配置失败: %w", err)
	}
	return cfg, nil
}

func loadDatabaseSettings(data []byte) error {
	cfg, err := readDatabaseSettings(data)
	if err != nil {
		return err
	}
	global.UpdateAppConfig(func(current *configManager.Server) {
		current.Mysql, current.System.DbType = cfg.Mysql, cfg.System.DbType
	})
	return nil
}

func yamlHasPath(node *yaml.Node, path string) bool {
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	for _, part := range strings.Split(path, ".") {
		var child *yaml.Node
		for i := 0; node.Kind == yaml.MappingNode && i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == part {
				child = node.Content[i+1]
				break
			}
		}
		if child == nil {
			return false
		}
		node = child
	}
	return true
}

func yamlScalarValue(node *yaml.Node, path string) string {
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	for _, part := range strings.Split(path, ".") {
		var child *yaml.Node
		for i := 0; node.Kind == yaml.MappingNode && i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == part {
				child = node.Content[i+1]
				break
			}
		}
		if child == nil {
			return ""
		}
		node = child
	}
	if node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

func writeDatabaseConfigFile(path string, data []byte) error {
	// No-db images symlink /app/config.yaml into persistent storage. Replacing
	// that symlink would appear to save successfully, then lose the change on
	// container replacement. Atomically replace its destination instead.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	path = resolved
	f, err := os.CreateTemp(filepath.Dir(path), ".database-config-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err == nil {
		return nil
	}
	// Docker may bind-mount the individual YAML file. It cannot be replaced by
	// rename; preserve that mount and write it in place only for this errno.
	if errors.Is(err, syscall.EBUSY) {
		if err = os.WriteFile(path, data, 0600); err == nil {
			err = os.Chmod(path, 0600)
		}
	}
	return err
}

// reloadConfig 重新加载配置文件到 global.APP_CONFIG
// 手动修改 config.yaml 后调用此方法，会：
// 1. 将 YAML 配置同步到数据库
// 2. 通过 ConfigManager 回调同步到 global.APP_CONFIG
// 3. 清除配置修改标志（因为现在 YAML 是最新的）
func (s *InitService) reloadConfig() error {
	configPath := databaseConfigPath()
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
	databaseCfg, err := readDatabaseSettings(configData)
	if err != nil {
		return err
	}
	tempConfig.Mysql, tempConfig.System.DbType = databaseCfg.Mysql, databaseCfg.System.DbType

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
