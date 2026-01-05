package task

import (
	"fmt"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
)

// CheckInstanceFrozenStatus 检查实例是否被冻结
// 如果实例被冻结，根据操作类型决定是否允许操作
func CheckInstanceFrozenStatus(instanceID uint, operationType string) error {
	var instance providerModel.Instance
	if err := global.APP_DB.Select("id, is_frozen, frozen_reason").First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("获取实例状态失败: %v", err)
	}

	// 如果实例被冻结
	if instance.IsFrozen {
		// 删除操作允许对冻结的实例进行
		if operationType == "delete" {
			global.APP_LOG.Info("允许对冻结的实例执行删除操作",
				zap.Uint("instance_id", instanceID),
				zap.String("frozen_reason", instance.FrozenReason))
			return nil
		}

		// 其他操作不允许对冻结的实例进行
		global.APP_LOG.Warn("实例已冻结，拒绝操作",
			zap.Uint("instance_id", instanceID),
			zap.String("operation_type", operationType),
			zap.String("frozen_reason", instance.FrozenReason))
		return fmt.Errorf("实例已冻结，无法进行%s操作", operationType)
	}

	return nil
}

// CheckProviderFrozenStatus 检查Provider是否被冻结
// 如果Provider被冻结，根据操作类型决定是否允许操作
func CheckProviderFrozenStatus(providerID uint, operationType string) error {
	var provider providerModel.Provider
	if err := global.APP_DB.Select("id, is_frozen, frozen_reason").First(&provider, providerID).Error; err != nil {
		return fmt.Errorf("获取Provider状态失败: %v", err)
	}

	// 如果Provider被冻结
	if provider.IsFrozen {
		// 删除操作允许对冻结的Provider进行
		if operationType == "delete" {
			global.APP_LOG.Info("允许对冻结的Provider执行删除操作",
				zap.Uint("provider_id", providerID),
				zap.String("frozen_reason", provider.FrozenReason))
			return nil
		}

		// 其他操作不允许对冻结的Provider进行
		global.APP_LOG.Warn("Provider已冻结，拒绝操作",
			zap.Uint("provider_id", providerID),
			zap.String("operation_type", operationType),
			zap.String("frozen_reason", provider.FrozenReason))
		return fmt.Errorf("Provider已冻结，无法进行%s操作", operationType)
	}

	return nil
}
