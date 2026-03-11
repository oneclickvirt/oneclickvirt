package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
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

	if err := v.ReadInConfig(); err != nil {
		// 读取失败降级为内存默认配置，不中断启动流程
		fmt.Fprintf(os.Stderr, "[VIPER WARN] 配置文件读取失败: %v，将使用默认配置\n", err)
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
			global.SetAppConfig(newCfg)
		}
	})

	var initCfg config.Server
	if err := v.Unmarshal(&initCfg); err != nil {
		fmt.Fprintf(os.Stderr, "[VIPER WARN] 初始配置解析失败: %v，将使用默认配置\n", err)
	} else {
		global.SetAppConfig(initCfg)
	}

	// 设置各字段的安全默认值
	setDefaults(v)

	return v
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
