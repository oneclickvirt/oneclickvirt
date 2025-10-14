package auth

import (
	"fmt"

	"oneclickvirt/config"
	"oneclickvirt/global"
	configModel "oneclickvirt/model/config"

	"go.uber.org/zap"
)

type ConfigService struct{}

func (s *ConfigService) UpdateConfig(req configModel.UpdateConfigRequest) error {
	global.APP_LOG.Info("开始更新配置")

	// 获取配置管理器
	configManager := config.GetConfigManager()
	if configManager == nil {
		return fmt.Errorf("配置管理器未初始化")
	}

	// 构建配置更新映射
	configUpdates := make(map[string]interface{})

	// 认证配置
	authConfig := map[string]interface{}{
		"enableEmail":              req.Auth.EnableEmail,
		"enableTelegram":           req.Auth.EnableTelegram,
		"enableQQ":                 req.Auth.EnableQQ,
		"enableOAuth2":             req.Auth.EnableOAuth2,
		"enablePublicRegistration": req.Auth.EnablePublicRegistration,
		"emailSMTPHost":            req.Auth.EmailSMTPHost,
		"emailSMTPPort":            req.Auth.EmailSMTPPort,
		"emailUsername":            req.Auth.EmailUsername,
		"emailPassword":            req.Auth.EmailPassword,
		"telegramBotToken":         req.Auth.TelegramBotToken,
		"qqAppID":                  req.Auth.QQAppID,
		"qqAppKey":                 req.Auth.QQAppKey,
	}
	configUpdates["auth"] = authConfig

	// 配额配置
	quotaConfig := map[string]interface{}{
		"defaultLevel": req.Quota.DefaultLevel,
	}

	// 转换等级限制配置
	if req.Quota.LevelLimits != nil {
		levelLimits := make(map[string]interface{})
		for level, modelLimit := range req.Quota.LevelLimits {
			levelKey := fmt.Sprintf("%d", level)

			// 验证资源限制值，不允许为空或0
			if modelLimit.MaxInstances <= 0 {
				return fmt.Errorf("等级 %d 的最大实例数不能为空或小于等于0", level)
			}

			if modelLimit.MaxTraffic <= 0 {
				return fmt.Errorf("等级 %d 的流量限制不能为空或小于等于0", level)
			}

			// 验证 MaxResources
			if modelLimit.MaxResources == nil {
				return fmt.Errorf("等级 %d 的资源配置不能为空", level)
			}

			// 验证各项资源限制
			if cpu, ok := modelLimit.MaxResources["cpu"]; !ok || cpu == nil {
				return fmt.Errorf("等级 %d 的CPU配置不能为空", level)
			} else if cpuVal, ok := cpu.(float64); ok && cpuVal <= 0 {
				return fmt.Errorf("等级 %d 的CPU配置不能小于等于0", level)
			} else if cpuVal, ok := cpu.(int); ok && cpuVal <= 0 {
				return fmt.Errorf("等级 %d 的CPU配置不能小于等于0", level)
			}

			if memory, ok := modelLimit.MaxResources["memory"]; !ok || memory == nil {
				return fmt.Errorf("等级 %d 的内存配置不能为空", level)
			} else if memVal, ok := memory.(float64); ok && memVal <= 0 {
				return fmt.Errorf("等级 %d 的内存配置不能小于等于0", level)
			} else if memVal, ok := memory.(int); ok && memVal <= 0 {
				return fmt.Errorf("等级 %d 的内存配置不能小于等于0", level)
			}

			if disk, ok := modelLimit.MaxResources["disk"]; !ok || disk == nil {
				return fmt.Errorf("等级 %d 的磁盘配置不能为空", level)
			} else if diskVal, ok := disk.(float64); ok && diskVal <= 0 {
				return fmt.Errorf("等级 %d 的磁盘配置不能小于等于0", level)
			} else if diskVal, ok := disk.(int); ok && diskVal <= 0 {
				return fmt.Errorf("等级 %d 的磁盘配置不能小于等于0", level)
			}

			if bandwidth, ok := modelLimit.MaxResources["bandwidth"]; !ok || bandwidth == nil {
				return fmt.Errorf("等级 %d 的带宽配置不能为空", level)
			} else if bwVal, ok := bandwidth.(float64); ok && bwVal <= 0 {
				return fmt.Errorf("等级 %d 的带宽配置不能小于等于0", level)
			} else if bwVal, ok := bandwidth.(int); ok && bwVal <= 0 {
				return fmt.Errorf("等级 %d 的带宽配置不能小于等于0", level)
			}

			levelLimits[levelKey] = map[string]interface{}{
				"maxInstances": modelLimit.MaxInstances,
				"maxResources": modelLimit.MaxResources,
				"maxTraffic":   modelLimit.MaxTraffic,
			}
		}
		quotaConfig["levelLimits"] = levelLimits
	}
	configUpdates["quota"] = quotaConfig

	// 邀请码配置
	inviteCodeConfig := map[string]interface{}{
		"enabled":  req.InviteCode.Enabled,
		"required": req.InviteCode.Required,
	}
	configUpdates["inviteCode"] = inviteCodeConfig

	// 通过配置管理器批量更新配置
	if err := configManager.UpdateConfig(configUpdates); err != nil {
		global.APP_LOG.Error("配置更新失败", zap.Error(err))
		return fmt.Errorf("配置更新失败: %v", err)
	}

	global.APP_LOG.Info("配置更新完成")
	return nil
}

func (s *ConfigService) GetConfig() map[string]interface{} {
	// 获取配置管理器
	configManager := config.GetConfigManager()
	if configManager == nil {
		global.APP_LOG.Error("配置管理器未初始化")
		return map[string]interface{}{}
	}

	// 从配置管理器获取扁平化配置
	flatConfig := configManager.GetAllConfig()
	global.APP_LOG.Info("获取扁平化配置", zap.Int("count", len(flatConfig)))

	// 记录所有auth相关的配置
	for key, value := range flatConfig {
		if len(key) >= 4 && key[:4] == "auth" {
			global.APP_LOG.Info("扁平化配置项", zap.String("key", key), zap.Any("value", value))
		}
	}

	// 将扁平化配置转换为嵌套结构
	result := unflattenConfig(flatConfig)

	// 记录转换后的auth配置
	if auth, exists := result["auth"]; exists {
		global.APP_LOG.Info("转换后的auth配置", zap.Any("auth", auth))
	}

	return result
}

// unflattenConfig 将扁平化配置转换为嵌套结构
// 例如: {"auth.enableEmail": true} => {"auth": {"enableEmail": true}}
func unflattenConfig(flat map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	for key, value := range flat {
		parts := splitKey(key)
		setNestedValue(result, parts, value)
	}

	return result
}

// splitKey 分割配置键
func splitKey(key string) []string {
	parts := []string{}
	current := ""

	for _, char := range key {
		if char == '.' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(char)
		}
	}

	if current != "" {
		parts = append(parts, current)
	}

	return parts
}

// setNestedValue 设置嵌套值
func setNestedValue(m map[string]interface{}, keys []string, value interface{}) {
	if len(keys) == 0 {
		return
	}

	if len(keys) == 1 {
		m[keys[0]] = value
		return
	}

	key := keys[0]
	if _, exists := m[key]; !exists {
		m[key] = make(map[string]interface{})
	}

	if nested, ok := m[key].(map[string]interface{}); ok {
		setNestedValue(nested, keys[1:], value)
	}
}

// SaveInstanceTypePermissions 保存实例类型权限配置
func (s *ConfigService) SaveInstanceTypePermissions(minLevelForContainer, minLevelForVM, minLevelForDeleteContainer, minLevelForDeleteVM, minLevelForResetContainer, minLevelForResetVM int) error {
	global.APP_LOG.Info("更新实例类型权限配置",
		zap.Int("minLevelForContainer", minLevelForContainer),
		zap.Int("minLevelForVM", minLevelForVM),
		zap.Int("minLevelForDeleteContainer", minLevelForDeleteContainer),
		zap.Int("minLevelForDeleteVM", minLevelForDeleteVM),
		zap.Int("minLevelForResetContainer", minLevelForResetContainer),
		zap.Int("minLevelForResetVM", minLevelForResetVM))

	// 获取配置管理器
	configManager := config.GetConfigManager()
	if configManager == nil {
		return fmt.Errorf("配置管理器未初始化")
	}

	// 构建实例类型权限配置 - 使用带连字符的key确保正确写回YAML
	instanceTypePermissions := map[string]interface{}{
		"min-level-for-container":        minLevelForContainer,
		"min-level-for-vm":               minLevelForVM,
		"min-level-for-delete-container": minLevelForDeleteContainer,
		"min-level-for-delete-vm":        minLevelForDeleteVM,
		"min-level-for-reset-container":  minLevelForResetContainer,
		"min-level-for-reset-vm":         minLevelForResetVM,
	}

	// 更新配置
	configUpdates := map[string]interface{}{
		"quota.instance-type-permissions": instanceTypePermissions,
	}

	if err := configManager.UpdateConfig(configUpdates); err != nil {
		global.APP_LOG.Error("保存实例类型权限配置失败", zap.Error(err))
		return fmt.Errorf("保存实例类型权限配置失败: %v", err)
	}

	// 立即同步到全局配置（避免需要重启服务）
	global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer = minLevelForContainer
	global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM = minLevelForVM
	global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteContainer = minLevelForDeleteContainer
	global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteVM = minLevelForDeleteVM
	global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetContainer = minLevelForResetContainer
	global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetVM = minLevelForResetVM

	global.APP_LOG.Info("实例类型权限配置保存成功，已同步到全局配置")
	return nil
}

// UpdateOAuth2Config 更新OAuth2配置
func (s *ConfigService) UpdateOAuth2Config(updates map[string]interface{}) error {
	configManager := config.GetConfigManager()
	if configManager == nil {
		return fmt.Errorf("配置管理器未初始化")
	}

	configUpdates := make(map[string]interface{})
	for key, value := range updates {
		configUpdates[fmt.Sprintf("oauth2.%s", key)] = value
	}

	if err := configManager.UpdateConfig(configUpdates); err != nil {
		global.APP_LOG.Error("更新OAuth2配置失败", zap.Error(err))
		return err
	}

	return nil
}
