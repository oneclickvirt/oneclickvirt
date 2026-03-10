package initialize

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"oneclickvirt/config"
	"oneclickvirt/global"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// 默认配置
func getDefaultConfig() config.Server {
	// 根据架构自动检测数据库类型
	defaultDbType := detectDatabaseType()

	return config.Server{
		System: config.System{
			Env:           "public",
			Addr:          8888,
			DbType:        defaultDbType,
			UseMultipoint: false,
			UseRedis:      false,
		},
		JWT: config.JWT{
			// SigningKey 会在 core/viper.go 中自动生成，无需配置
			ExpiresTime: "7d",
			BufferTime:  "1d",
			Issuer:      "oneclickvirt",
		},
		Zap: config.Zap{
			Level:         "info",
			Prefix:        "[oneclickvirt]",
			Format:        "console",
			Director:      "logs",
			EncodeLevel:   "LowercaseColorLevelEncoder",
			StacktraceKey: "stacktrace",
			ShowLine:      true,
			LogInConsole:  true,
		},
		Mysql: config.Mysql{
			Path:   "127.0.0.1",
			Port:   "3306",
			Config: "charset=utf8mb4&parseTime=True&loc=Asia%2FShanghai&time_zone=%27%2B08%3A00%27",

			Dbname:       "oneclickvirt",
			Username:     "root",
			Password:     "root",
			Prefix:       "",
			Singular:     false,
			Engine:       "InnoDB",
			MaxIdleConns: 10,
			MaxOpenConns: 100,
			LogMode:      "info",
			LogZap:       false,
			MaxLifetime:  3600,
			AutoCreate:   true,
		},
		Auth: config.Auth{
			EnableEmail:              false,
			EnableTelegram:           false,
			EnableQQ:                 false,
			EnableOAuth2:             false,
			EnablePublicRegistration: false,
			EmailSMTPHost:            "",
			EmailSMTPPort:            587,
			EmailUsername:            "",
			EmailPassword:            "",
			TelegramBotToken:         "",
			QQAppID:                  "",
			QQAppKey:                 "",
		},
		Quota: config.Quota{
			DefaultLevel: 1,
			InstanceTypePermissions: config.InstanceTypePermissions{
				MinLevelForContainer:       1,
				MinLevelForVM:              1,
				MinLevelForDeleteContainer: 1,
				MinLevelForDeleteVM:        1,
				MinLevelForResetContainer:  1,
				MinLevelForResetVM:         1,
			},
			LevelLimits: map[int]config.LevelLimitInfo{
				1: {
					MaxInstances: 1,
					MaxResources: map[string]interface{}{
						"cpu":    1,
						"memory": 1025,
						"disk":   1,
					},
				},
				2: {
					MaxInstances: 3,
					MaxResources: map[string]interface{}{
						"cpu":    2,
						"memory": 1024,
						"disk":   20,
					},
				},
				3: {
					MaxInstances: 5,
					MaxResources: map[string]interface{}{
						"cpu":    4,
						"memory": 2048,
						"disk":   40,
					},
				},
				4: {
					MaxInstances: 10,
					MaxResources: map[string]interface{}{
						"cpu":    8,
						"memory": 4096,
						"disk":   80,
					},
				},
				5: {
					MaxInstances: 20,
					MaxResources: map[string]interface{}{
						"cpu":    16,
						"memory": 8192,
						"disk":   160,
					},
				},
			},
		},
		InviteCode: config.InviteCode{
			Enabled:  false,
			Required: false,
		},
		Captcha: config.Captcha{
			Enabled:    false,
			Width:      120,
			Height:     40,
			Length:     4,
			ExpireTime: 5,
		},
		Cors: config.CORS{
			Mode:      "allow-all",
			Whitelist: []string{"http://localhost:8080", "http://127.0.0.1:8080"},
		},
		Redis: config.Redis{
			Addr:     "127.0.0.1:6379",
			Password: "",
			DB:       0,
		},
	}
}

// 创建默认配置文件
func createDefaultConfigFile(configPath string) error {
	defaultConfig := getDefaultConfig()

	// 确保目录存在
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建配置目录失败: %v", err)
	}

	// 将配置写入文件
	data, err := yaml.Marshal(defaultConfig)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %v", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %v", err)
	}

	// 此时日志系统尚未就绪，使用 stdout 输出提示性信息
	fmt.Printf("[CONFIG] 已创建默认配置文件: %s\n", configPath)
	return nil
}

// selectDatabaseType 根据配置中的 DbType 进行合法性校验并必要时自动检测则修正。
// 注意：该函数在日志系统初始化之前调用，必须使用 fmt 输出。
func selectDatabaseType(cfg *config.Server) {
	switch cfg.System.DbType {
	case "mysql", "mariadb":
		if cfg.Mysql.Dbname == "" && !cfg.Mysql.AutoCreate {
			fmt.Fprintf(os.Stderr, "[CONFIG WARN] %s 数据库名为空且未开启 AutoCreate，请检查配置\n", cfg.System.DbType)
		}
		fmt.Printf("[CONFIG] 使用数据库类型: %s\n", cfg.System.DbType)
	default:
		detectedType := detectDatabaseType()
		fmt.Fprintf(os.Stderr, "[CONFIG WARN] 不支持的数据库类型 %q，自动模式检测为: %s\n", cfg.System.DbType, detectedType)
		cfg.System.DbType = detectedType
	}
}

// 检测当前环境中的数据库类型
func detectDatabaseType() string {
	// 检查环境变量
	if dbType := os.Getenv("DB_TYPE"); dbType != "" {
		if dbType == "mysql" || dbType == "mariadb" {
			return dbType
		}
	}

	// 检查架构来决定默认数据库类型（与Dockerfile中的逻辑一致）
	arch := runtime.GOARCH
	if arch == "amd64" {
		return "mysql"
	} else {
		return "mariadb"
	}
}

// 初始化配置
func InitConfig(configPath ...string) *viper.Viper {
	var config string
	if len(configPath) == 0 {
		config = "config.yaml"
	} else {
		config = configPath[0]
	}

	v := viper.New()
	v.SetConfigFile(config)
	v.SetConfigType("yaml")

	// 配置文件不存在时自动生成默认配置
	if _, err := os.Stat(config); os.IsNotExist(err) {
		fmt.Printf("[CONFIG] 配置文件 %q 不存在，正在生成默认配置...\n", config)
		if err := createDefaultConfigFile(config); err != nil {
			fmt.Fprintf(os.Stderr, "[CONFIG ERROR] 创建默认配置文件失败: %v，降级为内存配置\n", err)
			global.SetAppConfig(getDefaultConfig())
			return v
		}
	}

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "[CONFIG ERROR] 读取配置文件失败: %v，降级为内存默认配置\n", err)
		global.SetAppConfig(getDefaultConfig())
		return v
	}

	// 监听配置文件变化
	v.WatchConfig()
	v.OnConfigChange(func(e fsnotify.Event) {
		fmt.Printf("[CONFIG] 配置文件变更: %s\n", e.Name)

		newConfig := getDefaultConfig()
		if err := v.Unmarshal(&newConfig); err != nil {
			fmt.Fprintf(os.Stderr, "[CONFIG WARN] 重载配置失败: %v，保持原有配置\n", err)
			return
		}

		if err := validateConfig(&newConfig); err != nil {
			fmt.Fprintf(os.Stderr, "[CONFIG WARN] 新配置校验失败: %v，保持原有配置\n", err)
			return
		}

		selectDatabaseType(&newConfig)
		global.SetAppConfig(newConfig)
		fmt.Println("[CONFIG] 配置已成功重新加载")

		// 日志系统已就绪后同步写入结构化日志
		if global.APP_LOG != nil {
			global.APP_LOG.Info("配置文件重新加载成功", zap.String("file", e.Name))
		}
	})

	// 解析配置到全局变量
	loadedConfig := getDefaultConfig()
	if err := v.Unmarshal(&loadedConfig); err != nil {
		fmt.Fprintf(os.Stderr, "[CONFIG ERROR] 解析配置文件失败: %v，降级为内存默认配置\n", err)
		global.SetAppConfig(getDefaultConfig())
	} else {
		if err := validateConfig(&loadedConfig); err != nil {
			fmt.Fprintf(os.Stderr, "[CONFIG ERROR] 配置校验失败: %v，降级为内存默认配置\n", err)
			global.SetAppConfig(getDefaultConfig())
		} else {
			selectDatabaseType(&loadedConfig)
			global.SetAppConfig(loadedConfig)
		}
	}

	return v
}

// validateConfig 验证配置的有效性
func validateConfig(cfg *config.Server) error {
	// 验证基本配置
	if cfg.System.Addr <= 0 || cfg.System.Addr > 65535 {
		return fmt.Errorf("无效的端口号: %d", cfg.System.Addr)
	}

	// JWT签名密钥会在 core/viper.go 中自动生成，这里无需验证

	// 验证数据库配置
	if cfg.System.DbType == "" {
		return fmt.Errorf("数据库类型不能为空")
	}

	return nil
}
