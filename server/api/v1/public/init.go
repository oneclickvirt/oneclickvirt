package public

import (
	"net/http"
	"oneclickvirt/service/auth"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/system"
	"os"
	"runtime"
	"strconv"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	configModel "oneclickvirt/model/config"
	"oneclickvirt/source"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// CheckInit 检查系统初始化状态
// @Summary 检查系统初始化状态
// @Description 检查系统是否需要进行初始化设置
// @Tags 系统初始化
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "检查成功"
// @Router /public/init/check [get]
func CheckInit(c *gin.Context) {
	var (
		message  = "前往初始化数据库"
		needInit = true
	)

	// 首先检查业务系统初始化标志文件是否存在（最可靠的判断方式）
	initFlagPath := "./storage/.system_initialized"
	if _, err := os.Stat(initFlagPath); err == nil {
		// 标志文件存在，说明系统已初始化
		message = "数据库无需初始化"
		needInit = false
		global.APP_LOG.Debug("检测到系统初始化标志文件，系统已初始化", zap.String("flagPath", initFlagPath))

		c.JSON(http.StatusOK, common.Success(gin.H{
			"needInit": needInit,
			"message":  message,
		}))
		return
	}

	// 标志文件不存在，继续其他检查
	global.APP_LOG.Debug("系统初始化标志文件不存在，继续检查数据库状态", zap.String("flagPath", initFlagPath))

	// 检查数据库连接是否存在
	if global.APP_DB == nil {
		message = "数据库未连接，需要初始化"
		needInit = true
		global.APP_LOG.Info("数据库连接为空，需要初始化")
	} else {
		// 验证数据库连接是否有效
		sqlDB, err := global.APP_DB.DB()
		if err != nil {
			message = "数据库连接无效，需要初始化"
			needInit = true
			global.APP_LOG.Warn("获取数据库连接失败", zap.Error(err))
		} else if err := sqlDB.Ping(); err != nil {
			message = "数据库连接测试失败，需要初始化"
			needInit = true
			global.APP_LOG.Warn("数据库连接ping失败", zap.Error(err))
		} else {
			// 检查数据库配置是否完整
			if global.APP_CONFIG.Mysql.Dbname == "" || global.APP_CONFIG.Mysql.Path == "" {
				message = "数据库配置不完整，需要初始化"
				needInit = true
				global.APP_LOG.Info("数据库配置不完整，需要初始化")
			} else {
				// 检查users表是否存在
				hasUsersTable := global.APP_DB.Migrator().HasTable("users")
				if !hasUsersTable {
					message = "数据库表未初始化，需要初始化"
					needInit = true
					global.APP_LOG.Info("users表不存在，需要初始化")
				} else {
					// 使用服务层检查是否有用户数据
					// 使用defer + recover捕获可能的panic
					func() {
						defer func() {
							if r := recover(); r != nil {
								message = "检查用户数据异常，需要初始化"
								needInit = true
								global.APP_LOG.Warn("检查用户数据时发生panic", zap.Any("panic", r))
							}
						}()

						systemStatsService := resources.SystemStatsService{}
						hasUsers, err := systemStatsService.CheckUserExists()
						if err != nil {
							message = "检查用户数据失败，需要初始化"
							needInit = true
							global.APP_LOG.Warn("检查用户数据失败", zap.Error(err))
						} else if !hasUsers {
							message = "未找到用户数据，需要初始化"
							needInit = true
							global.APP_LOG.Info("数据库中无用户数据，需要初始化")
						} else {
							message = "数据库无需初始化"
							needInit = false
							global.APP_LOG.Debug("系统已初始化")
						}
					}()
				}
			}
		}
	}

	global.APP_LOG.Debug("初始化状态检查",
		zap.Bool("needInit", needInit),
		zap.String("message", message))

	c.JSON(http.StatusOK, common.Success(gin.H{
		"needInit": needInit,
		"message":  message,
	}))
}

// TestDatabaseConnection 测试数据库连接
// @Summary 测试数据库连接
// @Description 测试数据库连接是否可用，用于初始化前验证数据库配置
// @Tags 系统初始化
// @Accept json
// @Produce json
// @Param request body object true "数据库连接参数"
// @Success 200 {object} common.Response "连接成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "连接失败"
// @Router /public/test-db-connection [post]
func TestDatabaseConnection(c *gin.Context) {
	var req struct {
		Type     string `json:"type" binding:"required"`
		Host     string `json:"host" binding:"required"`
		Port     string `json:"port" binding:"required"`
		Database string `json:"database" binding:"required"`
		Username string `json:"username" binding:"required"`
		Password string `json:"password"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Warn("数据库连接测试参数错误", zap.Error(err))
		c.JSON(http.StatusBadRequest, common.Error("参数错误"))
		return
	}

	// 支持MySQL和MariaDB
	if req.Type != "mysql" && req.Type != "mariadb" {
		c.JSON(http.StatusBadRequest, common.Error("仅支持MySQL和MariaDB数据库"))
		return
	}

	// 使用InitService测试连接
	initService := &system.InitService{}

	// 转换端口字符串为整数
	port, err := strconv.Atoi(req.Port)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Error("端口格式错误"))
		return
	}

	dbConfig := configModel.DatabaseConfig{
		Type:     req.Type,
		Host:     req.Host,
		Port:     port,
		Database: req.Database,
		Username: req.Username,
		Password: req.Password,
	}

	if err := initService.TestDatabaseConnection(dbConfig); err != nil {
		global.APP_LOG.Warn("数据库连接测试失败",
			zap.String("host", req.Host),
			zap.String("port", req.Port),
			zap.String("database", req.Database),
			zap.String("username", req.Username),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Error("数据库连接失败: "+err.Error()))
		return
	}

	global.APP_LOG.Info("数据库连接测试成功",
		zap.String("host", req.Host),
		zap.String("port", req.Port),
		zap.String("database", req.Database),
		zap.String("username", req.Username))

	c.JSON(http.StatusOK, common.Success("数据库连接测试成功"))
}

// InitSystem 初始化系统
// @Summary 初始化系统
// @Description 进行系统的初始化设置，创建管理员和默认用户
// @Tags 系统初始化
// @Accept json
// @Produce json
// @Param request body object true "初始化请求参数"
// @Success 200 {object} common.Response "初始化成功"
// @Failure 400 {object} common.Response "参数错误或系统已初始化"
// @Failure 500 {object} common.Response "初始化失败"
// @Router /public/init [post]
func InitSystem(c *gin.Context) {
	var req struct {
		Admin struct {
			Username string `json:"username" binding:"required"`
			Password string `json:"password" binding:"required"`
			Email    string `json:"email" binding:"required"`
		} `json:"admin" binding:"required"`
		User struct {
			Username string `json:"username" binding:"required"`
			Password string `json:"password" binding:"required"`
			Email    string `json:"email" binding:"required"`
		} `json:"user" binding:"required"`
		Database struct {
			Type     string `json:"type" binding:"required"`
			Host     string `json:"host"`
			Port     string `json:"port"`
			Database string `json:"database"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"database" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Error("参数错误"))
		return
	}

	// 先检查数据库配置并确保数据库连接
	// 转换端口字符串为整数
	port, err := strconv.Atoi(req.Database.Port)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Error("数据库端口格式错误"))
		return
	}

	// 创建数据库配置
	dbConfig := configModel.DatabaseConfig{
		Type:     req.Database.Type,
		Host:     req.Database.Host,
		Port:     port,
		Database: req.Database.Database,
		Username: req.Database.Username,
		Password: req.Database.Password,
	}

	// 初始化服务
	initService := &system.InitService{}

	// 测试数据库连接
	if err := initService.TestDatabaseConnection(dbConfig); err != nil {
		c.JSON(http.StatusInternalServerError, common.Error("数据库连接失败: "+err.Error()))
		return
	}

	// 如果数据库已经连接，检查是否有用户数据
	// 但要确保连接到正确的数据库
	if global.APP_DB != nil {
		// 先尝试使用当前连接检查，如果失败则跳过检查
		systemStatsService := resources.SystemStatsService{}
		hasUsers, err := systemStatsService.CheckUserExists()
		if err == nil && hasUsers {
			c.JSON(http.StatusBadRequest, common.Error("系统已初始化"))
			return
		}
		// 如果检查失败（比如没有选择数据库），继续初始化流程
		if err != nil {
			global.APP_LOG.Debug("检查用户数据失败，继续初始化流程", zap.Error(err))
		}
	}

	// 确保数据库和表结构
	if err := initService.EnsureDatabase(dbConfig); err != nil {
		c.JSON(http.StatusInternalServerError, common.Error("数据库初始化失败: "+err.Error()))
		return
	}

	// Step 1: 创建管理员和用户
	authService := auth.AuthService{}
	adminInfo := auth.UserInfo{
		Username: req.Admin.Username,
		Password: req.Admin.Password,
		Email:    req.Admin.Email,
	}
	userInfo := auth.UserInfo{
		Username: req.User.Username,
		Password: req.User.Password,
		Email:    req.User.Email,
	}
	if err := authService.InitSystemWithUsers(adminInfo, userInfo); err != nil {
		c.JSON(http.StatusInternalServerError, common.Error(err.Error()))
		return
	}

	// Step 2: 初始化系统种子数据
	// 这些操作会自动在各自的短事务中完成
	source.InitSeedData()

	// Step 3: 初始化系统镜像
	source.SeedSystemImages()

	// 创建业务系统初始化标志文件
	initFlagPath := "./storage/.system_initialized"
	initFlagDir := "./storage"

	// 确保目录存在
	if err := os.MkdirAll(initFlagDir, 0755); err != nil {
		global.APP_LOG.Warn("创建storage目录失败", zap.Error(err))
	}

	// 写入业务系统初始化标志文件
	flagContent := "System initialized at: " + time.Now().Format(time.RFC3339)
	if err := os.WriteFile(initFlagPath, []byte(flagContent), 0644); err != nil {
		global.APP_LOG.Warn("创建系统初始化标志文件失败", zap.Error(err))
	} else {
		global.APP_LOG.Info("成功创建系统初始化标志文件", zap.String("path", initFlagPath))
	}

	// 系统初始化完成后，触发完整系统重新初始化
	go func() {
		global.APP_LOG.Info("系统初始化完成，开始完整系统重新初始化")

		// 等待一小段时间确保数据库事务完成
		time.Sleep(2 * time.Second)

		// 调用系统初始化完成回调
		if global.APP_SYSTEM_INIT_CALLBACK != nil {
			global.APP_SYSTEM_INIT_CALLBACK()
		}

		global.APP_LOG.Info("完整系统重新初始化完成")
	}()

	c.JSON(http.StatusOK, common.Success("系统初始化成功"))
}

// GetRegisterConfig 获取注册配置信息
// @Summary 获取注册配置信息
// @Description 获取注册页面所需的配置信息（不需要认证）
// @Tags 系统初始化
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Router /public/register-config [get]
func GetRegisterConfig(c *gin.Context) {
	config := map[string]interface{}{
		"auth": map[string]interface{}{
			"enablePublicRegistration": global.APP_CONFIG.Auth.EnablePublicRegistration,
		},
		"inviteCode": map[string]interface{}{
			"enabled": global.APP_CONFIG.InviteCode.Enabled,
		},
		"oauth2Enabled": global.APP_CONFIG.Auth.EnableOAuth2,
	}
	c.JSON(http.StatusOK, common.Success(config))
}

// GetPublicSystemConfig 获取公开的系统配置信息
// @Summary 获取公开的系统配置信息
// @Description 获取系统公开配置信息（不需要认证），如默认语言等，从内存配置读取
// @Tags 系统配置
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Router /public/system-config [get]
func GetPublicSystemConfig(c *gin.Context) {
	// 从内存配置读取公开的系统配置，避免依赖数据库
	// 这样可以确保在数据库未初始化时前端仍然能正常工作
	result := make(map[string]interface{})

	// 检查数据库是否可用
	dbAvailable := false
	if global.APP_DB != nil {
		sqlDB, err := global.APP_DB.DB()
		if err == nil && sqlDB.Ping() == nil {
			dbAvailable = true
		}
	}

	// 如果数据库可用，尝试从数据库读取配置
	if dbAvailable {
		var configs []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}

		if err := global.APP_DB.Table("system_configs").
			Select("`key`, `value`").
			Where("is_public = ? AND deleted_at IS NULL", true).
			Find(&configs).Error; err == nil {
			global.APP_LOG.Info("从数据库查询到的公开配置数量", zap.Int("count", len(configs)))

			// 转换为map格式并进行字段映射
			for _, config := range configs {
				switch config.Key {
				case "other.default-language":
					result["default_language"] = config.Value
				case "other.max-avatar-size":
					result["max_avatar_size"] = config.Value
				default:
					result[config.Key] = config.Value
				}
			}
		} else {
			global.APP_LOG.Warn("从数据库获取公开系统配置失败，使用默认配置", zap.Error(err))
		}
	} else {
		global.APP_LOG.Info("数据库不可用，使用默认配置")
	}

	// 如果数据库不可用或未配置，提供默认值
	if len(result) == 0 {
		// 从配置中读取默认值
		if global.APP_CONFIG.Other.DefaultLanguage != "" {
			result["default_language"] = global.APP_CONFIG.Other.DefaultLanguage
		} else {
			result["default_language"] = "zh" // 默认中文
		}

		if global.APP_CONFIG.Other.MaxAvatarSize > 0 {
			result["max_avatar_size"] = strconv.FormatFloat(global.APP_CONFIG.Other.MaxAvatarSize, 'f', -1, 64)
		} else {
			result["max_avatar_size"] = "2" // 默认2MB
		}

		global.APP_LOG.Info("使用默认配置",
			zap.String("default_language", result["default_language"].(string)),
			zap.String("max_avatar_size", result["max_avatar_size"].(string)))
	}

	c.JSON(http.StatusOK, common.Success(result))
}

// GetRecommendedDatabaseType 获取推荐的数据库类型
// @Summary 获取推荐的数据库类型
// @Description 根据系统架构获取推荐的数据库类型
// @Tags 系统初始化
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Router /public/recommended-db-type [get]
func GetRecommendedDatabaseType(c *gin.Context) {
	var recommendedType string
	var reason string

	arch := runtime.GOARCH
	if arch == "amd64" {
		recommendedType = "mysql"
		reason = "AMD64架构推荐使用MySQL以获得最佳性能"
	} else {
		recommendedType = "mariadb"
		reason = "ARM64架构推荐使用MariaDB以获得更好的兼容性"
	}

	response := map[string]interface{}{
		"recommendedType": recommendedType,
		"reason":          reason,
		"architecture":    arch,
		"supportedTypes":  []string{"mysql", "mariadb"},
	}

	c.JSON(http.StatusOK, common.Success(response))
}
