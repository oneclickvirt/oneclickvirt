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
	return getProviderInstanceByIDWithOptions(context.Background(), providerID, false)
}

// GetProviderInstanceByIDForRecovery is intentionally narrow: callers that
// have already proved a Provider was health-auto-frozen may reconnect it long
// enough to perform one authoritative recovery discovery.  Normal user and
// administrator operations must continue to use GetProviderInstanceByID, which
// rejects frozen Providers.
func GetProviderInstanceByIDForRecovery(providerID uint) (provider.Provider, error) {
	return GetProviderInstanceByIDForRecoveryContext(context.Background(), providerID)
}

// GetProviderInstanceByIDForRecoveryContext is the cancellable variant used by
// reboot recovery. It preserves the narrow auto-frozen authorization while
// ensuring a timed-out recovery cannot start a fresh connection afterwards.
func GetProviderInstanceByIDForRecoveryContext(ctx context.Context, providerID uint) (provider.Provider, error) {
	return getProviderInstanceByIDWithOptions(ctx, providerID, true)
}

func getProviderInstanceByIDWithOptions(ctx context.Context, providerID uint, allowFrozen bool) (provider.Provider, error) {
	if global.APP_DB == nil {
		return nil, fmt.Errorf("数据库连接不可用")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
		providerSvc.RemoveProviderContext(ctx, providerID)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	// 从数据库获取Provider信息
	var dbProvider providerModel.Provider
	if err := global.APP_DB.WithContext(ctx).First(&dbProvider, providerID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("Provider ID %d 不存在", providerID)
		}
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 尝试加载Provider
	if err := providerSvc.LoadProviderWithOptionsContext(ctx, dbProvider, allowFrozen); err != nil {
		return nil, fmt.Errorf("加载Provider失败: %w", err)
	}

	// 重新获取Provider实例，并确认已经真实连接；否则不要把不可用缓存返回给调用方。
	providerInstance, exists = providerSvc.GetProviderByID(providerID)
	if !exists {
		return nil, fmt.Errorf("Provider ID %d 加载后仍然不可用", providerID)
	}
	if !providerInstance.IsConnected() {
		providerSvc.RemoveProviderContext(ctx, providerID)
		return nil, fmt.Errorf("Provider ID %d 加载后仍未连接", providerID)
	}

	return providerInstance, nil
}

// EnsureProviderConnected 确保Provider已连接并可用
// 对于 Agent 模式的 Provider：等待 Agent WebSocket 连接就绪（最长 45 秒）
// 对于 SSH 模式的 Provider：尝试重连后立即返回结果
func EnsureProviderConnected(ctx context.Context, providerID uint) (provider.Provider, error) {
	return ensureProviderConnectedWithOptions(ctx, providerID, false)
}

// EnsureProviderConnectedForRecovery is used only after the recovery
// scheduler atomically claimed an automatically frozen Provider.  It does not
// make manually frozen Providers eligible; that authorization is enforced by
// the scheduler before this function is called.
func EnsureProviderConnectedForRecovery(ctx context.Context, providerID uint) (provider.Provider, error) {
	return ensureProviderConnectedWithOptions(ctx, providerID, true)
}

func ensureProviderConnectedWithOptions(ctx context.Context, providerID uint, allowFrozen bool) (provider.Provider, error) {
	if global.APP_DB == nil {
		return nil, fmt.Errorf("数据库连接不可用")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	providerSvc := GetProviderService()
	if providerInstance, exists := providerSvc.GetProviderByID(providerID); exists && providerInstance.IsConnected() {
		return providerInstance, nil
	}

	// Agent Providers are intentionally loadable before their reverse WebSocket
	// is online. Do not route them through getProviderInstanceByIDWithOptions:
	// that helper correctly rejects a non-connected cached Provider for ordinary
	// callers, but doing so here would remove the freshly loaded Agent executor
	// before this function gets a chance to wait for the Agent reconnect.
	var dbProvider providerModel.Provider
	if err := global.APP_DB.WithContext(ctx).First(&dbProvider, providerID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("Provider ID %d 不存在", providerID)
		}
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}
	if dbProvider.IsReverseAgent() {
		providerInstance, exists := providerSvc.GetProviderByID(providerID)
		if !exists {
			if err := providerSvc.LoadProviderWithOptionsContext(ctx, dbProvider, allowFrozen); err != nil {
				return nil, fmt.Errorf("加载Provider失败: %w", err)
			}
			providerInstance, exists = providerSvc.GetProviderByID(providerID)
		}
		if !exists {
			return nil, fmt.Errorf("Provider ID %d 加载后仍然不可用", providerID)
		}
		if providerInstance.IsConnected() {
			return providerInstance, nil
		}

		// Agent manages its own reconnect loop. Poll only the in-memory health
		// flag, with a bounded deadline and context-aware timer, so a canceled
		// recovery task does not sleep through the remaining backoff interval.
		deadline := time.Now().Add(45 * time.Second)
		interval := 500 * time.Millisecond
		for {
			if providerInstance, ok := providerSvc.GetProviderByID(providerID); ok && providerInstance.IsConnected() {
				return providerInstance, nil
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			wait := interval
			if wait > remaining {
				wait = remaining
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return nil, ctx.Err()
			case <-timer.C:
			}
			if interval < 5*time.Second {
				interval *= 2
				if interval > 5*time.Second {
					interval = 5 * time.Second
				}
			}
		}
		return nil, fmt.Errorf("Provider ID %d 的Agent在45秒内未连接", providerID)
	}

	providerInstance, err := getProviderInstanceByIDWithOptions(ctx, providerID, allowFrozen)
	if err != nil {
		return nil, err
	}

	if providerInstance.IsConnected() {
		return providerInstance, nil
	}

	if err := providerSvc.ReloadProviderContext(ctx, providerID); err != nil {
		var dbProvider providerModel.Provider
		if dbErr := global.APP_DB.WithContext(ctx).First(&dbProvider, providerID).Error; dbErr != nil {
			return nil, fmt.Errorf("获取Provider信息失败: %w", dbErr)
		}
		if loadErr := providerSvc.LoadProviderWithOptionsContext(ctx, dbProvider, allowFrozen); loadErr != nil {
			return nil, fmt.Errorf("重连Provider失败: %w", loadErr)
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
