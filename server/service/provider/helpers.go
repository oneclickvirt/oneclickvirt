package provider

import (
	"context"
	"fmt"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"gorm.io/gorm"
)

// GetProviderInstanceByID 通过ID获取Provider实例（全局统一封装）
// 如果Provider未加载，会尝试从数据库加载并初始化
func GetProviderInstanceByID(providerID uint) (provider.Provider, error) {
	// 获取Provider服务
	providerSvc := GetProviderService()

	// 尝试从内存中获取
	providerInstance, exists := providerSvc.GetProviderByID(providerID)
	if exists {
		return providerInstance, nil
	}

	// 从数据库获取Provider信息
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("Provider ID %d 不存在", providerID)
		}
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 尝试加载Provider
	if err := providerSvc.LoadProvider(dbProvider); err != nil {
		return nil, fmt.Errorf("加载Provider失败: %w", err)
	}

	// 重新获取Provider实例
	providerInstance, exists = providerSvc.GetProviderByID(providerID)
	if !exists {
		return nil, fmt.Errorf("Provider ID %d 加载后仍然不可用", providerID)
	}

	return providerInstance, nil
}

// EnsureProviderConnected 确保Provider已连接并可用
func EnsureProviderConnected(ctx context.Context, providerID uint) (provider.Provider, error) {
	providerInstance, err := GetProviderInstanceByID(providerID)
	if err != nil {
		return nil, err
	}

	// 可以在这里添加健康检查逻辑
	// 例如：调用 providerInstance 的某个方法验证连接

	return providerInstance, nil
}

// GetProviderWithDatabase 获取Provider实例和数据库记录
func GetProviderWithDatabase(providerID uint) (provider.Provider, *providerModel.Provider, error) {
	// 从数据库获取Provider信息
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil, fmt.Errorf("Provider ID %d 不存在", providerID)
		}
		return nil, nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 获取Provider实例
	providerInstance, err := GetProviderInstanceByID(providerID)
	if err != nil {
		return nil, &dbProvider, err
	}

	return providerInstance, &dbProvider, nil
}
