package public

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/config"
	"oneclickvirt/service/auth"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/system"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	configModel "oneclickvirt/model/config"
	"oneclickvirt/source"
	"oneclickvirt/utils"

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
	initialized, err := system.IsDatabaseInitialized(global.APP_DB)
	if err != nil {
		// A marker only means this installation was initialized before. When the
		// database is temporarily unavailable it prevents an existing deployment
		// from being redirected into the destructive first-run flow.
		markerExists := system.HasSystemInitializedMarker()
		needInit := !markerExists
		message := "数据库未连接，需要初始化"
		if markerExists {
			message = "数据库暂时不可用，系统正在自动重连"
		}
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("检查初始化状态时数据库不可用",
				zap.Bool("markerExists", markerExists),
				zap.Bool("needInit", needInit),
				zap.Error(err))
		}
		common.ResponseSuccess(c, gin.H{
			"needInit": needInit,
			"ready":    false,
			"state":    "database_unavailable",
			"message":  message,
		})
		return
	}

	if initialized {
		if err := system.EnsureSystemInitializedMarker(); err != nil && global.APP_LOG != nil {
			global.APP_LOG.Warn("补全系统初始化标志文件失败", zap.Error(err))
		}
		ready := config.GetConfigManager() != nil &&
			global.CONFIG_MANAGER_READY.Load() &&
			strings.TrimSpace(global.APP_JWT_SECRET) != ""
		state := "ready"
		message := "数据库无需初始化"
		if !ready {
			state = "starting"
			message = "数据库已初始化，系统服务正在启动"
		}
		common.ResponseSuccess(c, gin.H{
			"needInit": false,
			"ready":    ready,
			"state":    state,
			"message":  message,
		})
		return
	}

	if err := system.RemoveSystemInitializedMarker(); err != nil && global.APP_LOG != nil {
		global.APP_LOG.Warn("清理失效的系统初始化标志文件失败", zap.Error(err))
	}
	common.ResponseSuccess(c, gin.H{
		"needInit": true,
		"ready":    false,
		"state":    "needs_initialization",
		"message":  "前往初始化数据库",
	})
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
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	// 支持MySQL和MariaDB
	if req.Type != "mysql" && req.Type != "mariadb" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "仅支持MySQL和MariaDB数据库"))
		return
	}

	// 使用InitService测试连接
	initService := &system.InitService{}

	// 转换端口字符串为整数
	port, err := strconv.Atoi(req.Port)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "端口格式错误"))
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
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	global.APP_LOG.Debug("数据库连接测试成功",
		zap.String("host", req.Host),
		zap.String("port", req.Port),
		zap.String("database", req.Database),
		zap.String("username", req.Username))

	common.ResponseSuccess(c, nil, "数据库连接测试成功")
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
			Username string `json:"username"`
			Password string `json:"password"`
			Email    string `json:"email"`
			Enabled  bool   `json:"enabled"`
		} `json:"user"`
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
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	if err := utils.ValidateUsername(req.Admin.Username); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	if err := utils.ValidateOptionalEmail(req.Admin.Email); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	if req.User.Enabled {
		if err := utils.ValidateUsername(req.User.Username); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
			return
		}
		if err := utils.ValidateOptionalEmail(req.User.Email); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
			return
		}
	}

	// 先检查数据库配置并确保数据库连接
	// 转换端口字符串为整数
	port, err := strconv.Atoi(req.Database.Port)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "数据库端口格式错误"))
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

	// 重置进度追踪（共9步）
	global.APP_INIT_PROGRESS.Reset([]string{
		"验证数据库连接",
		"检查初始化状态",
		"创建数据库结构",
		"创建管理员账户",
		"初始化种子数据",
		"初始化系统镜像",
		"重新连接数据库",
		"注册系统服务",
		"启动调度器",
	})

	// Step 0: 测试数据库连接
	global.APP_INIT_PROGRESS.StartStep(0)
	if err := initService.TestDatabaseConnection(dbConfig); err != nil {
		global.APP_INIT_PROGRESS.FailStep(0, err.Error())
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	global.APP_INIT_PROGRESS.CompleteStep(0)

	// Step 1: 检查是否已初始化
	global.APP_INIT_PROGRESS.StartStep(1)
	if global.APP_DB != nil {
		systemStatsService := resources.SystemStatsService{}
		hasUsers, err := systemStatsService.CheckUserExists()
		if err == nil && hasUsers {
			// 恢复 idle 状态：拒绝重复初始化不属于失败，避免污染全局进度状态
			global.APP_INIT_PROGRESS.Abort()
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, "系统已初始化"))
			return
		}
		if err != nil {
			global.APP_LOG.Debug("检查用户数据失败，继续初始化流程", zap.Error(err))
		}
	}
	global.APP_INIT_PROGRESS.CompleteStep(1)

	// Step 2: 确保数据库和表结构
	global.APP_INIT_PROGRESS.StartStep(2)
	if err := initService.EnsureDatabase(dbConfig); err != nil {
		global.APP_INIT_PROGRESS.FailStep(2, err.Error())
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	// The login endpoint remains reachable while the asynchronous post-init
	// callback starts schedulers and other services. Load the persistent signing
	// key before creating users so the first token cannot be invalidated by that
	// callback switching away from the startup fallback key.
	if _, err := system.InitializePersistentJWTSecret(global.APP_DB); err != nil {
		global.APP_INIT_PROGRESS.FailStep(2, fmt.Sprintf("JWT密钥初始化失败: %v", err))
		common.ResponseWithError(c, common.NewError(common.CodeDatabaseError, "JWT密钥初始化失败，请稍后重试"))
		return
	}
	global.APP_INIT_PROGRESS.CompleteStep(2)

	// Step 3: 创建管理员和用户
	global.APP_INIT_PROGRESS.StartStep(3)
	authService := auth.AuthService{}
	adminInfo := auth.UserInfo{
		Username: req.Admin.Username,
		Password: req.Admin.Password,
		Email:    req.Admin.Email,
	}
	var userInfoPtr *auth.UserInfo
	if req.User.Enabled && req.User.Username != "" && req.User.Password != "" && req.User.Email != "" {
		userInfoPtr = &auth.UserInfo{
			Username: req.User.Username,
			Password: req.User.Password,
			Email:    req.User.Email,
			Enabled:  true,
		}
	}
	if err := authService.InitSystemWithUsers(adminInfo, userInfoPtr); err != nil {
		global.APP_INIT_PROGRESS.FailStep(3, err.Error())
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	global.APP_INIT_PROGRESS.CompleteStep(3)

	// Step 4: 初始化系统种子数据
	global.APP_INIT_PROGRESS.StartStep(4)
	source.InitSeedData()
	global.APP_INIT_PROGRESS.CompleteStep(4)

	// Step 5: 初始化系统镜像与本机QEMU节点
	global.APP_INIT_PROGRESS.StartStep(5)
	source.SeedSystemImages()
	source.SeedLocalQEMUProviderIfAvailable()
	global.APP_INIT_PROGRESS.CompleteStep(5)

	// 标志文件仅用于识别曾经初始化过的部署；数据库仍是状态真值来源。
	if err := system.EnsureSystemInitializedMarker(); err != nil {
		global.APP_LOG.Warn("创建系统初始化标志文件失败", zap.Error(err))
	} else {
		global.APP_LOG.Info("成功创建系统初始化标志文件", zap.String("path", system.SystemInitializedFlagPath))
	}

	// 系统初始化完成后，触发完整系统重新初始化（异步，进度由回调内部更新）
	go func() {
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("完整系统重新初始化发生panic",
					zap.Any("panic", r))
				global.APP_INIT_PROGRESS.FailStep(-1, "系统重新初始化发生内部错误")
			}
		}()

		global.APP_LOG.Info("系统初始化完成，开始完整系统重新初始化")
		// 等待一小段时间确保数据库事务完成
		time.Sleep(1 * time.Second)
		if global.APP_SYSTEM_INIT_CALLBACK != nil {
			global.APP_SYSTEM_INIT_CALLBACK()
		}
		global.APP_LOG.Info("完整系统重新初始化完成")
	}()

	common.ResponseSuccess(c, nil, "系统初始化成功")
}

// GetInitProgress 获取系统初始化进度
// @Summary 获取系统初始化进度
// @Description 轮询此接口获取系统初始化的实时进度，status 为 success 时初始化完成
// @Tags 系统初始化
// @Produce json
// @Success 200 {object} common.Response "进度信息"
// @Router /public/init-progress [get]
func GetInitProgress(c *gin.Context) {
	common.ResponseSuccess(c, global.APP_INIT_PROGRESS.Snapshot())
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
	captchaEnabled := global.GetAppConfig().Captcha.Enabled
	registrationEnabled := global.GetAppConfig().Auth.EnablePublicRegistration
	inviteCodeEnabled := global.GetAppConfig().InviteCode.Enabled
	oauth2Enabled := global.GetAppConfig().Auth.EnableOAuth2
	kycEnabled := global.GetAppConfig().Auth.EnableKYC
	kycMethod := global.GetAppConfig().KYC.Method
	domainEnabled := global.GetAppConfig().Auth.EnableDomain
	checkinEnabled := global.GetAppConfig().Auth.EnableCheckin

	if configManager := config.GetConfigManager(); configManager != nil {
		if value, exists := configManager.GetConfig("captcha.enabled"); exists {
			if enabled, ok := value.(bool); ok {
				captchaEnabled = enabled
			}
		}
		if value, exists := configManager.GetConfig("auth.enable-public-registration"); exists {
			if enabled, ok := value.(bool); ok {
				registrationEnabled = enabled
			}
		}
		if value, exists := configManager.GetConfig("invite-code.enabled"); exists {
			if enabled, ok := value.(bool); ok {
				inviteCodeEnabled = enabled
			}
		}
		if value, exists := configManager.GetConfig("auth.enable-oauth2"); exists {
			if enabled, ok := value.(bool); ok {
				oauth2Enabled = enabled
			}
		}
		if value, exists := configManager.GetConfig("auth.enable-kyc"); exists {
			if enabled, ok := value.(bool); ok {
				kycEnabled = enabled
			}
		}
		if value, exists := configManager.GetConfig("kyc.method"); exists {
			if method, ok := value.(string); ok {
				kycMethod = method
			}
		}
		if value, exists := configManager.GetConfig("auth.enable-domain"); exists {
			if enabled, ok := value.(bool); ok {
				domainEnabled = enabled
			}
		}
		if value, exists := configManager.GetConfig("auth.enable-checkin"); exists {
			if enabled, ok := value.(bool); ok {
				checkinEnabled = enabled
			}
		}
	}

	config := map[string]interface{}{
		"auth": map[string]interface{}{
			"enablePublicRegistration": registrationEnabled,
		},
		"inviteCode": map[string]interface{}{
			"enabled": inviteCodeEnabled,
		},
		"oauth2Enabled":  oauth2Enabled,
		"kycEnabled":     kycEnabled,
		"kycMethod":      kycMethod,
		"domainEnabled":  domainEnabled,
		"checkinEnabled": checkinEnabled,
		"captchaEnabled": captchaEnabled,
	}
	common.ResponseSuccess(c, config)
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
			global.APP_LOG.Debug("从数据库查询到的公开配置数量", zap.Int("count", len(configs)))

			// 转换为map格式并进行字段映射
			for _, config := range configs {
				switch config.Key {
				case "other.default-language":
					result["default_language"] = config.Value
				case "other.logo-url":
					result["logo_url"] = config.Value
				case "other.site-name":
					result["site_name"] = config.Value
				default:
					result[config.Key] = config.Value
				}
			}
		} else {
			global.APP_LOG.Warn("从数据库获取公开系统配置失败，使用默认配置", zap.Error(err))
		}
	} else {
		global.APP_LOG.Debug("数据库不可用，使用默认配置")
	}

	// 如果数据库不可用或未配置，提供默认值
	if len(result) == 0 {
		// 从配置中读取默认值
		if global.GetAppConfig().Other.DefaultLanguage != "" {
			result["default_language"] = global.GetAppConfig().Other.DefaultLanguage
		} else {
			result["default_language"] = "zh" // 默认中文
		}

		if global.GetAppConfig().Other.LogoURL != "" {
			result["logo_url"] = global.GetAppConfig().Other.LogoURL
		}

		if global.GetAppConfig().Other.SiteName != "" {
			result["site_name"] = global.GetAppConfig().Other.SiteName
		}

		global.APP_LOG.Debug("使用默认配置",
			zap.String("default_language", result["default_language"].(string)))
	}

	common.ResponseSuccess(c, result)
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

	common.ResponseSuccess(c, response)
}
