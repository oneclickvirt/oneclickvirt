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
	cfg.Quota.LevelLimits = config.DefaultLevelLimits()

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
