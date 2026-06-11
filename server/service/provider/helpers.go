package provider

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// GetProviderInstanceByID 通过ID获取Provider实例（全局统一封装）
// 如果Provider未加载，会尝试从数据库加载并初始化
func GetProviderInstanceByID(providerID uint) (provider.Provider, error) {
	// 获取Provider服务
	providerSvc := GetProviderService()

	// 尝试从内存中获取。若存在但已不健康，必须先清理并重载，
	// 否则后续 GetProviderInstanceByID 调用会拿到 stale provider，表现为
	// 健康检查已恢复但仍提示"Provider不可用/SSH client not initialized"。
	providerInstance, exists := providerSvc.GetProviderByID(providerID)
	if exists {
		if providerInstance.IsConnected() {
			return providerInstance, nil
		}
		global.APP_LOG.Info("Provider内存缓存存在但连接不可用，准备重载",
			zap.Uint("providerID", providerID),
			zap.String("provider", providerInstance.GetName()))
		providerSvc.RemoveProvider(providerID)
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

	// 重新获取Provider实例，并确认已经真实连接；否则不要把不可用缓存返回给调用方。
	providerInstance, exists = providerSvc.GetProviderByID(providerID)
	if !exists {
		return nil, fmt.Errorf("Provider ID %d 加载后仍然不可用", providerID)
	}
	if !providerInstance.IsConnected() {
		providerSvc.RemoveProvider(providerID)
		return nil, fmt.Errorf("Provider ID %d 加载后仍未连接", providerID)
	}

	return providerInstance, nil
}

// EnsureProviderConnected 确保Provider已连接并可用
// 对于 Agent 模式的 Provider：等待 Agent WebSocket 连接就绪（最长 45 秒）
// 对于 SSH 模式的 Provider：尝试重连后立即返回结果
func EnsureProviderConnected(ctx context.Context, providerID uint) (provider.Provider, error) {
	providerInstance, err := GetProviderInstanceByID(providerID)
	if err != nil {
		return nil, err
	}

	if providerInstance.IsConnected() {
		return providerInstance, nil
	}

	providerSvc := GetProviderService()
	if err := providerSvc.ReloadProvider(providerID); err != nil {
		var dbProvider providerModel.Provider
		if dbErr := global.APP_DB.First(&dbProvider, providerID).Error; dbErr != nil {
			return nil, fmt.Errorf("获取Provider信息失败: %w", dbErr)
		}
		if loadErr := providerSvc.LoadProvider(dbProvider); loadErr != nil {
			return nil, fmt.Errorf("重连Provider失败: %w", loadErr)
		}
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// Agent 模式：等待 Agent WebSocket 连接就绪（最长 45 秒，Agent 自身重连间隔通常 10-30 秒）
	// SSH 模式：不等待，立即返回结果（SSH 连接在 LoadProvider 时已同步建立）
	if dbProvider.ConnectionType == "agent" {
		// 先快速检查一次，避免不必要的等待
		if providerInstance, ok := providerSvc.GetProviderByID(providerID); ok && providerInstance.IsConnected() {
			return providerInstance, nil
		}

		deadline := time.Now().Add(45 * time.Second)
		delay := 500 * time.Millisecond
		maxDelay := 5 * time.Second
		firstWarning := true
		for {
			providerInstance, ok := providerSvc.GetProviderByID(providerID)
			if ok && providerInstance.IsConnected() {
				return providerInstance, nil
			}

			if time.Now().After(deadline) {
				break
			}

			if ctx != nil {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
			}

			if firstWarning && time.Now().After(deadline.Add(-35*time.Second)) {
				global.APP_LOG.Warn("等待 Agent 连接就绪中",
					zap.Uint("providerID", providerID),
					zap.Duration("elapsed", 45*time.Second-time.Until(deadline)))
				firstWarning = false
			}

			time.Sleep(delay)
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}

	return nil, fmt.Errorf("Provider ID %d 连接后仍然不可用", providerID)
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
