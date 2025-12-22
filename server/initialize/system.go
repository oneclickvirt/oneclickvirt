package initialize

import (
	"context"
	"time"

	"oneclickvirt/core"
	"oneclickvirt/global"
	"oneclickvirt/service/auth"
	"oneclickvirt/service/lifecycle"
	"oneclickvirt/service/log"
	"oneclickvirt/service/pmacct"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/scheduler"
	"oneclickvirt/service/storage"
	"oneclickvirt/service/system"
	"oneclickvirt/service/task"
	userProviderService "oneclickvirt/service/user/provider"
	"oneclickvirt/utils"

	// 导入端口映射 providers 以触发其 init() 函数进行注册
	_ "oneclickvirt/provider/portmapping/docker"
	_ "oneclickvirt/provider/portmapping/incus"
	_ "oneclickvirt/provider/portmapping/iptables"
	_ "oneclickvirt/provider/portmapping/lxd"

	"go.uber.org/zap"
)

// InitializeSystem 初始化系统基础组件
func InitializeSystem() {
	// 初始化核心组件
	global.APP_VP = core.Viper()
	global.APP_LOG = core.Zap()
	zap.ReplaceGlobals(global.APP_LOG)

	global.APP_LOG.Info("系统初始化开始")
	global.APP_LOG.Info("系统配置加载完成",
		zap.String("env", global.APP_CONFIG.System.Env),
		zap.Int("addr", global.APP_CONFIG.System.Addr),
		zap.Bool("captcha_enabled", global.APP_CONFIG.Captcha.Enabled),
	)

	// 创建系统级别的关闭上下文
	global.APP_SHUTDOWN_CONTEXT, global.APP_SHUTDOWN_CANCEL = context.WithCancel(context.Background())

	// 启动日志采样器清理任务（现在 APP_SHUTDOWN_CONTEXT 已初始化）
	core.StartSamplerCleanup(global.APP_SHUTDOWN_CONTEXT)

	// 启动日志速率限制器清理任务
	logRateLimiter := utils.GetLogRateLimiter()
	logRateLimiter.StartCleanupTask(global.APP_SHUTDOWN_CONTEXT)

	// 初始化全局SSH连接池
	sshPool := utils.InitGlobalSSHPool(global.APP_LOG)
	global.APP_SSH_POOL = sshPool

	// 初始化 HTTP Client Manager（启动定期清理）
	httpManager := utils.GetHTTPClientManager()
	global.APP_LOG.Debug("HTTP Client Manager已初始化")
	_ = httpManager // 避免未使用警告

	// 初始化LRU验证码缓存
	global.APP_CAPTCHA_STORE = utils.NewLRUCaptchaCache(utils.MaxCaptchaItems)
	global.APP_LOG.Debug("LRU验证码缓存初始化完成", zap.Int("capacity", utils.MaxCaptchaItems))

	// 初始化存储目录结构
	initializeStorage()

	// 启动日志轮转定时任务
	initializeLogRotation()

	// 尝试连接数据库，但不强制要求成功
	global.APP_DB = Gorm()
	isSystemInitialized := CheckSystemInitialized()

	if isSystemInitialized {
		// 系统已初始化，执行完整的初始化流程
		InitializeFullSystem()
		global.APP_LOG.Info("系统完整初始化完成")
	} else {
		global.APP_LOG.Warn("系统未初始化，运行在待初始化模式")
		global.APP_LOG.Info("请访问前端初始化页面完成系统初始化")
	}
}

// initializeStorage 初始化存储目录结构
func initializeStorage() {
	storageService := storage.GetStorageService()
	if err := storageService.InitializeStorage(); err != nil {
		global.APP_LOG.Error("存储目录初始化失败", zap.Error(err))
		// 不要panic，让应用继续运行，但记录错误
	} else {
		global.APP_LOG.Debug("存储目录初始化完成")
	}
}

// initializeLogRotation 初始化日志轮转定时任务
func initializeLogRotation() {
	if global.APP_CONFIG.Zap.RetentionDay > 0 {
		logRotationService := log.GetLogRotationService()

		// 设置cleanup coordinator的上下文
		log.GetCleanupCoordinator().SetContext(global.APP_SHUTDOWN_CONTEXT)

		// 启动定时清理任务（每天凌晨1点执行，避开零点轮换高峰），支持优雅退出
		go func() {
			defer func() {
				if r := recover(); r != nil {
					global.APP_LOG.Error("日志轮转任务panic", zap.Any("panic", r))
				}
			}()

			for {
				now := time.Now()
				// 计算到下一个凌晨1点的时间（避开零点的rotate高峰）
				next := time.Date(now.Year(), now.Month(), now.Day(), 1, 0, 0, 0, now.Location())
				if now.After(next) {
					next = next.Add(24 * time.Hour)
				}

				duration := next.Sub(now)
				global.APP_LOG.Info("日志清理任务已安排",
					zap.Time("nextRun", next),
					zap.Duration("duration", duration))

				// 使用可取消的定时器等待
				timer := time.NewTimer(duration)
				select {
				case <-timer.C:
					// 执行日志清理（通过CleanupOldLogs，它内部也使用coordinator）
					global.APP_LOG.Info("开始执行定时日志清理任务")
					if err := logRotationService.CleanupOldLogs(); err != nil {
						global.APP_LOG.Error("定时日志清理失败", zap.Error(err))
					} else {
						global.APP_LOG.Info("定时日志清理完成")
					}
				case <-global.APP_SHUTDOWN_CONTEXT.Done():
					// 系统关闭，停止日志轮转任务
					timer.Stop()
					global.APP_LOG.Info("日志轮转任务已停止")
					return
				}
				// timer已经被使用或停止，无需再次停止
			}
		}()
	}
}

// CheckSystemInitialized 检查系统是否已经初始化
func CheckSystemInitialized() bool {
	if global.APP_DB == nil {
		global.APP_LOG.Debug("数据库连接不存在，系统未初始化")
		return false
	}

	// 验证数据库连接
	sqlDB, err := global.APP_DB.DB()
	if err != nil {
		global.APP_LOG.Debug("获取数据库连接失败", zap.Error(err))
		return false
	}

	if err := sqlDB.Ping(); err != nil {
		global.APP_LOG.Debug("数据库连接测试失败", zap.Error(err))
		return false
	}

	// 检查users表是否存在
	if !global.APP_DB.Migrator().HasTable("users") {
		global.APP_LOG.Debug("users表不存在，系统未初始化")
		return false
	}

	// 检查是否有用户数据（作为初始化完成的标志）
	// 使用defer+recover防止可能的panic
	var userCount int64
	func() {
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Warn("查询用户表时发生panic", zap.Any("panic", r))
				userCount = 0
			}
		}()

		err = global.APP_DB.Table("users").Count(&userCount).Error
	}()

	if err != nil {
		// 如果表不存在或查询失败，说明未初始化
		global.APP_LOG.Debug("查询用户表失败，系统未初始化", zap.Error(err))
		return false
	}

	if userCount == 0 {
		global.APP_LOG.Debug("用户表为空，系统未初始化")
		return false
	}

	global.APP_LOG.Debug("系统已初始化", zap.Int64("userCount", userCount))
	return true
}

// InitializeFullSystem 执行完整的系统初始化（仅在系统已初始化时调用）
func InitializeFullSystem() {
	// 确保数据库连接存在
	if global.APP_DB == nil {
		global.APP_LOG.Warn("数据库未连接，跳过完整系统初始化")
		return
	}

	// 注册数据库表（确保表结构是最新的）
	// 注意：这里不会重复迁移，只是确保表结构完整
	global.APP_LOG.Debug("确保数据库表结构完整")
	RegisterTables(global.APP_DB)

	InitializeConfigManager()
	global.APP_LOG.Debug("数据库连接和表注册完成")

	// 初始化JWT密钥（从数据库加载或生成新密钥）
	initializeJWTSecret()

	// 初始化JWT密钥管理服务
	initializeJWTService()

	// Provider服务现在采用按需连接，不再预加载
	global.APP_LOG.Debug("Provider服务配置为按需连接模式")

	// 初始化调度器服务
	initializeSchedulers()
}

// initializeJWTSecret 初始化JWT密钥（从数据库持久化加载）
func initializeJWTSecret() {
	jwtSecretService := system.GetJWTSecretService()
	if err := jwtSecretService.InitializeJWTSecret(global.APP_DB); err != nil {
		global.APP_LOG.Error("JWT密钥初始化失败", zap.Error(err))
		// 使用配置文件中的默认密钥作为后备
		global.APP_JWT_SECRET = global.APP_CONFIG.JWT.SigningKey
	} else {
		// 更新全局变量
		global.APP_JWT_SECRET = jwtSecretService.GetSecretKey()
		global.APP_LOG.Info("JWT密钥初始化成功")
	}
}

// initializeJWTService 初始化JWT密钥管理服务
func initializeJWTService() {
	jwtService := &auth.JWTKeyService{}
	if err := jwtService.InitializeJWTKeys(); err != nil {
		global.APP_LOG.Error("JWT密钥服务初始化失败", zap.Error(err))
	} else {
		global.APP_LOG.Debug("JWT密钥管理服务初始化完成")
	}
}

// syncProvidersDataOnStartup 启动时同步Provider层面的数据
func syncProvidersDataOnStartup() {
	global.APP_LOG.Info("开始同步Provider层面的数据（资源和流量统计）")

	// 获取所有Provider
	var providers []struct {
		ID   uint
		Name string
	}

	if err := global.APP_DB.Model(&struct {
		ID   uint   `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}{}).Table("providers").
		Where("status IN (?)", []string{"active", "partial"}).
		Select("id, name").
		Find(&providers).Error; err != nil {
		global.APP_LOG.Error("获取Provider列表失败", zap.Error(err))
		return
	}

	if len(providers) == 0 {
		global.APP_LOG.Debug("没有需要同步的Provider")
		return
	}

	global.APP_LOG.Info("找到需要同步的Provider", zap.Int("count", len(providers)))

	// 同步每个Provider的数据
	successCount := 0
	resourceService := &resources.ResourceService{}

	for _, prov := range providers {
		// 1. 同步资源使用情况（基于数据库中的实例记录）
		if err := resourceService.SyncProviderResources(prov.ID); err != nil {
			global.APP_LOG.Warn("同步Provider资源失败",
				zap.Uint("providerID", prov.ID),
				zap.String("providerName", prov.Name),
				zap.Error(err))
		} else {
			global.APP_LOG.Debug("Provider资源同步成功",
				zap.Uint("providerID", prov.ID),
				zap.String("providerName", prov.Name))
		}

		// 流量数据从pmacct_traffic_records实时查询，无需同步

		successCount++
	}

	global.APP_LOG.Info("Provider数据同步完成",
		zap.Int("total", len(providers)),
		zap.Int("success", successCount))
}

// initializeSchedulers 初始化调度器服务
func initializeSchedulers() {
	lifecycleMgr := lifecycle.GetManager()

	// 初始化任务服务（只有在数据库已初始化时才创建）
	taskService := task.GetTaskService()
	// 设置全局任务服务实例，避免循环依赖
	userProviderService.SetGlobalTaskService(taskService)
	// 注册任务服务到生命周期管理器
	lifecycleMgr.Register("TaskService", taskService)

	// 启动前先同步Provider层面的数据（资源和流量统计）
	syncProvidersDataOnStartup()

	// 启动调度器服务
	schedulerService := scheduler.NewSchedulerService(taskService)
	global.APP_SCHEDULER = schedulerService
	schedulerService.StartScheduler()
	lifecycleMgr.Register("SchedulerService", schedulerService)

	// 启动监控调度器（使用全局shutdown context确保可以正确关闭）
	pmacctService := pmacct.NewService()
	monitoringSchedulerService := scheduler.NewMonitoringSchedulerService(pmacctService)
	global.APP_MONITORING_SCHEDULER = monitoringSchedulerService
	monitoringSchedulerService.Start(global.APP_SHUTDOWN_CONTEXT)
	lifecycleMgr.Register("MonitoringScheduler", monitoringSchedulerService)

	// 启动Provider健康检查调度器（使用全局shutdown context确保可以正确关闭）
	providerHealthSchedulerService := scheduler.NewProviderHealthSchedulerService()
	global.APP_PROVIDER_HEALTH_SCHEDULER = providerHealthSchedulerService
	providerHealthSchedulerService.Start(global.APP_SHUTDOWN_CONTEXT)
	lifecycleMgr.Register("ProviderHealthScheduler", providerHealthSchedulerService)

	// 注册pmacct批处理器
	pmacctBatchProcessor := pmacct.GetBatchProcessor()
	lifecycleMgr.Register("PmacctBatchProcessor", pmacctBatchProcessor)

	// 注册全局SSH连接池
	sshPool := utils.GetGlobalSSHPool()
	lifecycleMgr.Register("GlobalSSHPool", sshPool)

	// 注册验证码缓存（如果已创建）
	// 这里先获取缓存实例
	type CaptchaCacheCloser interface {
		Stop()
	}
	// 实际上MemoryCaptchaCache需要通过某种方式暴露出来，暂时跳过
	// 后续可以添加全局captcha cache变量

	// 注册资源预留服务
	reservationService := resources.GetResourceReservationService()
	lifecycleMgr.Register("ResourceReservationService", reservationService)

	// 注册JWT黑名单服务
	jwtBlacklistService := auth.GetJWTBlacklistService()
	lifecycleMgr.Register("JWTBlacklistService", jwtBlacklistService)

	// 注册验证码缓存
	if global.APP_CAPTCHA_STORE != nil {
		lifecycleMgr.Register("CaptchaCache", global.APP_CAPTCHA_STORE)
	}

	// 注册HTTP客户端（用于关闭空闲连接）
	httpClientManager := utils.GetHTTPClientManager()
	lifecycleMgr.Register("HTTPClientManager", httpClientManager)

	// 注册日志速率限制器
	logRateLimiter := utils.GetLogRateLimiter()
	lifecycleMgr.Register("LogRateLimiter", logRateLimiter)

	// 注册日志轮转服务
	logRotationService := log.GetLogRotationService()
	lifecycleMgr.Register("LogRotationService", logRotationService)

	global.APP_LOG.Info("所有调度器和全局服务已启动并注册到生命周期管理器")
}

// InitializePostSystemInit 系统初始化完成后的完整初始化
func InitializePostSystemInit() {
	// 重新初始化数据库连接（确保使用最新配置）
	global.APP_DB = Gorm()
	if global.APP_DB == nil {
		global.APP_LOG.Error("系统初始化完成后重新连接数据库失败")
		return
	}

	// 注册数据库表
	RegisterTables(global.APP_DB)

	// 重新初始化配置管理器
	ReInitializeConfigManager()
	global.APP_LOG.Debug("数据库连接、表注册和配置管理器重新初始化完成")

	// 初始化JWT密钥管理服务
	initializeJWTService()

	// 初始化任务服务（如果还未初始化）
	if global.APP_SCHEDULER == nil {
		initializeSchedulers()
	}
}

// SetSystemInitCallback 设置系统初始化完成后的回调函数
func SetSystemInitCallback() {
	global.APP_SYSTEM_INIT_CALLBACK = func() {
		global.APP_LOG.Info("执行系统初始化完成后的完整初始化")
		InitializePostSystemInit()
	}
}
