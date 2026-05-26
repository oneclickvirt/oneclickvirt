package source

import (
	"oneclickvirt/config"
	"oneclickvirt/global"

	"go.uber.org/zap"
)

func initLevelConfigurations() {
	global.APP_LOG.Info("开始初始化等级与带宽配置")

	// 检查配置是否已经初始化
	if len(global.GetAppConfig().Quota.LevelLimits) > 0 {
		global.APP_LOG.Debug("等级配置已存在，跳过初始化")
		return
	}

	// 创建默认的等级配置（如果配置为空）—— copy-on-write 安全
	cfg := global.GetAppConfig()
	if cfg.Quota.LevelLimits == nil {
		cfg.Quota.LevelLimits = make(map[int]config.LevelLimitInfo)
	}

	// 设置默认等级配置（操作本地副本，最后原子写入）
	// 等级1: 最低档次
	cfg.Quota.LevelLimits[1] = config.LevelLimitInfo{
		MaxInstances: 1,
		MaxResources: map[string]interface{}{
			"cpu":       1,
			"memory":    350,  // 350MB
			"disk":      1024, // 1GB
			"bandwidth": 100,  // 100Mbps
		},
		MaxTraffic: 102400, // 100GB
		ExpiryDays: 0,      // 0表示不过期
	}

	// 等级2: 中级档次
	cfg.Quota.LevelLimits[2] = config.LevelLimitInfo{
		MaxInstances: 3,
		MaxResources: map[string]interface{}{
			"cpu":       2,
			"memory":    1024,  // 1GB
			"disk":      20480, // 20GB
			"bandwidth": 200,   // 200Mbps
		},
		MaxTraffic: 204800, // 200GB
		ExpiryDays: 0,      // 0表示不过期
	}

	// 等级3: 高级档次
	cfg.Quota.LevelLimits[3] = config.LevelLimitInfo{
		MaxInstances: 5,
		MaxResources: map[string]interface{}{
			"cpu":       4,
			"memory":    2048,  // 2GB
			"disk":      40960, // 40GB
			"bandwidth": 500,   // 500Mbps
		},
		MaxTraffic: 307200, // 300GB
		ExpiryDays: 0,      // 0表示不过期
	}

	// 等级4: 超级档次
	cfg.Quota.LevelLimits[4] = config.LevelLimitInfo{
		MaxInstances: 10,
		MaxResources: map[string]interface{}{
			"cpu":       8,
			"memory":    4096,  // 4GB
			"disk":      81920, // 80GB
			"bandwidth": 1000,  // 1000Mbps
		},
		MaxTraffic: 409600, // 400GB
		ExpiryDays: 0,      // 0表示不过期
	}

	// 等级5: 管理员档次
	cfg.Quota.LevelLimits[5] = config.LevelLimitInfo{
		MaxInstances: 20,
		MaxResources: map[string]interface{}{
			"cpu":       16,
			"memory":    8192,   // 8GB
			"disk":      163840, // 160GB
			"bandwidth": 2000,   // 2000Mbps
		},
		MaxTraffic: 512000, // 500GB
		ExpiryDays: 0,      // 0表示不过期
	}

	global.SetAppConfig(cfg) // 原子写入
	global.APP_LOG.Info("等级与带宽配置初始化完成")

	// 初始化实例类型权限配置
	initInstanceTypePermissions()
}

// initInstanceTypePermissions 初始化实例类型权限配置
func initInstanceTypePermissions() {
	global.APP_LOG.Info("开始初始化实例类型权限配置")

	// 检查配置是否已经设置
	permissions := global.GetAppConfig().Quota.InstanceTypePermissions
	if permissions.MinLevelForContainer != 0 || permissions.MinLevelForVM != 0 ||
		permissions.MinLevelForDeleteContainer != 0 || permissions.MinLevelForDeleteVM != 0 ||
		permissions.MinLevelForResetContainer != 0 || permissions.MinLevelForResetVM != 0 {
		global.APP_LOG.Debug("实例类型权限配置已存在，跳过初始化")
		return
	}

	// copy-on-write 写入
	cfg := global.GetAppConfig()
	cfg.Quota.InstanceTypePermissions = config.InstanceTypePermissions{
		MinLevelForContainer:       1, // 所有等级用户都可以创建容器
		MinLevelForVM:              3, // 等级3及以上可以创建虚拟机
		MinLevelForDeleteContainer: 1, // 等级1及以上可以删除容器
		MinLevelForDeleteVM:        2, // 等级2及以上可以删除虚拟机
		MinLevelForResetContainer:  1, // 等级1及以上可以重置容器系统
		MinLevelForResetVM:         2, // 等级2及以上可以重置虚拟机系统
	}
	global.SetAppConfig(cfg)

	p := cfg.Quota.InstanceTypePermissions
	global.APP_LOG.Info("实例类型权限配置初始化完成",
		zap.Int("minLevelForContainer", p.MinLevelForContainer),
		zap.Int("minLevelForVM", p.MinLevelForVM),
		zap.Int("minLevelForDeleteContainer", p.MinLevelForDeleteContainer),
		zap.Int("minLevelForDeleteVM", p.MinLevelForDeleteVM),
		zap.Int("minLevelForResetContainer", p.MinLevelForResetContainer),
		zap.Int("minLevelForResetVM", p.MinLevelForResetVM))
}
