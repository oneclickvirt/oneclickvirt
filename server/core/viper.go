package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/config"
	"oneclickvirt/global"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Viper 初始化并返回 viper 实例。
//
// 说明：
//   - 该函数在日志系统（global.APP_LOG）就绪之前执行，所有输出均使用 fmt 包；
//   - 错误与告警写入 stderr，提示性信息写入 stdout；
//   - 读取失败时不 panic，改为使用内存默认配置继续运行，保证服务可启动；
//   - 通过 OnConfigChange 热重载配置，日志系统就绪后同步写入结构化日志。
func Viper(path ...string) *viper.Viper {
	var cfgFile string
	if len(path) == 0 {
		cfgFile = "config.yaml"
	} else {
		cfgFile = path[0]
	}

	v := viper.New()
	v.SetConfigFile(cfgFile)
	v.SetConfigType("yaml")

	// 先设置安全默认值，确保即使配置文件读取失败也有合理的基础值
	setDefaults(v)
	applyEnvOverrides(v)

	if err := v.ReadInConfig(); err != nil {
		// 读取失败降级为内存默认配置，不中断启动流程
		fmt.Fprintf(os.Stderr, "[VIPER WARN] 配置文件读取失败: %v，将使用默认配置\n", err)
		// 仍然从默认值构建配置并设置全局变量，避免 GetAppConfig() 返回零值
		var defaultCfg config.Server
		if uerr := v.Unmarshal(&defaultCfg); uerr == nil {
			normalizeDatabaseConfig(&defaultCfg)
			global.SetAppConfig(defaultCfg)
		}
		return v
	}
	v.WatchConfig()
	v.OnConfigChange(func(e fsnotify.Event) {
		// 一旦 ConfigManager 已从数据库完成初始化，配置的权威来源由 ConfigManager 管理，
		// 不再通过 viper 文件监听覆盖 global.APP_CONFIG。
		// 这避免了以下竞态条件：启动阶段 RestoreConfigFromDatabase 写入 YAML 触发的
		// 延迟 fsnotify 事件，在用户 API 保存回调更新 global.APP_CONFIG 之后才到达，
		// 从而将内存中的值重置为启动时的快照。
		if global.CONFIG_MANAGER_READY.Load() {
			fmt.Printf("[VIPER] 配置文件变更（ConfigManager 已就绪，跳过热重载）: %s\n", e.Name)
			return
		}
		fmt.Printf("[VIPER] 配置文件变更: %s\n", e.Name)
		var newCfg config.Server
		if err := v.Unmarshal(&newCfg); err != nil {
			fmt.Fprintf(os.Stderr, "[VIPER WARN] 热重载配置解析失败: %v，保持原有配置\n", err)
		} else {
			normalizeDatabaseConfig(&newCfg)
			global.SetAppConfig(newCfg)
		}
	})

	var initCfg config.Server
	if err := v.Unmarshal(&initCfg); err != nil {
		fmt.Fprintf(os.Stderr, "[VIPER WARN] 初始配置解析失败: %v，将使用默认配置\n", err)
		// 即使解析失败也要设置基本配置，避免 GetAppConfig() 返回零值
		// （零值 Cors.Mode=="" 会导致 CORS 使用白名单模式，非 localhost 请求返回 403）
		fallbackCfg := config.Server{}
		fallbackCfg.System.Addr = 8888
		fallbackCfg.System.DbType = "mysql"
		fallbackCfg.System.Env = "public"
		fallbackCfg.Cors.Mode = "whitelist"
		global.SetAppConfig(fallbackCfg)
	} else {
		normalizeDatabaseConfig(&initCfg)
		global.SetAppConfig(initCfg)
	}

	return v
}

func normalizeDatabaseConfig(cfg *config.Server) {
	if strings.TrimSpace(cfg.System.DbType) == "" {
		cfg.System.DbType = "mysql"
	}
	if strings.TrimSpace(cfg.Mysql.Path) == "" {
		cfg.Mysql.Path = "127.0.0.1"
	}
	if strings.TrimSpace(cfg.Mysql.Port) == "" {
		cfg.Mysql.Port = "3306"
	}
	if strings.TrimSpace(cfg.Mysql.Config) == "" {
		cfg.Mysql.Config = "charset=utf8mb4&parseTime=True&loc=Asia%2FShanghai&time_zone=%27%2B08%3A00%27"
	}
	if strings.TrimSpace(cfg.Mysql.Dbname) == "" {
		cfg.Mysql.Dbname = "oneclickvirt"
	}
	if strings.TrimSpace(cfg.Mysql.Username) == "" {
		cfg.Mysql.Username = "root"
	}
	if strings.TrimSpace(cfg.Mysql.Engine) == "" {
		cfg.Mysql.Engine = "InnoDB"
	}
	if cfg.Mysql.MaxIdleConns <= 0 {
		cfg.Mysql.MaxIdleConns = 20
	}
	if cfg.Mysql.MaxOpenConns <= 0 {
		cfg.Mysql.MaxOpenConns = 200
	}
	if cfg.Mysql.MaxLifetime <= 0 {
		cfg.Mysql.MaxLifetime = 1800
	}
}

// setDefaults 设置配置项安全默认值。
// 注意：对于已在 config.yaml 中定义的重项，viper 的 SetDefault 不会覆盖文件中的值。
func setDefaults(v *viper.Viper) {
	v.SetDefault("system.env", "public")
	v.SetDefault("system.addr", 8080)
	v.SetDefault("system.db-type", "mysql")
	v.SetDefault("system.oss-type", "local")
	v.SetDefault("system.use-multipoint", false)
	v.SetDefault("system.use-redis", false)
	v.SetDefault("system.iplimit-count", 15000)
	v.SetDefault("system.iplimit-time", 3600)
	v.SetDefault("mysql.path", "127.0.0.1")
	v.SetDefault("mysql.port", "3306")
	v.SetDefault("mysql.config", "charset=utf8mb4&parseTime=True&loc=Asia%2FShanghai&time_zone=%27%2B08%3A00%27")
	v.SetDefault("mysql.db-name", "oneclickvirt")
	v.SetDefault("mysql.username", "root")
	v.SetDefault("mysql.engine", "InnoDB")
	v.SetDefault("mysql.max-idle-conns", 20)
	v.SetDefault("mysql.max-open-conns", 200)
	v.SetDefault("mysql.max-lifetime", 1800)
	v.SetDefault("mysql.auto-create", true)

	// 生成随机安全的 JWT 默认签名密钒（如果 config.yaml 未配置）
	randomKey := generateSecureJWTKey()
	if err := validateJWTKeyStrength(randomKey); err != nil {
		// 此时日志系统尚未就绪，必须使用 fmt 输出
		fmt.Fprintf(os.Stderr, "[VIPER WARN] JWT 密钒强度不足: %v，将重新生成\n", err)
		randomKey = generateSecureJWTKey()
	}

	v.SetDefault("jwt.signing-key", randomKey)
	v.SetDefault("jwt.expires-time", "7d")
	v.SetDefault("jwt.buffer-time", "1d")
	v.SetDefault("jwt.issuer", "oneclickvirt")

	v.SetDefault("zap.level", "info")
	v.SetDefault("zap.format", "console")
	v.SetDefault("zap.prefix", "[oneclickvirt]")
	v.SetDefault("zap.director", "logs")
	v.SetDefault("zap.show-line", true)
	v.SetDefault("zap.encode-level", "LowercaseColorLevelEncoder")
	v.SetDefault("zap.stacktrace-key", "stacktrace")
	v.SetDefault("zap.log-in-console", true)

	v.SetDefault("maintenance.enable-data-cleanup", true)
	v.SetDefault("maintenance.data-cleanup-interval-hours", 24)
	v.SetDefault("maintenance.audit-log-retention-days", 30)
	v.SetDefault("maintenance.traffic-history-retention-days", 180)
	v.SetDefault("maintenance.cleanup-batch-size", 5000)
	v.SetDefault("maintenance.optimize-after-cleanup", false)

	// CORS 默认值：白名单模式，生产环境避免通配 Origin。
	v.SetDefault("cors.mode", "whitelist")
}

func applyEnvOverrides(v *viper.Viper) {
	envToKey := map[string]string{
		"DB_HOST":     "mysql.path",
		"DB_PORT":     "mysql.port",
		"DB_NAME":     "mysql.db-name",
		"DB_USER":     "mysql.username",
		"DB_PASSWORD": "mysql.password",
		"DB_TYPE":     "system.db-type",
		"SERVER_PORT": "system.addr",
	}
	for envName, configKey := range envToKey {
		if value, exists := os.LookupEnv(envName); exists {
			value = normalizeDeploymentEnvValue(envName, value)
			// Deployment tools often declare optional variables as empty strings.
			// Treating those as authoritative erased a persisted no-db connection
			// during image replacement, so only non-empty values override YAML.
			if value != "" {
				v.Set(configKey, value)
			}
		}
	}
}

// normalizeDeploymentEnvValue accepts the values produced by normal shell and
// Compose quoting, while also repairing a common deployment mistake where the
// quote characters themselves are persisted in Docker's environment (for
// example DB_PORT='"3306"'). Those literal quotes otherwise reach the MySQL
// DSN and turn the port into tcp/"3306", breaking every restart even though
// the persisted YAML is correct.
func normalizeDeploymentEnvValue(name, value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 2 || name == "DB_PASSWORD" {
		return value
	}

	if value[0] == '"' && value[len(value)-1] == '"' {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return strings.TrimSpace(unquoted)
		}
	}
	if value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}

// generateSecureJWTKey 生成一个随机 256 位十六进制字符串作为 JWT 签名密钒。
// 如果 “crypto/rand” 失败，降级为基于纳秒级时间戳的后备密钒。
func generateSecureJWTKey() string {
	b := make([]byte, 32) // 256 位 = 64 个十六进制字符
	if _, err := rand.Read(b); err != nil {
		// 极低概率失败，使用纳秒时间戳拼接得到足夠长度的密钒
		backupKey := fmt.Sprintf("oneclickvirt-backup-%d", time.Now().UnixNano())
		for len(backupKey) < 64 {
			backupKey += fmt.Sprintf("-%d", time.Now().UnixNano())
		}
		return backupKey[:64]
	}
	return hex.EncodeToString(b)
}

// validateJWTKeyStrength 校验 JWT 签名密钒的强度要求：
//   - 长度不少于 32 字符；
//   - 不包含常见弱密钒模式。
func validateJWTKeyStrength(key string) error {
	if len(key) < 32 {
		return fmt.Errorf("JWT密钥长度不足，当前长度: %d，最小要求: 32", len(key))
	}

	// 检查是否是弱密钥
	weakKeys := []string{
		"secret",
		"password",
		"12345",
		"test",
		"jwt-secret",
		"your-secret-key",
		"change-me",
	}

	for _, weak := range weakKeys {
		if strings.Contains(strings.ToLower(key), weak) {
			return fmt.Errorf("JWT密钥包含弱模式，请使用更强的密钥")
		}
	}

	return nil
}
